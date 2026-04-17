package admin

import (
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
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
