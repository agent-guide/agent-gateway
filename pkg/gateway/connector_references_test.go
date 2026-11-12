package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/connector"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func referenceConnector(id string) connector.ConnectorConfig {
	return connector.ConnectorConfig{ID: id, Name: "Tools", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
}
func referencingAgent(id, connectorID string) agent.Agent {
	return agent.Agent{ID: id, Name: "Worker", Disabled: true, Runtime: agent.Runtime{Type: agent.RuntimeTypeACP, ACP: &agent.ACPRuntime{AgentType: "codex", CWD: "/tmp", AllowedRoots: []string{"/tmp"}}}, Resources: agent.Resources{ConnectorIDs: []string{connectorID}}}
}
func TestConnectorDeletionRequiresReleasingAgentReferences(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	ctx := t.Context()
	m := g.ConnectorManager()
	cfg := referenceConnector("tools")
	if err := m.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	a := referencingAgent("worker", "tools")
	if err := g.agentManager.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "tools"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("disabled Agent did not guard deletion: %v", err)
	}
	cfg.Name = "Tools renamed"
	cfg.Disabled = true
	if err := m.Update(ctx, "tools", cfg); err != nil {
		t.Fatalf("reference blocked rename/revocation: %v", err)
	}
	a.Resources.ConnectorIDs = nil
	if err := g.agentManager.Update(ctx, a.ID, a); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "tools"); err != nil {
		t.Fatal(err)
	}
	if err := g.agentManager.Update(ctx, a.ID, referencingAgent(a.ID, "tools")); !errors.Is(err, ErrDependencyNotConfigured) {
		t.Fatalf("missing connector write accepted: %v", err)
	}
}
func TestConnectorDeletionAndAgentWritesAreSerializable(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	ctx := t.Context()
	for i := range 12 {
		id := fmt.Sprintf("tools-%d", i)
		a := referencingAgent(fmt.Sprintf("worker-%d", i), id)
		if err := g.ConnectorManager().Create(ctx, referenceConnector(id)); err != nil {
			t.Fatal(err)
		}
		var createErr, deleteErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); createErr = g.agentManager.Create(ctx, a) }()
		go func() { defer wg.Done(); deleteErr = g.ConnectorManager().Delete(ctx, id) }()
		wg.Wait()
		_, connectorErr := g.ConnectorManager().Get(ctx, id)
		_, agentExists := g.agentManager.GetSnapshot(a.ID)
		if createErr == nil {
			if !errors.Is(deleteErr, ErrResourceReferenced) || connectorErr != nil || !agentExists {
				t.Fatalf("Agent winner left invalid state: %v %v %v %v", createErr, deleteErr, connectorErr, agentExists)
			}
		} else if !errors.Is(createErr, ErrDependencyNotConfigured) || deleteErr != nil || !errors.Is(connectorErr, connector.ErrConnectorNotConfigured) || agentExists {
			t.Fatalf("delete winner left invalid state: %v %v %v %v", createErr, deleteErr, connectorErr, agentExists)
		}
	}
}
func TestConnectorDeletionProtectsRetainedAgentSnapshot(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	ctx := t.Context()
	if err := g.ConnectorManager().Create(ctx, referenceConnector("tools")); err != nil {
		t.Fatal(err)
	}
	a := referencingAgent("worker", "tools")
	if err := g.agentManager.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	store, _ := backend.Get(schema.StoreAgents)
	if err := store.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	} // Storage-only deletion simulates a retained execution generation.
	if err := g.ConnectorManager().Delete(ctx, "tools"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("retained snapshot lost reference guard: %v", err)
	}
	if err := g.agentManager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Delete(ctx, "tools"); err != nil {
		t.Fatal(err)
	}
}
func TestConnectorDeletionProtectsDisabledPublishingRoute(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	ctx := t.Context()
	if err := g.ConnectorManager().Create(ctx, referenceConnector("tools")); err != nil {
		t.Fatal(err)
	}
	r := routecore.RouteConfig{ID: "publish-tools", Kind: routecore.RouteKindMCP, Protocol: routecore.RouteProtocolMCP, Disabled: true, MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/tools"}, TargetPolicy: json.RawMessage(`{"kind":"connector","connector_id":"tools"}`)}
	if err := g.RouteConfigManager().Create(ctx, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Delete(ctx, "tools"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatal(err)
	}
	if err := g.RouteConfigManager().Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Delete(ctx, "tools"); err != nil {
		t.Fatal(err)
	}
}
func TestHTTPToolRemovalUsesStagedIndependentUpdates(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	ctx := t.Context()
	var cfg connector.ConnectorConfig
	if err := json.Unmarshal([]byte(`{"id":"tools","name":"Tools","type":"http_api","http_api":{"base_url":"https://example.org","tools":[{"name":"old","description":"Old tool","input_schema":{"type":"object"},"request":{"method":"GET","path":"/old"}},{"name":"new","description":"New tool","input_schema":{"type":"object"},"request":{"method":"GET","path":"/new"}}]}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := g.ProviderManager().CreateConfig(ctx, provider.ProviderConfig{Id: "model-provider", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	if err := g.RouteConfigManager().Create(ctx, providerRoute("chat", "model-provider"), ""); err != nil {
		t.Fatal(err)
	}
	a := agent.Agent{ID: "worker", Name: "Worker", Disabled: true, Runtime: agent.Runtime{Type: agent.RuntimeTypeBuiltin, Builtin: &agent.BuiltinRuntime{Model: agent.BuiltinModel{LLMRouteID: "chat"}, Topology: agent.BuiltinTopology{Kind: agent.TopologyKindSingle}, Tools: []agent.BuiltinToolSelection{{ConnectorID: "tools", Tools: []string{"old"}}}}}, Routes: agent.Routes{LLMRouteIDs: []string{"chat"}}, Resources: agent.Resources{ConnectorIDs: []string{"tools"}}}
	if err := g.agentManager.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAPI.Tools = cfg.HTTPAPI.Tools[1:]
	if err := g.ConnectorManager().Update(ctx, cfg.ID, cfg); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("selected tool removal accepted: %v", err)
	}
	stored, _ := g.ConnectorManager().Get(ctx, "tools")
	if len(stored.HTTPAPI.Tools) != 2 {
		t.Fatal("rejected update changed catalog")
	}
	a.Runtime.Builtin.Tools[0].Tools = []string{"missing"}
	if err := g.agentManager.Update(ctx, a.ID, a); !errors.Is(err, ErrDependencyNotConfigured) {
		t.Fatalf("unknown selection accepted: %v", err)
	}
	a.Runtime.Builtin.Tools[0].Tools = []string{"new"}
	if err := g.agentManager.Update(ctx, a.ID, a); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Update(ctx, cfg.ID, cfg); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("execution edit while referenced: %v", err)
	}
	if err := g.ConnectorManager().Delete(ctx, "tools"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatal("selected Connector deletion accepted")
	}
	a.Runtime.Builtin.Tools = nil
	a.Resources.ConnectorIDs = nil
	if err := g.agentManager.Update(ctx, a.ID, a); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Update(ctx, cfg.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if err := g.ConnectorManager().Delete(ctx, "tools"); err != nil {
		t.Fatal(err)
	}
}

func TestReferencedConnectorExecutionEditsAndRefreshFailClosed(t *testing.T) {
	for _, owner := range []string{"agent", "route"} {
		t.Run(owner, func(t *testing.T) {
			g, backend, _ := controlPlaneGateway(t)
			m := g.ConnectorManager()
			cfg := referenceConnector("tools")
			if err := m.Create(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}
			if owner == "agent" {
				if err := g.agentManager.Create(t.Context(), referencingAgent("worker", cfg.ID)); err != nil {
					t.Fatal(err)
				}
			} else {
				r := routecore.RouteConfig{ID: "publish", Kind: routecore.RouteKindMCP, Protocol: routecore.RouteProtocolMCP, Disabled: true, MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/tools"}, TargetPolicy: json.RawMessage(`{"kind":"connector","connector_id":"tools"}`)}
				if err := g.RouteConfigManager().Create(t.Context(), r, ""); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := m.State(t.Context(), cfg.ID)
			cfg.Name = "New display"
			cfg.Description = "Display notes"
			cfg.Disabled = true
			if err := m.Update(t.Context(), cfg.ID, cfg); err != nil {
				t.Fatal(err)
			}
			after, _ := m.State(t.Context(), cfg.ID)
			if before.Generation != after.Generation {
				t.Fatal("display/revocation replaced execution")
			}
			cfg.MCP.Command = "changed"
			if err := m.Update(t.Context(), cfg.ID, cfg); !errors.Is(err, ErrResourceReferenced) {
				t.Fatalf("referenced execution edit: %v", err)
			}
			stored, _ := m.Get(t.Context(), cfg.ID)
			store, _ := backend.Get(schema.StoreConnectors)
			if err := store.Update(t.Context(), &cfg); err != nil {
				t.Fatal(err)
			}
			if err := m.Refresh(t.Context()); !errors.Is(err, ErrResourceReferenced) {
				t.Fatalf("Refresh bypassed references: %v", err)
			}
			current, _ := m.Get(t.Context(), cfg.ID)
			if current.MCP.Command != stored.MCP.Command {
				t.Fatal("failed Refresh changed snapshot")
			}
		})
	}
}

func TestConnectorExecutionEditsAndAgentWritesAreSerializable(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	for i := range 12 {
		cfg := referenceConnector(fmt.Sprintf("tools-edit-%d", i))
		m := g.ConnectorManager()
		if err := m.Create(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		a := referencingAgent(fmt.Sprintf("worker-edit-%d", i), cfg.ID)
		cfg.MCP.Command = "new-command"
		var createErr, editErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); createErr = g.agentManager.Create(t.Context(), a) }()
		go func() { defer wg.Done(); editErr = m.Update(t.Context(), cfg.ID, cfg) }()
		wg.Wait()
		if createErr != nil {
			t.Fatalf("reference creation failed: %v", createErr)
		}
		current, _ := m.Get(t.Context(), cfg.ID)
		if editErr != nil && !errors.Is(editErr, ErrResourceReferenced) {
			t.Fatal(editErr)
		}
		if editErr == nil && current.MCP.Command != "new-command" || editErr != nil && current.MCP.Command != "unused" {
			t.Fatal("edit winner and snapshot disagree")
		}
	}
}
