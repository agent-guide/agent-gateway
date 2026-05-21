package service_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/mcp/service"
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

// newFakeStdioConfig returns an MCPServiceConfig that launches this test
// binary as a fake MCP stdio server.
func newFakeStdioConfig(id string) service.MCPServiceConfig {
	return service.MCPServiceConfig{
		ID:        id,
		Name:      "fake-stdio-" + id,
		Transport: service.TransportStdio,
		Command:   os.Args[0],
		// -test.run=^$ ensures the subprocess runs TestMain and exits before
		// any test function is executed.
		Args: []string{"-test.run=^$"},
		Env:  map[string]string{"MCP_STDIO_TEST_SERVER": "1"},
	}
}

func newInMemoryManager(t *testing.T, cfgs ...service.MCPServiceConfig) *service.Manager {
	t.Helper()
	store := newTestConfigStore(t)
	mgr := service.NewManager(store)
	ctx := context.Background()
	for _, cfg := range cfgs {
		if err := mgr.Create(ctx, cfg); err != nil {
			t.Fatalf("create service %q: %v", cfg.ID, err)
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
