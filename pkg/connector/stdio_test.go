package connector_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/connector"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

// TestMain intercepts the test binary when it is re-launched as a fake MCP
// stdio server (MCP_STDIO_TEST_SERVER=1). In that mode it runs a minimal
// JSON-RPC server on stdin/stdout and exits without running any tests.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_STDIO_TEST_SERVER") == "1" {
		runFakeMCPServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeMCPServer reads JSON-RPC messages from stdin and writes responses to
// stdout, simulating a minimal MCP server over stdio.
func runFakeMCPServer() {
	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var msg transport.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			_ = enc.Encode(transport.Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: map[string]any{
					"protocolVersion": "2025-11-25",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "fake-mcp", "version": "0.1"},
				},
			})
		case "notifications/initialized":
			// notification — no response
		case "tools/list":
			_ = enc.Encode(transport.Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: map[string]any{
					"tools": []any{
						map[string]any{
							"name":        "echo",
							"description": "echoes input",
							"inputSchema": map[string]any{"type": "object"},
						},
					},
				},
			})
		case "tools/call":
			_ = enc.Encode(transport.Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "ok"},
					},
				},
			})
		}
	}
}

// newFakeStdioConfig returns an ConnectorConfig that launches this test
// binary as a fake MCP stdio server.
func newFakeStdioConfig(id string) connector.ConnectorConfig {
	return connector.ConnectorConfig{
		ID:   id,
		Name: "fake-stdio-" + id, Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio,
			Command: os.Args[0],
			// -test.run=^$ ensures the subprocess runs TestMain and exits before
			// any test function is executed.
			Args: []string{"-test.run=^$"},
			Env:  map[string]string{"MCP_STDIO_TEST_SERVER": "1"}},
	}
}

func newInMemoryManager(t *testing.T, cfgs ...connector.ConnectorConfig) *connector.Manager {
	t.Helper()
	store := newTestConfigStore(t)
	mgr := connector.NewManager(store)
	t.Cleanup(mgr.Close)
	ctx := context.Background()
	for _, cfg := range cfgs {
		if err := mgr.Create(ctx, cfg); err != nil {
			t.Fatalf("create connector %q: %v", cfg.ID, err)
		}
	}
	return mgr
}

func TestManagerStdioInitialize(t *testing.T) {
	cfg := newFakeStdioConfig("svc1")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	payload, err := mgr.Initialize(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if payload["protocolVersion"] != "2025-11-25" {
		t.Errorf("unexpected protocolVersion: %v", payload["protocolVersion"])
	}
}

func TestManagerStdioListTools(t *testing.T) {
	cfg := newFakeStdioConfig("svc2")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := mgr.ListTools(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("unexpected tool name: %q", tools[0].Name)
	}
}

func TestManagerStdioCallTool(t *testing.T) {
	cfg := newFakeStdioConfig("svc3")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := mgr.CallTool(ctx, cfg.ID, "echo", map[string]any{"input": "hello"}, nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result == nil || result.Content == nil {
		t.Fatal("expected non-nil content")
	}
}

func TestManagerStdioSessionReuse(t *testing.T) {
	cfg := newFakeStdioConfig("svc4")
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Two consecutive calls must reuse the same session (no reconnect).
	if _, err := mgr.ListTools(ctx, cfg.ID); err != nil {
		t.Fatalf("first ListTools: %v", err)
	}
	if _, err := mgr.ListTools(ctx, cfg.ID); err != nil {
		t.Fatalf("second ListTools: %v", err)
	}
}

func TestStdioSessionSurvivesInitializingRequestCompletion(t *testing.T) {
	cfg := newFakeStdioConfig("persistent")
	manager := newInMemoryManager(t, cfg)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	if _, err := manager.Initialize(ctx, cfg.ID); err != nil {
		cancel()
		t.Fatal(err)
	}
	before := manager.GetGatewaySession(cfg.ID)
	cancel()
	// A request lifetime must not be the lifetime of its cached subprocess.
	if _, err := manager.ListTools(t.Context(), cfg.ID); err != nil {
		t.Fatal(err)
	}
	after := manager.GetGatewaySession(cfg.ID)
	if before.ID != after.ID {
		t.Fatal("request completion killed cached stdio process")
	}
	closeCtx, closeCancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer closeCancel()
	if err := manager.CloseContext(closeCtx); err != nil {
		t.Fatal(err)
	}
	if session := manager.GetGatewaySession(cfg.ID); session == nil || session.State != connectormcp.SessionStateClosed {
		t.Fatal("shutdown retained ready subprocess session")
	}
}
