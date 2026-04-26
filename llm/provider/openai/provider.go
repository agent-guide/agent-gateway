// Package openai implements the OpenAI provider.
package openai

import (
	"context"
	"fmt"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/internal/httpclient"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider/openaibase"
)

func init() {
	provider.RegisterProviderFactory("openai", New)
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	*openaibase.Base
}

// New creates a new OpenAI provider.
//
// Optional config.Options keys:
//   - "organization": string → sent as OpenAI-Organization header.
//   - "project":      string → sent as OpenAI-Project header.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.Network.Defaults()

	// Inject OpenAI-specific headers via ExtraHeaders so Base.setHeaders picks them up.
	if config.Network.ExtraHeaders == nil {
		config.Network.ExtraHeaders = make(map[string]string)
	}
	if v, ok := config.Options["organization"]; ok {
		if s, ok := v.(string); ok && s != "" {
			config.Network.ExtraHeaders["OpenAI-Organization"] = s
		}
	}
	if v, ok := config.Options["project"]; ok {
		if s, ok := v.(string); ok && s != "" {
			config.Network.ExtraHeaders["OpenAI-Project"] = s
		}
	}

	return &Provider{Base: openaibase.NewBase(config)}, nil
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.openai",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	p.ensureBase()
	if err := provider.ValidateProviderType(&p.ProviderConfig, "openai"); err != nil {
		return err
	}
	built, err := New(p.ProviderConfig)
	if err != nil {
		return err
	}
	mod, ok := built.(*Provider)
	if !ok {
		return fmt.Errorf("openai: unexpected provider type %T", built)
	}
	*p = *mod
	return nil
}

func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	p.ensureBase()
	return provider.UnmarshalCaddyfileConfig(d, &p.ProviderConfig)
}

func (p *Provider) Generate(ctx context.Context, req *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	p.ensureBase()
	return provider.RetryGenerate(p.ProviderConfig.Network, func() (*provider.GenerateResponse, error) {
		chatModel, messages, opts, err := p.newChatModel(ctx, req)
		if err != nil {
			return nil, err
		}
		msg, err := chatModel.Generate(ctx, messages, opts...)
		if err != nil {
			return nil, provider.WrapEinoError(err)
		}
		return provider.FromEinoMessage(msg), nil
	})
}

func (p *Provider) Stream(ctx context.Context, req *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	p.ensureBase()
	chatModel, messages, opts, err := p.newChatModel(ctx, req)
	if err != nil {
		return nil, err
	}
	stream, err := chatModel.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, provider.WrapEinoError(err)
	}
	return stream, nil
}

func (p *Provider) newChatModel(ctx context.Context, req *provider.GenerateRequest) (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
	state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
	if err != nil {
		return nil, nil, nil, err
	}

	cfg := &einoopenai.ChatModelConfig{
		APIKey:     state.APIKey,
		BaseURL:    state.BaseURL,
		Model:      state.ModelName,
		HTTPClient: httpclient.BuildHTTPClient(p.ProviderConfig.Network),
	}

	chatModel, err := einoopenai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return chatModel, state.Messages, state.Options, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           true,
		Vision:          true,
		Embeddings:      true,
		ContextWindow:   128000,
		MaxOutputTokens: 16384,
	}
}

func (p *Provider) Config() provider.ProviderConfig {
	p.ensureBase()
	return p.ProviderConfig
}

func (p *Provider) ensureBase() {
	if p.Base == nil {
		p.Base = openaibase.NewBase(provider.ProviderConfig{})
	}
}

// Interface guards.
var (
	_ caddy.Provisioner          = (*Provider)(nil)
	_ caddyfile.Unmarshaler      = (*Provider)(nil)
	_ provider.Provider          = (*Provider)(nil)
	_ provider.EmbeddingProvider = (*Provider)(nil)
)
