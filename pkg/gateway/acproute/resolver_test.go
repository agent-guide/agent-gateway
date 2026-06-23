package acproute

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

// TestUpdateConfigRejectsSlashRouteID ensures the shared resolver entry (reused
// by the Admin API, CLI, and bundle apply) rejects a slash-bearing route id
// before touching the config store, symmetric to CreateConfig.
func TestUpdateConfigRejectsSlashRouteID(t *testing.T) {
	r := &ACPRouteResolver{}
	err := r.UpdateConfig(context.Background(), "acp:svc:/acp", routecore.AgentRouteConfig{})
	if !errors.Is(err, ErrInvalidRouteID) {
		t.Fatalf("UpdateConfig() error = %v, want ErrInvalidRouteID", err)
	}
}
