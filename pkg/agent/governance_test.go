package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRejectUnsupportedBudgetsIncludingZero(t *testing.T) {
	for _, budget := range []*Budget{{}, {MaxTurnsPerDay: 1}, {MaxTokensPerDay: 1}, {MaxTokensPerDay: -1}} {
		a := snapshotTestAgent("budget-agent")
		a.Policy.Budget = budget
		if err := a.Validate(); err == nil || !strings.Contains(err.Error(), `agent "budget-agent" policy.budget is unsupported`) {
			t.Fatalf("Validate = %v", err)
		}
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeStoredAgentConfig(data); err == nil || !strings.Contains(err.Error(), "repair-architecture-config") {
			t.Fatalf("stored decode = %v", err)
		}
		m := NewManager(newTestConfigStore())
		if err := m.Create(context.Background(), a); err == nil {
			t.Fatal("Create accepted budget")
		}
	}
	for _, policy := range []string{`{}`, `{"budget":null}`, `{"max_agent_depth":3}`} {
		a := snapshotTestAgent("supported")
		if err := json.Unmarshal([]byte(policy), &a.Policy); err != nil {
			t.Fatal(err)
		}
		if err := a.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidGovernanceUpdateAndRefreshRetainGeneration(t *testing.T) {
	ctx := context.Background()
	store := newTestConfigStore().(*testConfigStore)
	m := NewManager(store)
	a := snapshotTestAgent("a")
	if err := m.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	generation := m.SnapshotGeneration()
	a.Policy.Budget = &Budget{}
	if err := m.Update(ctx, "a", a); err == nil {
		t.Fatal("Update accepted unsupported budget")
	}
	if m.SnapshotGeneration() != generation {
		t.Fatal("rejected update published a generation")
	}
	if store.items["a"].Policy.Budget != nil {
		t.Fatal("rejected update persisted budget")
	}
	// Simulate an old stored record delivered by a raw backend. Refresh must
	// independently validate before replacing the last valid generation.
	store.items["a"].Policy.Budget = &Budget{}
	if err := m.Refresh(ctx); err == nil {
		t.Fatal("Refresh accepted old budget")
	}
	got, ok := m.GetSnapshot("a")
	if !ok || got.Policy.Budget != nil || m.SnapshotGeneration() != generation {
		t.Fatal("failed Refresh replaced the valid generation")
	}
}
