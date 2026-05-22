// Package zhipu implements the Zhipu BigModel provider.
package zhipu

import (
	"context"
	"strings"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/openaibase"
)

func init() {
	provider.RegisterProviderFactory("zhipu", New)
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

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.ensureBase()
	zap.L().Info("zhipu: chat request",
		zap.String("model", req.Model),
		zap.Int("message_count", len(req.Messages)),
	)
	resp, err := provider.RetryProviderCall(p.ProviderConfig.Network, func() (*provider.ChatResponse, error) {
		chatModel, messages, opts, err := p.newChatModel(ctx, req)
		if err != nil {
			return nil, err
		}
		zap.L().Info("zhipu: calling upstream generate",
			zap.String("model", req.Model),
			zap.String("base_url", p.ProviderConfig.BaseURL),
		)
		msg, err := chatModel.Generate(ctx, messages, opts...)
		if err != nil {
			return nil, statuserr.Wrap(openaibase.NormalizeError(err), 502)
		}
		return provider.ChatResponseFromEinoMessage(msg), nil
	})
	if err != nil {
		zap.L().Info("zhipu: chat failed", zap.String("model", req.Model), zap.Error(err))
		return nil, err
	}
	contentLen := 0
	finishReason := ""
	if resp != nil && resp.Message != nil {
		contentLen = len(resp.Message.Content)
		finishReason = provider.FinishReason(resp.Message)
	}
	zap.L().Info("zhipu: chat response received",
		zap.String("model", req.Model),
		zap.Int("content_length", contentLen),
		zap.String("finish_reason", finishReason),
	)
	return resp, nil
}

func (p *Provider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	p.ensureBase()
	zap.L().Info("zhipu: stream request",
		zap.String("model", req.Model),
		zap.Int("message_count", len(req.Messages)),
		zap.String("base_url", p.ProviderConfig.BaseURL),
	)
	chatModel, messages, opts, err := p.newChatModel(ctx, req)
	if err != nil {
		return nil, err
	}
	stream, err := chatModel.Stream(ctx, messages, opts...)
	if err != nil {
		zap.L().Info("zhipu: stream failed", zap.String("model", req.Model), zap.Error(err))
		return nil, statuserr.Wrap(openaibase.NormalizeError(err), 502)
	}
	zap.L().Info("zhipu: stream started", zap.String("model", req.Model))
	return stream, nil
}

func (p *Provider) CreateResponses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	return provider.CreateResponsesViaChat(ctx, p, req)
}

func (p *Provider) StreamResponses(ctx context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	return provider.StreamResponsesViaChat(ctx, p, req)
}

func (p *Provider) newChatModel(ctx context.Context, req *provider.ChatRequest) (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
	state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
	if err != nil {
		return nil, nil, nil, err
	}

	cfg := &einoopenai.ChatModelConfig{
		BaseURL:    p.ProviderConfig.BaseURL,
		Model:      state.ModelName,
		HTTPClient: httpclient.BuildHTTPClient(p.ProviderConfig.Network),
	}
	cfg.APIKey = provider.APIKeyFromContextOrConfig(ctx, p.ProviderConfig.APIKey)

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
	_ provider.Provider          = (*Provider)(nil)
	_ provider.EmbeddingProvider = (*Provider)(nil)
	_ provider.ResponsesProvider = (*Provider)(nil)
)
