package connector_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/connector"
	"github.com/agent-guide/agent-gateway/pkg/connector/httpapi"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

func httpConnector(t *testing.T, base, path string) connector.ConnectorConfig {
	t.Helper()
	raw := fmt.Sprintf(`{"id":"orders","name":"Orders","type":"http_api","http_api":{"base_url":%q,"tools":[{"name":"lookup","description":"Look up","input_schema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]},"request":{"method":"POST","path":%q,"body":{"id":{"argument":"id"}}}}]}}`, base, path)
	var cfg connector.ConnectorConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}
func httpManager(t *testing.T, server *httptest.Server) *connector.Manager {
	t.Helper()
	manager := connector.NewManager(newTestConfigStore(t))
	configureLocalEngine(t, manager, server)
	return manager
}
func configureLocalEngine(t *testing.T, m *connector.Manager, s *httptest.Server) {
	t.Helper()
	p := httpapi.DefaultPolicy()
	p.Rules = []httpapi.Rule{{Action: "allow", Schemes: []string{"http"}, Hosts: []string{"127.0.0.1"}, Ports: []int{s.Listener.Addr().(*net.TCPAddr).Port}, CIDRs: []string{"127.0.0.1/32"}}}
	engine, err := httpapi.NewEngine(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.ConfigureHTTP(engine)
	t.Cleanup(m.Close)
}
func TestHTTPLeasePinsCatalogAndMappingAndFencesIncarnations(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer s.Close()
	ctx := context.Background()
	m := httpManager(t, s)
	cfg := httpConnector(t, s.URL, "/v1")
	if err := m.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	old, err := m.Acquire(ctx, "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	first, _ := old.ListTools(ctx)
	cfg.HTTPAPI.Tools[0].Request.Path = "/v2"
	if err := m.Update(ctx, "orders", cfg); err != nil {
		t.Fatal(err)
	}
	current, _ := m.Acquire(ctx, "orders")
	defer current.Close()
	second, _ := current.ListTools(ctx)
	if first.Generation == second.Generation {
		t.Fatal("catalog generation did not change")
	}
	if _, err := old.ListTools(ctx); err == nil {
		t.Fatal("stale catalog handle accepted")
	}
	if _, err := old.CallTool(ctx, cr.ToolCall{Name: "lookup", Arguments: map[string]any{"id": "one"}}); err == nil {
		t.Fatal("stale execution handle accepted")
	}
	out, err := current.CallTool(ctx, cr.ToolCall{Name: "lookup", Arguments: map[string]any{"id": "one"}})
	if err != nil || out.Result.IsError || out.HTTP == nil {
		t.Fatalf("call: %#v %v", out, err)
	}

	mu.Lock()
	if strings.Join(paths, ",") != "/v2" {
		t.Fatalf("mixed mappings: %v", paths)
	}
	mu.Unlock()
	if err := m.Delete(ctx, "orders"); err != nil {
		t.Fatal(err)
	}
	if err := m.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := old.CallTool(ctx, cr.ToolCall{Name: "lookup", Arguments: map[string]any{"id": "one"}}); err == nil {
		t.Fatal("deleted lease revived after recreation")
	}
}
func TestHTTPLeaseConcurrentCatalogAndUncertainWrite(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); fmt.Fprint(w, "sensitive response") }))
	defer s.Close()
	m := httpManager(t, s)
	ctx := context.Background()
	if err := m.Create(ctx, httpConnector(t, s.URL, "/write")); err != nil {
		t.Fatal(err)
	}
	lease, _ := m.Acquire(ctx, "orders")
	defer lease.Close()
	var wg sync.WaitGroup
	tokens := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			cat, err := lease.ListTools(ctx)
			if err != nil {
				t.Error(err)
			}
			tokens <- cat.Generation
		})
	}
	wg.Wait()
	close(tokens)
	var token string
	for v := range tokens {
		if token != "" && v != token {
			t.Fatal("concurrent listing repinned catalog")
		}
		token = v
	}
	out, err := lease.CallTool(ctx, cr.ToolCall{Name: "lookup", Arguments: map[string]any{"id": "one"}})
	if err != nil || out.Failure == nil || !out.Failure.OutcomeUncertain || out.Result == nil || !out.Result.IsError || strings.Contains(fmt.Sprint(out.Result.Content), "sensitive") {
		t.Fatalf("unsafe failure: %#v %v", out, err)
	}
}

type uncertainStore struct {
	*testConfigStore
	fail bool
}

func (s *uncertainStore) Update(ctx context.Context, obj any) error {
	if err := s.testConfigStore.Update(ctx, obj); err != nil {
		return err
	}
	if s.fail {
		return errors.New("lost write response")
	}
	return nil
}
func TestUnconfirmedConnectorWriteIsGatedAndRecoveredWithoutReplay(t *testing.T) {
	ctx := context.Background()
	store := &uncertainStore{testConfigStore: newTestConfigStore(t).(*testConfigStore)}
	m := connector.NewManager(store)
	defer m.Close()
	cfg := connector.ConnectorConfig{ID: "docs", Name: "Docs", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
	if err := m.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Description = "updated"
	store.fail = true
	if err := m.Update(ctx, "docs", cfg); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("write not pending: %v", err)
	}
	pending, ok := m.PendingMutation("docs")
	if !ok || pending.OperationID == "" {
		t.Fatal("missing operation identity")
	}
	if _, err := m.Get(ctx, "docs"); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal("pending connector readable for execution")
	}
	for _, operation := range []struct {
		name string
		call func() error
	}{
		{"initialize", func() error { _, err := m.Initialize(ctx, "docs"); return err }},
		{"tools", func() error { _, err := m.ListTools(ctx, "docs"); return err }},
		{"tools_page", func() error { _, err := m.ListToolsPage(ctx, "docs", ""); return err }},
		{"resources", func() error { _, err := m.ListResources(ctx, "docs"); return err }},
		{"resource_templates", func() error { _, err := m.ListResourceTemplates(ctx, "docs"); return err }},
		{"prompts", func() error { _, err := m.ListPrompts(ctx, "docs"); return err }},
		{"read_resource", func() error { _, err := m.ReadResource(ctx, "docs", "resource://docs", nil); return err }},
		{"get_prompt", func() error { _, err := m.GetPrompt(ctx, "docs", "lookup", nil, nil); return err }},
		{"complete", func() error {
			_, err := m.Complete(ctx, "docs", basemcp.CompletionReference{Type: "ref/prompt", Name: "lookup"}, basemcp.CompletionArgument{Name: "query"}, nil)
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call()
			var boundary *cr.CallError
			var management *configmgr.PendingMutationError
			if !errors.As(err, &boundary) || boundary.Category != "mutation_pending" || !errors.Is(err, configmgr.ErrMutationPending) || !errors.As(err, &management) || management.Mutation.OperationID != pending.OperationID {
				t.Fatalf("pending admission lost typed contracts: %v", err)
			}
			if err.Error() != "mutation_pending: connector requires recovery" {
				t.Fatalf("execution error exposed management details: %v", err)
			}
		})
	}
	if err := m.Refresh(ctx); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal("refresh bypassed pending mutation")
	}
	if err := m.Recover(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(ctx, "docs")
	if err != nil || got.Description != "updated" {
		t.Fatalf("recovery: %#v %v", got, err)
	}
	if !store.fail {
		t.Fatal("test failed to leave writes failing during recovery")
	}
}
func TestConnectorStrictUnionAndHTTPNulls(t *testing.T) {
	for _, raw := range []string{`{"id":"a","name":"A","type":"mcp","mcp":{"transport":"stdio","command":"x"},"http_api":null}`, `{"id":"a","name":"A","type":"mcp","transport":null}`, `{"id":"a","id":"b","name":"A","type":"mcp"}`, `{"id":"a","name":"A","type":"http_api","http_api":{"base_url":"https://example.com","tools":[],"timeout_ms":0}}`, `{"id":"a","name":"A","type":"http_api","http_api":{"base_url":"https://example.com","tools":[],"auth":{"type":"none","credential_ref":""}}}`} {
		var c connector.ConnectorConfig
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			t.Fatalf("invalid representation accepted: %s", raw)
		}
	}
}
