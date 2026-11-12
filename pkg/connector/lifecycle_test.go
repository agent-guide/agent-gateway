package connector_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/connector"
	"github.com/agent-guide/agent-gateway/pkg/connector/httpapi"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
)

func TestBusyHTTPRejectsEditsDeletionAndPreservesMetadataGeneration(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer s.Close()
	m := httpManager(t, s)
	cfg := httpConnector(t, s.URL, "/call")
	if err := m.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	l, _ := m.Acquire(t.Context(), cfg.ID)
	defer l.Close()
	generation := l.Descriptor().Generation
	finished := make(chan error, 1)
	go func() {
		_, err := l.CallTool(t.Context(), cr.ToolCall{Name: "lookup", Arguments: map[string]any{"id": "1"}})
		finished <- err
	}()
	<-started
	changed := httpConnector(t, s.URL, "/new")
	if err := m.Update(t.Context(), cfg.ID, changed); !errors.Is(err, connector.ErrConnectorBusy) {
		t.Fatalf("busy edit: %v", err)
	}
	if err := m.Delete(t.Context(), cfg.ID); !errors.Is(err, connector.ErrConnectorBusy) {
		t.Fatalf("busy delete: %v", err)
	}
	cfg.Name = "Changed display"
	if err := m.Update(t.Context(), cfg.ID, cfg); err != nil {
		t.Fatal(err)
	}
	state, _ := m.State(t.Context(), cfg.ID)
	if state.Generation != generation {
		t.Fatal("metadata replaced execution generation")
	}
	if err := m.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, _ = m.State(t.Context(), cfg.ID)
	if state.Generation != generation {
		t.Fatal("refresh replaced unchanged execution")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := m.Update(t.Context(), cfg.ID, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CallTool(t.Context(), cr.ToolCall{Name: "lookup", Arguments: map[string]any{"id": "1"}}); err == nil {
		t.Fatal("stale handle admitted")
	}
}
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
func TestConnectorCloseCancelsAndWaitsForHTTPWork(t *testing.T) {
	started := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer s.Close()
	m := httpManager(t, s)
	cfg := httpConnector(t, s.URL, "/wait")
	if err := m.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); m.CallTool(t.Context(), cfg.ID, "lookup", map[string]any{"id": "1"}, nil) }()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := m.CloseContext(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("close returned before work finished")
	}
	if _, err := m.Acquire(t.Context(), cfg.ID); err == nil {
		t.Fatal("closed manager acquired")
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

func TestConnectorShutdownDeadlineRetainsUnfinishedWork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	manager := connector.NewManager(newTestConfigStore(t))
	policy := httpapi.DefaultPolicy()
	policy.Rules = []httpapi.Rule{{Action: "allow", Schemes: []string{"http"}, Hosts: []string{"127.0.0.1"}, Ports: []int{server.Listener.Addr().(*net.TCPAddr).Port}, CIDRs: []string{"127.0.0.1/32"}}}
	engine, err := httpapi.NewEngine(policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, release := make(chan struct{}), make(chan struct{})
	engine.Observe = func(context.Context, httpapi.HTTPExecution, time.Time) { close(observed); <-release }
	manager.ConfigureHTTP(engine)
	defer manager.Close()
	cfg := httpConnector(t, server.URL, "/call")
	if err := manager.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.CallTool(t.Context(), cfg.ID, "lookup", map[string]any{"id": "1"}, nil)
	}()
	<-observed
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err = manager.CloseContext(ctx)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown concealed unfinished work: %v", err)
	}
	<-done
	if err := manager.CloseContext(t.Context()); err != nil {
		t.Fatalf("completed shutdown: %v", err)
	}
}

func TestBusyRefreshRetainsPublishedExecution(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	store := newTestConfigStore(t)
	manager := connector.NewManager(store)
	configureLocalEngine(t, manager, server)
	cfg := httpConnector(t, server.URL, "/old")
	if err := manager.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := manager.State(t.Context(), cfg.ID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.CallTool(t.Context(), cfg.ID, "lookup", map[string]any{"id": "1"}, nil)
	}()
	<-started
	cfg.HTTPAPI.Tools[0].Request.Path = "/next"
	if err := store.Update(t.Context(), &cfg); err != nil {
		t.Fatal(err)
	}
	err := manager.Refresh(t.Context())
	close(release)
	<-done
	if !errors.Is(err, connector.ErrConnectorBusy) {
		t.Fatalf("busy Refresh: %v", err)
	}
	after, _ := manager.State(t.Context(), cfg.ID)
	if before.Generation != after.Generation {
		t.Fatal("failed Refresh changed generation")
	}
	if err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ = manager.State(t.Context(), cfg.ID)
	if before.Generation == after.Generation {
		t.Fatal("idle Refresh did not publish execution")
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
