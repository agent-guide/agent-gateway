package codex

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/openaibase"
)

const defaultBaseURL = "https://chatgpt.com/backend-api/codex"

func init() {
	provider.RegisterProviderFactory("codex", New)
}

type Provider struct {
	*openaibase.Base
}

func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.Network.Defaults()

	base := openaibase.NewBase(config)
	base.SetAuthHeaders = newCodexAuthHeaders(config)

	return &Provider{Base: base}, nil
}

func newCodexAuthHeaders(config provider.ProviderConfig) func(ctx context.Context, req *http.Request) {
	return func(ctx context.Context, req *http.Request) {
		accessToken := extractAccessToken(ctx)
		if accessToken == "" {
			accessToken = strings.TrimSpace(config.APIKey)
		}
		if accessToken != "" {
			req.Header.Set("Authorization", "Bearer "+accessToken)
		}

		if accountID := extractAccountID(ctx); accountID != "" {
			req.Header.Set("Chatgpt-Account-Id", accountID)
		}

		req.Header.Set("Originator", "codex-tui")
	}
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.ensureBase()
	state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
	if err != nil {
		return nil, err
	}
	respReq := chatStateToResponsesRequest(state, false)
	resp, err := p.CreateResponses(ctx, respReq)
	if err != nil {
		return nil, err
	}
	return responsesToChatResponse(resp), nil
}

func (p *Provider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	p.ensureBase()
	state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
	if err != nil {
		return nil, err
	}
	respReq := chatStateToResponsesRequest(state, true)
	eventStream, err := p.StreamResponses(ctx, respReq)
	if err != nil {
		return nil, err
	}
	return responsesEventStreamToMessageStream(eventStream), nil
}

func (p *Provider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (p *Provider) CreateResponses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	p.ensureBase()
	return p.Base.DoCreateResponses(ctx, sanitizeResponsesRequest(req))
}

func (p *Provider) StreamResponses(ctx context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	p.ensureBase()
	return p.Base.DoStreamResponses(ctx, sanitizeResponsesRequest(req))
}

func sanitizeResponsesRequest(req *provider.ResponsesRequest) *provider.ResponsesRequest {
	req.MaxOutputTokens = 0
	storeFalse := false
	req.Store = &storeFalse
	if strings.TrimSpace(req.Instructions) == "" {
		req.Instructions = "You are a helpful assistant."
	}
	return req
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           false,
		Vision:          false,
		Embeddings:      false,
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

func extractAccessToken(ctx context.Context) string {
	cred, ok := provider.CredentialFromContext(ctx)
	if !ok || cred == nil {
		return ""
	}
	if cred.Type == credentialmgr.TypeCLIAuthToken {
		if cred.Metadata != nil {
			if token, _ := cred.Metadata["access_token"].(string); strings.TrimSpace(token) != "" {
				return strings.TrimSpace(token)
			}
		}
	}
	return strings.TrimSpace(cred.APIKey())
}

func extractAccountID(ctx context.Context) string {
	cred, ok := provider.CredentialFromContext(ctx)
	if !ok || cred == nil || cred.Attributes == nil {
		return ""
	}
	return strings.TrimSpace(cred.Attributes["account_id"])
}

func chatStateToResponsesRequest(state *provider.ChatRequestState, stream bool) *provider.ResponsesRequest {
	req := &provider.ResponsesRequest{
		Model:  state.ModelName,
		Input:  messagesToResponsesInput(state.Messages),
		Stream: stream,
	}
	if state.CommonOptions != nil {
		if state.CommonOptions.Temperature != nil {
			req.Temperature = float64(*state.CommonOptions.Temperature)
		}
		if state.CommonOptions.TopP != nil {
			req.TopP = float64(*state.CommonOptions.TopP)
		}
		if state.CommonOptions.MaxTokens != nil {
			req.MaxOutputTokens = *state.CommonOptions.MaxTokens
		}
	}
	return req
}

func messagesToResponsesInput(messages []*schema.Message) []any {
	items := make([]any, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		items = append(items, map[string]any{
			"type":    "message",
			"role":    string(msg.Role),
			"content": []any{map[string]any{"type": "input_text", "text": msg.Content}},
		})
	}
	return items
}

func responsesToChatResponse(resp *provider.ResponsesResponse) *provider.ChatResponse {
	if resp == nil {
		return &provider.ChatResponse{}
	}
	var text string
	for _, out := range resp.Output {
		for _, c := range out.Content {
			text += c.Text
		}
	}
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: text,
	}
	if resp.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{
			FinishReason: "stop",
			Usage: &schema.TokenUsage{
				PromptTokens:     resp.Usage.InputTokens,
				CompletionTokens: resp.Usage.OutputTokens,
			},
		}
	}
	return &provider.ChatResponse{Message: msg}
}

func responsesEventStreamToMessageStream(eventStream *schema.StreamReader[*provider.ResponsesStreamEvent]) *schema.StreamReader[*schema.Message] {
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer eventStream.Close()
		defer sw.Close()
		for {
			event, err := eventStream.Recv()
			if err != nil {
				if err == io.EOF {
					return
				}
				sw.Send(nil, err)
				return
			}
			if event == nil {
				continue
			}
			switch event.Type {
			case "response.output_text.delta":
				if event.Delta != "" {
					sw.Send(&schema.Message{
						Role:    schema.Assistant,
						Content: event.Delta,
					}, nil)
				}
			case "response.completed":
				if event.Response != nil && event.Response.Usage != nil {
					sw.Send(&schema.Message{
						Role:    schema.Assistant,
						Content: "",
						ResponseMeta: &schema.ResponseMeta{
							FinishReason: "stop",
							Usage: &schema.TokenUsage{
								PromptTokens:     event.Response.Usage.InputTokens,
								CompletionTokens: event.Response.Usage.OutputTokens,
							},
						},
					}, nil)
				}
			}
		}
	}()
	return sr
}

var (
	_ provider.Provider          = (*Provider)(nil)
	_ provider.ResponsesProvider = (*Provider)(nil)
)
