package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dispatcher "github.com/agent-guide/caddy-agent-gateway/dispatcher"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"github.com/agent-guide/caddy-agent-gateway/internal/httplog"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/caddyserver/caddy/v2"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// Handler handles OpenAI-format API requests (/v1/chat/completions, etc.).
type Handler struct {
	logger *zap.Logger
}

func init() {
	dispatcher.RegisterLLMApiHandlerType("openai")
	caddy.RegisterModule(Handler{})
}

// CaddyModule returns the Caddy module information.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_route_dispatcher.llm_apis.openai",
		New: func() caddy.Module { return new(Handler) },
	}
}

// NewHandler creates a Handler.
func NewHandler() *Handler {
	return &Handler{logger: zap.NewNop()}
}

func (h *Handler) Name() string {
	return "openai"
}

func (h *Handler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger(h)
	return nil
}

func (h *Handler) MatchLLMApi(r *http.Request) bool {
	return r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/chat/completions" ||
		r.URL.Path == "/v1/responses" || r.URL.Path == "/responses" ||
		r.URL.Path == "/v1/models" || r.URL.Path == "/models" ||
		r.URL.Path == "/v1/embeddings" || r.URL.Path == "/embeddings"
}

func (h *Handler) PrepareLLMApiRequest(r *http.Request) (*dispatcher.PreparedLLMApiRequest, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	if r.URL.Path == "/v1/responses" || r.URL.Path == "/responses" {
		var req ResponsesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid request: %s", err)
		}
		return &dispatcher.PreparedLLMApiRequest{
			Type:             provider.LLMApiRequestTypeResponses,
			ResponsesRequest: &req,
			StreamRequested:  req.Stream,
			RawRequest:       &req,
		}, nil
	}

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %s", err)
	}

	conv := &Converter{}
	return &dispatcher.PreparedLLMApiRequest{
		Type:            provider.LLMApiRequestTypeChat,
		ChatRequest:     conv.ToInternal(&req),
		StreamRequested: req.Stream,
		RawRequest:      &req,
	}, nil
}

// ServeLLMApi handles OpenAI-compatible API requests.
func (h *Handler) ServeLLMApi(w http.ResponseWriter, r *http.Request, prov provider.Provider, prepared *dispatcher.PreparedLLMApiRequest) error {
	if r.Method != http.MethodPost {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai"}, w, r, http.StatusMethodNotAllowed, "method not allowed", fmt.Errorf("method %s not allowed", r.Method))
		return nil
	}

	if r.URL.Path == "/v1/responses" || r.URL.Path == "/responses" {
		return h.serveResponses(w, r, prov, prepared)
	}

	var req *ChatCompletionRequest
	ok := false
	if prepared != nil {
		req, ok = prepared.RawRequest.(*ChatCompletionRequest)
	}
	if !ok || req == nil || prepared == nil || prepared.Type != provider.LLMApiRequestTypeChat || prepared.ChatRequest == nil {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai"}, w, r, http.StatusBadRequest, "invalid request", fmt.Errorf("prepare request returned invalid openai payload"))
		return nil
	}

	chatReq := prepared.ChatRequest
	if prov == nil {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: chatReq.Model}, w, r, http.StatusServiceUnavailable, "provider is not configured", fmt.Errorf("provider is not configured"))
		return nil
	}

	if prepared.Stream() {
		h.serveStream(w, r, prov, chatReq)
		return nil
	}

	resp, err := prov.Chat(r.Context(), chatReq)
	if err != nil {
		_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: chatReq.Model}, w, r, err, "generate response")
		return nil
	}
	conv := &Converter{}
	_ = httpjson.Write(w, http.StatusOK, conv.FromInternal(resp, chatReq.Model))
	return nil
}

func (h *Handler) serveResponses(w http.ResponseWriter, r *http.Request, prov provider.Provider, prepared *dispatcher.PreparedLLMApiRequest) error {
	if prepared == nil || prepared.Type != provider.LLMApiRequestTypeResponses || prepared.ResponsesRequest == nil {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai"}, w, r, http.StatusBadRequest, "invalid request", fmt.Errorf("prepare request returned invalid responses payload"))
		return nil
	}
	req, ok := prepared.RawRequest.(*ResponsesRequest)
	if !ok || req == nil {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai"}, w, r, http.StatusBadRequest, "invalid request", fmt.Errorf("prepare request returned invalid responses payload"))
		return nil
	}

	respReq := prepared.ResponsesRequest
	if prov == nil {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: respReq.Model}, w, r, http.StatusServiceUnavailable, "provider is not configured", fmt.Errorf("provider is not configured"))
		return nil
	}

	if prepared.Stream() {
		if responsesProv, ok := prov.(provider.ResponsesProvider); ok {
			stream, err := responsesProv.StreamResponses(r.Context(), respReq)
			if err == nil {
				h.writeProviderResponsesStream(w, r, stream, respReq.Model)
				return nil
			}
			if !isResponsesUnsupported(err) {
				_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: respReq.Model}, w, r, err, "start response stream")
				return nil
			}
		}
		chatReq, err := responsesToInternal(req)
		if err != nil {
			_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: respReq.Model}, w, r, http.StatusBadRequest, err.Error(), fmt.Errorf("convert responses fallback request: %w", err))
			return nil
		}
		h.serveResponsesStream(w, r, prov, chatReq)
		return nil
	}

	if responsesProv, ok := prov.(provider.ResponsesProvider); ok {
		resp, err := responsesProv.CreateResponses(r.Context(), respReq)
		if err == nil {
			_ = httpjson.Write(w, http.StatusOK, resp)
			return nil
		}
		if !isResponsesUnsupported(err) {
			_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: respReq.Model}, w, r, err, "create response")
			return nil
		}
	}
	chatReq, err := responsesToInternal(req)
	if err != nil {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: respReq.Model}, w, r, http.StatusBadRequest, err.Error(), fmt.Errorf("convert responses fallback request: %w", err))
		return nil
	}

	resp, err := prov.Chat(r.Context(), chatReq)
	if err != nil {
		_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: chatReq.Model}, w, r, err, "generate response")
		return nil
	}
	_ = httpjson.Write(w, http.StatusOK, responsesFromInternal(resp, chatReq.Model))
	return nil
}

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, chatReq *provider.ChatRequest) {
	ctx := r.Context()
	stream, err := prov.StreamChat(ctx, chatReq)
	if err != nil {
		_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: chatReq.Model}, w, r, err, "start stream")
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("receive stream chunk: %w", err),
				zap.String("protocol", "openai"),
				zap.String("model", chatReq.Model),
			)
			break
		}

		payload, err := json.Marshal(toStreamChunk(chatReq.Model, chunk))
		if err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("marshal stream chunk: %w", err),
				zap.String("protocol", "openai"),
				zap.String("model", chatReq.Model),
			)
			break
		}
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if canFlush {
			flusher.Flush()
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if canFlush {
		flusher.Flush()
	}
}

func (h *Handler) serveResponsesStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, chatReq *provider.ChatRequest) {
	ctx := r.Context()
	stream, err := prov.StreamChat(ctx, chatReq)
	if err != nil {
		_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: chatReq.Model}, w, r, err, "start stream")
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	resp := responsesFromInternal(&provider.ChatResponse{}, chatReq.Model)
	if err := writeResponsesEvent(w, responsesCreatedEvent(resp)); err == nil && canFlush {
		flusher.Flush()
	}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("receive responses stream chunk: %w", err),
				zap.String("protocol", "openai"),
				zap.String("model", chatReq.Model),
			)
			break
		}

		if chunk != nil && chunk.Content != "" {
			resp.Output[0].Content[0].Text += chunk.Content
			if err := writeResponsesEvent(w, responsesDeltaEvent(resp.Output[0].ID, chunk.Content)); err != nil {
				httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("marshal responses stream chunk: %w", err),
					zap.String("protocol", "openai"),
					zap.String("model", chatReq.Model),
				)
				break
			}
			if canFlush {
				flusher.Flush()
			}
		}
	}

	if err := writeResponsesEvent(w, responsesCompletedEvent(resp)); err == nil && canFlush {
		flusher.Flush()
	}
}

func (h *Handler) serveProviderResponsesStream(w http.ResponseWriter, r *http.Request, prov provider.ResponsesProvider, req *provider.ResponsesRequest, model string) {
	stream, err := prov.StreamResponses(r.Context(), req)
	if err != nil {
		_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "openai", Model: model}, w, r, err, "start response stream")
		return
	}
	h.writeProviderResponsesStream(w, r, stream, model)
}

func (h *Handler) writeProviderResponsesStream(w http.ResponseWriter, r *http.Request, stream *schema.StreamReader[*provider.ResponsesStreamEvent], model string) {
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("receive provider responses stream chunk: %w", err),
				zap.String("protocol", "openai"),
				zap.String("model", model),
			)
			break
		}
		if event == nil {
			continue
		}
		if err := writeResponsesEvent(w, event); err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("marshal provider responses stream chunk: %w", err),
				zap.String("protocol", "openai"),
				zap.String("model", model),
			)
			break
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

func isResponsesUnsupported(err error) bool {
	var se statuserr.StatusError
	if !errors.As(err, &se) || se.StatusCode() != http.StatusNotImplemented {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "responses api is not supported")
}

func writeResponsesEvent(w http.ResponseWriter, event *provider.ResponsesStreamEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, payload)
	return err
}

type chatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

type chunkDelta struct {
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	ToolCalls []schema.ToolCall `json:"tool_calls,omitempty"`
}

func toStreamChunk(model string, msg *schema.Message) *chatCompletionChunk {
	chunk := &chatCompletionChunk{
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chunkChoice{{
			Index: 0,
			Delta: chunkDelta{
				Role:    string(msg.Role),
				Content: msg.Content,
			},
		}},
	}
	if len(msg.ToolCalls) > 0 {
		chunk.Choices[0].Delta.ToolCalls = msg.ToolCalls
	}
	if msg.ResponseMeta != nil {
		chunk.Choices[0].FinishReason = msg.ResponseMeta.FinishReason
	}
	return chunk
}

var (
	_ caddy.Provisioner        = (*Handler)(nil)
	_ dispatcher.LLMApiHandler = (*Handler)(nil)
)
