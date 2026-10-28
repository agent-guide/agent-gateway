package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

func chatAndCaptureRequest(t *testing.T, options map[string]any, req *provider.ChatRequest) map[string]any {
	t.Helper()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1710000000,
			"model":"gpt-4.1",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}
		}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		ProviderType: "openai",
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
	if _, err := p.Chat(ctx, req); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	return captured
}

func TestChatCarriesResponsesContextToOpenAICompatiblePayload(t *testing.T) {
	chatReq, err := provider.ResponsesToChatRequest(&provider.ResponsesRequest{
		Model: "gpt-4.1",
		Input: "hello",
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

	captured := chatAndCaptureRequest(t, nil, chatReq)

	if captured["user"] != "user-1" {
		t.Fatalf("user = %#v, want user-1", captured["user"])
	}
	if captured["parallel_tool_calls"] != true {
		t.Fatalf("parallel_tool_calls = %#v, want true", captured["parallel_tool_calls"])
	}
	if captured["store"] != false {
		t.Fatalf("store = %#v, want false", captured["store"])
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

func TestCompactCCDropsUnsupportedMetadataAndUser(t *testing.T) {
	chatReq, err := provider.ResponsesToChatRequest(&provider.ResponsesRequest{
		Model:     "gpt-4.1",
		Input:     "hello",
		Metadata:  map[string]any{"user_id": "abc123"},
		User:      "user-1",
		Reasoning: map[string]any{"effort": "high"},
	})
	if err != nil {
		t.Fatalf("ResponsesToChatRequest returned error: %v", err)
	}

	captured := chatAndCaptureRequest(t, map[string]any{"compact": "cc"}, chatReq)

	if _, ok := captured["metadata"]; ok {
		t.Fatalf("metadata should be dropped in compact=cc mode: %#v", captured["metadata"])
	}
	if _, ok := captured["user"]; ok {
		t.Fatalf("user should be dropped in compact=cc mode: %#v", captured["user"])
	}
	// Unrelated fields must still pass through.
	if captured["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", captured["reasoning_effort"])
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestStreamRequiresFinishReason(t *testing.T) {
	for _, tc := range []struct {
		name, tail string
		wantError  bool
	}{
		{"truncated", "", true},
		{"done_without_finish", "data: [DONE]\n\n", true},
		{"stop", "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", false},
		{"tool_calls", "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"+tc.tail)
			}))
			defer server.Close()
			p, err := New(provider.ProviderConfig{ProviderType: "openai", APIKey: "test", BaseURL: server.URL + "/v1"})
			if err != nil {
				t.Fatal(err)
			}
			stream, err := p.StreamChat(context.Background(), &provider.ChatRequest{Model: "test"})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var content string
			for {
				msg, err := stream.Recv()
				if err != nil {
					if tc.wantError && !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("want unexpected EOF, got %v", err)
					}
					if !tc.wantError && err != io.EOF {
						t.Fatal(err)
					}
					break
				}
				if msg != nil {
					content += msg.Content
				}
			}
			if content != "partial" {
				t.Fatalf("partial content lost: %q", content)
			}
		})
	}
}

func TestStreamFinishCancellationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, finish   string
		terminal, want error
	}{
		{"finished_then_canceled", "stop", nil, io.EOF},
		{"unfinished_then_canceled", "", nil, context.Canceled},
		{"finished_with_transport_error", "stop", io.ErrUnexpectedEOF, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source, writer := schema.Pipe[*schema.Message](2)
			writer.Send(&schema.Message{Content: "answer", ResponseMeta: &schema.ResponseMeta{FinishReason: tc.finish}}, nil)
			if tc.terminal != nil {
				writer.Send(nil, tc.terminal)
			}
			writer.Close()
			stream := requireStreamFinish(ctx, source)
			defer stream.Close()
			if msg, err := stream.Recv(); err != nil || msg.Content != "answer" {
				t.Fatalf("first chunk = %v, %v", msg, err)
			}
			cancel()
			if _, err := stream.Recv(); !errors.Is(err, tc.want) {
				t.Fatalf("terminal error = %v, want %v", err, tc.want)
			}
		})
	}
}
