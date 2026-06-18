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
	agent_gateway_admin
	`)

	handler, err := parseAgentGatewayAdmin(httpcaddyfile.Helper{Dispenser: d})
	if err != nil {
		t.Fatalf("parse admin handler: %v", err)
	}
	if _, ok := handler.(*AgentGatewayAdminHandler); !ok {
		t.Fatalf("handler type = %T, want *AgentGatewayAdminHandler", handler)
	}
}

func TestParseAgentGatewayAdminRejectsEmbeddedAuthOptions(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway_admin {
		admin_user alice
	}
	`)

	if _, err := parseAgentGatewayAdmin(httpcaddyfile.Helper{Dispenser: d}); err == nil {
		t.Fatal("expected embedded admin auth option to be rejected")
	}
}
