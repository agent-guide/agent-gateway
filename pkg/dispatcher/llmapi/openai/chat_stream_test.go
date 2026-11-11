package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ingress "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	upstream "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
)

func TestChatStreamUsageThroughRealProvider(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for _, include := range []bool{false, true} {
			for _, haveUsage := range []bool{false, true} {
				t.Run(fmt.Sprintf("compact=%t/include=%t/upstream_usage=%t", compact, include, haveUsage), func(t *testing.T) {
					hits := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						hits++
						w.Header().Set("Content-Type", "text/event-stream")
						sep := "data: "
						if compact {
							sep = "data:"
						}
						fmt.Fprintf(w, "%s%s\n\n", sep, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"hello"}}]}`)
						fmt.Fprintf(w, "%s%s\n\n", sep, `{"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
						if haveUsage {
							fmt.Fprintf(w, "%s%s\n\n", sep, `{"id":"c","model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":9,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":1}}}`)
						}
						fmt.Fprint(w, sep+"[DONE]") // Deliberately omit the final newline.
					}))
					defer server.Close()
					p, err := upstream.New(provider.ProviderConfig{BaseURL: server.URL, APIKey: "test"})
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					body := fmt.Sprintf(`{"model":"m","stream":true,"stream_options":{"include_usage":%t},"messages":[{"role":"user","content":"hi"}]}`, include)
					req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
					h := ingress.NewHandler()
					prepared, _, err := h.PrepareLLMApiRequest(req)
					if err != nil {
						t.Fatal(err)
					}
					rec := httptest.NewRecorder()
					if err = h.ServeLLMApi(rec, req, p, prepared); err != nil {
						t.Fatal(err)
					}
					if rec.Code != 200 || hits != 1 {
						t.Fatalf("status=%d calls=%d body=%s", rec.Code, hits, rec.Body.String())
					}
					var chunks []map[string]json.RawMessage
					done := false
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data:") {
							continue
						}
						data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
						if data == "[DONE]" {
							done = true
							continue
						}
						if done {
							t.Fatal("chunk after DONE")
						}
						var chunk map[string]json.RawMessage
						if err = json.Unmarshal([]byte(data), &chunk); err != nil {
							t.Fatal(err)
						}
						chunks = append(chunks, chunk)
					}
					if !done || !strings.Contains(rec.Body.String(), "hello") {
						t.Fatalf("incomplete output: %s", rec.Body.String())
					}
					want := 2
					if include && haveUsage {
						want++
					}
					if len(chunks) != want {
						t.Fatalf("chunks=%d want %d: %s", len(chunks), want, rec.Body.String())
					}
					for i, chunk := range chunks {
						raw, exists := chunk["usage"]
						if !include {
							if exists {
								t.Fatal("usage returned without opt-in")
							}
							continue
						}
						if i < 2 {
							if string(raw) != "null" {
								t.Fatalf("content chunk usage=%s", raw)
							}
							continue
						}
						if string(chunk["choices"]) != "[]" {
							t.Fatalf("usage chunk choices=%s", chunk["choices"])
						}
						var u struct {
							Prompt            int            `json:"prompt_tokens"`
							Completion        int            `json:"completion_tokens"`
							Total             int            `json:"total_tokens"`
							PromptDetails     map[string]int `json:"prompt_tokens_details"`
							CompletionDetails map[string]int `json:"completion_tokens_details"`
						}
						if err = json.Unmarshal(raw, &u); err != nil {
							t.Fatal(err)
						}
						if u.Prompt != 3 || u.Completion != 2 || u.Total != 9 || u.PromptDetails["cached_tokens"] != 1 || u.CompletionDetails["reasoning_tokens"] != 1 {
							t.Fatalf("usage=%+v", u)
						}
					}
				})
			}
		}
	}
}
