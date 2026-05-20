package mcproute

import (
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func mustConfigFromMCPRoute(t *testing.T, route MCPRouteConfig) routecore.AgentRouteConfig {
	t.Helper()
	cfg, err := route.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig() error = %v", err)
	}
	return cfg
}
