package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

const (
	defaultBaseURL                       = "https://api.anthropic.com"
	anthropicVersion                     = "2023-06-01"
	anthropicBeta                        = "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24"
	oauthTokenPrefix                     = "sk-ant-oat-"
	authModeBearer                       = "bearer_only"
	authModeAPIKey                       = "api_key"
	defaultClaudeCodeMaxTokens           = 32000
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

		var payload messagesResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, statuserr.Wrap(fmt.Errorf("claudecode: decode response: %w", err), http.StatusBadGateway)
		}
		return payload.toChatResponse(), nil
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
	go readMessageStream(resp.Body, sw)
	return sr, nil
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
	model := state.ModelName
	maxTokens := defaultClaudeCodeMaxTokens
	if state.CommonOptions != nil {
		if state.CommonOptions.MaxTokens != nil && *state.CommonOptions.MaxTokens > 0 {
			maxTokens = *state.CommonOptions.MaxTokens
		}
	}

	req := &messagesRequest{
		Model:             model,
		MaxTokens:         maxTokens,
		Messages:          make([]messageItem, 0, len(state.Messages)),
		System:            buildSystemBlocks(session),
		Metadata:          buildRequestMetadata(session),
		Thinking:          &thinkingConfig{Type: "adaptive"},
		ContextManagement: &contextManagement{Edits: []contextManagementEdit{{Type: "clear_thinking_20251015", Keep: "all"}}},
		OutputConfig:      &outputConfig{Effort: "high"},
		Stream:            stream,
	}

	if state.CommonOptions != nil {
		if state.CommonOptions.Temperature != nil {
			req.Temperature = float64(*state.CommonOptions.Temperature)
		}
		if state.CommonOptions.TopP != nil {
			req.TopP = float64(*state.CommonOptions.TopP)
		}
		if len(state.CommonOptions.Stop) > 0 {
			req.StopSequences = state.CommonOptions.Stop
		}
		req.Tools = toolInfosToToolDefs(state.CommonOptions.Tools)
		if state.CommonOptions.ToolChoice != nil {
			req.ToolChoice = buildAnthropicToolChoice(state.CommonOptions.ToolChoice, state.CommonOptions.AllowedToolNames)
		}
	} else {
		req.Tools = []toolDef{}
	}

	if chatOpts := provider.GetChatOptions(state.Options...); chatOpts.TopK > 0 {
		req.TopK = chatOpts.TopK
	}

	req.Messages = convertMessages(state.Messages, req)
	return req
}

func toolInfosToToolDefs(tools []*schema.ToolInfo) []toolDef {
	out := make([]toolDef, 0, len(tools))
	for _, ti := range tools {
		if ti == nil {
			continue
		}
		js, err := ti.ToJSONSchema()
		if err != nil {
			continue
		}
		raw, err := json.Marshal(js)
		if err != nil {
			continue
		}
		out = append(out, toolDef{Name: ti.Name, Description: ti.Desc, InputSchema: raw})
	}
	return out
}

func buildAnthropicToolChoice(tc *schema.ToolChoice, allowedNames []string) json.RawMessage {
	if tc == nil {
		return nil
	}
	switch *tc {
	case schema.ToolChoiceForbidden:
		return json.RawMessage(`{"type":"none"}`)
	case schema.ToolChoiceForced:
		if len(allowedNames) > 0 {
			b, _ := json.Marshal(map[string]string{"type": "tool", "name": allowedNames[0]})
			return b
		}
		return json.RawMessage(`{"type":"any"}`)
	default: // ToolChoiceAllowed
		return json.RawMessage(`{"type":"auto"}`)
	}
}

// convertMessages converts eino schema messages to Anthropic messageItems,
// appending user system prompt blocks and grouping consecutive Tool messages.
func convertMessages(msgs []*schema.Message, req *messagesRequest) []messageItem {
	var out []messageItem
	i := 0
	for i < len(msgs) {
		msg := msgs[i]
		if msg == nil {
			i++
			continue
		}
		switch msg.Role {
		case schema.System:
			req.System = append(req.System, systemBlock{Type: "text", Text: strings.TrimSpace(msg.Content)})
			i++
		case schema.Assistant:
			out = append(out, convertAssistantMessage(msg))
			i++
		case schema.Tool:
			// Group consecutive Tool messages into one user message with tool_result blocks.
			j := i
			for j < len(msgs) && msgs[j] != nil && msgs[j].Role == schema.Tool {
				j++
			}
			out = append(out, convertToolResultMessages(msgs[i:j]))
			i = j
		default:
			item := convertUserMessage(msg)
			if len(item.Content) > 0 {
				out = append(out, item)
			}
			i++
		}
	}
	return out
}

func convertAssistantMessage(msg *schema.Message) messageItem {
	var blocks []contentBlock
	text := strings.TrimSpace(msg.Content)
	if text != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: text})
	}
	for _, tc := range msg.ToolCalls {
		input := json.RawMessage(tc.Function.Arguments)
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		blocks = append(blocks, contentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return messageItem{Role: "assistant", Content: blocks}
}

func convertToolResultMessages(msgs []*schema.Message) messageItem {
	var blocks []contentBlock
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		block := contentBlock{
			Type:      "tool_result",
			ToolUseID: msg.ToolCallID,
			Content:   msg.Content,
		}
		blocks = append(blocks, block)
	}
	return messageItem{Role: "user", Content: blocks}
}

func convertUserMessage(msg *schema.Message) messageItem {
	var blocks []contentBlock

	// Handle multimodal content.
	if len(msg.UserInputMultiContent) > 0 {
		for _, part := range msg.UserInputMultiContent {
			switch part.Type {
			case schema.ChatMessagePartTypeText:
				if strings.TrimSpace(part.Text) != "" {
					blocks = append(blocks, contentBlock{
						Type:         "text",
						Text:         part.Text,
						CacheControl: &cacheControl{Type: "ephemeral"},
					})
				}
			case schema.ChatMessagePartTypeImageURL:
				if part.Image != nil {
					src := &imageSource{}
					if part.Image.Base64Data != nil {
						src.Type = "base64"
						src.Data = *part.Image.Base64Data
						src.MediaType = part.Image.MIMEType
					} else if part.Image.URL != nil {
						src.Type = "url"
						src.URL = *part.Image.URL
					}
					blocks = append(blocks, contentBlock{Type: "image", Source: src})
				}
			}
		}
		return messageItem{Role: "user", Content: blocks}
	}

	// Plain text content.
	text := strings.TrimSpace(msg.Content)
	if text != "" {
		blocks = append(blocks, contentBlock{
			Type:         "text",
			Text:         text,
			CacheControl: &cacheControl{Type: "ephemeral"},
		})
	}
	return messageItem{Role: "user", Content: blocks}
}

func buildRequestMetadata(session requestSession) requestMetadata {
	userIDPayload, _ := json.Marshal(map[string]string{
		"device_id":    session.DeviceID,
		"account_uuid": "",
		"session_id":   session.SessionID,
	})
	return requestMetadata{
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

// --- Wire types ---

type messagesRequest struct {
	Model             string             `json:"model"`
	MaxTokens         int                `json:"max_tokens"`
	Messages          []messageItem      `json:"messages"`
	System            []systemBlock      `json:"system,omitempty"`
	Tools             []toolDef          `json:"tools"`
	ToolChoice        json.RawMessage    `json:"tool_choice,omitempty"`
	Metadata          requestMetadata    `json:"metadata"`
	Thinking          *thinkingConfig    `json:"thinking,omitempty"`
	ContextManagement *contextManagement `json:"context_management,omitempty"`
	OutputConfig      *outputConfig      `json:"output_config,omitempty"`
	Temperature       float64            `json:"temperature,omitempty"`
	TopP              float64            `json:"top_p,omitempty"`
	TopK              int                `json:"top_k,omitempty"`
	StopSequences     []string           `json:"stop_sequences,omitempty"`
	Stream            bool               `json:"stream,omitempty"`
}

type messageItem struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
	// image
	Source *imageSource `json:"source,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type requestMetadata struct {
	UserID string `json:"user_id"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type contextManagement struct {
	Edits []contextManagementEdit `json:"edits,omitempty"`
}

type contextManagementEdit struct {
	Type string `json:"type"`
	Keep string `json:"keep"`
}

type outputConfig struct {
	Effort string `json:"effort"`
}

type responseBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type messagesResponse struct {
	Content    []responseBlock `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (r *messagesResponse) toChatResponse() *provider.ChatResponse {
	if r == nil {
		return &provider.ChatResponse{}
	}

	var textParts []string
	var toolCalls []schema.ToolCall
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				textParts = append(textParts, b.Text)
			}
		case "tool_use":
			inputStr := string(b.Input)
			if inputStr == "" {
				inputStr = "{}"
			}
			toolCalls = append(toolCalls, schema.ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      b.Name,
					Arguments: inputStr,
				},
			})
		}
	}

	return &provider.ChatResponse{
		Message: &schema.Message{
			Role:      schema.Assistant,
			Content:   strings.Join(textParts, "\n"),
			ToolCalls: toolCalls,
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: r.StopReason,
				Usage: &schema.TokenUsage{
					PromptTokens:     r.Usage.InputTokens,
					CompletionTokens: r.Usage.OutputTokens,
				},
			},
		},
	}
}

// streamState tracks in-progress tool_use blocks during SSE streaming.
type streamState struct {
	pendingToolCalls map[int]*pendingToolCall
}

type pendingToolCall struct {
	index int
	id    string
	name  string
	input strings.Builder
}

func readMessageStream(body io.ReadCloser, sw *schema.StreamWriter[*schema.Message]) {
	defer body.Close()
	defer sw.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	state := &streamState{pendingToolCalls: make(map[int]*pendingToolCall)}
	var eventName string
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			if err := emitStreamEvent(eventName, data.String(), sw, state); err != nil {
				sw.Send(nil, err)
				return
			}
			eventName = ""
			data.Reset()
		}
	}
	if err := scanner.Err(); err != nil {
		sw.Send(nil, fmt.Errorf("claudecode: read stream: %w", err))
	}
}

func emitStreamEvent(eventName string, payload string, sw *schema.StreamWriter[*schema.Message], state *streamState) error {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil
	}

	switch eventName {
	case "content_block_start":
		var event struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("claudecode: decode content_block_start: %w", err)
		}
		if event.ContentBlock.Type == "tool_use" {
			state.pendingToolCalls[event.Index] = &pendingToolCall{
				index: event.Index,
				id:    event.ContentBlock.ID,
				name:  event.ContentBlock.Name,
			}
		}

	case "content_block_delta":
		var event struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("claudecode: decode stream delta: %w", err)
		}
		switch event.Delta.Type {
		case "text_delta":
			if event.Delta.Text != "" {
				sw.Send(&schema.Message{Role: schema.Assistant, Content: event.Delta.Text}, nil)
			}
		case "input_json_delta":
			if ptc, ok := state.pendingToolCalls[event.Index]; ok {
				ptc.input.WriteString(event.Delta.PartialJSON)
			}
		}

	case "content_block_stop":
		var event struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("claudecode: decode content_block_stop: %w", err)
		}
		if ptc, ok := state.pendingToolCalls[event.Index]; ok {
			inputStr := ptc.input.String()
			if inputStr == "" {
				inputStr = "{}"
			}
			sw.Send(&schema.Message{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID:   ptc.id,
					Type: "function",
					Function: schema.FunctionCall{
						Name:      ptc.name,
						Arguments: inputStr,
					},
				}},
			}, nil)
			delete(state.pendingToolCalls, event.Index)
		}

	case "message_delta":
		var event struct {
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("claudecode: decode message delta: %w", err)
		}
		if event.Usage.OutputTokens > 0 || event.Delta.StopReason != "" {
			sw.Send(&schema.Message{
				Role:    schema.Assistant,
				Content: "",
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: event.Delta.StopReason,
					Usage: &schema.TokenUsage{
						CompletionTokens: event.Usage.OutputTokens,
					},
				},
			}, nil)
		}
	}
	return nil
}

var _ provider.Provider = (*Provider)(nil)
