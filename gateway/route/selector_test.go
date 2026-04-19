package route

import (
	"testing"
)

func TestDefaultRouteSelectorUsesPolicyStrategyAndFallback(t *testing.T) {
	selector := DefaultRouteSelector{}
	route := AgentRoute{
		ID: "chat-prod",
		Targets: []RouteTarget{
			{ProviderRef: "weighted", Mode: TargetModeWeighted, Weight: 1},
			{ProviderRef: "failover", Mode: TargetModeFailover, Priority: 1},
		},
		Policy: RoutePolicy{
			Selection: SelectionPolicy{Strategy: RouteSelectionStrategyFailover},
			Fallback:  FallbackPolicy{Enabled: true},
		},
	}

	target, err := selector.SelectTarget(route, RouteResolveRequest{})
	if err != nil {
		t.Fatalf("SelectTarget returned error: %v", err)
	}
	if target.ProviderRef != "failover" {
		t.Fatalf("unexpected target: got %q want %q", target.ProviderRef, "failover")
	}

	route.Policy.Selection.Strategy = RouteSelectionStrategyConditional
	target, err = selector.SelectTarget(route, RouteResolveRequest{})
	if err != nil {
		t.Fatalf("SelectTarget with fallback returned error: %v", err)
	}
	if target.ProviderRef != "weighted" {
		t.Fatalf("unexpected fallback target: got %q want %q", target.ProviderRef, "weighted")
	}
}
