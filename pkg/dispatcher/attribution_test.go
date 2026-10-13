package dispatcher

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"net/http"
	"net/http/httptest"
	"testing"
)

type attributionAPI struct {
	localLLMApiHandler
	v attribution.Value
}

func (h *attributionAPI) ServeLLMApi(w http.ResponseWriter, r *http.Request, _ provider.Provider, _ *PreparedLLMApiRequest) error {
	h.v, _ = attribution.FromContext(r.Context())
	w.WriteHeader(200)
	return nil
}
func TestCallerTenantHeaderIgnoredWithNoopObserver(t *testing.T) {
	g := gateway.NewAgentGateway()
	if err := g.Bootstrap(context.Background(), gateway.BootstrapOptions{StaticLLMRoutes: mustRouteConfigs(t, []llmroute.LLMRoute{{AgentRouteConfig: llmroute.AgentRouteConfig{ID: "local", Protocol: "local-stub", MatchPolicy: llmroute.RouteMatchPolicy{PathPrefix: "/"}}, TargetPolicy: &llmroute.RouteDirectProviderPolicy{ProviderTarget: llmroute.DirectProviderTarget{ProviderID: "unused"}}}})}); err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	api := &attributionAPI{}
	h := NewHandler(g, map[string]LLMApiHandler{"local-stub": api}, nil, HandlerOptions{})
	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Tenant-ID", "forged")
	req.Header.Set("X-Subject-ID", "forged")
	rec := httptest.NewRecorder()
	if err := h.Dispatch(rec, req, nil); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || api.v.Tenant != "" || api.v.Subject != "" || api.v.RouteID != "local" || api.v.TraceID == "" || api.v.SpanID == "" {
		t.Fatalf("status %d attribution %#v", rec.Code, api.v)
	}
}
