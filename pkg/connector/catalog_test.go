package connector_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/connector"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
)

func TestMCPLeaseExecutesToolsFromLaterPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(204)
			return
		}
		var msg struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch msg.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}}
		case "tools/list":
			if msg.Params["cursor"] == "page-2" {
				result = map[string]any{"tools": []any{map[string]any{"name": "later"}}}
			} else {
				result = map[string]any{"tools": []any{map[string]any{"name": "first"}}, "nextCursor": "page-2"}
			}
		case "tools/call":
			if msg.Params["name"] != "later" {
				t.Error("wrong tool")
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "later page executed"}}}
		default:
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}))
	defer server.Close()
	manager := connector.NewManager(newTestConfigStore(t))
	defer manager.Close()
	cfg := connector.ConnectorConfig{ID: "tools", Name: "Tools", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStreamableHTTP, URL: server.URL}}
	if err := manager.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	page, err := manager.ListToolsPage(t.Context(), cfg.ID, "")
	if err != nil || len(page.Tools) != 1 || page.NextCursor != "page-2" {
		t.Fatalf("ingress pagination changed: %#v %v", page, err)
	}
	tools, err := manager.ListTools(t.Context(), cfg.ID)
	if err != nil || len(tools) != 2 {
		t.Fatalf("Admin catalog incomplete: %#v %v", tools, err)
	}
	lease, err := manager.Acquire(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	outcome, err := lease.CallTool(t.Context(), cr.ToolCall{Name: "later"})
	if err != nil || outcome.Result == nil || outcome.Result.IsError {
		t.Fatalf("later tool unavailable: %#v %v", outcome, err)
	}
}
