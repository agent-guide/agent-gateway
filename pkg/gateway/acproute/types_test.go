package acproute

import (
	"encoding/json"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func TestACPRouteConfigToConfigRoundTrip(t *testing.T) {
	route := ACPRouteConfig{
		AgentRouteConfig: AgentRouteConfig{
			ID: "agent-route",
			MatchPolicy: RouteMatch{
				PathPrefix: "/acp",
			},
			AuthPolicy: RouteAuthPolicy{
				RequireVirtualKey: true,
			},
		},
		ServiceID: "codex-main",
	}

	cfg, err := route.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig returned error: %v", err)
	}
	if cfg.Kind != routecore.RouteKindACP {
		t.Fatalf("Kind = %q, want %q", cfg.Kind, routecore.RouteKindACP)
	}
	if cfg.Protocol != routecore.RouteProtocolACP {
		t.Fatalf("Protocol = %q, want %q", cfg.Protocol, routecore.RouteProtocolACP)
	}

	var target struct {
		Kind      routecore.RouteTargetPolicyKind `json:"kind"`
		ServiceID string                          `json:"service_id"`
	}
	if err := json.Unmarshal(cfg.TargetPolicy, &target); err != nil {
		t.Fatalf("unmarshal target policy: %v", err)
	}
	if target.Kind != routecore.RouteTargetPolicyKindACPService {
		t.Fatalf("target kind = %q, want %q", target.Kind, routecore.RouteTargetPolicyKindACPService)
	}
	if target.ServiceID != "codex-main" {
		t.Fatalf("service_id = %q, want codex-main", target.ServiceID)
	}

	decoded, err := NewACPRouteConfigFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewACPRouteConfigFromConfig returned error: %v", err)
	}
	if decoded.ServiceID != route.ServiceID {
		t.Fatalf("decoded service_id = %q, want %q", decoded.ServiceID, route.ServiceID)
	}
}
