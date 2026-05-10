package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	einodeepseek "github.com/cloudwego/eino-ext/components/model/deepseek"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestGenerateUsesDeepSeekAPI(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
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
			"model": "deepseek-chat",
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
		ProviderType: "deepseek",
		APIKey:       "test-key",
		BaseURL:      server.URL,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	p := prov.(*Provider)

	resp, err := p.Chat(context.Background(), &provider.ChatRequest{
		Model: "deepseek-chat",
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
	if !ok || len(messages) != 2 {
		t.Fatalf("unexpected messages: %#v", captured["messages"])
	}
	if got := messages[0].(map[string]any)["role"]; got != "system" {
		t.Fatalf("first role = %#v, want system", got)
	}
	if got := messages[1].(map[string]any)["content"]; got != "2 + 2 等于几？" {
		t.Fatalf("content = %#v, want string content", got)
	}
	if got := captured["model"]; got != "deepseek-chat" {
		t.Fatalf("model = %#v", got)
	}
	if got := captured["max_tokens"]; got != float64(128) {
		t.Fatalf("max_tokens = %#v", got)
	}
	if got := captured["temperature"]; got != 0.2 {
		t.Fatalf("temperature = %#v", got)
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
	if p.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("BaseURL = %q", p.BaseURL)
	}
}

func TestApplyOptions(t *testing.T) {
	cfg := &einodeepseek.ChatModelConfig{}
	applyOptions(cfg, map[string]any{
		"path":                 "beta/chat/completions",
		"response_format_type": "json_object",
		"max_tokens":           "256",
		"temperature":          "0.3",
		"top_p":                float64(0.9),
		"presence_penalty":     "0.1",
		"frequency_penalty":    "-0.2",
		"log_probs":            "true",
		"top_log_probs":        float64(2),
	})

	if cfg.Path != "beta/chat/completions" {
		t.Fatalf("Path = %q", cfg.Path)
	}
	if cfg.ResponseFormatType != einodeepseek.ResponseFormatTypeJSONObject {
		t.Fatalf("ResponseFormatType = %q", cfg.ResponseFormatType)
	}
	if cfg.MaxTokens != 256 || cfg.Temperature != 0.3 || cfg.TopP != 0.9 {
		t.Fatalf("common options not applied: %+v", cfg)
	}
	if cfg.PresencePenalty != 0.1 || cfg.FrequencyPenalty != -0.2 {
		t.Fatalf("penalties not applied: %+v", cfg)
	}
	if !cfg.LogProbs || cfg.TopLogProbs != 2 {
		t.Fatalf("logprobs not applied: %+v", cfg)
	}
}
