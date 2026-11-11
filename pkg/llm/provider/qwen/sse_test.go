package qwen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestQwenStreamingSSEOptionalSpace(t *testing.T) {
	for _, space := range []string{" ", ""} {
		for _, api := range []string{"chat", "responses"} {
			t.Run(api+"/space="+space, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/chat/completions" {
						t.Errorf("unexpected upstream path: %s", r.URL.Path)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					for _, payload := range []string{
						`{"id":"c","object":"chat.completion.chunk","model":"qwen-plus","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}`,
						`{"id":"c","object":"chat.completion.chunk","model":"qwen-plus","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
						`[DONE]`,
					} {
						fmt.Fprintf(w, "data:%s%s\n\n", space, payload)
					}
				}))
				defer server.Close()
				p, err := New(provider.ProviderConfig{BaseURL: server.URL, APIKey: "test"})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var text string
				if api == "chat" {
					sr, err := p.StreamChat(ctx, &provider.ChatRequest{Model: "qwen-plus", Messages: []*schema.Message{schema.UserMessage("hi")}})
					if err != nil {
						t.Fatal(err)
					}
					defer sr.Close()
					for {
						msg, err := sr.Recv()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						text += msg.Content
					}
				} else {
					sr, err := p.(provider.ResponsesProvider).StreamResponses(ctx, &provider.ResponsesRequest{Model: "qwen-plus", Input: "hi", Stream: true})
					if err != nil {
						t.Fatal(err)
					}
					defer sr.Close()
					var completed bool
					for {
						event, err := sr.Recv()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						if event.Type == "response.output_text.delta" {
							text += event.Delta
						}
						completed = completed || event.Type == "response.completed"
					}
					if !completed {
						t.Fatal("missing response.completed event")
					}
				}
				if text != "hello" {
					t.Fatalf("text = %q, want hello", text)
				}
			})
		}
	}
}
