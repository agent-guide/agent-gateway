package gateway

import (
	"context"
	"errors"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
)

type testManagedLocalAPIKeyStore struct {
	items    map[string]*localapikeypkg.LocalAPIKey
	getCalls int
}

func (s *testManagedLocalAPIKeyStore) ListByUserID(_ context.Context, userID string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if userID != "" && item.UserID != userID {
			continue
		}
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testManagedLocalAPIKeyStore) Create(_ context.Context, key string, _ string, obj any) error {
	item, ok := obj.(*localapikeypkg.LocalAPIKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*localapikeypkg.LocalAPIKey{}
	}
	cloned := *item
	s.items[key] = &cloned
	return nil
}

func (s *testManagedLocalAPIKeyStore) Update(_ context.Context, key string, obj any) error {
	if _, ok := s.items[key]; !ok {
		return configstoreintf.ErrNotFound
	}
	return s.Create(context.Background(), key, "", obj)
}

func (s *testManagedLocalAPIKeyStore) Delete(_ context.Context, key string) error {
	delete(s.items, key)
	return nil
}

func (s *testManagedLocalAPIKeyStore) Get(_ context.Context, key string) (any, error) {
	s.getCalls++
	item, ok := s.items[key]
	if !ok {
		return nil, configstoreintf.ErrNotFound
	}
	cloned := *item
	return &cloned, nil
}

func TestLocalAPIKeyManagerGetCachesDynamicKey(t *testing.T) {
	store := &testManagedLocalAPIKeyStore{
		items: map[string]*localapikeypkg.LocalAPIKey{
			"lk-test": {Key: "lk-test", UserID: "admin"},
		},
	}
	manager := NewLocalAPIKeyManager(store)

	got, err := manager.Get(context.Background(), "lk-test")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.UserID != "admin" {
		t.Fatalf("UserID = %q, want admin", got.UserID)
	}

	if _, err := manager.Get(context.Background(), "lk-test"); err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if store.getCalls != 1 {
		t.Fatalf("store get calls = %d, want 1", store.getCalls)
	}
}

func TestLocalAPIKeyManagerGetPrefersStaticKey(t *testing.T) {
	store := &testManagedLocalAPIKeyStore{
		items: map[string]*localapikeypkg.LocalAPIKey{
			"lk-test": {Key: "lk-test", Name: "dynamic"},
		},
	}
	manager := NewLocalAPIKeyManager(store)
	manager.InitStaticKeys([]localapikeypkg.LocalAPIKey{{Key: "lk-test", Name: "static"}})

	got, err := manager.Get(context.Background(), "lk-test")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name != "static" {
		t.Fatalf("Name = %q, want static", got.Name)
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0", store.getCalls)
	}
}

func TestLocalAPIKeyManagerCreateUpdateDeleteManageCache(t *testing.T) {
	store := &testManagedLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{}}
	manager := NewLocalAPIKeyManager(store)

	if err := manager.Create(context.Background(), localapikeypkg.LocalAPIKey{
		Key:    "lk-test",
		UserID: "admin",
		Name:   "created",
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	store.items["lk-test"].Name = "stale-store-value"
	got, err := manager.Get(context.Background(), "lk-test")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name != "created" {
		t.Fatalf("Name = %q, want created", got.Name)
	}

	if err := manager.Update(context.Background(), "lk-test", localapikeypkg.LocalAPIKey{
		UserID: "admin",
		Name:   "updated",
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, err = manager.Get(context.Background(), "lk-test")
	if err != nil {
		t.Fatalf("Get after update returned error: %v", err)
	}
	if got.Name != "updated" {
		t.Fatalf("Name = %q, want updated", got.Name)
	}

	if err := manager.Delete(context.Background(), "lk-test"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := manager.Get(context.Background(), "lk-test"); !errors.Is(err, ErrLocalAPIKeyNotConfigured) {
		t.Fatalf("Get after delete error = %v, want ErrLocalAPIKeyNotConfigured", err)
	}
}

func TestLocalAPIKeyManagerRejectsStaticKeyMutation(t *testing.T) {
	manager := NewLocalAPIKeyManager(&testManagedLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{}})
	manager.InitStaticKeys([]localapikeypkg.LocalAPIKey{{Key: "lk-test"}})

	if err := manager.Update(context.Background(), "lk-test", localapikeypkg.LocalAPIKey{}); !errors.Is(err, ErrStaticLocalAPIKeyReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticLocalAPIKeyReadOnly", err)
	}
	if err := manager.Delete(context.Background(), "lk-test"); !errors.Is(err, ErrStaticLocalAPIKeyReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticLocalAPIKeyReadOnly", err)
	}
}
