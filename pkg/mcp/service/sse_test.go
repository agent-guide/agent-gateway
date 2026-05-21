package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/mcp/service"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

// sseTestServer is an in-process HTTP server implementing the legacy SSE MCP transport.
// GET /sse opens the SSE event stream; POST /message accepts JSON-RPC requests and
// delivers responses back through the stream.
type sseTestServer struct {
	mu      sync.Mutex
	clients []chan string
	done    chan struct{}
	wg      sync.WaitGroup
}

func (s *sseTestServer) broadcast(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.clients {
		select {
		case ch <- line:
		default:
		}
	}
}

func (s *sseTestServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := make(chan string, 32)
	s.mu.Lock()
	s.clients = append(s.clients, ch)
	s.mu.Unlock()

	s.wg.Add(1)
	defer s.wg.Done()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		}
	}
}

func (s *sseTestServer) handleMessage(w http.ResponseWriter, r *http.Request) {
	var msg transport.Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var reply *transport.Message
	switch msg.Method {
	case "initialize":
		reply = &transport.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake-sse-mcp", "version": "0.1"},
			},
		}
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return
	case "tools/list":
		reply = &transport.Message{
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
		}
	case "tools/call":
		reply = &transport.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": "ok"},
				},
			},
		}
	default:
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if reply != nil {
		data, _ := json.Marshal(reply)
		s.broadcast(string(data))
	}
	w.WriteHeader(http.StatusAccepted)
}

// newFakeSSEConfig starts a fake SSE MCP server and returns its service config.
// The server is shut down via t.Cleanup: the done channel is closed so that
// SSE handler goroutines exit cleanly before httptest.Server.Close() is called.
func newFakeSSEConfig(t *testing.T, id string) service.MCPServiceConfig {
	t.Helper()
	srv := &sseTestServer{done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", srv.handleSSE)
	mux.HandleFunc("/message", srv.handleMessage)
	ts := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(srv.done) // signal SSE handler goroutines to exit
		srv.wg.Wait()   // wait until they do
		ts.Close()
	})
	return service.MCPServiceConfig{
		ID:        id,
		Name:      "fake-sse-" + id,
		Transport: service.TransportSSE,
		URL:       ts.URL + "/sse",
		PostURL:   ts.URL + "/message",
	}
}

func TestManagerSSEInitialize(t *testing.T) {
	cfg := newFakeSSEConfig(t, "sse1")
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

func TestManagerSSEListTools(t *testing.T) {
	cfg := newFakeSSEConfig(t, "sse2")
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

func TestManagerSSECallTool(t *testing.T) {
	cfg := newFakeSSEConfig(t, "sse3")
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

func TestManagerSSESessionReuse(t *testing.T) {
	cfg := newFakeSSEConfig(t, "sse4")
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

func TestManagerSSECallToolRelaysProgress(t *testing.T) {
	// Stand-alone server that sends a notifications/progress message before the final result.
	var (
		clientsMu sync.Mutex
		clients   []chan string
	)
	broadcast := func(line string) {
		clientsMu.Lock()
		defer clientsMu.Unlock()
		for _, ch := range clients {
			select {
			case ch <- line:
			default:
			}
		}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		ch := make(chan string, 32)
		clientsMu.Lock()
		clients = append(clients, ch)
		clientsMu.Unlock()
		wg.Add(1)
		defer wg.Done()
		for {
			select {
			case line := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			case <-r.Context().Done():
				return
			case <-done:
				return
			}
		}
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		var msg transport.Message
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch msg.Method {
		case "initialize":
			data, _ := json.Marshal(&transport.Message{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{
				"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
			}})
			broadcast(string(data))
		case "tools/call":
			// broadcast a progress notification first, then the final result
			prog, _ := json.Marshal(&transport.Message{
				JSONRPC: "2.0",
				Method:  "notifications/progress",
				Params: map[string]any{
					"progressToken": "tok-1",
					"progress":      float64(1),
					"total":         float64(3),
					"message":       "step 1",
				},
			})
			broadcast(string(prog))
			data, _ := json.Marshal(&transport.Message{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "done"}},
			}})
			broadcast(string(data))
		}
		w.WriteHeader(http.StatusAccepted)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(done)
		wg.Wait()
		ts.Close()
	})

	cfg := service.MCPServiceConfig{
		ID: "sse-progress", Name: "sse-progress",
		Transport: service.TransportSSE,
		URL:       ts.URL + "/sse",
		PostURL:   ts.URL + "/message",
	}
	mgr := newInMemoryManager(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	progressCh := make(chan service.UpstreamProgress, 8)
	result, err := mgr.CallTool(ctx, cfg.ID, "echo", nil, progressCh)
	close(progressCh)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	var collected []service.UpstreamProgress
	for n := range progressCh {
		collected = append(collected, n)
	}
	if len(collected) != 1 {
		t.Fatalf("expected 1 progress notification, got %d", len(collected))
	}
	p := collected[0]
	if p.Progress != 1 {
		t.Fatalf("unexpected progress value: %v", p.Progress)
	}
	if p.Total == nil || *p.Total != 3 {
		t.Fatalf("unexpected total: %v", p.Total)
	}
	if p.Message != "step 1" {
		t.Fatalf("unexpected message: %q", p.Message)
	}
}

func TestManagerSSEDerivePostURL(t *testing.T) {
	// Verify that an SSE service config with no PostURL set still works
	// because the Manager derives the POST URL from the stream URL (/sse → /message).
	cfg := newFakeSSEConfig(t, "sse5")
	cfg.PostURL = "" // clear; Manager must derive /message from /sse
	mgr := newInMemoryManager(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := mgr.ListTools(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("ListTools with derived postURL: %v", err)
	}
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
}
