package openai_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ingress "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	upstream "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/qwen"
)

func TestResponsesNamespaceThroughRealProviders(t *testing.T) {
	for _, kind := range []string{"qwen", "openai"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", kind, stream), func(t *testing.T) {
				hits := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "decode", 400)
						return
					}
					tools := body["tools"].([]any)
					if len(tools) != 2 {
						t.Errorf("tool count=%d", len(tools))
						http.Error(w, "tools", 400)
						return
					}
					names := []string{}
					for _, v := range tools {
						obj := v.(map[string]any)
						if obj["type"] != "function" {
							t.Errorf("not flattened: %v", obj)
						}
						if fn, ok := obj["function"].(map[string]any); ok {
							obj = fn
						}
						names = append(names, obj["name"].(string))
					}
					if names[0] == names[1] {
						t.Error("same-name tools collapsed")
					}
					choice := body["tool_choice"].(map[string]any)
					if fn, ok := choice["function"].(map[string]any); ok {
						choice = fn
					}
					if choice["name"] != names[1] {
						t.Errorf("wrong forced choice: %v", choice)
					}
					if kind == "qwen" {
						if r.URL.Path != "/chat/completions" {
							t.Errorf("path=%s", r.URL.Path)
						}
						if !stream {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"id":"c","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":%q,"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, names[1])
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":\"{}\"}}]}}]}\n\n", names[1])
						fmt.Fprint(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
						return
					}
					if r.URL.Path != "/responses" {
						t.Errorf("path=%s", r.URL.Path)
					}
					item := fmt.Sprintf(`{"type":"function_call","id":"fc","call_id":"call","name":%q,"arguments":"{}","future_field":true}`, names[1])
					echoTools, _ := json.Marshal(body["tools"])
					echoChoice, _ := json.Marshal(body["tool_choice"])
					response := `{"id":"resp","object":"response","output":[` + item + `],"vendor_counter":9007199254740993,"tools":` + string(echoTools) + `,"tool_choice":` + string(echoChoice) + `}`
					if !stream {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, response)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":%s}\n\n", response)
					fmt.Fprintf(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":%s}\n\n", item)
					fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
				}))
				defer server.Close()
				cfg := provider.ProviderConfig{BaseURL: server.URL, APIKey: "fixture", Network: provider.NetworkConfig{MaxRetries: -1}, Options: map[string]any{"responses_tool_namespaces": "flatten"}}
				var p provider.Provider
				var err error
				if kind == "qwen" {
					p, err = qwen.New(cfg)
				} else {
					p, err = upstream.New(cfg)
				}
				if err != nil {
					t.Fatal(err)
				}
				body := fmt.Sprintf(`{"model":"fixture","input":"inspect","stream":%v,"tools":[{"type":"namespace","name":"browser","tools":[{"type":"function","name":"get_status","parameters":{"type":"object"}}]},{"type":"namespace","name":"computer","tools":[{"type":"function","name":"get_status","parameters":{"type":"object"}}]}],"tool_choice":{"type":"function","namespace":"computer","name":"get_status"}}`, stream)
				r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				h := ingress.NewHandler()
				prepared, _, err := h.PrepareLLMApiRequest(r)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				if err = h.ServeLLMApi(w, r, p, prepared); err != nil {
					t.Fatal(err)
				}
				result := w.Body.String()
				if w.Code != 200 || !strings.Contains(result, `"namespace":"computer"`) || !strings.Contains(result, `"name":"get_status"`) || strings.Contains(result, "agw_fn_") {
					t.Fatalf("status=%d response=%s", w.Code, result)
				}
				if stream && !strings.Contains(result, "response.completed") {
					t.Fatalf("missing completion: %s", result)
				}
				if kind == "openai" && (!strings.Contains(result, `"future_field":true`) || !strings.Contains(result, `"vendor_counter":9007199254740993`)) {
					t.Fatalf("lost raw fields: %s", result)
				}
				if hits != 1 {
					t.Errorf("upstream hits=%d", hits)
				}
			})
		}
	}
}

func TestNativeResponsesPreservesNamespacesByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ResponsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if len(req.Tools) != 1 || req.Tools[0].Type != "namespace" || len(req.Tools[0].Tools) != 1 {
			t.Errorf("native tools=%+v", req.Tools)
		}
		fmt.Fprint(w, `{"id":"r","output":[{"type":"function_call","name":"get_status","namespace":"computer","call_id":"c","arguments":"{}","future":true}]}`)
	}))
	defer server.Close()
	p, err := upstream.New(provider.ProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	req := &provider.ResponsesRequest{Input: "inspect", Tools: []provider.ResponsesToolDefinition{{Type: "namespace", Name: "computer", Tools: []provider.ResponsesToolDefinition{{Type: "function", Name: "get_status"}}}}}
	r, err := p.(provider.ResponsesProvider).CreateResponses(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Output[0].Namespace != "computer" || !strings.Contains(string(r.RawJSON), `"future":true`) {
		t.Fatalf("response=%+v", r)
	}
	if req.Model != "" {
		t.Fatal("default model mutated input")
	}
}
