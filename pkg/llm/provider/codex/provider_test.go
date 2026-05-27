package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestChatStateToResponsesRequestMovesSystemMessagesToInstructions(t *testing.T) {
	state := &provider.ChatRequestState{
		ModelName: "gpt-5.4",
		Messages: []*schema.Message{
			schema.SystemMessage("You are Claude Code."),
			schema.SystemMessage("Follow the user's instructions."),
			schema.UserMessage("hello"),
		},
	}

	req := chatStateToResponsesRequest(state, false)

	if req.Instructions != "You are Claude Code.\nFollow the user's instructions." {
		t.Fatalf("instructions = %q", req.Instructions)
	}
	items, ok := req.Input.([]any)
	if !ok {
		t.Fatalf("input type = %T, want []any", req.Input)
	}
	if len(items) != 1 {
		t.Fatalf("input item count = %d, want 1", len(items))
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("input item type = %T, want map[string]any", items[0])
	}
	if got := item["role"]; got != "user" {
		t.Fatalf("role = %#v, want %q", got, "user")
	}
}

func TestSanitizeResponsesRequestProvidesDefaultInstructionsWhenMissing(t *testing.T) {
	req := sanitizeResponsesRequest(&provider.ResponsesRequest{
		Model: "gpt-5.4",
		Input: []any{},
	})

	if req.Instructions != "You are a helpful assistant." {
		t.Fatalf("instructions = %q", req.Instructions)
	}
}

func TestSanitizeResponsesRequestEnforcesCodexBackendControls(t *testing.T) {
	storeTrue := true
	req := sanitizeResponsesRequest(&provider.ResponsesRequest{
		Model:           "gpt-5.4",
		Input:           []any{},
		MaxOutputTokens: 1234,
		Store:           &storeTrue,
	})

	if req.MaxOutputTokens != 0 {
		t.Fatalf("max_output_tokens = %d, want 0", req.MaxOutputTokens)
	}
	if req.Store == nil || *req.Store != false {
		t.Fatalf("store = %#v, want false", req.Store)
	}
}

func TestChatCarriesRequestControlsToResponsesPayload(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_test",
			"object":"response",
			"created_at":1710000000,
			"model":"codex-mini",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}
		}`))
	}))
	defer server.Close()

	bFalse := false
	bTrue := true
	prov, err := New(provider.ProviderConfig{
		ProviderType: "codex",
		APIKey:       "test-key",
		BaseURL:      server.URL,
		DefaultModel: "default-model",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = prov.Chat(context.Background(), &provider.ChatRequest{
		Model: "request-model",
		Messages: []*schema.Message{
			schema.SystemMessage("system instructions"),
			schema.UserMessage("hello"),
		},
		Options: []einomodel.Option{
			einomodel.WithModel("option-model"),
			einomodel.WithTemperature(0.7),
			einomodel.WithTopP(0.9),
			einomodel.WithMaxTokens(1234),
			einomodel.WithStop([]string{"END"}),
			provider.WithTopK(17),
			provider.WithChatExtraFields(&provider.ChatExtraFields{
				ResponseFormat: map[string]any{
					"type": "json_schema",
					"json_schema": map[string]any{
						"name":   "answer",
						"schema": map[string]any{"type": "object"},
						"strict": true,
					},
				},
				Reasoning:         map[string]any{"summary": "auto"},
				ReasoningEffort:   "high",
				User:              "user-1",
				Metadata:          map[string]any{"trace_id": "abc123"},
				ParallelToolCalls: &bFalse,
				Store:             &bTrue,
			}),
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if captured["model"] != "request-model" {
		t.Fatalf("model = %#v, want request-model", captured["model"])
	}
	if _, ok := captured["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens = %#v, want omitted", captured["max_output_tokens"])
	}
	if _, ok := captured["top_k"]; ok {
		t.Fatalf("top_k = %#v, want omitted", captured["top_k"])
	}
	if _, ok := captured["stop"]; ok {
		t.Fatalf("stop = %#v, want omitted", captured["stop"])
	}
	if captured["store"] != false {
		t.Fatalf("store = %#v, want false", captured["store"])
	}
	if captured["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v, want false", captured["parallel_tool_calls"])
	}
	if captured["user"] != "user-1" {
		t.Fatalf("user = %#v, want user-1", captured["user"])
	}
	metadata, _ := captured["metadata"].(map[string]any)
	if metadata["trace_id"] != "abc123" {
		t.Fatalf("metadata = %+v, want trace_id", metadata)
	}
	reasoning, _ := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %+v, want effort and summary", reasoning)
	}
	text, _ := captured["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "answer" || format["strict"] != true {
		t.Fatalf("text.format = %+v, want flattened json_schema", format)
	}
}
