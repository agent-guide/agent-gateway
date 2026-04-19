package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/api"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"github.com/agent-guide/caddy-agent-gateway/internal/httplog"
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
	api.RegisterLLMApiHandlerName("openai")
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
		r.URL.Path == "/v1/models" || r.URL.Path == "/models" ||
		r.URL.Path == "/v1/embeddings" || r.URL.Path == "/embeddings"
}

func (h *Handler) PrepareLLMApiRequest(r *http.Request) (*api.PreparedLLMApiRequest, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %s", err)
	}

	conv := &Converter{}
	return &api.PreparedLLMApiRequest{
		GenerateRequest: conv.ToInternal(&req),
		Stream:          req.Stream,
		RawRequest:      &req,
	}, nil
}

// ServeLLMApi handles OpenAI-compatible API requests.
func (h *Handler) ServeLLMApi(w http.ResponseWriter, r *http.Request, prov provider.Provider, prepared *api.PreparedLLMApiRequest) error {
	if r.Method != http.MethodPost {
		_ = api.WriteLoggedError(h.logger, api.ErrorContext{Protocol: "openai"}, w, r, http.StatusMethodNotAllowed, "method not allowed", fmt.Errorf("method %s not allowed", r.Method))
		return nil
	}

	var req *ChatCompletionRequest
	ok := false
	if prepared != nil {
		req, ok = prepared.RawRequest.(*ChatCompletionRequest)
	}
	if !ok || req == nil || prepared == nil || prepared.GenerateRequest == nil {
		_ = api.WriteLoggedError(h.logger, api.ErrorContext{Protocol: "openai"}, w, r, http.StatusBadRequest, "invalid request", fmt.Errorf("prepare request returned invalid openai payload"))
		return nil
	}

	genReq := prepared.GenerateRequest
	if prov == nil {
		_ = api.WriteLoggedError(h.logger, api.ErrorContext{Protocol: "openai", Model: genReq.Model}, w, r, http.StatusServiceUnavailable, "provider is not configured", fmt.Errorf("provider is not configured"))
		return nil
	}

	if req.Stream {
		h.serveStream(w, r, prov, genReq)
		return nil
	}

	resp, err := prov.Generate(r.Context(), genReq)
	if err != nil {
		_ = api.WriteLoggedError(h.logger, api.ErrorContext{Protocol: "openai", Model: genReq.Model}, w, r, http.StatusBadGateway, err.Error(), fmt.Errorf("generate response: %w", err))
		return nil
	}
	conv := &Converter{}
	_ = httpjson.Write(w, http.StatusOK, conv.FromInternal(resp, genReq.Model))
	return nil
}

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, genReq *provider.GenerateRequest) {
	ctx := r.Context()
	stream, err := prov.Stream(ctx, genReq)
	if err != nil {
		_ = api.WriteLoggedError(h.logger, api.ErrorContext{Protocol: "openai", Model: genReq.Model}, w, r, http.StatusBadGateway, err.Error(), fmt.Errorf("start stream: %w", err))
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
				zap.String("model", genReq.Model),
			)
			break
		}

		payload, err := json.Marshal(toStreamChunk(genReq.Model, chunk))
		if err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("marshal stream chunk: %w", err),
				zap.String("protocol", "openai"),
				zap.String("model", genReq.Model),
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
	_ caddy.Provisioner = (*Handler)(nil)
	_ api.LLMApiHandler = (*Handler)(nil)
)
