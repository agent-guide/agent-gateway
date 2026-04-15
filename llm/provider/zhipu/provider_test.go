package zhipu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

func TestGenerateUsesStringContent(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/anthropic/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("unexpected x-api-key: %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersion {
			t.Fatalf("unexpected anthropic-version: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [{"type": "text", "text": "四"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 4, "output_tokens": 1}
		}`))
	}))
	defer server.Close()

	p := &Provider{
		ProviderConfig: provider.ProviderConfig{
			ProviderName: "zhipu",
			APIKey:       "test-key",
			BaseURL:      server.URL + "/api/anthropic",
		},
	}
	p.ProviderConfig.Network.Defaults()

	resp, err := p.Generate(context.Background(), &provider.GenerateRequest{
		Model: "glm-4.7",
		Messages: []*schema.Message{
			{Role: schema.System, Content: "用中文回答"},
			{Role: schema.User, Content: "2 + 2 等于几？"},
		},
		Options: []einomodel.Option{
			einomodel.WithMaxTokens(128),
			einomodel.WithTemperature(0.2),
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if resp == nil || resp.Message == nil || resp.Message.Content != "四" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected messages: %#v", captured["messages"])
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected first message: %#v", messages[0])
	}
	if got := first["content"]; got != "2 + 2 等于几？" {
		t.Fatalf("content = %#v, want string content", got)
	}
	if _, ok := first["content"].([]any); ok {
		t.Fatal("content was encoded as an Anthropic content block array")
	}
	if got := captured["system"]; got != "用中文回答" {
		t.Fatalf("system = %#v", got)
	}
	if got := captured["model"]; got != "glm-4.7" {
		t.Fatalf("model = %#v", got)
	}
	if got := captured["max_tokens"]; got != float64(minMaxTokens) {
		t.Fatalf("max_tokens = %#v", got)
	}
	if got := captured["temperature"]; got != 0.2 {
		t.Fatalf("temperature = %#v", got)
	}
	if _, ok := captured["stream"]; ok {
		t.Fatalf("stream should be omitted for non-streaming requests: %#v", captured["stream"])
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
	if p.ProviderName != "" {
		t.Fatalf("provider name should not be changed by New: %q", p.ProviderName)
	}
	if p.BaseURL != "https://open.bigmodel.cn/api/anthropic" {
		t.Fatalf("BaseURL = %q", p.BaseURL)
	}
}
