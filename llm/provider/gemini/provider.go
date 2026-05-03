// Package gemini implements the Google Gemini provider.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	einogemini "github.com/cloudwego/eino-ext/components/model/gemini"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/genai"

	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/pkg/httpclient"
)

func init() {
	provider.RegisterProviderFactory("gemini", New)
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	provider.ProviderConfig
	genaiClient *genai.Client // cached default client (no credential override)
}

// New creates a new Google Gemini provider.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://generativelanguage.googleapis.com"
	}
	config.Network.Defaults()

	defaultClient, err := buildGenaiClient(context.Background(), config.APIKey, config.BaseURL, config.Network, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini: init client: %w", err)
	}
	return &Provider{
		ProviderConfig: config,
		genaiClient:    defaultClient,
	}, nil
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.gemini",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	if err := provider.ValidateProviderType(&p.ProviderConfig, "gemini"); err != nil {
		return err
	}
	built, err := New(p.ProviderConfig)
	if err != nil {
		return err
	}
	mod, ok := built.(*Provider)
	if !ok {
		return fmt.Errorf("gemini: unexpected provider type %T", built)
	}
	*p = *mod
	return nil
}

func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	return provider.UnmarshalCaddyfileConfig(d, &p.ProviderConfig)
}

// buildGenaiClient constructs a genai.Client with the given credentials and network config.
// This is the single path for creating Gemini API clients in this package.
func buildGenaiClient(ctx context.Context, apiKey, baseURL string, network httpclient.NetworkConfig, cred *credentialmgr.Credential) (*genai.Client, error) {
	httpClient := httpclient.BuildHTTPClient(network)
	timeout := network.RequestTimeout()
	return genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:     apiKey,
		HTTPClient: httpClient,
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    baseURL,
			APIVersion: "v1beta",
			Timeout:    &timeout,
		},
	})
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return provider.RetryProviderCall(p.ProviderConfig.Network, func() (*provider.ChatResponse, error) {
		chatModel, messages, opts, err := p.newChatModel(ctx, req)
		if err != nil {
			return nil, err
		}
		msg, err := chatModel.Generate(ctx, messages, opts...)
		if err != nil {
			return nil, statuserr.Wrap(err, 502)
		}
		return provider.ChatResponseFromEinoMessage(msg), nil
	})
}

func (p *Provider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	chatModel, messages, opts, err := p.newChatModel(ctx, req)
	if err != nil {
		return nil, err
	}
	stream, err := chatModel.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, statuserr.Wrap(err, 502)
	}
	return stream, nil
}

// ListModels fetches available Gemini models from GET /v1beta/models.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	url := fmt.Sprintf("%s/v1beta/models?key=%s", p.ProviderConfig.BaseURL, p.ProviderConfig.APIKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini: build request: %w", err)
	}
	p.setHeaders(httpReq)

	httpClient := httpclient.BuildHTTPClient(p.ProviderConfig.Network)
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := provider.CheckResponse(resp); err != nil {
		return nil, err
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("gemini: decode models: %w", err)
	}

	out := make([]provider.ModelInfo, len(modelsResp.Models))
	for i, m := range modelsResp.Models {
		// Name is "models/gemini-1.5-pro" — strip the prefix for the ID.
		id := strings.TrimPrefix(m.Name, "models/")
		out[i] = provider.ModelInfo{
			ID:          id,
			Name:        m.DisplayName,
			DisplayName: m.DisplayName,
			Description: m.Description,
			Capabilities: provider.ModelCapabilities{
				ContextWindow:   m.InputTokenLimit,
				MaxOutputTokens: m.OutputTokenLimit,
			},
		}
	}
	return out, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           true,
		Vision:          true,
		Embeddings:      true,
		ContextWindow:   1000000,
		MaxOutputTokens: 8192,
	}
}

func (p *Provider) Config() provider.ProviderConfig {
	return p.ProviderConfig
}

func (p *Provider) newChatModel(ctx context.Context, req *provider.ChatRequest) (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
	state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
	if err != nil {
		return nil, nil, nil, err
	}

	// Reuse the cached client for the common path (no credential override).
	// Build a new one only when a per-request credential changes the API key or base URL.
	client := p.genaiClient
	if cred, ok := provider.CredentialFromContext(ctx); ok {
		client, err = buildGenaiClient(ctx, state.APIKey, state.BaseURL, p.ProviderConfig.Network, cred)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("gemini: build credential client: %w", err)
		}
	}

	cfg := &einogemini.Config{
		Client: client,
		Model:  state.ModelName,
	}
	chatModel, err := einogemini.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return chatModel, state.Messages, state.Options, nil
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	for k, v := range p.ProviderConfig.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
}

var (
	_ caddy.Provisioner     = (*Provider)(nil)
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ provider.Provider     = (*Provider)(nil)
)
