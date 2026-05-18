package mcproute

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerMatchPrefersMoreSpecificPath(t *testing.T) {
	manager := NewManager(nil)
	manager.InitStaticRoutes([]MCPRoute{
		{ID: "root", ServiceID: "svc-root", Match: RouteMatch{PathPrefix: "/"}},
		{ID: "api", ServiceID: "svc-api", Match: RouteMatch{PathPrefix: "/mcp/api"}},
	})

	req := httptest.NewRequest(http.MethodPost, "http://example.test/mcp/api/tools", nil)
	route, ok, err := manager.Match(req.Context(), req)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if !ok {
		t.Fatal("Match() ok = false, want true")
	}
	if route.ID != "api" {
		t.Fatalf("route.ID = %q, want api", route.ID)
	}
}

func TestManagerMatchHonorsMethod(t *testing.T) {
	manager := NewManager(nil)
	manager.InitStaticRoutes([]MCPRoute{
		{ID: "post-only", ServiceID: "svc-1", Match: RouteMatch{PathPrefix: "/mcp", Methods: []string{"POST"}}},
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.test/mcp", nil)
	_, ok, err := manager.Match(req.Context(), req)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if ok {
		t.Fatal("Match() ok = true, want false")
	}
}
