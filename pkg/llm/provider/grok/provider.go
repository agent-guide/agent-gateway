// Package grok implements the xAI Grok provider (OpenAI-compatible API)
// on top of the eino-ext openai component.
package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	provider.RegisterProviderFactory("grok", New)
}

type Provider struct {
	*openaibase.Base
	capabilities    provider.ProviderCapabilities
	reasoningEffort string
}

// New creates a new xAI Grok provider using xAI's OpenAI-compatible API.
//
// Optional config.Options keys:
//   - "reasoning_effort": none|low|medium|high|xhigh → default
//     `reasoning_effort`. Per-request reasoning fields override this option.
//     Aliases: "minimal" → "low", "max" → "xhigh". Omitted when unset so the
//     selected model uses its upstream default. `none` is forwarded only for
//     models that accept it (currently grok-4.3); other models omit the field
//     instead of sending a rejected value. Do not set a provider-level default
//     on a multi-model route: some models reject the field, while
//     grok-4.20-multi-agent accepts it with agent-count semantics.
func New(config provider.ProviderConfig) (provider.Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.x.ai/v1"
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	config.Network.Defaults()
	if _, err := provider.CompactModeFromOptions(config.Options); err != nil {
		return nil, err
	}
	if _, ok := config.Options["search_mode"]; ok {
		return nil, fmt.Errorf("grok: option search_mode is not supported; send Responses tools such as web_search or x_search on POST /v1/responses")
	}
	reasoningEffort, err := reasoningEffortFromOptions(config.Options)
	if err != nil {
		return nil, err
	}
	capabilities, err := provider.CapabilitiesFromOptions(config.Options, defaultCapabilities(),
		provider.CapabilityContextWindow,
		provider.CapabilityMaxOutputTokens,
		provider.CapabilityVision,
	)
	if err != nil {
		return nil, fmt.Errorf("grok: %w", err)
	}

	return &Provider{
		Base:            openaibase.NewBase(config),
		capabilities:    capabilities,
		reasoningEffort: reasoningEffort,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.ensureBase()
	return provider.RetryProviderCall(p.ProviderConfig.Network, func() (*provider.ChatResponse, error) {
		chatModel, messages, opts, err := p.newChatModel(ctx, req)
		if err != nil {
			return nil, err
		}
		msg, err := chatModel.Generate(ctx, messages, opts...)
		if err != nil {
			return nil, statuserr.Wrap(openaibase.NormalizeError(err), 502)
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
		return nil, statuserr.Wrap(openaibase.NormalizeError(err), 502)
	}
	return stream, nil
}

func (p *Provider) CreateResponses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	p.ensureBase()
	return p.Base.DoCreateResponses(ctx, shapeResponsesRequest(req, p.ProviderConfig.DefaultModel, p.reasoningEffort))
}

func (p *Provider) StreamResponses(ctx context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	p.ensureBase()
	return p.Base.DoStreamResponses(ctx, shapeResponsesRequest(req, p.ProviderConfig.DefaultModel, p.reasoningEffort))
}

func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	p.ensureBase()
	models, err := p.Base.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	capabilities := provider.ModelCapabilitiesFromProviderSummary(p.Capabilities())
	for i := range models {
		models[i].Capabilities = capabilities
	}
	return models, nil
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
	extraFields := provider.ChatCompletionsExtraFieldsFromOptions(provider.GrokChatCompletionsFields, state.Options...)
	if p.CCCompat {
		provider.StripCCUnsupportedChatFields(extraFields)
	}
	extraFields = applyReasoningEffort(extraFields, state.ModelName, p.reasoningEffort, requestOmitsReasoningEffort(state.Options))
	if len(extraFields) > 0 {
		opts = append(opts, einoopenai.WithExtraFields(extraFields))
	}
	opts = append(opts, einoopenai.WithRequestPayloadModifier(stripUnsupportedSamplingFields))

	return chatModel, state.Messages, opts, nil
}

func (p *Provider) Capabilities() provider.ProviderCapabilities {
	if p.capabilities != (provider.ProviderCapabilities{}) {
		return p.capabilities
	}
	return defaultCapabilities()
}

func defaultCapabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{
		Streaming:       true,
		Tools:           true,
		Vision:          true,
		ContextWindow:   500000,
		MaxOutputTokens: 128000,
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

func requestOmitsReasoningEffort(opts []einomodel.Option) bool {
	if extra := provider.ChatExtraFieldsFromOptions(opts...); extra != nil {
		if omittedReasoningEffort(extra.ReasoningEffort) {
			return true
		}
		if effort, _ := extra.Reasoning["effort"].(string); omittedReasoningEffort(effort) {
			return true
		}
		if typ, _ := extra.Thinking["type"].(string); strings.EqualFold(strings.TrimSpace(typ), "disabled") {
			return true
		}
	}
	if ctx := provider.ResponsesRequestContextFromOptions(opts...); ctx != nil {
		if effort, _ := ctx.Reasoning["effort"].(string); omittedReasoningEffort(effort) {
			return true
		}
	}
	return false
}

func applyReasoningEffort(extraFields map[string]any, model, defaultEffort string, omitRequested bool) map[string]any {
	effort, _ := extraFields["reasoning_effort"].(string)
	if strings.TrimSpace(effort) == "" {
		if omitRequested {
			effort = "none"
		} else {
			effort = defaultEffort
		}
	}
	normalized, ok := normalizeReasoningEffort(effort)
	if !ok || normalized == "" {
		delete(extraFields, "reasoning_effort")
		return extraFields
	}
	if normalized == "none" && !supportsNoneEffort(model) {
		delete(extraFields, "reasoning_effort")
		return extraFields
	}
	return provider.MergeExtraFields(extraFields, map[string]any{"reasoning_effort": normalized})
}

func shapeResponsesRequest(req *provider.ResponsesRequest, defaultModel, defaultEffort string) *provider.ResponsesRequest {
	if req == nil {
		return &provider.ResponsesRequest{Model: defaultModel}
	}
	shaped := *req
	if strings.TrimSpace(shaped.Model) == "" {
		shaped.Model = defaultModel
	}
	if req.Reasoning != nil {
		shaped.Reasoning = cloneMap(req.Reasoning)
	}
	effort, _ := shaped.Reasoning["effort"].(string)
	extra := map[string]any{}
	if strings.TrimSpace(effort) != "" {
		extra["reasoning_effort"] = effort
	}
	extra = applyReasoningEffort(extra, shaped.Model, defaultEffort, omittedReasoningEffort(effort))
	if extraEffort, _ := extra["reasoning_effort"].(string); extraEffort != "" {
		if shaped.Reasoning == nil {
			shaped.Reasoning = map[string]any{}
		}
		shaped.Reasoning["effort"] = extraEffort
	} else if shaped.Reasoning != nil {
		delete(shaped.Reasoning, "effort")
		if len(shaped.Reasoning) == 0 {
			shaped.Reasoning = nil
		}
	}
	return &shaped
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func omittedReasoningEffort(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "disabled":
		return true
	default:
		return false
	}
}

func supportsNoneEffort(model string) bool {
	name := canonicalGrokModel(model)
	return strings.HasPrefix(name, "grok-4.3") || strings.Contains(name, "non-reasoning")
}

func canonicalGrokModel(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// logprobs and top_logprobs are omitted: xAI documents them as silently
// ignored on grok-4.20 and newer, not as request errors.
var samplingFieldsUnsupportedByReasoning = []string{"stop", "presence_penalty", "frequency_penalty"}

func stripUnsupportedSamplingFields(_ context.Context, _ []*schema.Message, rawBody []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(rawBody))
	dec.UseNumber()
	var payload map[string]any
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("grok: decode chat payload: %w", err)
	}
	model, _ := payload["model"].(string)
	if !rejectsSamplingFields(model, payload) {
		return rawBody, nil
	}
	stripped := make([]string, 0, len(samplingFieldsUnsupportedByReasoning))
	for _, key := range samplingFieldsUnsupportedByReasoning {
		if _, ok := payload[key]; !ok {
			continue
		}
		delete(payload, key)
		stripped = append(stripped, key)
	}
	if len(stripped) == 0 {
		return rawBody, nil
	}
	zap.L().Debug("grok: stripped sampling fields unsupported by reasoning models",
		zap.String("model", model),
		zap.Strings("fields", stripped),
	)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, fmt.Errorf("grok: encode chat payload: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

// rejectsSamplingFields reports whether stop/presence_penalty/frequency_penalty
// must be stripped. Non-reasoning model ids and grok-4.3 with effort none keep
// those fields; unknown names fail closed so new reasoning models do not 400.
func rejectsSamplingFields(model string, payload map[string]any) bool {
	if strings.Contains(canonicalGrokModel(model), "non-reasoning") {
		return false
	}
	if omittedReasoningEffort(payloadReasoningEffort(payload)) && supportsNoneEffort(model) {
		return false
	}
	return true
}

func payloadReasoningEffort(payload map[string]any) string {
	if effort, _ := payload["reasoning_effort"].(string); strings.TrimSpace(effort) != "" {
		return effort
	}
	reasoning, _ := payload["reasoning"].(map[string]any)
	effort, _ := reasoning["effort"].(string)
	return effort
}

func reasoningEffortFromOptions(opts map[string]any) (string, error) {
	raw, ok := opts["reasoning_effort"]
	if !ok {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("grok: option reasoning_effort must be a string")
	}
	normalized, ok := normalizeReasoningEffort(value)
	if !ok {
		return "", fmt.Errorf("grok: option reasoning_effort must be one of none, low, medium, high, xhigh")
	}
	return normalized, nil
}

func normalizeReasoningEffort(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", true
	}
	switch normalized {
	case "minimal":
		return "low", true
	case "max":
		return "xhigh", true
	case "none", "low", "medium", "high", "xhigh":
		return normalized, true
	default:
		return "", false
	}
}

var (
	_ provider.Provider          = (*Provider)(nil)
	_ provider.ResponsesProvider = (*Provider)(nil)
)
