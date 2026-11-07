package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func TestRouteDeletionConflictDuringPendingAgentDeletion(t *testing.T) {
	store := &testRouteStore{items: map[string]*routecore.AgentRouteConfig{
		"chat":  {ID: "chat", Kind: routecore.RouteKindLLM, Protocol: "openai", MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/chat"}},
		"tools": {ID: "tools", Kind: routecore.RouteKindMCP, Protocol: "mcp", MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/tools"}},
	}}
	g := newTestAgentGateway(&testConfigStore{routeStore: store, agentStore: newAgentConfigStore()}, nil, nil, nil, nil)
	t.Cleanup(g.Close)
	a := agent.Agent{
		ID: "a", Name: "a", Disabled: true,
		Runtime: agent.Runtime{Type: agent.RuntimeTypeHTTP, HTTP: &agent.HTTPRuntime{CardURL: "https://example.org/card", Protocol: "a2a"}},
		Routes:  agent.Routes{LLMRouteIDs: []string{"chat"}, MCPRouteIDs: []string{"tools"}},
	}
	if err := g.AgentManager().Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	fail := true
	g.AgentManager().AddDefinitionListener(func(context.Context, []agent.Agent) agent.DefinitionCommit {
		if fail {
			panic("injected publication failure")
		}
		return nil
	})
	if err := g.AgentManager().Delete(t.Context(), a.ID); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("delete = %v", err)
	}
	if _, err := g.AgentManager().Get(t.Context(), a.ID); !errors.Is(err, agent.ErrAgentNotConfigured) {
		t.Fatalf("Agent still stored: %v", err)
	}
	h := NewHandler(g, nil)
	for _, tc := range []struct {
		id      string
		handler http.HandlerFunc
	}{{"chat", h.handleDeleteLLMRoute}, {"tools", h.handleDeleteMCPRoute}} {
		t.Run(tc.id, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodDelete, "/", nil)
			r.SetPathValue("id", tc.id)
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "resource is referenced") {
				t.Fatalf("delete = %d %s", w.Code, w.Body.String())
			}
			if _, err := g.AgentRouteConfigManager().Get(t.Context(), tc.id); err != nil {
				t.Fatalf("protected route removed: %v", err)
			}
		})
	}
	fail = false
	if err := g.AgentManager().Recover(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPendingMutationCanBeInspectedAndRecoveredThroughAdmin(t *testing.T) {
	g := newTestAgentGateway(&testConfigStore{agentStore: newAgentConfigStore()}, nil, nil, nil, nil)
	t.Cleanup(g.Close)
	a := agent.Agent{ID: "a", Name: "before", Disabled: true, Runtime: agent.Runtime{Type: agent.RuntimeTypeHTTP, HTTP: &agent.HTTPRuntime{CardURL: "https://example.org/card", Protocol: "a2a"}}}
	if err := g.AgentManager().Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	fail := true
	g.AgentManager().AddDefinitionListener(func(_ctx context.Context, _ []agent.Agent) agent.DefinitionCommit {
		if fail {
			panic("private listener diagnostic")
		}
		return nil
	})
	h := NewHandler(g, nil)
	call := func(handler http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/admin/agents/a", strings.NewReader(body))
		r.SetPathValue("id", "a")
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	a.Name = "after"
	body, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	w := call(h.handleUpdateAgent, http.MethodPut, string(body))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "private listener diagnostic") {
		t.Fatalf("update = %d %s", w.Code, w.Body.String())
	}
	stored, err := g.AgentManager().Get(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = call(h.handleAgentMutationStatus, http.MethodGet, "")
	var status struct {
		Pending *configmgr.PendingMutation `json:"pending_mutation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || status.Pending == nil || status.Pending.State != "pending_recovery" {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	w = call(h.handleUpdateAgent, http.MethodPut, string(body))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("write retry bypassed pending recovery")
	}
	if after, _ := g.AgentManager().Get(t.Context(), a.ID); !after.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Fatal("retry issued another write")
	}
	w = call(h.handleGetAgent, http.MethodGet, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"pending_mutation"`) {
		t.Fatal("resource read did not expose pending state")
	}
	fail = false
	w = call(h.handleRecoverAgent, http.MethodPost, "")
	if w.Code != http.StatusOK || g.AgentManager().PendingMutation(a.ID) != nil {
		t.Fatalf("recover = %d %s", w.Code, w.Body.String())
	}
	if got, _ := g.AgentManager().GetSnapshot(a.ID); got.Name != "after" || g.AgentManager().CheckExecution(got) != nil {
		t.Fatal("Admin recovery did not publish committed facts")
	}
}
