package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/gateway"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/caddyserver/caddy/v2"
)

type stubLLMApiHandler struct{}

func (stubLLMApiHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "agent_route_dispatcher.llm_apis.stub"}
}

func (stubLLMApiHandler) Name() string { return "stub" }

func (stubLLMApiHandler) MatchLLMApi(*http.Request) bool { return true }

func (stubLLMApiHandler) PrepareLLMApiRequest(*http.Request) (*PreparedLLMApiRequest, error) {
	return &PreparedLLMApiRequest{GenerateRequest: &provider.GenerateRequest{}}, nil
}

func (stubLLMApiHandler) ServeLLMApi(http.ResponseWriter, *http.Request, provider.Provider, *PreparedLLMApiRequest) error {
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

func TestAgentRouteDispatcherValidateUsesLoadedHandlers(t *testing.T) {
	dispatcher := &AgentRouteDispatcher{
		apiHandlers: map[string]LLMApiHandler{
			"openai": stubLLMApiHandler{},
		},
	}

	if err := dispatcher.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAgentRouteDispatcherDefersVirtualKeyUntilLLMApiMatch(t *testing.T) {
	gw := gateway.NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), gateway.BootstrapOptions{
		StaticRoutes: []routepkg.AgentRoute{{
			ID:     "broad-route",
			LLMAPI: "stub",
			Match:  routepkg.RouteMatch{PathPrefix: "/"},
			Policy: routepkg.RoutePolicy{
				Auth: routepkg.AuthPolicy{RequireVirtualKey: true},
			},
			Targets: []routepkg.RouteTarget{{ProviderRef: "openai"}},
		}},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	dispatcher := AgentRouteDispatcher{
		apiHandlers: map[string]LLMApiHandler{"stub": nonMatchingLLMApiHandler{}},
		gateway:     gw,
	}
	next := &nextHandler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	if err := dispatcher.ServeHTTP(rec, req, next); err != nil {
		t.Fatalf("ServeHTTP returned error: %v", err)
	}
	if !next.called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

func TestAgentRouteDispatcherRejectsDisabledLLMApiHandlerName(t *testing.T) {
	const handlerName = "test-disabled-llm-api-handler"
	RegisterLLMApiHandlerName(handlerName)
	if err := DisableLLMApiHandlerName(handlerName); err != nil {
		t.Fatalf("disable llm api handler name: %v", err)
	}
	defer func() {
		if err := EnableLLMApiHandlerName(handlerName); err != nil {
			t.Fatalf("restore llm api handler name: %v", err)
		}
	}()

	gw := gateway.NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), gateway.BootstrapOptions{
		StaticRoutes: []routepkg.AgentRoute{{
			ID:      "disabled-api-route",
			LLMAPI:  handlerName,
			Match:   routepkg.RouteMatch{PathPrefix: "/"},
			Targets: []routepkg.RouteTarget{{ProviderRef: "openai"}},
		}},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	dispatcher := AgentRouteDispatcher{
		apiHandlers: map[string]LLMApiHandler{handlerName: stubLLMApiHandler{}},
		gateway:     gw,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	if err := dispatcher.ServeHTTP(rec, req, &nextHandler{}); err != nil {
		t.Fatalf("ServeHTTP returned error: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRewriteRoutePathStripsMatchedPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tenant/v1/chat/completions", nil)
	rewritten := rewriteRoutePath(req, "/tenant")

	if rewritten.URL.Path != "/v1/chat/completions" {
		t.Fatalf("rewritten path = %q, want /v1/chat/completions", rewritten.URL.Path)
	}
	if req.URL.Path != "/tenant/v1/chat/completions" {
		t.Fatalf("original path mutated to %q", req.URL.Path)
	}
}

func TestRouteResolveRequestUsesPreparedModelAndStream(t *testing.T) {
	got := routeResolveRequest(&PreparedLLMApiRequest{
		GenerateRequest: &provider.GenerateRequest{Model: "gpt-4o-mini"},
		Stream:          true,
	})

	if got.Model != "gpt-4o-mini" {
		t.Fatalf("Model = %q, want gpt-4o-mini", got.Model)
	}
	if !got.Stream {
		t.Fatal("Stream = false, want true")
	}
}

func TestRouteResolveRequestHandlesNilPreparedRequest(t *testing.T) {
	got := routeResolveRequest(nil)

	if got.Model != "" {
		t.Fatalf("Model = %q, want empty", got.Model)
	}
	if got.Stream {
		t.Fatal("Stream = true, want false")
	}
}
