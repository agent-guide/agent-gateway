package mcproute

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func mustConfigFromMCPRoute(t *testing.T, route MCPRoute) routecore.RouteConfig {
	t.Helper()
	cfg, err := route.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig() error = %v", err)
	}
	return cfg
}

// TestUpdateConfigRejectsSlashRouteID ensures the shared resolver entry (reused
// by the Admin API, CLI, and bundle apply) rejects a slash-bearing route id
// before touching the config store, symmetric to CreateConfig.
func TestUpdateConfigRejectsSlashRouteID(t *testing.T) {
	r := &MCPRouteResolver{}
	err := r.UpdateConfig(context.Background(), "mcp:svc:/mcp", routecore.RouteConfig{})
	if !errors.Is(err, ErrInvalidRouteID) {
		t.Fatalf("UpdateConfig() error = %v, want ErrInvalidRouteID", err)
	}
}
