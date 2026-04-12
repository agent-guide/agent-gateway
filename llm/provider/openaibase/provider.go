package openaibase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
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
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	if config.Network.ProxyURL != "" {
		if proxyURL, err := url.Parse(config.Network.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &Base{
		ProviderConfig: config,
		client: &http.Client{
			Timeout:   config.Network.Timeout(),
			Transport: transport,
		},
	}
}

// ListModels fetches the model list from GET /v1/models.
func (b *Base) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.BaseURL+"/models", nil)
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
		out[i] = provider.ModelInfo{ID: m.ID, Name: m.ID}
	}
	return out, nil
}

// Embed generates vector embeddings via POST /v1/embeddings.
func (b *Base) Embed(ctx context.Context, req *provider.EmbedRequest) (*provider.EmbedResponse, error) {
	model := req.Model
	if model == "" {
		model = b.DefaultModel
	}

	embedReq := &EmbedRequest{Model: model, Input: req.Texts}
	body, err := json.Marshal(embedReq)
	if err != nil {
		return nil, fmt.Errorf("openaibase: marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.BaseURL+"/embeddings", bytes.NewReader(body))
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

	var embedResp EmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("openaibase: decode embed response: %w", err)
	}

	out := &provider.EmbedResponse{
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

func (b *Base) setHeaders(req *http.Request) {
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

	if b.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.APIKey)
	}
	for k, v := range b.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
}
