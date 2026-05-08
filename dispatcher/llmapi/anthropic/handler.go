package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dispatcher "github.com/agent-guide/caddy-agent-gateway/dispatcher"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"github.com/agent-guide/caddy-agent-gateway/internal/httplog"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	routepkg "github.com/agent-guide/caddy-agent-gateway/pkg/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
	"github.com/caddyserver/caddy/v2"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// Handler handles Anthropic-format API requests (/v1/messages).
type Handler struct {
	logger *zap.Logger
}

func init() {
	dispatcher.RegisterLLMApiHandlerType("anthropic")
	caddy.RegisterModule(Handler{})
}

// CaddyModule returns the Caddy module information.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_route_dispatcher.llm_apis.anthropic",
		New: func() caddy.Module { return new(Handler) },
	}
}

// NewHandler creates a Handler.
func NewHandler(_ provider.Provider) *Handler {
	return &Handler{logger: zap.NewNop()}
}

func (h *Handler) Name() string {
	return "anthropic"
}

func (h *Handler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger(h)
	return nil
}

func (h *Handler) MatchLLMApi(r *http.Request) bool {
	return r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens"
}

func (h *Handler) PrepareLLMApiRequest(r *http.Request) (*dispatcher.PreparedLLMApiRequest, routepkg.RequestRequirements, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, routepkg.RequestRequirements{}, fmt.Errorf("failed to read request body")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, routepkg.RequestRequirements{}, fmt.Errorf("invalid request: %s", err)
	}

	conv := &Converter{}
	prepared := &dispatcher.PreparedLLMApiRequest{
		Type:            provider.LLMApiRequestTypeChat,
		ChatRequest:     conv.ToInternal(&req),
		StreamRequested: req.Stream,
		RawRequest:      &req,
	}
	requestRequirements := routepkg.RequestRequirements{
		Model:            req.Model,
		RequireStreaming: req.Stream,
	}
	return prepared, requestRequirements, nil
}

// ServeLLMApi handles Anthropic-compatible API requests.
func (h *Handler) ServeLLMApi(w http.ResponseWriter, r *http.Request, prov provider.Provider, prepared *dispatcher.PreparedLLMApiRequest) error {
	if r.Method != http.MethodPost {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "anthropic"}, w, r, http.StatusMethodNotAllowed, "method not allowed", fmt.Errorf("method %s not allowed", r.Method))
		return nil
	}

	if strings.HasSuffix(r.URL.Path, "/count_tokens") {
		h.handleCountTokens(w, r)
		return nil
	}
	h.handleMessages(w, r, prov, prepared)
	return nil
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request, prov provider.Provider, prepared *dispatcher.PreparedLLMApiRequest) {
	var req *MessagesRequest
	ok := false
	if prepared != nil {
		req, ok = prepared.RawRequest.(*MessagesRequest)
	}
	if !ok || req == nil || prepared == nil || prepared.Type != provider.LLMApiRequestTypeChat || prepared.ChatRequest == nil {
		var err error
		prepared, _, err = h.PrepareLLMApiRequest(r)
		if err != nil {
			_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "anthropic"}, w, r, statuserr.StatusCode(err, http.StatusBadGateway), err.Error(), fmt.Errorf("prepare request: %w", err))
			return
		}
		var castOK bool
		req, castOK = prepared.RawRequest.(*MessagesRequest)
		if !castOK || req == nil || prepared.Type != provider.LLMApiRequestTypeChat || prepared.ChatRequest == nil {
			_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "anthropic"}, w, r, http.StatusBadRequest, "invalid request", fmt.Errorf("prepare request returned invalid anthropic payload"))
			return
		}
	}

	chatReq := prepared.ChatRequest
	if prov == nil {
		_ = dispatcher.WriteLoggedError(h.logger, dispatcher.ErrorContext{Protocol: "anthropic", Model: chatReq.Model}, w, r, http.StatusServiceUnavailable, "provider is not configured", fmt.Errorf("provider is not configured"))
		return
	}

	if prepared.Stream() {
		h.serveStream(w, r, prov, chatReq, req.Model)
		return
	}

	resp, err := prov.Chat(r.Context(), chatReq)
	if err != nil {
		_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "anthropic", Model: chatReq.Model}, w, r, err, "generate response")
		return
	}
	conv := &Converter{}
	_ = httpjson.Write(w, http.StatusOK, conv.FromInternal(resp, req.Model))
}

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, chatReq *provider.ChatRequest, model string) {
	ctx := r.Context()
	stream, err := prov.StreamChat(ctx, chatReq)
	if err != nil {
		_ = dispatcher.WriteProviderError(h.logger, dispatcher.ErrorContext{Protocol: "anthropic", Model: chatReq.Model}, w, r, err, "start stream")
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	writeSSEEvent(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"model": model, "content": []any{},
			"stop_reason": nil,
			"usage":       map[string]int{"input_tokens": 0, "output_tokens": 0},
		},
	})
	writeSSEEvent(w, "content_block_start", map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]string{"type": "text", "text": ""},
	})
	if canFlush {
		flusher.Flush()
	}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("receive stream chunk: %w", err),
				zap.String("protocol", "anthropic"),
				zap.String("model", chatReq.Model),
			)
			break
		}
		if text := extractText(chunk); text != "" {
			writeSSEEvent(w, "content_block_delta", map[string]any{
				"type": "content_block_delta", "index": 0,
				"delta": map[string]string{"type": "text_delta", "text": text},
			})
			if canFlush {
				flusher.Flush()
			}
		}
	}

	writeSSEEvent(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	writeSSEEvent(w, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": 0},
	})
	writeSSEEvent(w, "message_stop", map[string]any{"type": "message_stop"})
	if canFlush {
		flusher.Flush()
	}
}

func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "count_tokens is not supported")
}

// extractText returns the text content from a streaming message chunk.
func extractText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	return msg.Content
}

func writeSSEEvent(w http.ResponseWriter, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

var (
	_ caddy.Provisioner        = (*Handler)(nil)
	_ dispatcher.LLMApiHandler = (*Handler)(nil)
)
