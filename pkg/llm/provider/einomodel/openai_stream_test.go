package einomodel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	bridge "github.com/agent-guide/agent-gateway/pkg/llm/provider/einomodel"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	"github.com/cloudwego/eino/schema"
)

// Exercise the actual OpenAI transport and eino bridge, including a usage-only
// chunk and a final SSE marker without a newline. Missing upstream usage must
// remain unknown rather than being invented by the bridge.
func TestOpenAIStreamUsageAndUnterminatedDone(t *testing.T) {
	for _, withUsage := range []bool{false, true} {
		t.Run(fmt.Sprintf("usage=%t", withUsage), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Stream        bool `json:"stream"`
					StreamOptions struct {
						IncludeUsage bool `json:"include_usage"`
					} `json:"stream_options"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if !req.Stream || !req.StreamOptions.IncludeUsage {
					t.Error("stream did not request usage")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data:{\"id\":\"c1\",\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n")
				fmt.Fprint(w, "data:{\"id\":\"c1\",\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				if withUsage {
					fmt.Fprint(w, "data:{\"id\":\"c1\",\"model\":\"test\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
				}
				fmt.Fprint(w, "data:[DONE]")
			}))
			defer server.Close()
			p, err := openai.New(provider.ProviderConfig{ProviderType: "openai", APIKey: "test-key", BaseURL: server.URL + "/v1"})
			if err != nil {
				t.Fatal(err)
			}
			m, err := bridge.New(p, "test")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("hello")})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var content string
			var usage *schema.TokenUsage
			for {
				msg, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				content += msg.Content
				if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
					usage = msg.ResponseMeta.Usage
				}
			}
			if content != "hello" {
				t.Fatalf("content=%q", content)
			}
			if withUsage {
				if usage == nil || usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 15 {
					t.Fatalf("usage=%+v", usage)
				}
			} else if usage != nil && (usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0) {
				t.Fatalf("fabricated usage: %+v", usage)
			}
		})
	}
}
