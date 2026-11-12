package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
)

func TestCallToolProducesSafeTypedOutcomes(t *testing.T) {
	for _, mode := range []string{"success", "invalid_arguments", "unknown_tool", "get_failure", "post_failure", "panic"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(mode, "failure") {
					w.WriteHeader(500)
					fmt.Fprint(w, "secret upstream content")
					return
				}
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer server.Close()
			engine := localEngine(t, server, nil)
			catalog := testCatalog(t, server.URL, "")
			call := cr.ToolCall{Name: "lookup", Arguments: map[string]any{"id": "one"}}
			switch mode {
			case "invalid_arguments":
				call.Arguments = map[string]any{}
			case "unknown_tool":
				call.Name = "missing"
			case "post_failure":
				catalog.tools["lookup"].tool.Request.Method = "POST"
			case "panic":
				catalog = nil
			}
			out, err := engine.CallTool(t.Context(), "orders", catalog, call)
			if mode == "invalid_arguments" || mode == "unknown_tool" || mode == "panic" {
				category := mode
				if mode == "panic" {
					category = "internal_error"
				}
				var callErr *cr.CallError
				if !errors.As(err, &callErr) || callErr.Category != category || requests.Load() != 0 {
					t.Fatalf("preflight sent work or leaked error: %#v %v", out, err)
				}
				return
			}
			if err != nil || out.Result == nil || out.HTTP == nil || requests.Load() != 1 {
				t.Fatalf("missing outcome or replay: %#v %v calls=%d", out, err, requests.Load())
			}
			if mode == "success" {
				if out.Failure != nil || out.Result.IsError {
					t.Fatalf("success failed: %#v", out)
				}
			} else {
				if out.Failure == nil || !out.Result.IsError || out.Failure.OutcomeUncertain != (mode == "post_failure") || strings.Contains(fmt.Sprint(out.Result.Content), "secret") {
					t.Fatalf("unsafe failure: %#v", out)
				}
			}
		})
	}
}
