package claudecode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/anthropicbase"
)

const (
	defaultBaseURL                       = "https://api.anthropic.com"
	anthropicVersion                     = "2023-06-01"
	anthropicBeta                        = "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24"
	oauthTokenPrefix                     = "sk-ant-oat-"
	authModeBearer                       = "bearer_only"
	authModeAPIKey                       = "api_key"
	defaultClaudeCodeMaxTokens           = 32000
	defaultClaudeCodeEffort              = "high"
	defaultClaudeCodeUserAgent           = "claude-cli/2.1.150 (external, sdk-cli)"
	defaultClaudeCodeBillingHeaderPrefix = "x-anthropic-billing-header: cc_version=2.1.150.4c2; cc_entrypoint=sdk-cli; cch="
)

const defaultClaudeCodeSystemPrompt = "\nYou are an interactive agent that helps users with software engineering tasks.\n"

func init() {
	provider.RegisterProviderFactory("claudecode", New)
}

type Provider struct {
	provider.ProviderConfig
	client *http.Client
}

func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
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
			return nil, statuserr.Wrap(fmt.Errorf("claudecode: request failed: %w", err), http.StatusBadGateway)
		}
		defer resp.Body.Close()

		if err := provider.CheckResponse(resp); err != nil {
			return nil, statuserr.Wrap(err, http.StatusBadGateway)
		}

		var payload anthropicbase.MessagesResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, statuserr.Wrap(fmt.Errorf("claudecode: decode response: %w", err), http.StatusBadGateway)
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
		return nil, statuserr.Wrap(fmt.Errorf("claudecode: stream request failed: %w", err), http.StatusBadGateway)
	}
	if err := provider.CheckResponse(resp); err != nil {
		resp.Body.Close()
		return nil, statuserr.Wrap(err, http.StatusBadGateway)
	}

	sr, sw := schema.Pipe[*schema.Message](16)
	go anthropicbase.ReadMessageStream(resp.Body, sw, "claudecode")
	return sr, nil
}

func (p *Provider) CreateResponses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	return provider.CreateResponsesViaChat(ctx, p, req)
}

func (p *Provider) StreamResponses(ctx context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	return provider.StreamResponsesViaChat(ctx, p, req)
}

func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.ProviderConfig.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("claudecode: build request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq, newRequestSession()); err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("claudecode: request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := provider.CheckResponse(resp); err != nil {
		return nil, err
	}

	var modelsResp struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("claudecode: decode models: %w", err)
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
	session := newRequestSession()
	body, err := p.buildRequestPayload(state, stream, session)
	if err != nil {
		return nil, err
	}

	url := p.ProviderConfig.BaseURL + "/v1/messages?beta=true"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("claudecode: build request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq, session); err != nil {
		return nil, err
	}
	return httpReq, nil
}

func (p *Provider) setHeaders(ctx context.Context, req *http.Request, session requestSession) error {
	auth := authFromContextOrConfig(ctx, p.ProviderConfig.APIKey, p.authMode())
	if auth.authorization == "" && auth.apiKey == "" {
		return fmt.Errorf("claudecode: missing upstream credential")
	}

	if auth.authorization != "" {
		req.Header.Set("Authorization", auth.authorization)
	}
	if auth.apiKey != "" {
		req.Header.Set("x-api-key", auth.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("anthropic-beta", anthropicBeta)
	req.Header.Set("User-Agent", defaultClaudeCodeUserAgent)
	req.Header.Set("x-claude-code-session-id", session.SessionID)
	for k, v := range p.ProviderConfig.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
	return nil
}

func (p *Provider) buildRequestPayload(state *provider.ChatRequestState, stream bool, session requestSession) ([]byte, error) {
	msgReq := buildMessagesRequest(state, stream, session)
	body, err := json.Marshal(msgReq)
	if err != nil {
		return nil, fmt.Errorf("claudecode: marshal request: %w", err)
	}
	return body, nil
}

type authHeader struct {
	authorization string
	apiKey        string
}

func authFromContextOrConfig(ctx context.Context, fallback string, authMode string) authHeader {
	mode := strings.TrimSpace(strings.ToLower(authMode))
	if mode == "" {
		mode = authModeBearer
	}

	if cred, ok := provider.CredentialFromContext(ctx); ok && cred != nil {
		if cred.Type == credentialmgr.TypeCLIAuthToken && cred.Metadata != nil {
			if token, _ := cred.Metadata["access_token"].(string); strings.TrimSpace(token) != "" {
				return authHeader{
					authorization: "Bearer " + strings.TrimSpace(token),
				}
			}
		}
		if cred.Type == credentialmgr.TypeAPIKey && strings.TrimSpace(cred.APIKey()) != "" {
			if mode == authModeAPIKey {
				return authHeader{
					apiKey: strings.TrimSpace(cred.APIKey()),
				}
			}
			return authHeader{
				authorization: "Bearer " + strings.TrimSpace(cred.APIKey()),
			}
		}
	}

	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return authHeader{}
	}
	if strings.HasPrefix(fallback, oauthTokenPrefix) {
		return authHeader{
			authorization: "Bearer " + fallback,
		}
	}
	if mode == authModeAPIKey {
		return authHeader{
			apiKey: fallback,
		}
	}
	return authHeader{
		authorization: "Bearer " + fallback,
	}
}

func (p *Provider) authMode() string {
	if p == nil || p.ProviderConfig.Options == nil {
		return ""
	}
	mode, _ := p.ProviderConfig.Options["auth_mode"].(string)
	return strings.ToLower(strings.TrimSpace(mode))
}

type requestSession struct {
	SessionID   string
	DeviceID    string
	BillingCode string
}

func newRequestSession() requestSession {
	sessionID := uuid.NewString()
	sum := sha256.Sum256([]byte(sessionID))
	return requestSession{
		SessionID:   sessionID,
		DeviceID:    fmt.Sprintf("%x", sum[:]),
		BillingCode: fmt.Sprintf("%x", sum[:])[:5],
	}
}

func buildMessagesRequest(state *provider.ChatRequestState, stream bool, session requestSession) *messagesRequest {
	return anthropicbase.BuildMessagesRequest(state, anthropicbase.BuildMessagesOptions{
		DefaultMaxTokens:  defaultClaudeCodeMaxTokens,
		Stream:            stream,
		System:            buildSystemBlocks(session),
		Metadata:          buildRequestMetadata(session),
		Thinking:          &thinkingConfig{Type: "adaptive"},
		ContextManagement: &contextManagement{Edits: []contextManagementEdit{{Type: "clear_thinking_20251015", Keep: "all"}}},
		OutputConfig:      &outputConfig{Effort: requestEffort(state), Format: anthropicbase.OutputFormatFromState(state)},
		CacheUserText:     true,
	})
}

// requestEffort derives Claude's output_config.effort from the inbound reasoning
// effort, preferring the chat-completions field over the Responses reasoning
// object. The result is normalized onto the set Claude accepts.
func requestEffort(state *provider.ChatRequestState) string {
	if state == nil {
		return defaultClaudeCodeEffort
	}
	if extra := provider.ChatExtraFieldsFromOptions(state.Options...); extra != nil {
		if effort := strings.TrimSpace(extra.ReasoningEffort); effort != "" {
			return normalizeEffort(effort)
		}
		if effort := effortFromReasoning(extra.Reasoning); effort != "" {
			return normalizeEffort(effort)
		}
	}
	if ctx := provider.ResponsesRequestContextFromOptions(state.Options...); ctx != nil {
		if effort := effortFromReasoning(ctx.Reasoning); effort != "" {
			return normalizeEffort(effort)
		}
	}
	return defaultClaudeCodeEffort
}

// normalizeEffort maps an inbound reasoning-effort value onto the set Claude's
// output_config.effort accepts (low/medium/high). OpenAI's "minimal" has no
// Claude equivalent and folds to "low"; empty or unrecognized values fall back
// to the Claude Code default.
func normalizeEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "minimal":
		return "low"
	default:
		return defaultClaudeCodeEffort
	}
}

func effortFromReasoning(reasoning map[string]any) string {
	if len(reasoning) == 0 {
		return ""
	}
	effort, _ := reasoning["effort"].(string)
	return strings.TrimSpace(effort)
}

// buildRequestMetadata emits the metadata.user_id payload exactly as the Claude
// Code CLI does. The value is an opaque end-user identifier, so request fields
// without a Messages API equivalent are intentionally not smuggled in here.
func buildRequestMetadata(session requestSession) *requestMetadata {
	userIDPayload, _ := json.Marshal(map[string]string{
		"device_id":    session.DeviceID,
		"account_uuid": "",
		"session_id":   session.SessionID,
	})
	return &requestMetadata{
		UserID: string(userIDPayload),
	}
}

func buildSystemBlocks(session requestSession) []systemBlock {
	return []systemBlock{
		{
			Type: "text",
			Text: defaultClaudeCodeBillingHeaderPrefix + session.BillingCode + ";",
		},
		{
			Type:         "text",
			Text:         "You are a Claude agent, built on Anthropic's Claude Agent SDK.",
			CacheControl: &cacheControl{Type: "ephemeral"},
		},
		{
			Type: "text",
			Text: defaultClaudeCodeSystemPrompt,
		},
	}
}

// --- Wire type aliases retained for claudecode package tests. ---

type messagesRequest = anthropicbase.MessagesRequest
type messageItem = anthropicbase.MessageItem
type contentBlock = anthropicbase.ContentBlock
type imageSource = anthropicbase.ImageSource
type systemBlock = anthropicbase.SystemBlock
type cacheControl = anthropicbase.CacheControl
type toolDef = anthropicbase.ToolDef
type requestMetadata = anthropicbase.RequestMetadata
type thinkingConfig = anthropicbase.ThinkingConfig
type contextManagement = anthropicbase.ContextManagement
type contextManagementEdit = anthropicbase.ContextManagementEdit
type outputConfig = anthropicbase.OutputConfig
type responseBlock = anthropicbase.ResponseBlock
type messagesResponse = anthropicbase.MessagesResponse

var (
	_ provider.Provider          = (*Provider)(nil)
	_ provider.ResponsesProvider = (*Provider)(nil)
)
