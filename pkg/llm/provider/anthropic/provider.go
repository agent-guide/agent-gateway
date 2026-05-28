// Package anthropic implements the Anthropic provider (Claude models).
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/anthropicbase"
)

const anthropicVersion = "2023-06-01"
const defaultMaxTokens = 4096

func init() {
	provider.RegisterProviderFactory("anthropic", New)
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
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.Network.Defaults()

	return &Provider{
		ProviderConfig: config,
		client:         httpclient.BuildHTTPClient(config.Network),
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return provider.RetryProviderCall(p.ProviderConfig.Network, func() (*provider.ChatResponse, error) {
		state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
		if err != nil {
			return nil, err
		}

		httpReq, err := p.newMessagesRequest(ctx, state, false)
		if err != nil {
			return nil, err
		}

		resp, err := p.client.Do(httpReq)
		if err != nil {
			return nil, statuserr.Wrap(fmt.Errorf("anthropic: request failed: %w", err), http.StatusBadGateway)
		}
		defer resp.Body.Close()

		if err := provider.CheckResponse(resp); err != nil {
			return nil, statuserr.Wrap(err, http.StatusBadGateway)
		}

		var payload anthropicbase.MessagesResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, statuserr.Wrap(fmt.Errorf("anthropic: decode response: %w", err), http.StatusBadGateway)
		}
		return payload.ToChatResponse(), nil
	})
}

func (p *Provider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newMessagesRequest(ctx, state, true)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, statuserr.Wrap(fmt.Errorf("anthropic: stream request failed: %w", err), http.StatusBadGateway)
	}
	if err := provider.CheckResponse(resp); err != nil {
		resp.Body.Close()
		return nil, statuserr.Wrap(err, http.StatusBadGateway)
	}

	sr, sw := schema.Pipe[*schema.Message](16)
	go anthropicbase.ReadMessageStream(resp.Body, sw, "anthropic")
	return sr, nil
}

func (p *Provider) CreateResponses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	return provider.CreateResponsesViaChat(ctx, p, req)
}

func (p *Provider) StreamResponses(ctx context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	return provider.StreamResponsesViaChat(ctx, p, req)
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

	var modelsResp anthropicbase.ModelsResponse
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

func (p *Provider) newMessagesRequest(ctx context.Context, state *provider.ChatRequestState, stream bool) (*http.Request, error) {
	body, err := p.buildRequestPayload(state, stream)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.ProviderConfig.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	p.setHeaders(httpReq)
	return httpReq, nil
}

func (p *Provider) buildRequestPayload(state *provider.ChatRequestState, stream bool) ([]byte, error) {
	msgReq := buildMessagesRequest(state, stream)
	body, err := json.Marshal(msgReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	return body, nil
}

func buildMessagesRequest(state *provider.ChatRequestState, stream bool) *anthropicbase.MessagesRequest {
	return anthropicbase.BuildMessagesRequest(state, anthropicbase.BuildMessagesOptions{
		DefaultMaxTokens:       defaultMaxTokens,
		Stream:                 stream,
		Metadata:               requestMetadata(state),
		Thinking:               requestThinking(state),
		OutputConfig:           requestOutputConfig(state),
		DisableParallelToolUse: disableParallelToolUse(state),
	})
}

func requestOutputConfig(state *provider.ChatRequestState) *anthropicbase.OutputConfig {
	if format := anthropicbase.OutputFormatFromState(state); format != nil {
		return &anthropicbase.OutputConfig{Format: format}
	}
	return nil
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if apiKey := provider.APIKeyFromContextOrConfig(req.Context(), p.ProviderConfig.APIKey); apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	for k, v := range p.ProviderConfig.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
}

func requestMetadata(state *provider.ChatRequestState) *anthropicbase.RequestMetadata {
	userID := requestUserID(state)
	if userID == "" {
		return nil
	}
	return &anthropicbase.RequestMetadata{UserID: userID}
}

func requestUserID(state *provider.ChatRequestState) string {
	if state == nil {
		return ""
	}
	if extra := provider.ChatExtraFieldsFromOptions(state.Options...); extra != nil {
		if user := strings.TrimSpace(extra.User); user != "" {
			return user
		}
		if user := metadataUserID(extra.Metadata); user != "" {
			return user
		}
	}
	if ctx := provider.ResponsesRequestContextFromOptions(state.Options...); ctx != nil {
		if user := strings.TrimSpace(ctx.User); user != "" {
			return user
		}
		if user := metadataUserID(ctx.Metadata); user != "" {
			return user
		}
	}
	return ""
}

func metadataUserID(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	userID, _ := metadata["user_id"].(string)
	return strings.TrimSpace(userID)
}

func disableParallelToolUse(state *provider.ChatRequestState) bool {
	if state == nil {
		return false
	}
	if extra := provider.ChatExtraFieldsFromOptions(state.Options...); extra != nil && extra.ParallelToolCalls != nil {
		return !*extra.ParallelToolCalls
	}
	if ctx := provider.ResponsesRequestContextFromOptions(state.Options...); ctx != nil && ctx.ParallelToolCalls != nil {
		return !*ctx.ParallelToolCalls
	}
	return false
}

func requestThinking(state *provider.ChatRequestState) *anthropicbase.ThinkingConfig {
	if state == nil {
		return nil
	}
	if extra := provider.ChatExtraFieldsFromOptions(state.Options...); extra != nil {
		if thinking := thinkingFromReasoning(extra.Reasoning); thinking != nil {
			return thinking
		}
		if thinking := thinkingFromEffort(extra.ReasoningEffort); thinking != nil {
			return thinking
		}
	}
	if ctx := provider.ResponsesRequestContextFromOptions(state.Options...); ctx != nil {
		if thinking := thinkingFromReasoning(ctx.Reasoning); thinking != nil {
			return thinking
		}
	}
	return nil
}

func thinkingFromReasoning(reasoning map[string]any) *anthropicbase.ThinkingConfig {
	if len(reasoning) == 0 {
		return nil
	}
	if typ, _ := reasoning["type"].(string); strings.EqualFold(strings.TrimSpace(typ), "disabled") {
		return &anthropicbase.ThinkingConfig{Type: "disabled"}
	}
	if budget := intFromAny(reasoning["budget_tokens"]); budget > 0 {
		return &anthropicbase.ThinkingConfig{Type: "enabled", BudgetTokens: budget}
	}
	if budget := intFromAny(reasoning["max_tokens"]); budget > 0 {
		return &anthropicbase.ThinkingConfig{Type: "enabled", BudgetTokens: budget}
	}
	if effort, _ := reasoning["effort"].(string); effort != "" {
		return thinkingFromEffort(effort)
	}
	return nil
}

func thinkingFromEffort(effort string) *anthropicbase.ThinkingConfig {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal", "low":
		return &anthropicbase.ThinkingConfig{Type: "enabled", BudgetTokens: 1024}
	case "medium":
		return &anthropicbase.ThinkingConfig{Type: "enabled", BudgetTokens: 4096}
	case "high":
		return &anthropicbase.ThinkingConfig{Type: "enabled", BudgetTokens: 8192}
	default:
		return nil
	}
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

var (
	_ provider.Provider          = (*Provider)(nil)
	_ provider.ResponsesProvider = (*Provider)(nil)
)
