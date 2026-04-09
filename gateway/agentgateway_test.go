package gateway

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type fixedSelector struct {
	target routepkg.RouteTarget
}

func (s fixedSelector) SelectTarget(routepkg.Route, routepkg.ResolveRequest) (*routepkg.RouteTarget, error) {
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
	route := routepkg.Route{
		ID: "chat-prod",
		Targets: []routepkg.RouteTarget{
			{ProviderRef: "openai", Mode: routepkg.TargetModeWeighted, Weight: 1},
			{ProviderRef: "openrouter", Mode: routepkg.TargetModeWeighted, Weight: 1},
		},
	}
	gw := NewAgentGateway()
	rm := NewRouteManager(nil)
	rm.cacheDynamicRoute(route)
	gw.Configure(nil, rm, NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		if name == "openrouter" {
			return testProvider{}, true
		}
		return nil, false
	}), nil, nil, fixedSelector{target: routepkg.RouteTarget{ProviderRef: "openrouter"}})

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
	rm := NewRouteManager(nil)
	rm.cacheDynamicRoute(routepkg.Route{ID: "chat-prod"})
	gw.Configure(nil, rm, nil, nil, nil, nil)

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
	rm := NewRouteManager(nil)
	rm.cacheDynamicRoute(routepkg.Route{
		ID:      "chat-prod",
		Targets: []routepkg.RouteTarget{{ProviderRef: "openai", Disabled: true}},
	})
	gw.Configure(nil, rm, NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		return testProvider{}, true
	}), nil, nil, nil)

	if err := gw.ValidateRoute(context.Background(), "chat-prod"); err == nil {
		t.Fatal("ValidateRoute returned nil error, want definition rejection")
	}
}

func TestValidateRouteChecksUniqueProviderRefs(t *testing.T) {
	var resolved []string

	gw := NewAgentGateway()
	rm := NewRouteManager(nil)
	rm.cacheDynamicRoute(routepkg.Route{
		ID: "chat-prod",
		Targets: []routepkg.RouteTarget{
			{ProviderRef: "openai"},
			{ProviderRef: "openai"},
			{ProviderRef: "anthropic"},
		},
	})
	gw.Configure(nil, rm, ProviderResolverFunc(func(ctx context.Context, ref string) (provider.Provider, string, error) {
		resolved = append(resolved, ref)
		return testProvider{}, ref, nil
	}), nil, nil, nil)

	if err := gw.ValidateRoute(context.Background(), "chat-prod"); err != nil {
		t.Fatalf("ValidateRoute returned error: %v", err)
	}
	if fmt.Sprint(resolved) != "[openai anthropic]" {
		t.Fatalf("resolved provider refs = %v, want [openai anthropic]", resolved)
	}
}
