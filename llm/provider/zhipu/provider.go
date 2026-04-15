// Package zhipu implements the Zhipu BigModel provider.
package zhipu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

const (
	anthropicVersion = "2023-06-01"
	minMaxTokens     = 1024
)

func init() {
	provider.RegisterProviderFactory("zhipu", New)
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	provider.ProviderConfig
	client *http.Client
}

// New creates a new Zhipu provider using BigModel's Anthropic-compatible API.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://open.bigmodel.cn/api/anthropic"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.Network.Defaults()

	return &Provider{
		ProviderConfig: config,
		client: &http.Client{
			Timeout: config.Network.Timeout(),
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.zhipu",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	if err := provider.ValidateProviderName(&p.ProviderConfig, "zhipu"); err != nil {
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
	return provider.UnmarshalCaddyfileConfig(d, &p.ProviderConfig)
}

func (p *Provider) Generate(ctx context.Context, req *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	return provider.RetryGenerate(p.ProviderConfig.Network, func() (*provider.GenerateResponse, error) {
		return p.generate(ctx, req)
	})
}

func (p *Provider) Stream(ctx context.Context, req *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	return p.stream(ctx, req)
}

// ListModels fetches available models from GET /v1/models.
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(p.ProviderConfig.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("zhipu: build request: %w", err)
	}
	p.setHeaders(httpReq, p.ProviderConfig.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("zhipu: request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := provider.CheckResponse(resp); err != nil {
		return nil, err
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("zhipu: decode models: %w", err)
	}

	out := make([]provider.ModelInfo, len(modelsResp.Data))
	for i, m := range modelsResp.Data {
		out[i] = provider.ModelInfo{ID: m.ID, Name: m.DisplayName}
	}
	return out, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           false,
		Vision:          false,
		ContextWindow:   128000,
		MaxOutputTokens: 8192,
	}
}

func (p *Provider) Config() provider.ProviderConfig {
	return p.ProviderConfig
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messageRequest struct {
	Model         string    `json:"model"`
	MaxTokens     int       `json:"max_tokens"`
	Messages      []message `json:"messages"`
	System        string    `json:"system,omitempty"`
	Temperature   *float64  `json:"temperature,omitempty"`
	TopP          *float64  `json:"top_p,omitempty"`
	StopSequences []string  `json:"stop_sequences,omitempty"`
	Stream        *bool     `json:"stream,omitempty"`
}

type messageResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type streamEvent struct {
	Type         string       `json:"type"`
	ContentBlock contentBlock `json:"content_block"`
	Delta        struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (p *Provider) generate(ctx context.Context, req *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	state, payload, err := p.newPayload(ctx, req, false)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("zhipu: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(state.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("zhipu: build request: %w", err)
	}
	p.setHeaders(httpReq, state.APIKey)

	resp, err := provider.BuildHTTPClient(p.ProviderConfig, nil, state.Credential).Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("zhipu: request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := provider.CheckResponse(resp); err != nil {
		if p.debugRequestEnabled() {
			return nil, fmt.Errorf("%w; request_body=%s", err, string(body))
		}
		return nil, err
	}

	var out messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("zhipu: decode response: %w", err)
	}

	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: contentText(out.Content),
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: out.StopReason,
			Usage: &schema.TokenUsage{
				PromptTokens:     out.Usage.InputTokens,
				CompletionTokens: out.Usage.OutputTokens,
				TotalTokens:      out.Usage.InputTokens + out.Usage.OutputTokens,
			},
		},
	}
	return &provider.GenerateResponse{Message: msg}, nil
}

func (p *Provider) stream(ctx context.Context, req *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	state, payload, err := p.newPayload(ctx, req, true)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("zhipu: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(state.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("zhipu: build request: %w", err)
	}
	p.setHeaders(httpReq, state.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := provider.BuildHTTPClient(p.ProviderConfig, nil, state.Credential).Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("zhipu: request failed: %w", err)
	}

	if err := provider.CheckResponse(resp); err != nil {
		resp.Body.Close()
		if p.debugRequestEnabled() {
			return nil, fmt.Errorf("%w; request_body=%s", err, string(body))
		}
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer resp.Body.Close()
		defer sw.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}

			var event streamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				_ = sw.Send(nil, fmt.Errorf("zhipu: decode stream event: %w", err))
				return
			}

			msg := streamMessage(event)
			if msg == nil {
				continue
			}
			if sw.Send(msg, nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			_ = sw.Send(nil, fmt.Errorf("zhipu: read stream: %w", err))
		}
	}()

	return sr, nil
}

func (p *Provider) newPayload(ctx context.Context, req *provider.GenerateRequest, stream bool) (*provider.ChatRequestState, messageRequest, error) {
	state, err := provider.ResolveChatRequest(ctx, p.ProviderConfig, req)
	if err != nil {
		return nil, messageRequest{}, err
	}

	payload := messageRequest{
		Model:     state.ModelName,
		MaxTokens: 4096,
	}
	if stream {
		payload.Stream = &stream
	}
	if state.CommonOptions.MaxTokens != nil && *state.CommonOptions.MaxTokens > 0 {
		payload.MaxTokens = *state.CommonOptions.MaxTokens
	}
	if payload.MaxTokens < minMaxTokens {
		payload.MaxTokens = minMaxTokens
	}
	if state.CommonOptions.Temperature != nil {
		v := normalizeFloat(float64(*state.CommonOptions.Temperature))
		payload.Temperature = &v
	}
	if state.CommonOptions.TopP != nil {
		v := normalizeFloat(float64(*state.CommonOptions.TopP))
		payload.TopP = &v
	}
	if len(state.CommonOptions.Stop) > 0 {
		payload.StopSequences = state.CommonOptions.Stop
	}

	var system []string
	for _, msg := range state.Messages {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.System:
			if msg.Content != "" {
				system = append(system, msg.Content)
			}
		case schema.User:
			payload.Messages = append(payload.Messages, message{Role: "user", Content: msg.Content})
		case schema.Assistant:
			payload.Messages = append(payload.Messages, message{Role: "assistant", Content: msg.Content})
		default:
			return nil, messageRequest{}, fmt.Errorf("zhipu: unsupported message role %q", msg.Role)
		}
	}
	if len(system) > 0 {
		payload.System = strings.Join(system, "\n\n")
	}
	if len(payload.Messages) == 0 {
		return nil, messageRequest{}, fmt.Errorf("zhipu: at least one user or assistant message is required")
	}

	return state, payload, nil
}

func (p *Provider) setHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	for k, v := range p.ProviderConfig.Network.ExtraHeaders {
		req.Header.Set(k, v)
	}
}

func (p *Provider) debugRequestEnabled() bool {
	v, ok := p.ProviderConfig.Options["debug_request"]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true") || x == "1" || strings.EqualFold(x, "yes")
	default:
		return false
	}
}

func normalizeFloat(v float64) float64 {
	return math.Round(v*1_000_000) / 1_000_000
}

func contentText(blocks []contentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if block.Text != "" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

func streamMessage(event streamEvent) *schema.Message {
	switch event.Type {
	case "content_block_start":
		if event.ContentBlock.Text == "" {
			return nil
		}
		return &schema.Message{Role: schema.Assistant, Content: event.ContentBlock.Text}
	case "content_block_delta":
		if event.Delta.Text == "" {
			return nil
		}
		return &schema.Message{Role: schema.Assistant, Content: event.Delta.Text}
	case "message_delta":
		if event.Delta.StopReason == "" && event.Usage.OutputTokens == 0 {
			return nil
		}
		return &schema.Message{
			Role: schema.Assistant,
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: event.Delta.StopReason,
				Usage: &schema.TokenUsage{
					CompletionTokens: event.Usage.OutputTokens,
					TotalTokens:      event.Usage.InputTokens + event.Usage.OutputTokens,
				},
			},
		}
	default:
		return nil
	}
}

var (
	_ caddy.Provisioner     = (*Provider)(nil)
	_ caddyfile.Unmarshaler = (*Provider)(nil)
	_ provider.Provider     = (*Provider)(nil)
)
