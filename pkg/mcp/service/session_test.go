package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/mcp/service"
)

// newFakeStreamableHTTPConfig creates a minimal streamable-HTTP MCP test server and
// returns its service config. The server assigns an upstream session ID via the
// MCP-Session-Id response header on the initialize call.
func newFakeStreamableHTTPConfig(t *testing.T, id string) service.MCPServiceConfig {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("MCP-Session-Id", "upstream-session-abc123")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{}}}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return service.MCPServiceConfig{
		ID:        id,
		Name:      "fake-http-" + id,
		Transport: service.TransportStreamableHTTP,
		URL:       ts.URL + "/mcp",
	}
}

func TestManagerGatewaySessionReadyAfterConnect(t *testing.T) {
	cfg := newFakeSSEConfig(t, "sess1")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := mgr.ListTools(ctx, cfg.ID); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	s := mgr.GetGatewaySession(cfg.ID)
	if s == nil {
		t.Fatal("expected non-nil session")
	}
	if s.State != service.SessionStateReady {
		t.Errorf("expected state ready, got %q", s.State)
	}
	if s.ServiceID != cfg.ID {
		t.Errorf("expected service_id %q, got %q", cfg.ID, s.ServiceID)
	}
	if s.Transport != service.TransportSSE {
		t.Errorf("expected transport sse, got %q", s.Transport)
	}
	if s.ID == "" {
		t.Error("expected non-empty gateway session ID")
	}
	if s.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestManagerGatewaySessionClosedAfterInvalidation(t *testing.T) {
	cfg := newFakeSSEConfig(t, "sess2")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Establish a session.
	if _, err := mgr.ListTools(ctx, cfg.ID); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	// Deleting the service triggers invalidateDiscoverySession → session closed.
	if err := mgr.Delete(ctx, cfg.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	s := mgr.GetGatewaySession(cfg.ID)
	if s == nil {
		t.Fatal("expected session entry to persist after delete")
	}
	if s.State != service.SessionStateClosed {
		t.Errorf("expected state closed, got %q", s.State)
	}
}

func TestManagerGatewaySessionNewIDOnReconnect(t *testing.T) {
	cfg := newFakeSSEConfig(t, "sess3")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First connection.
	if _, err := mgr.ListTools(ctx, cfg.ID); err != nil {
		t.Fatalf("first ListTools: %v", err)
	}
	s1 := mgr.GetGatewaySession(cfg.ID)
	if s1 == nil {
		t.Fatal("expected session after first connect")
	}

	// Update the service config to force invalidation + reconnect.
	updated := cfg
	updated.Description = "changed"
	if err := mgr.Update(ctx, cfg.ID, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Second connection creates a new session.
	if _, err := mgr.ListTools(ctx, cfg.ID); err != nil {
		t.Fatalf("second ListTools: %v", err)
	}
	s2 := mgr.GetGatewaySession(cfg.ID)
	if s2 == nil {
		t.Fatal("expected session after second connect")
	}
	if s2.ID == s1.ID {
		t.Error("expected a new gateway session ID after reconnect, got the same one")
	}
	if s2.State != service.SessionStateReady {
		t.Errorf("expected state ready after reconnect, got %q", s2.State)
	}
}

func TestManagerGatewaySessionUpstreamSessionID(t *testing.T) {
	cfg := newFakeStreamableHTTPConfig(t, "sess4")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := mgr.Initialize(ctx, cfg.ID); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	s := mgr.GetGatewaySession(cfg.ID)
	if s == nil {
		t.Fatal("expected non-nil session")
	}
	if s.State != service.SessionStateReady {
		t.Errorf("expected ready, got %q", s.State)
	}
	// The fake server sets MCP-Session-Id: upstream-session-abc123.
	if s.UpstreamSessionID == "" {
		t.Error("expected non-empty upstream session ID for streamable_http transport")
	}
}

func TestManagerListGatewaySessionsAllServices(t *testing.T) {
	cfgA := newFakeSSEConfig(t, "sessAll-a")
	cfgB := newFakeSSEConfig(t, "sessAll-b")
	mgr := newInMemoryManager(t, cfgA, cfgB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := mgr.ListTools(ctx, cfgA.ID); err != nil {
		t.Fatalf("ListTools A: %v", err)
	}
	if _, err := mgr.ListTools(ctx, cfgB.ID); err != nil {
		t.Fatalf("ListTools B: %v", err)
	}

	all := mgr.ListGatewaySessions()
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(all))
	}

	sA := mgr.GetGatewaySession(cfgA.ID)
	if sA == nil || sA.ServiceID != cfgA.ID {
		t.Errorf("expected session for service A, got %v", sA)
	}
}
