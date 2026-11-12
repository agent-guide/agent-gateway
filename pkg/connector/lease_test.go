package connector_test

import (
	"context"
	"encoding/json"
	"errors"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/connector"
)

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
	cfg := connector.ConnectorConfig{ID: "docs", Name: "Docs", Type: "mcp", MCP: &connector.MCPConfig{Transport: connector.TransportStdio, Command: "unused"}}
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
