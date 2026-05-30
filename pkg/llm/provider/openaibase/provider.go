package openaibase

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

// Base provides shared utilities for OpenAI-compatible providers.
// It currently backs list-models, embeddings, and shared auth/header handling.
//
// Features provided out of the box:
//   - Proxy support via config.Network.ProxyURL
//   - Extra request headers via config.Network.ExtraHeaders
type Base struct {
	provider.ProviderConfig
	client         *http.Client
	SetAuthHeaders func(ctx context.Context, req *http.Request)
	// CCCompat enables Claude Code CLI compatibility mode for OpenAI-compatible
	// chat upstreams. It is read from the `cc_compat` provider option.
	CCCompat bool
}

// NewBase creates a Base using the supplied config.
// Call config.Network.Defaults() before passing it here.
// Proxy is configured automatically from config.Network.ProxyURL when non-empty.
func NewBase(config provider.ProviderConfig) *Base {
	return &Base{
		ProviderConfig: config,
		client:         httpclient.BuildHTTPClient(config.Network),
		CCCompat:       config.BoolOption("cc_compat"),
	}
}

// ListModels fetches the model list from GET /v1/models.
func (b *Base) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	baseURL := strings.TrimRight(b.ProviderConfig.BaseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("openaibase: build request: %w", err)
	}
	b.setHeaders(httpReq)

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaibase: request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := provider.CheckResponse(resp); err != nil {
		return nil, err
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("openaibase: decode models: %w", err)
	}

	out := make([]provider.ModelInfo, len(modelsResp.Data))
	for i, m := range modelsResp.Data {
		out[i] = provider.ModelInfo{
			ID:           m.ID,
			Name:         m.ID,
			DisplayName:  m.ID,
			Capabilities: provider.ModelCapabilitiesFromProviderSummary(provider.ProviderCapabilities{Streaming: true, Tools: true, Vision: true, Embeddings: true}),
		}
	}
	return out, nil
}

// Embed generates vector embeddings via POST /v1/embeddings.
func (b *Base) Embedding(ctx context.Context, req *provider.EmbeddingRequest) (*provider.EmbeddingResponse, error) {
	baseURL := strings.TrimRight(b.ProviderConfig.BaseURL, "/")
	model := req.Model
	if model == "" {
		model = b.DefaultModel
	}

	embedReq := &EmbeddingRequest{Model: model, Input: req.Texts}
	body, err := json.Marshal(embedReq)
	if err != nil {
		return nil, fmt.Errorf("openaibase: marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaibase: build embed request: %w", err)
	}
	b.setHeaders(httpReq)

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaibase: embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := provider.CheckResponse(resp); err != nil {
		return nil, err
	}

	var embedResp EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("openaibase: decode embed response: %w", err)
	}

	out := &provider.EmbeddingResponse{
		Model: embedResp.Model,
		Usage: provider.Usage{
			InputTokens:  embedResp.Usage.PromptTokens,
			OutputTokens: embedResp.Usage.CompletionTokens,
		},
	}
	for _, d := range embedResp.Data {
		out.Embeddings = append(out.Embeddings, d.Embedding)
	}
	return out, nil
}

// DoCreateResponses sends a minimal OpenAI-compatible request to POST /responses.
// Providers should expose this only when the upstream actually supports it.
func (b *Base) DoCreateResponses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	return provider.RetryProviderCall(b.ProviderConfig.Network, func() (*provider.ResponsesResponse, error) {
		httpReq, err := b.newResponsesRequest(ctx, req)
		if err != nil {
			return nil, err
		}

		resp, err := b.client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("openaibase: responses request failed: %w", err)
		}
		defer resp.Body.Close()

		if err := provider.CheckResponse(resp); err != nil {
			return nil, err
		}

		rawBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("openaibase: read responses response: %w", err)
		}
		var out provider.ResponsesResponse
		if err := json.Unmarshal(rawBody, &out); err != nil {
			return nil, fmt.Errorf("openaibase: decode responses response: %w", err)
		}
		out.RawJSON = append(json.RawMessage(nil), bytes.TrimSpace(rawBody)...)
		return &out, nil
	})
}

// DoStreamResponses opens a minimal OpenAI-compatible responses SSE stream.
// Providers should expose this only when the upstream actually supports it.
func (b *Base) DoStreamResponses(ctx context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	httpReq, err := b.newResponsesRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaibase: responses stream request failed: %w", err)
	}
	if err := provider.CheckResponse(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}

	sr, sw := schema.Pipe[*provider.ResponsesStreamEvent](16)
	go b.readResponsesStream(resp.Body, sw)
	return sr, nil
}

func (b *Base) newResponsesRequest(ctx context.Context, req *provider.ResponsesRequest) (*http.Request, error) {
	if req.Model == "" {
		req.Model = b.DefaultModel
	}
	baseURL := strings.TrimRight(b.ProviderConfig.BaseURL, "/")
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openaibase: marshal responses request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaibase: build responses request: %w", err)
	}
	b.setHeaders(httpReq)
	return httpReq, nil
}

func (b *Base) readResponsesStream(body io.ReadCloser, sw *schema.StreamWriter[*provider.ResponsesStreamEvent]) {
	defer body.Close()
	defer sw.Close()

	reader := bufio.NewReader(body)
	var eventName string
	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			sw.Send(nil, fmt.Errorf("openaibase: read responses stream: %w", err))
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			if data.Len() == 0 {
				eventName = ""
				continue
			}
			payload := data.String()
			if strings.TrimSpace(payload) == "[DONE]" {
				return
			}
			event, err := decodeResponsesStreamEvent(eventName, payload)
			if err != nil {
				sw.Send(nil, err)
				return
			}
			if event != nil {
				if sw.Send(event, nil) {
					return
				}
				if isResponsesTerminalEvent(event.Type) {
					return
				}
			}
			eventName = ""
			data.Reset()
		}
		if err == io.EOF {
			if data.Len() > 0 {
				payload := data.String()
				if strings.TrimSpace(payload) == "[DONE]" {
					return
				}
				event, decodeErr := decodeResponsesStreamEvent(eventName, payload)
				if decodeErr != nil {
					sw.Send(nil, decodeErr)
					return
				}
				if event != nil {
					_ = sw.Send(event, nil)
				}
			}
			return
		}
	}
}

func isResponsesTerminalEvent(eventType string) bool {
	switch eventType {
	case "response.completed", "response.failed", "response.incomplete":
		return true
	default:
		return false
	}
}

func decodeResponsesStreamEvent(eventName string, payload string) (*provider.ResponsesStreamEvent, error) {
	if strings.TrimSpace(payload) == "" || strings.TrimSpace(payload) == "[DONE]" {
		return nil, nil
	}

	var raw responseStreamEventEnvelope
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, fmt.Errorf("openaibase: decode responses stream event: %w", err)
	}

	typ := strings.TrimSpace(raw.Type)
	if typ == "" {
		typ = strings.TrimSpace(eventName)
	}

	out := &provider.ResponsesStreamEvent{
		Type:         typ,
		Delta:        raw.Delta,
		ItemID:       raw.ItemID,
		OutputIndex:  raw.OutputIndex,
		ContentIndex: raw.ContentIndex,
		RawJSON:      normalizeResponsesStreamPayload([]byte(payload), typ),
	}
	if raw.Item != nil {
		buf, err := json.Marshal(raw.Item)
		if err != nil {
			return nil, fmt.Errorf("openaibase: re-marshal item event payload: %w", err)
		}
		var item provider.ResponsesResponseOutput
		if err := json.Unmarshal(buf, &item); err != nil {
			return nil, fmt.Errorf("openaibase: decode item event body: %w", err)
		}
		out.Item = &item
	}
	if raw.Response != nil {
		buf, err := json.Marshal(raw.Response)
		if err != nil {
			return nil, fmt.Errorf("openaibase: re-marshal response event payload: %w", err)
		}
		var resp provider.ResponsesResponse
		if err := json.Unmarshal(buf, &resp); err != nil {
			return nil, fmt.Errorf("openaibase: decode response event body: %w", err)
		}
		out.Response = &resp
	}
	return out, nil
}

func normalizeResponsesStreamPayload(payload []byte, typ string) json.RawMessage {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return append(json.RawMessage(nil), trimmed...)
	}
	if _, ok := obj["type"]; !ok && strings.TrimSpace(typ) != "" {
		typeValue, err := json.Marshal(typ)
		if err == nil {
			obj["type"] = typeValue
			if normalized, err := json.Marshal(obj); err == nil {
				return normalized
			}
		}
	}
	return append(json.RawMessage(nil), trimmed...)
}

func (b *Base) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")

	if b.SetAuthHeaders != nil {
		b.SetAuthHeaders(req.Context(), req)
	} else if apiKey := provider.APIKeyFromContextOrConfig(req.Context(), b.ProviderConfig.APIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range b.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
}
