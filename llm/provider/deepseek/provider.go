// Package deepseek implements the DeepSeek provider.
package deepseek

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	einodeepseek "github.com/cloudwego/eino-ext/components/model/deepseek"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider/openaibase"
)

func init() {
	provider.RegisterProviderFactory("deepseek", New)
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	*openaibase.Base
}

// New creates a new DeepSeek provider using eino-ext's DeepSeek model.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.deepseek.com"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.Network.Defaults()

	return &Provider{Base: openaibase.NewBase(config)}, nil
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.deepseek",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	p.ensureBase()
	if err := provider.ValidateProviderType(&p.ProviderConfig, "deepseek"); err != nil {
		return err
	}
	built, err := New(p.ProviderConfig)
	if err != nil {
		return err
	}
	mod, ok := built.(*Provider)
	if !ok {
		return fmt.Errorf("deepseek: unexpected provider type %T", built)
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

	cfg := &einodeepseek.ChatModelConfig{
		APIKey:     state.APIKey,
		BaseURL:    state.BaseURL,
		Model:      state.ModelName,
		Timeout:    p.ProviderConfig.Network.Timeout(),
		HTTPClient: provider.BuildHTTPClient(p.ProviderConfig, nil),
	}
	applyOptions(cfg, p.ProviderConfig.Options)

	chatModel, err := einodeepseek.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return chatModel, state.Messages, state.Options, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           true,
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

func applyOptions(cfg *einodeepseek.ChatModelConfig, opts map[string]any) {
	if len(opts) == 0 {
		return
	}
	if v := stringOption(opts, "path"); v != "" {
		cfg.Path = v
	}
	if v := stringOption(opts, "response_format_type"); v != "" {
		cfg.ResponseFormatType = einodeepseek.ResponseFormatType(v)
	}
	if v, ok := intOption(opts, "max_tokens"); ok {
		cfg.MaxTokens = v
	}
	if v, ok := float32Option(opts, "temperature"); ok {
		cfg.Temperature = v
	}
	if v, ok := float32Option(opts, "top_p"); ok {
		cfg.TopP = v
	}
	if v, ok := float32Option(opts, "presence_penalty"); ok {
		cfg.PresencePenalty = v
	}
	if v, ok := float32Option(opts, "frequency_penalty"); ok {
		cfg.FrequencyPenalty = v
	}
	if v, ok := boolOption(opts, "log_probs"); ok {
		cfg.LogProbs = v
	}
	if v, ok := intOption(opts, "top_log_probs"); ok {
		cfg.TopLogProbs = v
	}
}

func stringOption(opts map[string]any, key string) string {
	v, ok := opts[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func intOption(opts map[string]any, key string) (int, bool) {
	switch v := opts[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		return i, err == nil
	default:
		return 0, false
	}
}

func float32Option(opts map[string]any, key string) (float32, bool) {
	switch v := opts[key].(type) {
	case float32:
		return v, true
	case float64:
		return float32(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 32)
		return float32(f), err == nil
	default:
		return 0, false
	}
}

func boolOption(opts map[string]any, key string) (bool, bool) {
	switch v := opts[key].(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return b, err == nil
	default:
		return false, false
	}
}

var (
	_ caddy.Provisioner     = (*Provider)(nil)
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ provider.Provider     = (*Provider)(nil)
)
