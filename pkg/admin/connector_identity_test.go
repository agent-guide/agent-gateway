package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/connector"
	"github.com/agent-guide/agent-gateway/pkg/connector/httpapi"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

type uncertainConnectorStore struct {
	*testConnectorStore
	failRead bool
}

func (s *uncertainConnectorStore) Update(ctx context.Context, obj any) error {
	if err := s.testConnectorStore.Update(ctx, obj); err != nil {
		return err
	}
	return errors.New("lost write response")
}

func (s *uncertainConnectorStore) Get(ctx context.Context, keys ...any) (any, error) {
	if s.failRead {
		return nil, errors.New("storage unavailable")
	}
	return s.testConnectorStore.Get(ctx, keys...)
}

func TestConnectorToolCallAdmissionStatuses(t *testing.T) {
	for _, state := range []string{"missing", "disabled", "pending"} {
		t.Run(state, func(t *testing.T) {
			cfg := connector.ConnectorConfig{ID: "orders", Name: "Orders", Type: "mcp", Disabled: state == "disabled", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
			store := &uncertainConnectorStore{testConnectorStore: &testConnectorStore{items: map[string]*connector.ConnectorConfig{}}}
			if state != "missing" {
				store.items[cfg.ID] = &cfg
			}
			gateway := newTestAgentGateway(&testConfigStore{connectorStore: store}, nil, nil, nil, nil)
			t.Cleanup(gateway.Close)
			if state == "pending" {
				cfg.Description = "updated"
				if err := gateway.ConnectorManager().Update(t.Context(), cfg.ID, cfg); !errors.Is(err, configmgr.ErrMutationPending) {
					t.Fatalf("expected unconfirmed write: %v", err)
				}
				store.failRead = true
				if err := gateway.ConnectorManager().Recover(t.Context(), cfg.ID); !errors.Is(err, configmgr.ErrMutationPending) {
					t.Fatalf("expected recovery backoff: %v", err)
				}
			}
			response := httptest.NewRecorder()
			NewHandler(gateway, nil).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/connectors/orders/tools/call", bytes.NewBufferString(`{"name":"lookup"}`)))
			want := http.StatusServiceUnavailable
			if state == "missing" {
				want = http.StatusNotFound
			}
			if response.Code != want {
				t.Fatalf("%s: HTTP %d, want %d: %s", state, response.Code, want, response.Body.String())
			}
			if state == "pending" && response.Header().Get("Retry-After") == "" {
				t.Fatal("pending mutation lost recovery backoff")
			}
		})
	}
}

func TestConnectorPendingReadsPreserveRecoveryBackoff(t *testing.T) {
	cfg := connector.ConnectorConfig{ID: "orders", Name: "Orders", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
	store := &uncertainConnectorStore{testConnectorStore: &testConnectorStore{items: map[string]*connector.ConnectorConfig{cfg.ID: &cfg}}}
	gateway := newTestAgentGateway(&testConfigStore{connectorStore: store}, nil, nil, nil, nil)
	t.Cleanup(gateway.Close)
	cfg.Description = "updated"
	if err := gateway.ConnectorManager().Update(t.Context(), cfg.ID, cfg); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("expected unconfirmed write: %v", err)
	}
	store.failRead = true
	if err := gateway.ConnectorManager().Recover(t.Context(), cfg.ID); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("expected recovery backoff: %v", err)
	}
	handler := NewHandler(gateway, nil)
	for _, tc := range []struct {
		path string
		body string
	}{
		{"", ""},
		{"/capabilities", ""},
		{"/sessions", ""},
		{"/tools", ""},
		{"/resources", ""},
		{"/resource-templates", ""},
		{"/prompts", ""},
		{"/resources/read", `{"uri":"resource://orders"}`},
		{"/prompts/get", `{"name":"orders"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			method := http.MethodGet
			if tc.body != "" {
				method = http.MethodPost
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, "/admin/connectors/orders"+tc.path, bytes.NewBufferString(tc.body)))
			if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" {
				t.Fatalf("pending mutation: HTTP %d, Retry-After %q: %s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
			}
		})
	}
}

func TestMCPRouteWritesPreserveConnectorRecoveryBackoff(t *testing.T) {
	cfg := connector.ConnectorConfig{ID: "orders", Name: "Orders", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
	store := &uncertainConnectorStore{testConnectorStore: &testConnectorStore{items: map[string]*connector.ConnectorConfig{cfg.ID: &cfg}}}
	gateway := newTestAgentGateway(&testConfigStore{connectorStore: store, routeStore: &testMCPRouteStore{items: map[string]*routecore.RouteConfig{}}}, nil, nil, nil, nil)
	t.Cleanup(gateway.Close)
	handler := NewHandler(gateway, nil)
	body := `{"id":"publish-orders","connector_id":"orders","match_policy":{"path_prefix":"/orders"}}`
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/admin/mcp/routes", bytes.NewBufferString(body)))
	if created.Code != http.StatusCreated {
		t.Fatalf("create initial route: HTTP %d: %s", created.Code, created.Body.String())
	}
	cfg.Description = "updated"
	if err := gateway.ConnectorManager().Update(t.Context(), cfg.ID, cfg); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("expected unconfirmed connector write: %v", err)
	}
	store.failRead = true
	if err := gateway.ConnectorManager().Recover(t.Context(), cfg.ID); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("expected recovery backoff: %v", err)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		path := "/admin/mcp/routes"
		if method == http.MethodPut {
			path += "/publish-orders"
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" {
			t.Fatalf("%s lost connector recovery status: HTTP %d, Retry-After %q: %s", method, response.Code, response.Header().Get("Retry-After"), response.Body.String())
		}
	}
}

func TestDeleteMissingConnectorReturnsNotFound(t *testing.T) {
	gateway := newTestAgentGateway(&testConfigStore{connectorStore: &testConnectorStore{}}, nil, nil, nil, nil)
	t.Cleanup(gateway.Close)
	response := httptest.NewRecorder()
	NewHandler(gateway, nil).ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/admin/connectors/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing delete: HTTP %d: %s", response.Code, response.Body.String())
	}
	if _, pending := gateway.ConnectorManager().PendingMutation("missing"); pending {
		t.Fatal("missing delete requires recovery")
	}
}

func TestMissingConnectorDiscoveryReturnsNotFound(t *testing.T) {
	gateway := newTestAgentGateway(&testConfigStore{connectorStore: &testConnectorStore{}}, nil, nil, nil, nil)
	t.Cleanup(gateway.Close)
	handler := NewHandler(gateway, nil)
	for _, tc := range []struct{ path, body string }{
		{"/capabilities", ""},
		{"/tools", ""},
		{"/resources", ""},
		{"/resource-templates", ""},
		{"/prompts", ""},
		{"/resources/read", `{"uri":"resource://missing"}`},
		{"/prompts/get", `{"name":"missing"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			method := http.MethodGet
			if tc.body != "" {
				method = http.MethodPost
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, "/admin/connectors/missing"+tc.path, bytes.NewBufferString(tc.body)))
			if response.Code != http.StatusNotFound {
				t.Fatalf("missing connector: HTTP %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestConnectorUpdateIDContract(t *testing.T) {
	cfg := connector.ConnectorConfig{ID: "orders", Name: "Orders", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
	gateway := newTestAgentGateway(&testConfigStore{connectorStore: &testConnectorStore{items: map[string]*connector.ConnectorConfig{"orders": &cfg}}}, nil, nil, nil, nil)
	t.Cleanup(gateway.Close)
	handler := NewHandler(gateway, nil)
	for _, tc := range []struct {
		id     string
		status int
	}{{"different", http.StatusBadRequest}, {"orders", http.StatusOK}, {"", http.StatusOK}} {
		body := cfg
		body.ID = tc.id
		body.Name = "Order center"
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/admin/connectors/orders", bytes.NewReader(raw))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("body ID %q: status %d: %s", tc.id, response.Code, response.Body.String())
		}
		stored, err := gateway.ConnectorManager().Get(t.Context(), "orders")
		if err != nil || stored.ID != "orders" {
			t.Fatalf("stable reference lost: %#v %v", stored, err)
		}
		if tc.status == http.StatusOK && stored.Name != "Order center" {
			t.Fatal("display name update failed")
		}
	}
}

func TestReferencedConnectorDeleteReturnsConflict(t *testing.T) {
	cfg := connector.ConnectorConfig{ID: "orders", Name: "Orders", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
	gateway := newTestAgentGateway(&testConfigStore{connectorStore: &testConnectorStore{items: map[string]*connector.ConnectorConfig{"orders": &cfg}}, agentStore: newAgentConfigStore()}, nil, nil, nil, nil)
	t.Cleanup(gateway.Close)
	a := agentpkg.Agent{ID: "worker", Name: "Worker", Disabled: true, Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeHTTP, HTTP: &agentpkg.HTTPRuntime{CardURL: "https://example.org/card", Protocol: "a2a"}}, Resources: agentpkg.Resources{ConnectorIDs: []string{"orders"}}}
	if err := gateway.AgentManager().Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(gateway, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/admin/connectors/orders", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("delete status: %d %s", response.Code, response.Body.String())
	}
	a.Resources.ConnectorIDs = nil
	if err := gateway.AgentManager().Update(t.Context(), a.ID, a); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/admin/connectors/orders", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unreferenced delete: %d %s", response.Code, response.Body.String())
	}
}

func TestConnectorToolCallClientErrors(t *testing.T) {
	var cfg connector.ConnectorConfig
	if err := json.Unmarshal([]byte(`{"id":"orders","name":"Orders","type":"http_api","http_api":{"base_url":"https://example.com","tools":[{"name":"lookup","description":"Look up an order","input_schema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false},"request":{"method":"GET","path":"/orders"}}]}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	store := &testConnectorStore{items: map[string]*connector.ConnectorConfig{cfg.ID: &cfg}}
	gateway := newTestAgentGateway(&testConfigStore{connectorStore: store}, nil, nil, nil, nil)
	t.Cleanup(gateway.Close)
	// No upstream request may be made for either invalid call.
	policy := httpapi.DefaultPolicy()
	policy.DefaultAction = "deny"
	engine, err := httpapi.NewEngine(policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway.ConnectorManager().ConfigureHTTP(engine)
	for _, tc := range []struct{ body, category string }{
		{`{"name":"missing","arguments":{}}`, "unknown_tool"},
		{`{"name":"lookup","arguments":{}}`, "invalid_arguments"},
		{`{"name":"lookup","arguments":{"id":42}}`, "invalid_arguments"},
	} {
		w := httptest.NewRecorder()
		NewHandler(gateway, nil).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/connectors/orders/tools/call", bytes.NewBufferString(tc.body)))
		if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte(tc.category)) {
			t.Fatalf("%s: status=%d response=%s", tc.body, w.Code, w.Body.String())
		}
	}
}
