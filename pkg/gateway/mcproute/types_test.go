package mcproute

import (
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func TestMCPRouteConfigToConfigRoundTrip(t *testing.T) {
	t.Parallel()

	route := MCPRoute{
		RouteMetadata: RouteMetadata{
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
		ConnectorID: " svc-main ",
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

	decoded, err := NewMCPRouteFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewMCPRouteFromConfig() error = %v", err)
	}
	if decoded.ConnectorID != "svc-main" {
		t.Fatalf("ConnectorID = %q, want svc-main", decoded.ConnectorID)
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

	routeCfg := MCPRoute{
		RouteMetadata: RouteMetadata{
			ID:          "mcp-route-1",
			Kind:        routecore.RouteKindMCP,
			Protocol:    routecore.RouteProtocolMCP,
			MatchPolicy: RouteMatch{PathPrefix: "/mcp"},
		},
		ConnectorID: "svc-main",
	}
	cfg, err := routeCfg.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig() error = %v", err)
	}

	route, err := NewMCPRouteFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewMCPRouteFromConfig() error = %v", err)
	}
	if route.ConnectorID != "svc-main" {
		t.Fatalf("ConnectorID = %q, want svc-main", route.ConnectorID)
	}

	cfgView := route
	if cfgView.ConnectorID != "svc-main" {
		t.Fatalf("Config().ConnectorID = %q, want svc-main", cfgView.ConnectorID)
	}
}
