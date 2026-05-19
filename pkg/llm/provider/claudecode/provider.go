package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
	anthropicBeta    = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14"
	userAgent        = "claude-cli/2.1.63 (external, cli)"
)

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
	if err := p.setHeaders(ctx, httpReq); err != nil {
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
		Tools:           false,
		Vision:          false,
		ContextWindow:   200000,
		MaxOutputTokens: 8192,
	}
}

func (p *Provider) Config() provider.ProviderConfig {
	return p.ProviderConfig
}

func (p *Provider) newMessagesRequest(ctx context.Context, state *provider.ChatRequestState, stream bool) (*http.Request, error) {
	msgReq := buildMessagesRequest(state, stream)
	body, err := json.Marshal(msgReq)
	if err != nil {
		return nil, fmt.Errorf("claudecode: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ProviderConfig.BaseURL+"/v1/messages?beta=true", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("claudecode: build request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, err
	}
	return httpReq, nil
}

func (p *Provider) setHeaders(ctx context.Context, req *http.Request) error {
	token := accessTokenFromContextOrConfig(ctx, p.ProviderConfig.APIKey)
	if token == "" {
		return fmt.Errorf("claudecode: missing OAuth access token")
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("anthropic-beta", anthropicBeta)
	req.Header.Set("x-app", "cli")
	req.Header.Set("User-Agent", userAgent)
	for k, v := range p.ProviderConfig.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
	return nil
}

func accessTokenFromContextOrConfig(ctx context.Context, fallback string) string {
	if cred, ok := provider.CredentialFromContext(ctx); ok && cred != nil {
		if cred.Type == credentialmgr.TypeCLIAuthToken && cred.Metadata != nil {
			if token, _ := cred.Metadata["access_token"].(string); strings.TrimSpace(token) != "" {
				return strings.TrimSpace(token)
			}
		}
	}
	return strings.TrimSpace(fallback)
}

func buildMessagesRequest(state *provider.ChatRequestState, stream bool) *messagesRequest {
	model := state.ModelName
	maxTokens := 4096
	if state.CommonOptions != nil {
		if state.CommonOptions.MaxTokens != nil && *state.CommonOptions.MaxTokens > 0 {
			maxTokens = *state.CommonOptions.MaxTokens
		}
	}

	req := &messagesRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  make([]messageItem, 0, len(state.Messages)),
		Stream:    stream,
	}
	if state.CommonOptions != nil {
		if state.CommonOptions.Temperature != nil {
			req.Temperature = float64(*state.CommonOptions.Temperature)
		}
		if state.CommonOptions.TopP != nil {
			req.TopP = float64(*state.CommonOptions.TopP)
		}
	}

	var systemParts []string
	for _, msg := range state.Messages {
		if msg == nil {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		switch msg.Role {
		case schema.System:
			systemParts = append(systemParts, text)
		case schema.Assistant:
			req.Messages = append(req.Messages, messageItem{Role: "assistant", Content: text})
		case schema.User:
			req.Messages = append(req.Messages, messageItem{Role: "user", Content: text})
		default:
			req.Messages = append(req.Messages, messageItem{Role: "user", Content: text})
		}
	}
	req.System = strings.Join(systemParts, "\n\n")
	return req
}

func readMessageStream(body io.ReadCloser, sw *schema.StreamWriter[*schema.Message]) {
	defer body.Close()
	defer sw.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

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
			if err := emitStreamEvent(eventName, data.String(), sw); err != nil {
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

func emitStreamEvent(eventName string, payload string, sw *schema.StreamWriter[*schema.Message]) error {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil
	}

	switch eventName {
	case "content_block_delta":
		var event struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("claudecode: decode stream delta: %w", err)
		}
		if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
			sw.Send(&schema.Message{Role: schema.Assistant, Content: event.Delta.Text}, nil)
		}
	case "message_delta":
		var event struct {
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("claudecode: decode message delta: %w", err)
		}
		if event.Usage.OutputTokens > 0 {
			sw.Send(&schema.Message{
				Role:    schema.Assistant,
				Content: "",
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: "stop",
					Usage: &schema.TokenUsage{
						CompletionTokens: event.Usage.OutputTokens,
					},
				},
			}, nil)
		}
	}
	return nil
}

type messagesRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Messages    []messageItem `json:"messages"`
	System      string        `json:"system,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	TopP        float64       `json:"top_p,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type messageItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (r *messagesResponse) toChatResponse() *provider.ChatResponse {
	if r == nil {
		return &provider.ChatResponse{}
	}

	var parts []string
	for _, block := range r.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}

	return &provider.ChatResponse{
		Message: &schema.Message{
			Role:    schema.Assistant,
			Content: strings.Join(parts, "\n"),
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

var _ provider.Provider = (*Provider)(nil)
