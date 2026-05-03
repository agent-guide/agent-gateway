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

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/pkg/httpclient"
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
	client *http.Client
}

// NewBase creates a Base using the supplied config.
// Call config.Network.Defaults() before passing it here.
// Proxy is configured automatically from config.Network.ProxyURL when non-empty.
func NewBase(config provider.ProviderConfig) *Base {
	return &Base{
		ProviderConfig: config,
		client:         httpclient.BuildHTTPClient(config.Network),
	}
}

// ListModels fetches the model list from GET /v1/models.
func (b *Base) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	apiKey, baseURL := provider.ResolveCredential(ctx, b.ProviderConfig)
	baseURL = strings.TrimRight(baseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("openaibase: build request: %w", err)
	}
	b.setHeaders(httpReq, apiKey)

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
	apiKey, baseURL := provider.ResolveCredential(ctx, b.ProviderConfig)
	baseURL = strings.TrimRight(baseURL, "/")
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
	b.setHeaders(httpReq, apiKey)

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

		var out provider.ResponsesResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, fmt.Errorf("openaibase: decode responses response: %w", err)
		}
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
	apiKey, baseURL := provider.ResolveCredential(ctx, b.ProviderConfig)
	baseURL = strings.TrimRight(baseURL, "/")
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openaibase: marshal responses request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaibase: build responses request: %w", err)
	}
	b.setHeaders(httpReq, apiKey)
	return httpReq, nil
}

func (b *Base) readResponsesStream(body io.ReadCloser, sw *schema.StreamWriter[*provider.ResponsesStreamEvent]) {
	defer body.Close()
	defer sw.Close()

	scanner := bufio.NewScanner(body)
	var eventName string
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
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
			event, err := decodeResponsesStreamEvent(eventName, data.String())
			if err != nil {
				sw.Send(nil, err)
				return
			}
			if event != nil {
				sw.Send(event, nil)
			}
			eventName = ""
			data.Reset()
		}
	}
	if err := scanner.Err(); err != nil {
		sw.Send(nil, fmt.Errorf("openaibase: read responses stream: %w", err))
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

func (b *Base) setHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Content-Type", "application/json")

	// Per-request credential override: use OAuth access_token from CLI login.
	if cred, ok := provider.CredentialFromContext(req.Context()); ok {
		if token, _ := cred.Metadata["access_token"].(string); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			for k, v := range b.Network.ExtraHeaders {
				req.Header.Set(k, v)
			}
			return
		}
	}

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range b.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
}
