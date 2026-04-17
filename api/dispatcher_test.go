package api

import (
	"net/http"
	"testing"

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
