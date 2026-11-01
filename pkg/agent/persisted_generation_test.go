package agent

import (
	"context"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"testing"
	"time"
)

// Simulate a durable store replacing application timestamps on write.
type timestampStore struct{ configstore.ConfigStore }

func (s timestampStore) Create(ctx context.Context, value any) error {
	a := value.(configstore.ObjectUnwrapper).ConfigStoreObject().(*Agent)
	copy := *a
	copy.UpdatedAt = time.Unix(100, 0).UTC()
	return s.ConfigStore.Create(ctx, &copy)
}
func (s timestampStore) Update(ctx context.Context, value any) error {
	a := value.(configstore.ObjectUnwrapper).ConfigStoreObject().(*Agent)
	copy := *a
	copy.UpdatedAt = time.Unix(200, 0).UTC()
	return s.ConfigStore.Update(ctx, &copy)
}
func TestPublishedSnapshotMatchesPersistedTimes(t *testing.T) {
	store := timestampStore{newTestConfigStore()}
	manager := NewManager(store)
	a := Agent{ID: "persisted", Name: "Persisted", Runtime: Runtime{Type: RuntimeTypeACP, ACP: &ACPRuntime{AgentType: "codex", CWD: "/tmp"}}}
	if err := manager.Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	verify := func() {
		stored, err := manager.Get(t.Context(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, ok := manager.GetSnapshot(a.ID)
		if !ok || !snapshot.UpdatedAt.Equal(stored.UpdatedAt) || !snapshot.CreatedAt.Equal(stored.CreatedAt) {
			t.Fatal("published fingerprint differs from persisted facts")
		}
	}
	verify()
	if err := manager.Update(t.Context(), a.ID, a); err != nil {
		t.Fatal(err)
	}
	verify()
	if err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	verify()
}

type readbackFailureStore struct {
	configstore.ConfigStore
	failNext bool
}

func (s *readbackFailureStore) Create(ctx context.Context, v any) error {
	err := s.ConfigStore.Create(ctx, v)
	s.failNext = err == nil
	return err
}
func (s *readbackFailureStore) Update(ctx context.Context, v any) error {
	err := s.ConfigStore.Update(ctx, v)
	s.failNext = err == nil
	return err
}
func (s *readbackFailureStore) Get(ctx context.Context, id ...any) (any, error) {
	if s.failNext {
		s.failNext = false
		return nil, errors.New("readback unavailable")
	}
	return s.ConfigStore.Get(ctx, id...)
}
func TestPersistedMutationReadbackFailureRecoversOnRefresh(t *testing.T) {
	store := &readbackFailureStore{ConfigStore: newTestConfigStore()}
	manager := NewManager(store)
	a := Agent{ID: "recover", Name: "Before", Runtime: Runtime{Type: RuntimeTypeACP, ACP: &ACPRuntime{AgentType: "codex", CWD: "/tmp"}}}
	if err := manager.Create(t.Context(), a); err == nil {
		t.Fatal("claimed verified creation")
	}
	if _, ok := manager.GetSnapshot(a.ID); ok {
		t.Fatal("published unverified generation")
	}
	if err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.Name = "After"
	if err := manager.Update(t.Context(), a.ID, a); err == nil {
		t.Fatal("claimed verified update")
	}
	old, _ := manager.GetSnapshot(a.ID)
	if old.Name != "Before" {
		t.Fatal("published unverified update")
	}
	if err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetSnapshot(a.ID)
	if current.Name != "After" {
		t.Fatal("did not recover persisted update")
	}
}
