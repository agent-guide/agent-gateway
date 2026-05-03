// Package anthropic implements the Anthropic provider (Claude models).
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	einoclaude "github.com/cloudwego/eino-ext/components/model/claude"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/pkg/httpclient"
)

const anthropicVersion = "2023-06-01"

func init() {
	provider.RegisterProviderFactory("anthropic", New)
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	provider.ProviderConfig
	client *http.Client
}

// New creates a new Anthropic provider.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.anthropic.com"
	}
	config.Network.Defaults()

	return &Provider{
		ProviderConfig: config,
		client:         httpclient.BuildHTTPClient(config.Network),
	}, nil
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.anthropic",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	if err := provider.ValidateProviderType(&p.ProviderConfig, "anthropic"); err != nil {
		return err
	}
	built, err := New(p.ProviderConfig)
	if err != nil {
		return err
	}
	mod, ok := built.(*Provider)
	if !ok {
		return fmt.Errorf("anthropic: unexpected provider type %T", built)
	}
	*p = *mod
	return nil
}

func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	return provider.UnmarshalCaddyfileConfig(d, &p.ProviderConfig)
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

// ListModels fetches available Claude models from GET /v1/models.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.ProviderConfig.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := provider.CheckResponse(resp); err != nil {
		return nil, err
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("anthropic: decode models: %w", err)
	}

	out := make([]provider.ModelInfo, len(modelsResp.Data))
	for i, m := range modelsResp.Data {
		out[i] = provider.ModelInfo{
			ID:           m.ID,
			Name:         m.DisplayName,
			DisplayName:  m.DisplayName,
			Capabilities: provider.ModelCapabilitiesFromProviderSummary(p.Capabilities()),
		}
	}
	return out, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           true,
		Vision:          true,
		ContextWindow:   200000,
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

	maxTokens := 0
	if state.CommonOptions.MaxTokens != nil {
		maxTokens = *state.CommonOptions.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	cfg := &einoclaude.Config{
		APIKey:     state.APIKey,
		Model:      state.ModelName,
		MaxTokens:  maxTokens,
		HTTPClient: httpclient.BuildHTTPClient(p.ProviderConfig.Network),
	}
	if state.BaseURL != "" {
		cfg.BaseURL = &state.BaseURL
	}

	chatModel, err := einoclaude.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return chatModel, state.Messages, state.Options, nil
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.ProviderConfig.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	for k, v := range p.ProviderConfig.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
}

var (
	_ caddy.Provisioner     = (*Provider)(nil)
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ provider.Provider     = (*Provider)(nil)
)
