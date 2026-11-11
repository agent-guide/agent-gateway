package openai_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpcapture"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	ingress "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	upstream "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
)

func TestResponsesStreamEOFThroughRealProvider(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed=%t", complete), func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event:response.output_text.delta\ndata:{\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}")
				if complete {
					fmt.Fprint(w, "\n\nevent:response.completed\ndata:{\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"model\":\"m\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}")
				}
			}))
			defer server.Close()
			p, err := upstream.New(provider.ProviderConfig{BaseURL: server.URL, APIKey: "test"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			sink := &usage.InMemorySink{}
			span, ctx := usage.NewObserver(sink).Begin(ctx, usage.InteractionDimensions{RouteKind: "llm"})
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"m","input":"hello","stream":true}`)).WithContext(ctx)
			h := ingress.NewHandler()
			prepared, _, err := h.PrepareLLMApiRequest(req)
			if err != nil {
				t.Fatal(err)
			}
			raw := httptest.NewRecorder()
			rec := httpcapture.NewResponseRecorder(raw)
			if err := h.ServeLLMApi(rec, req, p, prepared); err != nil {
				t.Fatal(err)
			}
			span.Finish(usage.InteractionOutcome{Success: rec.StatusCode() < 400 && !rec.Failed(), StatusCode: rec.StatusCode()})
			if hits != 1 {
				t.Fatalf("upstream requests=%d want 1", hits)
			}
			if len(sink.Events) != 1 {
				t.Fatalf("usage events=%d", len(sink.Events))
			}
			ev := sink.Events[0].(usage.LLMUsageEvent)
			if ev.Success != complete || ev.StatusCode != 200 {
				t.Fatalf("outcome=%+v", ev)
			}
			if !strings.Contains(raw.Body.String(), "partial") {
				t.Fatalf("lost delta: %s", raw.Body.String())
			}
			if complete {
				if ev.TotalTokens != 5 {
					t.Fatalf("usage=%+v", ev)
				}
			} else {
				if ev.ErrorType != "provider_stream_failed" || !strings.Contains(raw.Body.String(), "event: error") {
					t.Fatalf("usage=%+v body=%s", ev, raw.Body.String())
				}
			}
		})
	}
}
