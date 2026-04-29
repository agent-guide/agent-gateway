// Package openrouter implements the OpenRouter provider (OpenAI-compatible API).
package openrouter

import (
	"context"
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	einoopenrouter "github.com/cloudwego/eino-ext/components/model/openrouter"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider/openaibase"
	"github.com/agent-guide/caddy-agent-gateway/pkg/httpclient"
)

func init() {
	provider.RegisterProviderFactory("openrouter", New)
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	*openaibase.Base
}

// New creates a new OpenRouter provider.
// OpenRouter requires HTTP-Referer and X-Title headers; set them via NetworkConfig.ExtraHeaders.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://openrouter.ai/api/v1"
	}
	config.Network.Defaults()
	return &Provider{Base: openaibase.NewBase(config)}, nil
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.openrouter",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	p.ensureBase()
	if err := provider.ValidateProviderType(&p.ProviderConfig, "openrouter"); err != nil {
		return err
	}
	built, err := New(p.ProviderConfig)
	if err != nil {
		return err
	}
	mod, ok := built.(*Provider)
	if !ok {
		return fmt.Errorf("openrouter: unexpected provider type %T", built)
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

	cfg := &einoopenrouter.Config{
		APIKey:     state.APIKey,
		BaseURL:    state.BaseURL,
		Model:      state.ModelName,
		HTTPClient: httpclient.BuildHTTPClient(p.ProviderConfig.Network),
	}

	chatModel, err := einoopenrouter.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return chatModel, state.Messages, state.Options, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming: true,
		Tools:     true,
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

var (
	_ caddy.Provisioner     = (*Provider)(nil)
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ provider.Provider     = (*Provider)(nil)
)
