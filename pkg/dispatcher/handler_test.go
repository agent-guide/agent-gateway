package dispatcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type stubLLMApiHandler struct{}

func (stubLLMApiHandler) Name() string { return "stub" }

func (stubLLMApiHandler) MatchLLMApi(*http.Request) bool { return true }

func (stubLLMApiHandler) PrepareLLMApiRequest(*http.Request) (*PreparedLLMApiRequest, routepkg.RequestRequirements, error) {
	return &PreparedLLMApiRequest{
		Type:        provider.LLMApiRequestTypeChat,
		ChatRequest: &provider.ChatRequest{},
	}, routepkg.RequestRequirements{}, nil
}

func (stubLLMApiHandler) ServeLLMApi(w http.ResponseWriter, _ *http.Request, _ provider.Provider, _ *PreparedLLMApiRequest) error {
	w.WriteHeader(http.StatusAccepted)
	return nil
}

type nonMatchingLLMApiHandler struct {
	stubLLMApiHandler
}

func (nonMatchingLLMApiHandler) MatchLLMApi(*http.Request) bool { return false }

type nextHandler struct {
	called bool
}

func (h *nextHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) error {
	h.called = true
	w.WriteHeader(http.StatusTeapot)
	return nil
}

func TestHandlerRequiresVirtualKeyBeforeLLMApiMatch(t *testing.T) {
	gw := gateway.NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), gateway.BootstrapOptions{
		StaticRoutes: []routepkg.LLMRoute{{
			AgentRouteConfig: routepkg.AgentRouteConfig{
				ID:          "broad-route",
				Protocol:    routepkg.RouteProtocol("stub"),
				MatchPolicy: routepkg.RouteMatchPolicy{PathPrefix: "/"},
				AuthPolicy:  routepkg.RouteAuthPolicy{RequireVirtualKey: true},
			},
			TargetPolicy: &routepkg.RouteDirectProviderPolicy{
				ProviderTarget: routepkg.DirectProviderTarget{
					ProviderID: "openai",
				},
			},
		}},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	handler := NewHandler(gw, map[string]LLMApiHandler{"stub": nonMatchingLLMApiHandler{}}, nil, HandlerOptions{})
	next := &nextHandler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	if err := handler.Dispatch(rec, req, next); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if next.called {
		t.Fatal("next handler should not be called when virtual key is required")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRewriteRoutePathStripsMatchedPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tenant/v1/chat/completions", nil)
	rewritten := RewriteRoutePath(req, "/tenant")

	if rewritten.URL.Path != "/v1/chat/completions" {
		t.Fatalf("rewritten path = %q, want /v1/chat/completions", rewritten.URL.Path)
	}
	if req.URL.Path != "/tenant/v1/chat/completions" {
		t.Fatalf("original path mutated to %q", req.URL.Path)
	}
}

func TestHandlerValidateAllowsMCPOnly(t *testing.T) {
	handler := NewHandler(nil, nil, nil, HandlerOptions{EnableMCP: true})
	if err := handler.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}
