package connector_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/connector"
)

func TestMCPInitializationIsBusyAndMetadataDoesNotReconnect(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var initializations atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg map[string]any
		json.NewDecoder(r.Body).Decode(&msg)
		var result any
		switch msg["method"] {
		case "initialize":
			initializations.Add(1)
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
	}))
	defer s.Close()
	m := connector.NewManager(newTestConfigStore(t))
	defer m.Close()
	cfg := connector.ConnectorConfig{ID: "tools", Name: "Tools", Type: "mcp", MCP: &connector.MCPConfig{Transport: connector.TransportStreamableHTTP, URL: s.URL}}
	if err := m.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := m.ListTools(t.Context(), cfg.ID); done <- err }()
	<-started
	changed := cfg
	copy := *cfg.MCP
	changed.MCP = &copy
	changed.MCP.URL += "?changed=1"
	if err := m.Update(t.Context(), cfg.ID, changed); !errors.Is(err, connector.ErrConnectorBusy) {
		t.Fatal(err)
	}
	if err := m.Delete(t.Context(), cfg.ID); !errors.Is(err, connector.ErrConnectorBusy) {
		t.Fatal(err)
	}
	cfg.Name = "Renamed"
	if err := m.Update(t.Context(), cfg.ID, cfg); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	before := m.GetGatewaySession(cfg.ID)
	if _, err := m.ListTools(t.Context(), cfg.ID); err != nil {
		t.Fatal(err)
	}
	after := m.GetGatewaySession(cfg.ID)
	if before.ID != after.ID || initializations.Load() != 1 {
		t.Fatal("display update reconnected MCP")
	}
}
func TestCloseDuringInitializationCannotPublishSession(t *testing.T) {
	started := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer s.Close()
	m := connector.NewManager(newTestConfigStore(t))
	cfg := connector.ConnectorConfig{ID: "mcp", Name: "MCP", Type: "mcp", MCP: &connector.MCPConfig{Transport: connector.TransportStreamableHTTP, URL: s.URL}}
	if err := m.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := m.ListTools(t.Context(), cfg.ID); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := m.CloseContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
	if session := m.GetGatewaySession(cfg.ID); session != nil && session.State == connector.SessionStateReady {
		t.Fatal("late initialization published ready")
	}
}

func TestMCPReconnectPreservesCompetingCall(t *testing.T) {
	held, release := make(chan struct{}), make(chan struct{})
	var initializations, lists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var msg struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch msg.Method {
		case "initialize":
			initializations.Add(1)
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}}
		case "tools/list":
			switch lists.Add(1) {
			case 2:
				close(held)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			case 3, 4:
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32603, "message": "temporary failure"}})
				return
			}
			result = map[string]any{"tools": []any{}}
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}))
	defer server.Close()
	manager := connector.NewManager(newTestConfigStore(t))
	defer manager.Close()
	cfg := connector.ConnectorConfig{ID: "tools", Name: "Tools", Type: "mcp", MCP: &connector.MCPConfig{Transport: connector.TransportStreamableHTTP, URL: server.URL}}
	if err := manager.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ListTools(t.Context(), cfg.ID); err != nil {
		t.Fatal(err)
	}
	before := manager.GetGatewaySession(cfg.ID)
	finished := make(chan error, 1)
	go func() { _, err := manager.ListTools(t.Context(), cfg.ID); finished <- err }()
	<-held
	_, err := manager.ListTools(t.Context(), cfg.ID)
	during := manager.GetGatewaySession(cfg.ID)
	close(release)
	heldErr := <-finished
	if err == nil || !strings.Contains(err.Error(), "temporary failure") {
		t.Fatalf("shared-session failure lost upstream error: %v", err)
	}
	if heldErr != nil {
		t.Fatalf("competing call interrupted: %v", heldErr)
	}
	if initializations.Load() != 1 || during == nil || during.ID != before.ID || during.State != connector.SessionStateReady {
		t.Fatalf("shared session replaced: %#v", during)
	}
	// The next failure has sole ownership and may reconnect once.
	if _, err := manager.ListTools(t.Context(), cfg.ID); err != nil {
		t.Fatalf("exclusive reconnect: %v", err)
	}
	after := manager.GetGatewaySession(cfg.ID)
	if initializations.Load() != 2 || after == nil || after.ID == before.ID {
		t.Fatalf("missing exclusive reconnect: %#v", after)
	}
}
