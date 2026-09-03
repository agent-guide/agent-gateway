package grok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestGenerateUsesOpenAICompatibleAPI(t *testing.T) {
	resp, captured := generateAndCapture(t, nil)
	if resp == nil || resp.Message == nil || resp.Message.Content != "四" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("unexpected messages: %#v", captured["messages"])
	}
	if got := messages[0].(map[string]any)["role"]; got != "system" {
		t.Fatalf("first role = %#v, want system", got)
	}
	if got := messages[1].(map[string]any)["content"]; got != "2 + 2 等于几？" {
		t.Fatalf("content = %#v, want string content", got)
	}
	if got := captured["model"]; got != "grok-4.6" {
		t.Fatalf("model = %#v", got)
	}
	if got := captured["max_tokens"]; got != float64(128) {
		t.Fatalf("max_tokens = %#v", got)
	}
	if got := captured["temperature"]; got != 0.2 {
		t.Fatalf("temperature = %#v", got)
	}
	if _, ok := captured["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be omitted by default: %#v", captured["reasoning_effort"])
	}
	if _, ok := captured["search_parameters"]; ok {
		t.Fatalf("search_parameters should be omitted by default: %#v", captured["search_parameters"])
	}
}

func TestGenerateUsesConfiguredReasoningEffort(t *testing.T) {
	_, captured := generateAndCapture(t, map[string]any{"reasoning_effort": "high"})
	if got := captured["reasoning_effort"]; got != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", got)
	}
}

func TestGenerateAliasesConfiguredReasoningEffort(t *testing.T) {
	_, captured := generateAndCapture(t, map[string]any{"reasoning_effort": "max"})
	if got := captured["reasoning_effort"]; got != "xhigh" {
		t.Fatalf("reasoning_effort = %#v, want xhigh", got)
	}
}

func TestRequestReasoningOverridesConfiguredEffort(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.6",
		Messages: []*schema.Message{schema.UserMessage("2 + 2 等于几？")},
		Options: []einomodel.Option{
			provider.WithChatExtraFields(&provider.ChatExtraFields{
				ReasoningEffort: "low",
			}),
		},
	}
	_, captured := generateAndCaptureRequest(t, map[string]any{"reasoning_effort": "high"}, req)
	if got := captured["reasoning_effort"]; got != "low" {
		t.Fatalf("reasoning_effort = %#v, want low from request", got)
	}
}

func TestDisabledAnthropicThinkingOmitsReasoningEffort(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.6",
		Messages: []*schema.Message{schema.UserMessage("2 + 2 等于几？")},
		Options: []einomodel.Option{
			provider.WithChatExtraFields(&provider.ChatExtraFields{
				Thinking: map[string]any{"type": "disabled"},
			}),
		},
	}
	_, captured := generateAndCaptureRequest(t, map[string]any{"reasoning_effort": "high"}, req)
	if _, ok := captured["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be omitted when thinking is disabled: %#v", captured["reasoning_effort"])
	}
}

func TestRequestNoneEffortOmitsFieldOnUnsupportedModels(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.6",
		Messages: []*schema.Message{schema.UserMessage("2 + 2 等于几？")},
		Options: []einomodel.Option{
			provider.WithChatExtraFields(&provider.ChatExtraFields{
				ReasoningEffort: "none",
			}),
		},
	}
	_, captured := generateAndCaptureRequest(t, map[string]any{"reasoning_effort": "high"}, req)
	if _, ok := captured["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be omitted when request effort is none: %#v", captured["reasoning_effort"])
	}
}

func TestRequestNoneEffortForwardedOnNonReasoningModel(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4-1-fast-non-reasoning",
		Messages: []*schema.Message{schema.UserMessage("2 + 2 等于几？")},
		Options: []einomodel.Option{
			provider.WithChatExtraFields(&provider.ChatExtraFields{
				ReasoningEffort: "none",
			}),
		},
	}
	_, captured := generateAndCaptureRequest(t, nil, req)
	if got := captured["reasoning_effort"]; got != "none" {
		t.Fatalf("reasoning_effort = %#v, want none on non-reasoning models", got)
	}
}

func TestRequestNoneEffortForwardedOnGrok43(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.3",
		Messages: []*schema.Message{schema.UserMessage("2 + 2 等于几？")},
		Options: []einomodel.Option{
			provider.WithChatExtraFields(&provider.ChatExtraFields{
				ReasoningEffort: "none",
			}),
		},
	}
	_, captured := generateAndCaptureRequest(t, map[string]any{"reasoning_effort": "high"}, req)
	if got := captured["reasoning_effort"]; got != "none" {
		t.Fatalf("reasoning_effort = %#v, want none on grok-4.3", got)
	}
}

func TestDisabledAnthropicThinkingSendsNoneOnGrok43(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.3",
		Messages: []*schema.Message{schema.UserMessage("2 + 2 等于几？")},
		Options: []einomodel.Option{
			provider.WithChatExtraFields(&provider.ChatExtraFields{
				Thinking: map[string]any{"type": "disabled"},
			}),
		},
	}
	_, captured := generateAndCaptureRequest(t, map[string]any{"reasoning_effort": "high"}, req)
	if got := captured["reasoning_effort"]; got != "none" {
		t.Fatalf("reasoning_effort = %#v, want none from disabled thinking on grok-4.3", got)
	}
}

func TestEmptyStringOptionsOmitFields(t *testing.T) {
	_, captured := generateAndCapture(t, map[string]any{
		"reasoning_effort": "",
	})
	if _, ok := captured["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be omitted for empty option: %#v", captured["reasoning_effort"])
	}
	if _, ok := captured["search_parameters"]; ok {
		t.Fatalf("search_parameters should not be injected: %#v", captured["search_parameters"])
	}
}

func TestStripsUnsupportedSamplingFields(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.6",
		Messages: []*schema.Message{schema.UserMessage("hi")},
		Options: []einomodel.Option{
			einomodel.WithStop([]string{"END"}),
		},
	}
	_, captured := generateAndCaptureRequest(t, nil, req)
	if _, ok := captured["stop"]; ok {
		t.Fatalf("stop should be stripped: %#v", captured["stop"])
	}

	out, err := stripUnsupportedSamplingFields(context.Background(), nil, []byte(`{
		"model":"grok-4.6",
		"stop":["END"],
		"presence_penalty":0.1,
		"frequency_penalty":0.2,
		"temperature":0.2,
		"seed":9007199254740993
	}`))
	if err != nil {
		t.Fatalf("stripUnsupportedSamplingFields returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("decode stripped payload: %v", err)
	}
	if _, ok := payload["stop"]; ok {
		t.Fatalf("stop = %#v, want omitted", payload["stop"])
	}
	if _, ok := payload["presence_penalty"]; ok {
		t.Fatalf("presence_penalty = %#v, want omitted", payload["presence_penalty"])
	}
	if _, ok := payload["frequency_penalty"]; ok {
		t.Fatalf("frequency_penalty = %#v, want omitted", payload["frequency_penalty"])
	}
	if payload["temperature"] != 0.2 || payload["model"] != "grok-4.6" {
		t.Fatalf("payload = %+v, want unrelated fields preserved", payload)
	}
	if !strings.Contains(string(out), "9007199254740993") {
		t.Fatalf("stripped payload lost integer seed: %s", out)
	}

	escaped, err := stripUnsupportedSamplingFields(context.Background(), nil, []byte(`{
		"model":"grok-4.6",
		"stop":["END"],
		"messages":[{"content":"a < b & c > d"}]
	}`))
	if err != nil {
		t.Fatalf("stripUnsupportedSamplingFields returned error: %v", err)
	}
	if !strings.Contains(string(escaped), `a < b & c > d`) {
		t.Fatalf("stripped payload HTML-escaped message content: %s", escaped)
	}
}

func TestKeepsSamplingFieldsForNonReasoningModel(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.20-0309-non-reasoning",
		Messages: []*schema.Message{schema.UserMessage("hi")},
		Options: []einomodel.Option{
			einomodel.WithStop([]string{"END"}),
		},
	}
	_, captured := generateAndCaptureRequest(t, nil, req)
	stop, ok := captured["stop"].([]any)
	if !ok || len(stop) != 1 || stop[0] != "END" {
		t.Fatalf("stop = %#v, want [END] preserved for non-reasoning models", captured["stop"])
	}

	in := []byte(`{"model":"grok-4.20-0309-non-reasoning","stop":["END"],"seed":9007199254740993}`)
	out, err := stripUnsupportedSamplingFields(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("stripUnsupportedSamplingFields returned error: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("non-reasoning payload was rewritten: %s", out)
	}
}

func TestKeepsSamplingFieldsForGrok43NoneEffort(t *testing.T) {
	req := &provider.ChatRequest{
		Model:    "grok-4.3",
		Messages: []*schema.Message{schema.UserMessage("hi")},
		Options: []einomodel.Option{
			einomodel.WithStop([]string{"END"}),
			provider.WithChatExtraFields(&provider.ChatExtraFields{ReasoningEffort: "none"}),
		},
	}
	_, captured := generateAndCaptureRequest(t, nil, req)
	stop, ok := captured["stop"].([]any)
	if !ok || len(stop) != 1 || stop[0] != "END" {
		t.Fatalf("stop = %#v, want [END] preserved for grok-4.3 none effort", captured["stop"])
	}
}

func TestRejectsSamplingFields(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		payload map[string]any
		want    bool
	}{
		{name: "grok-4.6", model: "grok-4.6", want: true},
		{name: "prefixed grok-4.6", model: "xai/grok-4.6", want: true},
		{name: "grok-4.5", model: "grok-4.5", want: true},
		{name: "grok-4.3 default", model: "grok-4.3", want: true},
		{name: "grok-4.3 none", model: "grok-4.3", payload: map[string]any{"reasoning_effort": "none"}, want: false},
		{name: "grok-4.3 none object", model: "grok-4.3", payload: map[string]any{"reasoning": map[string]any{"effort": "none"}}, want: false},
		{name: "grok-4.20 reasoning", model: "grok-4.20-0309-reasoning", want: true},
		{name: "multi-agent", model: "grok-4.20-multi-agent", want: true},
		{name: "grok-5", model: "grok-5", want: true},
		{name: "non-reasoning", model: "grok-4.20-0309-non-reasoning", want: false},
		{name: "prefixed non-reasoning", model: "xai/grok-4.20-non-reasoning", want: false},
		{name: "empty", model: "", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rejectsSamplingFields(tt.model, tt.payload); got != tt.want {
				t.Fatalf("rejectsSamplingFields(%q, %+v) = %v, want %v", tt.model, tt.payload, got, tt.want)
			}
		})
	}
}

func TestGenerateCarriesResponsesContextToOpenAICompatiblePayload(t *testing.T) {
	req, err := provider.ResponsesToChatRequest(&provider.ResponsesRequest{
		Model: "grok-4.6",
		Input: "2 + 2 等于几？",
		Text: map[string]any{
			"format": map[string]any{"type": "json_object"},
		},
		Metadata:          map[string]any{"trace_id": "abc123"},
		User:              "user-1",
		Reasoning:         map[string]any{"effort": "high"},
		ParallelToolCalls: boolPtr(true),
		Store:             boolPtr(false),
	})
	if err != nil {
		t.Fatalf("ResponsesToChatRequest returned error: %v", err)
	}

	_, captured := generateAndCaptureRequest(t, nil, req)
	if captured["user"] != "user-1" {
		t.Fatalf("user = %#v, want user-1", captured["user"])
	}
	if captured["parallel_tool_calls"] != true || captured["store"] != false {
		t.Fatalf("captured = %+v, want parallel_tool_calls/store", captured)
	}
	if captured["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", captured["reasoning_effort"])
	}
	metadata, _ := captured["metadata"].(map[string]any)
	if metadata["trace_id"] != "abc123" {
		t.Fatalf("metadata = %+v, want trace_id", metadata)
	}
	responseFormat, _ := captured["response_format"].(map[string]any)
	if responseFormat["type"] != "json_object" {
		t.Fatalf("response_format = %+v, want json_object", responseFormat)
	}
}

func TestGeneratePreservesAssistantReasoningForToolReplay(t *testing.T) {
	req := &provider.ChatRequest{
		Model: "grok-4.6",
		Messages: []*schema.Message{
			{
				Role:             schema.Assistant,
				ReasoningContent: "need to inspect first",
				ToolCalls: []schema.ToolCall{{
					ID: "call_1", Type: "function",
					Function: schema.FunctionCall{Name: "read_file", Arguments: `{"path":"README.md"}`},
				}},
			},
			{Role: schema.Tool, ToolCallID: "call_1", Content: "contents"},
		},
	}

	_, captured := generateAndCaptureRequest(t, nil, req)
	messages := captured["messages"].([]any)
	assistant := messages[0].(map[string]any)
	if assistant["reasoning_content"] != "need to inspect first" {
		t.Fatalf("assistant reasoning_content = %#v", assistant["reasoning_content"])
	}
}

func TestStreamReturnsReasoningContent(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"inspect\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{APIKey: "test-key", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := prov.StreamChat(context.Background(), &provider.ChatRequest{
		Model:    "grok-4.6",
		Messages: []*schema.Message{{Role: schema.User, Content: "inspect"}},
		Options:  []einomodel.Option{einomodel.WithStop([]string{"END"})},
	})
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}
	defer stream.Close()
	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv returned error: %v", err)
	}
	if chunk.ReasoningContent != "inspect" {
		t.Fatalf("reasoning_content = %q", chunk.ReasoningContent)
	}
	if _, ok := captured["stop"]; ok {
		t.Fatalf("stream stop should be stripped on reasoning models: %#v", captured["stop"])
	}
}

func TestCompactCCDropsUnsupportedMetadataAndUser(t *testing.T) {
	req, err := provider.ResponsesToChatRequest(&provider.ResponsesRequest{
		Model:     "grok-4.6",
		Input:     "2 + 2 等于几？",
		Metadata:  map[string]any{"user_id": "abc123"},
		User:      "user-1",
		Reasoning: map[string]any{"effort": "high"},
	})
	if err != nil {
		t.Fatalf("ResponsesToChatRequest returned error: %v", err)
	}

	_, captured := generateAndCaptureRequest(t, map[string]any{"compact": "cc"}, req)
	if _, ok := captured["metadata"]; ok {
		t.Fatalf("metadata should be dropped in compact=cc mode: %#v", captured["metadata"])
	}
	if _, ok := captured["user"]; ok {
		t.Fatalf("user should be dropped in compact=cc mode: %#v", captured["user"])
	}
	if captured["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", captured["reasoning_effort"])
	}
}

func TestNewDefaults(t *testing.T) {
	prov, err := New(provider.ProviderConfig{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	p, ok := prov.(*Provider)
	if !ok {
		t.Fatalf("unexpected provider type %T", prov)
	}
	if p.ProviderType != "" {
		t.Fatalf("provider type should not be changed by New: %q", p.ProviderType)
	}
	if p.BaseURL != "https://api.x.ai/v1" {
		t.Fatalf("BaseURL = %q", p.BaseURL)
	}
	caps := p.Capabilities()
	if !caps.Streaming || !caps.Tools || !caps.Vision || caps.Embeddings {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.ContextWindow != 500000 || caps.MaxOutputTokens != 128000 {
		t.Fatalf("capabilities = %+v, want 500k context and 128k output", caps)
	}
}

func TestListModelsUsesGrokCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"grok-4.6"}]}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	models, err := prov.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %#v, want one model", models)
	}
	caps := models[0].Capabilities
	if !caps.Streaming || !caps.Tools || !caps.Vision || caps.Embeddings {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.ContextWindow != 500000 || caps.MaxOutputTokens != 128000 {
		t.Fatalf("capabilities = %+v, want 500k context and 128k output", caps)
	}
}

func TestNewAcceptsNoneReasoningEffort(t *testing.T) {
	prov, err := New(provider.ProviderConfig{Options: map[string]any{"reasoning_effort": "none"}})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if prov.(*Provider).reasoningEffort != "none" {
		t.Fatalf("reasoningEffort = %q, want none", prov.(*Provider).reasoningEffort)
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]any
		want    string
	}{
		{
			name:    "reasoning_effort",
			options: map[string]any{"reasoning_effort": "ultra"},
			want:    "reasoning_effort must be one of none, low, medium, high, xhigh",
		},
		{
			name:    "reasoning_effort type",
			options: map[string]any{"reasoning_effort": 3},
			want:    "option reasoning_effort must be a string",
		},
		{
			name:    "search_mode",
			options: map[string]any{"search_mode": "auto"},
			want:    "option search_mode is not supported; send Responses tools such as web_search or x_search on POST /v1/responses",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(provider.ProviderConfig{Options: tt.options})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCreateResponsesForwardsStatefulFields(t *testing.T) {
	var captured map[string]any
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":1,"model":"grok-4.6","output":[]}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{APIKey: "test-key", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = prov.(*Provider).CreateResponses(context.Background(), &provider.ResponsesRequest{
		Model:              "grok-4.6",
		Input:              "continue",
		PreviousResponseID: "resp_prev",
		Tools:              []provider.ResponsesToolDefinition{mustUnmarshalResponsesTool(t, `{"type":"web_search","filters":{"allowed_domains":["x.ai"]},"enable_image_understanding":true}`)},
		Reasoning:          map[string]any{"effort": "low"},
	})
	if err != nil {
		t.Fatalf("CreateResponses returned error: %v", err)
	}
	if path != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", path)
	}
	if captured["previous_response_id"] != "resp_prev" {
		t.Fatalf("previous_response_id = %#v, want resp_prev", captured["previous_response_id"])
	}
	if captured["input"] != "continue" {
		t.Fatalf("input = %#v, want continue", captured["input"])
	}
	tools, _ := captured["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want web_search", captured["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Fatalf("tools = %#v, want type=web_search", captured["tools"])
	}
	filters, _ := tool["filters"].(map[string]any)
	domains, _ := filters["allowed_domains"].([]any)
	if len(domains) != 1 || domains[0] != "x.ai" {
		t.Fatalf("web_search filters = %#v, want allowed_domains [x.ai]", tool["filters"])
	}
	if tool["enable_image_understanding"] != true {
		t.Fatalf("enable_image_understanding = %#v", tool["enable_image_understanding"])
	}
	reasoning, _ := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "low" {
		t.Fatalf("reasoning = %#v, want effort=low", captured["reasoning"])
	}
}

func TestCreateResponsesSendsNoneEffortOnGrok43(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":1,"model":"grok-4.3","output":[]}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{APIKey: "test-key", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = prov.(*Provider).CreateResponses(context.Background(), &provider.ResponsesRequest{
		Model:     "grok-4.3",
		Input:     "hello",
		Reasoning: map[string]any{"effort": "none"},
	})
	if err != nil {
		t.Fatalf("CreateResponses returned error: %v", err)
	}
	reasoning, _ := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v, want effort=none", captured["reasoning"])
	}
}

func TestCreateResponsesUsesDefaultModelBeforeShapingEffort(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":1,"model":"grok-4.3","output":[]}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		APIKey:       "test-key",
		BaseURL:      server.URL + "/v1",
		DefaultModel: "grok-4.3",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = prov.(*Provider).CreateResponses(context.Background(), &provider.ResponsesRequest{
		Input:     "hello",
		Reasoning: map[string]any{"effort": "none"},
	})
	if err != nil {
		t.Fatalf("CreateResponses returned error: %v", err)
	}
	if captured["model"] != "grok-4.3" {
		t.Fatalf("model = %#v, want grok-4.3", captured["model"])
	}
	reasoning, _ := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v, want effort=none", captured["reasoning"])
	}
}

func TestCapabilityOverrides(t *testing.T) {
	prov, err := New(provider.ProviderConfig{
		Options: map[string]any{
			"context_window":    "2000000",
			"max_output_tokens": 128000,
			"vision":            false,
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	caps := prov.Capabilities()
	if caps.ContextWindow != 2000000 || caps.MaxOutputTokens != 128000 || caps.Vision {
		t.Fatalf("capabilities = %+v", caps)
	}
}

func generateAndCapture(t *testing.T, options map[string]any) (*provider.ChatResponse, map[string]any) {
	t.Helper()
	return generateAndCaptureRequest(t, options, &provider.ChatRequest{
		Model: "grok-4.6",
		Messages: []*schema.Message{
			{Role: schema.System, Content: "用中文回答"},
			{Role: schema.User, Content: "2 + 2 等于几？"},
		},
		Options: []einomodel.Option{
			einomodel.WithMaxTokens(128),
			einomodel.WithTemperature(0.2),
		},
	})
}

func generateAndCaptureRequest(t *testing.T, options map[string]any, req *provider.ChatRequest) (*provider.ChatResponse, map[string]any) {
	t.Helper()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1710000000,
			"model": "grok-4.6",
			"choices": [{
				"index": 0,
				"message": {"role": "assistant", "content": "四"},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 4, "completion_tokens": 1, "total_tokens": 5}
		}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		ProviderType: "grok",
		APIKey:       "test-key",
		BaseURL:      server.URL + "/v1",
		Options:      options,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	p := prov.(*Provider)

	ctx := provider.WithCredential(context.Background(), &credential.Credential{
		Attributes: map[string]string{"api_key": "test-key"},
	})
	resp, err := p.Chat(ctx, req)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	return resp, captured
}

func mustUnmarshalResponsesTool(t *testing.T, raw string) provider.ResponsesToolDefinition {
	t.Helper()
	var tool provider.ResponsesToolDefinition
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatalf("unmarshal responses tool: %v", err)
	}
	return tool
}

func boolPtr(v bool) *bool {
	return &v
}
