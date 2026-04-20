package route

import (
	"testing"
)

func TestDefaultRouteSelectorUsesPolicyStrategyAndFallback(t *testing.T) {
	selector := DefaultRouteSelector{}
	route := AgentRoute{
		ID: "chat-prod",
		Targets: []RouteTarget{
			{ProviderID: "weighted", Mode: TargetModeWeighted, Weight: 1},
			{ProviderID: "failover", Mode: TargetModeFailover, Priority: 1},
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
	if target.ProviderID != "failover" {
		t.Fatalf("unexpected target: got %q want %q", target.ProviderID, "failover")
	}

	route.Policy.Selection.Strategy = RouteSelectionStrategyConditional
	target, err = selector.SelectTarget(route, RouteResolveRequest{})
	if err != nil {
		t.Fatalf("SelectTarget with fallback returned error: %v", err)
	}
	if target.ProviderID != "weighted" {
		t.Fatalf("unexpected fallback target: got %q want %q", target.ProviderID, "weighted")
	}
}
