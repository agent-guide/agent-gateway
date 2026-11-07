package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type committedAgentErrorStore struct {
	configstore.ConfigStore
	writes int
}

func (s *committedAgentErrorStore) failAfterWrite(err error) error {
	s.writes++
	if err != nil {
		return err
	}
	return errors.New("injected error after write committed")
}

func (s *committedAgentErrorStore) Create(ctx context.Context, value any) error {
	return s.failAfterWrite(s.ConfigStore.Create(ctx, value))
}

func (s *committedAgentErrorStore) Update(ctx context.Context, value any) error {
	return s.failAfterWrite(s.ConfigStore.Update(ctx, value))
}

func (s *committedAgentErrorStore) Delete(ctx context.Context, keys ...any) error {
	return s.failAfterWrite(s.ConfigStore.Delete(ctx, keys...))
}

func TestNontransactionalAgentWriteErrorsRequireRecovery(t *testing.T) {
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			base := newTestConfigStore()
			a := snapshotTestAgent("a")
			if action != "create" {
				if err := NewManager(base).Create(t.Context(), a); err != nil {
					t.Fatal(err)
				}
			}
			store := &committedAgentErrorStore{ConfigStore: base}
			m := NewManager(store)
			if err := m.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			old, hadOld := m.GetSnapshot(a.ID)
			a.Name = "committed"
			write := func() error {
				switch action {
				case "create":
					return m.Create(t.Context(), a)
				case "update":
					return m.Update(t.Context(), a.ID, a)
				default:
					return m.Delete(t.Context(), a.ID)
				}
			}
			if err := write(); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatalf("unconfirmed write = %v", err)
			}
			pending := m.PendingMutation(a.ID)
			if pending == nil || pending.State != "pending_recovery" {
				t.Fatalf("pending = %+v", pending)
			}
			if err := m.CheckExecutionID(a.ID); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatal("unconfirmed definition executable")
			}
			if hadOld && !errors.Is(m.CheckExecution(old), configmgr.ErrMutationPending) {
				t.Fatal("stale snapshot executable")
			}
			if err := write(); !errors.Is(err, configmgr.ErrMutationPending) || store.writes != 1 || m.PendingMutation(a.ID).OperationID != pending.OperationID {
				t.Fatal("retry repeated the write or replaced its operation identity")
			}
			if err := m.Recover(t.Context(), a.ID); err != nil {
				t.Fatal(err)
			}
			if store.writes != 1 || m.PendingMutation(a.ID) != nil {
				t.Fatal("recovery replayed the write or retained the pending gate")
			}
			current, ok := m.GetSnapshot(a.ID)
			if action == "delete" {
				if ok {
					t.Fatal("recovery retained the deleted Agent")
				}
			} else if !ok || current.Name != "committed" || m.CheckExecution(current) != nil {
				t.Fatal("recovery did not publish the committed definition")
			}
		})
	}
}
