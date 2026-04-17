package admin

import (
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func TestAgentGatewayAdminHandlerModuleID(t *testing.T) {
	var h AgentGatewayAdminHandler
	if got := h.CaddyModule().ID; got != "http.handlers.agent_gateway_admin" {
		t.Fatalf("module id = %q, want %q", got, "http.handlers.agent_gateway_admin")
	}
}

func TestParseAgentGatewayAdminFromCaddyfile(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway_admin {
		admin_user alice
		admin_password_hash bcrypt-hash
	}
	`)

	handler, err := ParseAgentGatewayAdminForTest(httpcaddyfile.Helper{Dispenser: d})
	if err != nil {
		t.Fatalf("parse admin handler: %v", err)
	}

	adminHandler, ok := handler.(*AgentGatewayAdminHandler)
	if !ok {
		t.Fatalf("handler type = %T, want *AgentGatewayAdminHandler", handler)
	}
	if adminHandler.AdminUsername != "alice" {
		t.Fatalf("admin username = %q, want %q", adminHandler.AdminUsername, "alice")
	}
	if adminHandler.AdminPasswordHash != "bcrypt-hash" {
		t.Fatalf("admin password hash = %q, want %q", adminHandler.AdminPasswordHash, "bcrypt-hash")
	}
}

func TestCaddyfileServerHeuristicDoesNotMatchAPIServerID(t *testing.T) {
	if isCaddyfileGeneratedServerID("agentgw0") {
		t.Fatal("agentgw0 should not be treated as a Caddyfile-generated server ID")
	}
	if !isCaddyfileGeneratedServerID("srv1") {
		t.Fatal("srv1 should be treated as a Caddyfile-generated server ID")
	}
}

func TestCaddyfileRouteHeuristicUsesUngroupedRoutes(t *testing.T) {
	if hasCaddyfileRoutes(&caddyhttp.Server{
		Routes: caddyhttp.RouteList{{Group: "agentgw-route"}},
	}) {
		t.Fatal("grouped API-managed routes should not be treated as Caddyfile routes")
	}
	if !hasCaddyfileRoutes(&caddyhttp.Server{
		Routes: caddyhttp.RouteList{{}},
	}) {
		t.Fatal("ungrouped routes should be treated as Caddyfile routes")
	}
}
