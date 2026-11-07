package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
)

type failedReadbackStore struct {
	configstore.ConfigStore
	written bool
}

func (s *failedReadbackStore) Update(ctx context.Context, value any) error {
	err := s.ConfigStore.Update(ctx, value)
	s.written = err == nil
	return err
}
func (s *failedReadbackStore) Get(ctx context.Context, id ...any) (any, error) {
	if s.written {
		return nil, errors.New("injected authoritative readback failure")
	}
	return s.ConfigStore.Get(ctx, id...)
}

type transactionFaultStore struct {
	configstore.ConfigStore
	readbackFailure, commitUncertain bool
	writes                           int
}

func (s *transactionFaultStore) Transaction(ctx context.Context, apply func(configstore.ConfigStore) error) error {
	err := s.ConfigStore.(configstore.TransactionalStore).Transaction(ctx, func(store configstore.ConfigStore) error {
		s.writes++
		if s.readbackFailure {
			store = &failedReadbackStore{ConfigStore: store}
		}
		return apply(store)
	})
	if err == nil && s.commitUncertain {
		return errors.New("injected uncertain commit result")
	}
	return err
}

func TestSQLiteAgentReadbackRollbackAndUncertainCommitRecovery(t *testing.T) {
	ctx := t.Context()
	creator, err := configsqlite.Open(ctx, configsqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := creator.Database().DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	store, _ := backend.Get(schema.StoreAgents)
	m := agent.NewManager(store)
	a := agent.Agent{ID: "a", Name: "before", Disabled: true, Runtime: agent.Runtime{Type: agent.RuntimeTypeHTTP, HTTP: &agent.HTTPRuntime{CardURL: "https://example.org/card", Protocol: "a2a"}}}
	if err := m.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	fault := &transactionFaultStore{ConfigStore: store, readbackFailure: true}
	m = agent.NewManager(fault)
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	old, _ := m.GetSnapshot(a.ID)
	a.Name = "after"
	err = m.Update(ctx, a.ID, a)
	if err == nil || errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("known transaction rollback = %v", err)
	}
	stored, err := m.Get(ctx, a.ID)
	if err != nil || stored.Name != "before" {
		t.Fatalf("failed transaction committed: %v, %v", stored, err)
	}
	if m.PendingMutation(a.ID) != nil || m.CheckExecution(old) != nil {
		t.Fatal("known rollback unnecessarily blocked valid execution")
	}
	fault.readbackFailure = false
	fault.commitUncertain = true
	err = m.Update(ctx, a.ID, a)
	if !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("uncertain commit = %v", err)
	}
	writes := fault.writes
	if err := m.Update(ctx, a.ID, a); !errors.Is(err, configmgr.ErrMutationPending) || fault.writes != writes {
		t.Fatal("retry repeated an unconfirmed write")
	}
	if err := m.CheckExecution(old); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal("uncertain commit admitted stale execution")
	}
	if err := m.Recover(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if fault.writes != writes {
		t.Fatal("recovery issued another write")
	}
	current, ok := m.GetSnapshot(a.ID)
	if !ok || current.Name != "after" || m.CheckExecution(current) != nil {
		t.Fatal("recovery did not publish the committed SQLite facts")
	}
	restarted := agent.NewManager(store)
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := restarted.GetSnapshot(a.ID); got.Name != "after" {
		t.Fatal("restart did not retain recovered data")
	}
}
