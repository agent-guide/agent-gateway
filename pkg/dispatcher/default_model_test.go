package dispatcher_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/dispatcher"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/anthropic"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/cc"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	upstream "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
)

func TestDefaultModelResponseIdentity(t *testing.T) {
	for _, logical := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			for _, endpoint := range []struct {
				name, path string
				api        dispatcher.LLMApiHandler
			}{
				{"chat", "/v1/chat/completions", openai.NewHandler()},
				{"anthropic", "/v1/messages", anthropic.NewHandler(nil)},
				{"cc", "/v1/messages", cc.NewHandler(nil)},
			} {
				t.Run(fmt.Sprintf("%s/logical=%t/stream=%t", endpoint.name, logical, streaming), func(t *testing.T) {
					hits := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						hits++
						var req struct {
							Model string `json:"model"`
						}
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						if req.Model != "upstream-model" {
							t.Errorf("upstream model=%q", req.Model)
						}
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, "data:{\"id\":\"r\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\ndata:{\"id\":\"r\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata:[DONE]")
						} else {
							fmt.Fprint(w, `{"id":"r","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
						}
					}))
					defer server.Close()
					p, err := upstream.New(provider.ProviderConfig{Id: "p", ProviderType: "openai", BaseURL: server.URL, APIKey: "test"})
					if err != nil {
						t.Fatal(err)
					}
					name := "upstream-model"
					target := llmroute.RouteModelTarget{Name: name, Kind: "provider-model", ProviderID: "p", UpstreamModel: name}
					var models []modelcatalog.LogicalModel
					if logical {
						name = "chat"
						target = llmroute.RouteModelTarget{Name: name, Kind: "logical-model", LogicalModelID: "logical"}
						models = []modelcatalog.LogicalModel{{ID: "logical", Candidates: []modelcatalog.ModelCandidate{{ProviderID: "p", UpstreamModel: "upstream-model"}}}}
					}
					route := llmroute.LLMRoute{RouteMetadata: llmroute.RouteMetadata{ID: "test", Protocol: routecore.RouteProtocol(endpoint.api.Name()), MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/probe"}}, TargetPolicy: &llmroute.RouteModelPolicy{DefaultModel: name, ModelTargets: []llmroute.RouteModelTarget{target}}}
					cfg, err := route.ToConfig()
					if err != nil {
						t.Fatal(err)
					}
					g := gateway.NewAgentGateway()
					if err := g.Bootstrap(t.Context(), gateway.BootstrapOptions{StaticProviders: map[string]provider.Provider{"p": p}, StaticProviderModels: []modelcatalog.ProviderModel{{ProviderID: "p", UpstreamModel: "upstream-model", Enabled: true, CapabilityOverrides: &provider.ModelCapabilities{Streaming: true}}}, StaticLogicalModels: models, StaticLLMRoutes: []routecore.RouteConfig{cfg}}); err != nil {
						t.Fatal(err)
					}
					defer g.Close()
					h := dispatcher.NewHandler(g, map[string]dispatcher.LLMApiHandler{endpoint.api.Name(): endpoint.api}, nil, dispatcher.HandlerOptions{})
					w := httptest.NewRecorder()
					r := httptest.NewRequest("POST", "/probe"+endpoint.path, strings.NewReader(fmt.Sprintf(`{"messages":[{"role":"user","content":"hi"}],"max_tokens":10,"stream":%t}`, streaming)))
					h.ServeHTTP(w, r)
					if w.Code != 200 || hits != 1 {
						t.Fatalf("status=%d hits=%d body=%s", w.Code, hits, w.Body.String())
					}
					if !strings.Contains(w.Body.String(), `"model":"`+name+`"`) {
						t.Fatalf("missing route-visible model %q: %s", name, w.Body.String())
					}
				})
			}
		}
	}
}
