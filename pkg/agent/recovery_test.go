package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
)

func TestPendingMutationRetainsExclusiveMCPRouteOwnership(t *testing.T) {
	for _, action := range []string{"update", "delete"} {
		for _, bind := range []string{"create", "update"} {
			t.Run(action+"/"+bind, func(t *testing.T) {
				m := NewManager(newTestConfigStore())
				owner := snapshotTestAgent("owner")
				owner.Routes.MCPRouteIDs = []string{"tools"}
				if err := m.Create(t.Context(), owner); err != nil {
					t.Fatal(err)
				}
				next := snapshotTestAgent("next")
				if bind == "update" {
					if err := m.Create(t.Context(), next); err != nil {
						t.Fatal(err)
					}
				}
				fail := true
				m.AddDefinitionListener(func(context.Context, []Agent) DefinitionCommit {
					if fail {
						panic("injected publication failure")
					}
					return nil
				})
				var err error
				if action == "delete" {
					err = m.Delete(t.Context(), owner.ID)
				} else {
					owner.Routes.MCPRouteIDs = nil
					err = m.Update(t.Context(), owner.ID, owner)
				}
				if !errors.Is(err, configmgr.ErrMutationPending) {
					t.Fatalf("owner mutation = %v", err)
				}
				fail = false
				generation := m.SnapshotGeneration()
				next.Routes.MCPRouteIDs = []string{"tools"}
				write := func() error {
					if bind == "create" {
						return m.Create(t.Context(), next)
					}
					return m.Update(t.Context(), next.ID, next)
				}
				if err := write(); err == nil || !strings.Contains(err.Error(), `already bound by agent "owner"`) {
					t.Fatalf("rebinding before recovery = %v", err)
				}
				if m.SnapshotGeneration() != generation {
					t.Fatal("rejected binding published a generation")
				}
				if id, ok := m.ResolveAgentID("tools", "", ""); !ok || id != owner.ID {
					t.Fatalf("lost published ownership: %q, %v", id, ok)
				}
				if err := m.Recover(t.Context(), owner.ID); err != nil {
					t.Fatal(err)
				}
				if err := write(); err != nil {
					t.Fatalf("rebinding after recovery = %v", err)
				}
				if id, ok := m.ResolveAgentID("tools", "", ""); !ok || id != next.ID {
					t.Fatalf("new ownership: %q, %v", id, ok)
				}
			})
		}
	}
}

func TestPendingReadbackBlocksExecutionAndRetryWithoutRewriting(t *testing.T) {
	base := newTestConfigStore()
	store := &readbackFailureStore{ConfigStore: base}
	m := NewManager(store)
	a := snapshotTestAgent("a")
	if err := m.Create(t.Context(), a); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("create = %v", err)
	}
	state := m.PendingMutation(a.ID)
	if state == nil || state.OperationID == "" || state.State != "pending_recovery" {
		t.Fatalf("state = %+v", state)
	}
	if err := m.Create(t.Context(), a); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("retry did not identify committed outcome: %v", err)
	}
	if got := m.PendingMutation(a.ID); got.OperationID != state.OperationID {
		t.Fatal("retry replaced operation identity")
	}
	if err := m.CheckExecutionID(a.ID); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal("unverified definition executable")
	}
	if err := m.Recover(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	verified, ok := m.GetSnapshot(a.ID)
	if !ok || m.PendingMutation(a.ID) != nil || m.CheckExecution(verified) != nil {
		t.Fatal("recovery did not publish and open execution")
	}
	if err := m.Recover(t.Context(), a.ID); err != nil {
		t.Fatal("recovery was not idempotent")
	}
	other := snapshotTestAgent("other")
	// The faulting store writes each create, so explicitly recover its outcome.
	if err := m.Create(t.Context(), other); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal(err)
	}
	if err := m.Recover(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	unrelated, _ := m.GetSnapshot(other.ID)
	a.Name = "updated"
	if err := m.Update(t.Context(), a.ID, a); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal(err)
	}
	old, _ := m.GetSnapshot(a.ID)
	if old.Name == "updated" {
		t.Fatal("unverified update published")
	}
	if err := m.CheckExecution(old); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal("old definition silently executed during recovery")
	}
	if err := m.CheckExecution(unrelated); err != nil {
		t.Fatalf("unrelated resource blocked: %v", err)
	}
	if err := m.Delete(t.Context(), a.ID); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal("conflicting deletion bypassed recovery")
	}
	if err := m.Recover(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckExecution(old); !errors.Is(err, ErrDefinitionRetired) {
		t.Fatalf("previously captured definition admitted after recovery: %v", err)
	}
}

func TestPublicationFailureKeepsGenerationAndRecoversCommittedFacts(t *testing.T) {
	for _, phase := range []string{"prepare", "commit"} {
		t.Run(phase, func(t *testing.T) {
			m := NewManager(newTestConfigStore())
			now := time.Now()
			m.recoveryClock = func() time.Time { return now }
			a := snapshotTestAgent("a")
			if err := m.Create(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			old, _ := m.GetSnapshot(a.ID)
			generation := m.SnapshotGeneration()
			fail := true
			m.AddDefinitionListener(func(context.Context, []Agent) DefinitionCommit {
				if fail && phase == "prepare" {
					panic("injected failure")
				}
				return func() DefinitionCleanup {
					if fail && phase == "commit" {
						panic("injected failure")
					}
					return nil
				}
			})
			a.Name = "committed"
			if err := m.Update(t.Context(), a.ID, a); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatalf("update = %v", err)
			}
			if m.SnapshotGeneration() != generation {
				t.Fatal("failed publication advanced generation")
			}
			if got, _ := m.GetSnapshot(a.ID); got.Name != old.Name {
				t.Fatal("failed publication replaced snapshot")
			}
			stored, err := m.Get(t.Context(), a.ID)
			if err != nil || stored.Name != "committed" {
				t.Fatal("publication failure incorrectly rolled back committed data")
			}
			state := m.PendingMutation(a.ID)
			if err := m.Recover(t.Context(), a.ID); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatal("failed recovery claimed success")
			}
			if m.PendingMutation(a.ID).OperationID != state.OperationID {
				t.Fatal("failed recovery lost operation identity")
			}
			fail = false
			if err := m.Recover(t.Context(), a.ID); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatal("recovery ignored failure backoff")
			}
			now = now.Add(time.Second)
			if err := m.Recover(t.Context(), a.ID); err != nil {
				t.Fatal(err)
			}
			if got, _ := m.GetSnapshot(a.ID); got.Name != "committed" || m.PendingMutation(a.ID) != nil {
				t.Fatal("recovery did not publish authoritative state")
			}
		})
	}
}

func TestRestartRebuildsPendingCommittedState(t *testing.T) {
	base := newTestConfigStore()
	m := NewManager(&readbackFailureStore{ConfigStore: base})
	a := snapshotTestAgent("a")
	if err := m.Create(t.Context(), a); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal(err)
	}
	restarted := NewManager(base)
	if err := restarted.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, ok := restarted.GetSnapshot(a.ID)
	if !ok || restarted.CheckExecution(got) != nil {
		t.Fatal("restart did not reconstruct committed facts")
	}
}

func TestExecutionFenceRejectsDefinitionChangesWithSameTimestamp(t *testing.T) {
	m := NewManager(timestampStore{newTestConfigStore()})
	a := snapshotTestAgent("a")
	if err := m.Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if err := m.Update(t.Context(), a.ID, a); err != nil {
		t.Fatal(err)
	}
	old, _ := m.GetSnapshot(a.ID)
	a.Name = "changed with identical store timestamp"
	if err := m.Update(t.Context(), a.ID, a); err != nil {
		t.Fatal(err)
	}
	current, _ := m.GetSnapshot(a.ID)
	if !old.UpdatedAt.Equal(current.UpdatedAt) {
		t.Fatal("fixture did not preserve the authoritative timestamp")
	}
	if err := m.CheckExecution(old); !errors.Is(err, ErrDefinitionRetired) {
		t.Fatal("timestamp collision admitted a stale definition")
	}
	if err := m.CheckExecution(current); err != nil {
		t.Fatal(err)
	}
}
