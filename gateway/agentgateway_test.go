package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type fixedSelector struct {
	target routepkg.RouteTarget
}

func (s fixedSelector) SelectTarget(routepkg.AgentRoute, routepkg.RouteResolveRequest) (*routepkg.RouteTarget, error) {
	target := s.target
	return &target, nil
}

type countingSelector struct {
	calls int
}

func (s *countingSelector) SelectTarget(routepkg.AgentRoute, routepkg.RouteResolveRequest) (*routepkg.RouteTarget, error) {
	s.calls++
	return &routepkg.RouteTarget{ProviderRef: "openai"}, nil
}

type testProvider struct{}

func (testProvider) Generate(context.Context, *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	return nil, nil
}

func (testProvider) Stream(context.Context, *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (testProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (testProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (testProvider) Config() provider.ProviderConfig {
	return provider.ProviderConfig{}
}

func TestResolverUsesCustomSelector(t *testing.T) {
	route := routepkg.AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		Match:  routepkg.RouteMatch{PathPrefix: "/v1"},
		Targets: []routepkg.RouteTarget{
			{ProviderRef: "openai", Mode: routepkg.TargetModeWeighted, Weight: 1},
			{ProviderRef: "openrouter", Mode: routepkg.TargetModeWeighted, Weight: 1},
		},
	}
	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		StaticRoutes: []routepkg.AgentRoute{route},
		StaticProviders: map[string]provider.Provider{
			"openrouter": testProvider{},
		},
		Selector: fixedSelector{target: routepkg.RouteTarget{ProviderRef: "openrouter"}},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	resolvedRoute, err := gw.ResolveRoute(context.Background(), req)
	if err != nil {
		t.Fatalf("ResolveRoute returned error: %v", err)
	}
	prov, err := gw.ResolveProvider(context.Background(), resolvedRoute, routepkg.RouteResolveRequest{
		Model: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("ResolveProvider returned error: %v", err)
	}
	if prov == nil {
		t.Fatal("ResolveProvider returned nil provider")
	}
}

func TestResolveRejectsDisabledRoute(t *testing.T) {
	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		StaticRoutes: []routepkg.AgentRoute{{
			ID:       "chat-prod",
			Disabled: true,
			LLMAPI:   "openai",
			Match:    routepkg.RouteMatch{PathPrefix: "/v1"},
			Targets:  []routepkg.RouteTarget{{ProviderRef: "openai"}},
		}},
		StaticProviders: map[string]provider.Provider{
			"openai": testProvider{},
		},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := gw.ResolveRoute(context.Background(), req)
	if err == nil {
		t.Fatal("ResolveRoute returned nil error, want disabled route rejection")
	}
}
