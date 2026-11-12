package dispatcher

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/connector"
	"github.com/agent-guide/agent-gateway/pkg/connector/httpapi"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func TestHTTPConnectorPublishedThroughMCPIngress(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/docs" || r.URL.Query().Get("id") != "x" {
			t.Error("incorrect mapping")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"value":"HTTP TOOL MARKER"}`)
	}))
	defer server.Close()
	creator, err := sqlite.Open(t.Context(), sqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	backend := configstore.NewBackend(creator)
	defer backend.Close()
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	p := httpapi.DefaultPolicy()
	p.Rules = []httpapi.Rule{{Action: "allow", Schemes: []string{"http"}, Hosts: []string{"127.0.0.1"}, Ports: []int{server.Listener.Addr().(*net.TCPAddr).Port}, CIDRs: []string{"127.0.0.1/32"}}}
	raw, _ := json.Marshal(p)
	policyFile := t.TempDir() + "/policy.json"
	if err := os.WriteFile(policyFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	events := &usage.InMemorySink{}
	g := gateway.NewAgentGateway()
	if err := g.Bootstrap(t.Context(), gateway.BootstrapOptions{ConfigStoreBackend: backend, HTTPAPIPolicyFile: policyFile, UsageObserver: usage.NewObserver(events)}); err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	var cfg connector.ConnectorConfig
	raw = []byte(fmt.Sprintf(`{"id":"docs","name":"Documents","type":"http_api","http_api":{"base_url":%q,"tools":[{"name":"fetch_doc","description":"Fetch a document","input_schema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]},"request":{"method":"GET","path":"/docs","query":{"id":{"argument":"id"}}}}]}}`, server.URL))
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	route := routecore.RouteConfig{ID: "publish-docs", Kind: routecore.RouteKindMCP, Protocol: routecore.RouteProtocolMCP, MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/tools"}, TargetPolicy: json.RawMessage(`{"kind":"connector","connector_id":"docs"}`)}
	if err := g.RouteConfigManager().Create(t.Context(), route, "mcp"); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(g, nil, nil, HandlerOptions{EnableMCP: true})
	invoke := func(method, params string, status ...int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/tools", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, params)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if len(status) > 0 {
			want = status[0]
		}
		if rec.Code != want {
			t.Fatalf("%s: HTTP %d: %s", method, rec.Code, rec.Body.String())
		}
		return rec
	}
	if body := invoke("initialize", `{}`).Body.String(); !strings.Contains(body, `"tools"`) {
		t.Fatalf("initialize: %s", body)
	}
	if body := invoke("tools/list", `{}`).Body.String(); !strings.Contains(body, `fetch_doc`) {
		t.Fatalf("list: %s", body)
	}
	if calls.Load() != 0 {
		t.Fatal("discovery sent HTTP request")
	}
	if body := invoke("tools/call", `{"name":"fetch_doc","arguments":{"id":"x"}}`).Body.String(); !strings.Contains(body, "HTTP TOOL MARKER") {
		t.Fatalf("tool response: %s", body)
	}
	if calls.Load() != 1 {
		t.Fatalf("HTTP calls=%d", calls.Load())
	}
	if body := invoke("tools/call", `{"name":"fetch_doc","arguments":{"id":7}}`, http.StatusBadRequest).Body.String(); !strings.Contains(body, `invalid tool arguments`) {
		t.Fatalf("invalid args: %s", body)
	}
	if calls.Load() != 1 {
		t.Fatal("invalid schema reached HTTP upstream")
	}
	observed := 0
	for _, event := range events.Events {
		if ev, ok := event.(usage.MCPUsageEvent); ok && ev.Method == "tools/call" {
			observed++
			if ev.ConnectorType != "http_api" || ev.ServiceID != "docs" {
				t.Fatalf("incorrect usage: %+v", ev)
			}
			if observed == 1 && ev.HTTPExecution == nil {
				t.Fatalf("missing execution summary: %+v", ev)
			}
			if observed == 2 && ev.HTTPExecution != nil {
				t.Fatal("validation rejection fabricated HTTP summary")
			}
			if observed == 2 && (ev.Success || ev.ResultStatus != "error") {
				t.Fatalf("validation rejection recorded as success: %+v", ev)
			}
		}
	}
	if observed != 2 {
		t.Fatalf("tool observations=%d, want one per call", observed)
	}
}
