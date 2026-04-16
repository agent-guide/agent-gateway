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

func (s fixedSelector) SelectTarget(routepkg.AgentRoute, routepkg.ResolveRequest) (*routepkg.RouteTarget, error) {
	target := s.target
	return &target, nil
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
		ID: "chat-prod",
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
	resolved, err := gw.Resolve(context.Background(), route.ID, routepkg.ResolveRequest{
		HTTPRequest: req,
		Model:       "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.ProviderName != "openrouter" {
		t.Fatalf("unexpected provider: got %q want %q", resolved.ProviderName, "openrouter")
	}
}

func TestLookupRouteNormalizesRoutePolicy(t *testing.T) {
	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		StaticRoutes: []routepkg.AgentRoute{{ID: "chat-prod"}},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	got, err := gw.LookupRoute(context.Background(), "chat-prod")
	if err != nil {
		t.Fatal("Route returned false, want stored route")
	}
	if got.Policy.TimeoutSeconds != 120 {
		t.Fatalf("TimeoutSeconds = %d, want 120", got.Policy.TimeoutSeconds)
	}
	if got.Policy.Selection.Strategy != routepkg.RouteSelectionStrategyAuto {
		t.Fatalf("Selection.Strategy = %q, want %q", got.Policy.Selection.Strategy, routepkg.RouteSelectionStrategyAuto)
	}
}

func TestValidateRouteRejectsRouteWithoutEnabledTargets(t *testing.T) {
	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		StaticRoutes: []routepkg.AgentRoute{{
			ID:      "chat-prod",
			Targets: []routepkg.RouteTarget{{ProviderRef: "openai", Disabled: true}},
		}},
		StaticProviders: map[string]provider.Provider{
			"openai": testProvider{},
		},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	if err := gw.ValidateRoute(context.Background(), "chat-prod"); err == nil {
		t.Fatal("ValidateRoute returned nil error, want definition rejection")
	}
}

func TestResolveRejectsDisabledRoute(t *testing.T) {
	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		StaticRoutes: []routepkg.AgentRoute{{
			ID:       "chat-prod",
			Disabled: true,
			Targets:  []routepkg.RouteTarget{{ProviderRef: "openai"}},
		}},
		StaticProviders: map[string]provider.Provider{
			"openai": testProvider{},
		},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := gw.Resolve(context.Background(), "chat-prod", routepkg.ResolveRequest{
		HTTPRequest: req,
	})
	if err == nil {
		t.Fatal("Resolve returned nil error, want disabled route rejection")
	}
}
