// Package zhipu implements the Zhipu BigModel provider.
package zhipu

import (
	"context"
	"fmt"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider/openaibase"
	"github.com/agent-guide/caddy-agent-gateway/pkg/httpclient"
)

func init() {
	provider.RegisterProviderFactory("zhipu", New)
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	*openaibase.Base
}

// New creates a new Zhipu provider using BigModel's OpenAI-compatible API.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://open.bigmodel.cn/api/paas/v4"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.Network.Defaults()

	return &Provider{Base: openaibase.NewBase(config)}, nil
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.zhipu",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	p.ensureBase()
	if err := provider.ValidateProviderType(&p.ProviderConfig, "zhipu"); err != nil {
		return err
	}
	built, err := New(p.ProviderConfig)
	if err != nil {
		return err
	}
	mod, ok := built.(*Provider)
	if !ok {
		return fmt.Errorf("zhipu: unexpected provider type %T", built)
	}
	*p = *mod
	return nil
}

func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	p.ensureBase()
	return provider.UnmarshalCaddyfileConfig(d, &p.ProviderConfig)
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.ensureBase()
	return provider.RetryGenerate(p.ProviderConfig.Network, func() (*provider.ChatResponse, error) {
		chatModel, messages, opts, err := p.newChatModel(ctx, req)
		if err != nil {
			return nil, err
		}
		msg, err := chatModel.Generate(ctx, messages, opts...)
		if err != nil {
			return nil, provider.WrapEinoError(err)
		}
		return provider.ChatResponseFromEinoMessage(msg), nil
	})
}

func (p *Provider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
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

func (p *Provider) newChatModel(ctx context.Context, req *provider.ChatRequest) (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
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
	opts := append([]einomodel.Option(nil), state.Options...)
	if thinkingType := p.thinkingType(); thinkingType != "" {
		opts = append(opts, einoopenai.WithExtraFields(map[string]any{
			"thinking": map[string]any{
				"type": thinkingType,
			},
		}))
	}

	return chatModel, state.Messages, opts, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           true,
		Vision:          true,
		Embeddings:      true,
		ContextWindow:   128000,
		MaxOutputTokens: 8192,
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

func (p *Provider) thinkingType() string {
	v, ok := p.ProviderConfig.Options["thinking_type"]
	if !ok {
		return "disabled"
	}
	s, ok := v.(string)
	if !ok {
		return "disabled"
	}
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "disabled"
	}
	if s == "none" {
		return ""
	}
	return s
}

var (
	_ caddy.Provisioner          = (*Provider)(nil)
	_ caddyfile.Unmarshaler      = (*Provider)(nil)
	_ provider.Provider          = (*Provider)(nil)
	_ provider.EmbeddingProvider = (*Provider)(nil)
)
