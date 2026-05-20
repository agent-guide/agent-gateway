package mcproute

import (
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func TestMCPRouteConfigToConfigRoundTrip(t *testing.T) {
	t.Parallel()

	route := MCPRouteConfig{
		AgentRouteConfig: AgentRouteConfig{
			ID:          "mcp-route-1",
			Description: " test route ",
			Disabled:    true,
			AuthPolicy:  RouteAuthPolicy{RequireVirtualKey: true},
			MatchPolicy: RouteMatch{
				Host:       " example.com ",
				PathPrefix: " /mcp ",
				Methods:    []string{" POST "},
			},
		},
		ServiceID: " svc-main ",
	}

	cfg, err := route.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig() error = %v", err)
	}
	if cfg.Kind != RouteKindMCP {
		t.Fatalf("Kind = %q, want %q", cfg.Kind, RouteKindMCP)
	}
	if cfg.Protocol != RouteProtocolMCP {
		t.Fatalf("Protocol = %q, want %q", cfg.Protocol, RouteProtocolMCP)
	}

	decoded, err := NewMCPRouteConfigFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewMCPRouteConfigFromConfig() error = %v", err)
	}
	if decoded.ServiceID != "svc-main" {
		t.Fatalf("ServiceID = %q, want svc-main", decoded.ServiceID)
	}
	if decoded.Description != "test route" {
		t.Fatalf("Description = %q, want test route", decoded.Description)
	}
	if decoded.MatchPolicy.Host != "example.com" {
		t.Fatalf("Host = %q, want example.com", decoded.MatchPolicy.Host)
	}
	if decoded.MatchPolicy.PathPrefix != "/mcp" {
		t.Fatalf("PathPrefix = %q, want /mcp", decoded.MatchPolicy.PathPrefix)
	}
	if len(decoded.MatchPolicy.Methods) != 1 || decoded.MatchPolicy.Methods[0] != "POST" {
		t.Fatalf("Methods = %#v, want [POST]", decoded.MatchPolicy.Methods)
	}
}

func TestNewMCPRouteFromConfigCreatesRuntimeRoute(t *testing.T) {
	t.Parallel()

	routeCfg := MCPRouteConfig{
		AgentRouteConfig: AgentRouteConfig{
			ID:          "mcp-route-1",
			Kind:        routecore.RouteKindMCP,
			Protocol:    routecore.RouteProtocolMCP,
			MatchPolicy: RouteMatch{PathPrefix: "/mcp"},
		},
		ServiceID: "svc-main",
	}
	cfg, err := routeCfg.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig() error = %v", err)
	}

	route, err := NewMCPRouteFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewMCPRouteFromConfig() error = %v", err)
	}
	if route.ServiceID != "svc-main" {
		t.Fatalf("ServiceID = %q, want svc-main", route.ServiceID)
	}
	if route.State != (MCPRouteState{}) {
		t.Fatalf("State = %#v, want zero MCPRouteState", route.State)
	}

	cfgView := route.Config()
	if cfgView.ServiceID != "svc-main" {
		t.Fatalf("Config().ServiceID = %q, want svc-main", cfgView.ServiceID)
	}
}
