package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/internal/httplog"
	"github.com/agent-guide/agent-gateway/internal/statuserr"
	dispatcher "github.com/agent-guide/agent-gateway/pkg/dispatcher"
	llmroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// Handler handles Anthropic-format API requests (/v1/messages).
type Handler struct {
	logger              *zap.Logger
	name                string
	estimateCountTokens bool
}

// HandlerOptions configures an Anthropic-format handler variant.
type HandlerOptions struct {
	Name                string
	EstimateCountTokens bool
}

func init() {
	dispatcher.RegisterLLMApiHandlerType("anthropic")
}

// NewHandler creates a Handler.
func NewHandler(_ provider.Provider) *Handler {
	return NewHandlerWithOptions(HandlerOptions{Name: "anthropic"})
}

// NewHandlerWithOptions creates a configured Anthropic-format handler.
func NewHandlerWithOptions(opts HandlerOptions) *Handler {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "anthropic"
	}
	return &Handler{
		logger:              zap.NewNop(),
		name:                name,
		estimateCountTokens: opts.EstimateCountTokens,
	}
}

func (h *Handler) Name() string {
	if h == nil || strings.TrimSpace(h.name) == "" {
		return "anthropic"
	}
	return h.name
}

// SetLogger configures the handler logger.
func (h *Handler) SetLogger(logger *zap.Logger) {
	if logger == nil {
		logger = zap.NewNop()
	}
	h.logger = logger
}

func (h *Handler) MatchLLMApi(r *http.Request) bool {
	return r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens"
}

func (h *Handler) PrepareLLMApiRequest(r *http.Request) (*dispatcher.PreparedLLMApiRequest, llmroutepkg.RequestRequirements, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, llmroutepkg.RequestRequirements{}, fmt.Errorf("failed to read request body")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, llmroutepkg.RequestRequirements{}, fmt.Errorf("invalid request: %s", err)
	}

	h.logger.Debug(h.Name()+": request prepared",
		zap.String("model", req.Model),
		zap.Bool("stream", req.Stream),
		zap.Int("message_count", len(req.Messages)),
		zap.Int("max_tokens", req.MaxTokens),
	)

	conv := &Converter{}
	prepared := &dispatcher.PreparedLLMApiRequest{
		Type:            provider.LLMApiRequestTypeChat,
		ChatRequest:     conv.ToInternal(&req),
		StreamRequested: req.Stream,
		RawRequest:      &req,
	}
	requestRequirements := llmroutepkg.RequestRequirements{
		Model:            req.Model,
		RequireStreaming: req.Stream,
	}
	return prepared, requestRequirements, nil
}

// ServeLLMApi handles Anthropic-compatible API requests.
func (h *Handler) ServeLLMApi(w http.ResponseWriter, r *http.Request, prov provider.Provider, prepared *dispatcher.PreparedLLMApiRequest) error {
	if r.Method != http.MethodPost {
		h.writeError(w, r, http.StatusMethodNotAllowed, fmt.Errorf("method %s not allowed", r.Method))
		return nil
	}

	if strings.HasSuffix(r.URL.Path, "/count_tokens") {
		h.handleCountTokens(w, r, prepared)
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
			h.writeError(w, r, statuserr.StatusCode(err, http.StatusBadRequest), fmt.Errorf("prepare request: %w", err))
			return
		}
		var castOK bool
		req, castOK = prepared.RawRequest.(*MessagesRequest)
		if !castOK || req == nil || prepared.Type != provider.LLMApiRequestTypeChat || prepared.ChatRequest == nil {
			h.writeError(w, r, http.StatusBadRequest, fmt.Errorf("invalid request"))
			return
		}
	}

	chatReq := prepared.ChatRequest
	if prov == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, fmt.Errorf("provider is not configured"))
		return
	}

	if prepared.Stream() {
		h.serveStream(w, r, prov, chatReq, req.Model)
		return
	}

	h.logger.Debug(h.Name()+": calling provider",
		zap.String("model", chatReq.Model),
		zap.Int("message_count", len(chatReq.Messages)),
		zap.String("provider_type", prov.Config().ProviderType),
	)
	resp, err := prov.Chat(r.Context(), chatReq)
	if err != nil {
		h.writeProviderError(w, r, chatReq.Model, err)
		return
	}
	contentLen := 0
	finishReason := ""
	if resp != nil && resp.Message != nil {
		contentLen = len(resp.Message.Content)
		finishReason = provider.FinishReason(resp.Message)
	}
	h.logger.Debug(h.Name()+": provider response received",
		zap.String("model", chatReq.Model),
		zap.Int("content_length", contentLen),
		zap.String("finish_reason", finishReason),
	)
	conv := &Converter{}
	_ = httpjson.Write(w, http.StatusOK, conv.FromInternal(resp, req.Model))
}

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, chatReq *provider.ChatRequest, model string) {
	ctx := r.Context()
	h.logger.Debug(h.Name()+": starting stream",
		zap.String("model", chatReq.Model),
		zap.Int("message_count", len(chatReq.Messages)),
		zap.String("provider_type", prov.Config().ProviderType),
	)

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
	if canFlush {
		flusher.Flush()
	}

	stream, err := prov.StreamChat(ctx, chatReq)
	if err != nil {
		httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("open stream: %w", err),
			zap.String("protocol", h.Name()),
			zap.String("model", chatReq.Model),
		)
		writeSSEEvent(w, "error", anthropicErrorResponse{
			Type: "error",
			Error: anthropicErrorBody{
				Type:    "api_error",
				Message: err.Error(),
			},
		})
		if canFlush {
			flusher.Flush()
		}
		return
	}
	defer stream.Close()
	h.logger.Debug(h.Name()+": provider stream opened",
		zap.String("model", chatReq.Model),
		zap.String("provider_type", prov.Config().ProviderType),
	)

	chunkCount := 0
	textBlockStarted := false
	textBlockIndex := -1
	finalStopReason := "end_turn"
	finalOutputTokens := 0
	nextBlockIndex := 0
	emittedToolUse := false
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			httplog.Error(h.logger, "http request failed", r, http.StatusOK, fmt.Errorf("receive stream chunk: %w", err),
				zap.String("protocol", h.Name()),
				zap.String("model", chatReq.Model),
				zap.Int("chunks_received", chunkCount),
			)
			writeSSEEvent(w, "error", anthropicErrorResponse{
				Type: "error",
				Error: anthropicErrorBody{
					Type:    "api_error",
					Message: err.Error(),
				},
			})
			if canFlush {
				flusher.Flush()
			}
			return
		}
		chunkCount++

		if text := extractText(chunk); text != "" {
			if !textBlockStarted {
				textBlockIndex = nextBlockIndex
				writeSSEEvent(w, "content_block_start", map[string]any{
					"type": "content_block_start", "index": textBlockIndex,
					"content_block": map[string]string{"type": "text", "text": ""},
				})
				textBlockStarted = true
				nextBlockIndex++
			}
			writeSSEEvent(w, "content_block_delta", map[string]any{
				"type": "content_block_delta", "index": textBlockIndex,
				"delta": map[string]string{"type": "text_delta", "text": text},
			})
			if canFlush {
				flusher.Flush()
			}
		}

		// Emit tool_use blocks as a complete content block per tool call.
		for _, tc := range chunk.ToolCalls {
			idx := nextBlockIndex
			writeSSEEvent(w, "content_block_start", map[string]any{
				"type": "content_block_start", "index": idx,
				"content_block": map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": map[string]any{},
				},
			})
			if tc.Function.Arguments != "" {
				writeSSEEvent(w, "content_block_delta", map[string]any{
					"type": "content_block_delta", "index": idx,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
				})
			}
			writeSSEEvent(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
			nextBlockIndex++
			finalStopReason = "tool_use"
			emittedToolUse = true
			if canFlush {
				flusher.Flush()
			}
		}

		if chunk != nil && chunk.ResponseMeta != nil {
			if chunk.ResponseMeta.FinishReason != "" {
				reason := mapAnthropicStopReason(chunk.ResponseMeta.FinishReason)
				if reason == "tool_use" && !emittedToolUse {
					reason = "end_turn"
				}
				if reason != "" {
					finalStopReason = reason
				}
			}
			if chunk.ResponseMeta.Usage != nil && chunk.ResponseMeta.Usage.CompletionTokens > 0 {
				finalOutputTokens = chunk.ResponseMeta.Usage.CompletionTokens
			}
		}
	}

	h.logger.Debug(h.Name()+": stream completed",
		zap.String("model", model),
		zap.Int("chunks", chunkCount),
	)
	if textBlockStarted {
		writeSSEEvent(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": textBlockIndex})
	}
	writeSSEEvent(w, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": finalStopReason, "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": finalOutputTokens},
	})
	writeSSEEvent(w, "message_stop", map[string]any{"type": "message_stop"})
	if canFlush {
		flusher.Flush()
	}
}

func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request, prepared *dispatcher.PreparedLLMApiRequest) {
	if !h.estimateCountTokens {
		h.writeError(w, r, http.StatusNotImplemented, fmt.Errorf("count_tokens is not supported"))
		return
	}
	var req *MessagesRequest
	if prepared != nil {
		req, _ = prepared.RawRequest.(*MessagesRequest)
	}
	if req == nil {
		parsed, _, err := h.PrepareLLMApiRequest(r)
		if err != nil {
			h.writeError(w, r, statuserr.StatusCode(err, http.StatusBadRequest), fmt.Errorf("prepare request: %w", err))
			return
		}
		req, _ = parsed.RawRequest.(*MessagesRequest)
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"input_tokens": estimateAnthropicInputTokens(req),
	})
}

func estimateAnthropicInputTokens(req *MessagesRequest) int {
	if req == nil {
		return 1
	}
	chars := len(req.System.Text())
	for _, msg := range req.Messages {
		chars += len(msg.Role)
		chars += len(msg.Content.Text())
		for _, block := range msg.Content {
			chars += len(block.Type) + len(block.Text) + len(block.ID) + len(block.Name) + len(block.ToolUseID)
			chars += len(block.Input)
			if block.Source != nil {
				chars += len(block.Source.Type) + len(block.Source.MediaType) + len(block.Source.Data) + len(block.Source.URL)
			}
		}
	}
	for _, tool := range req.Tools {
		chars += len(tool.Name) + len(tool.Description) + len(tool.InputSchema)
	}
	tokens := chars / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

// writeError logs and writes an Anthropic-format error response.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, cause error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if r != nil {
		logFields := []zap.Field{zap.String("protocol", h.Name())}
		dispatcher.WriteHttpErrorLog(h.logger, w, r, status, "", cause, logFields...)
	}
	_ = httpjson.Write(w, status, anthropicErrorResponse{
		Type: "error",
		Error: anthropicErrorBody{
			Type:    errTypeForStatus(status),
			Message: msg,
		},
	})
}

// writeProviderError logs and writes an Anthropic-format error response for upstream errors.
func (h *Handler) writeProviderError(w http.ResponseWriter, r *http.Request, model string, err error) {
	status, msg := dispatcher.WriteProviderErrorLog(h.logger, w, r, h.Name(), model, "generate response", err)
	_ = httpjson.Write(w, status, anthropicErrorResponse{
		Type: "error",
		Error: anthropicErrorBody{
			Type:    errTypeForStatus(status),
			Message: msg,
		},
	})
}

// anthropicErrorResponse is the error format the Anthropic SDK expects.
type anthropicErrorResponse struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func errTypeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 400 && status < 500:
		return "invalid_request_error"
	default:
		return "api_error"
	}
}

// extractText returns the text content from a streaming message chunk.
func extractText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	return msg.Content
}

// mapAnthropicStopReason converts provider finish reasons to Anthropic stop reasons.
func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "stop", "end_turn":
		return "end_turn"
	case "length", "max_tokens":
		return "max_tokens"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return ""
	}
}

func writeSSEEvent(w http.ResponseWriter, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

var (
	_ dispatcher.LLMApiHandler = (*Handler)(nil)
)
