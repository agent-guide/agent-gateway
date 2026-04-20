package virtualkey

import (
	"context"
	"errors"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
)

type testManagedVirtualKeyStore struct {
	items    map[string]*VirtualKey
	getCalls int
}

func (s *testManagedVirtualKeyStore) ListByTag(_ context.Context, tag string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if tag != "" && item.Tag != tag {
			continue
		}
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testManagedVirtualKeyStore) Create(_ context.Context, key string, _ string, obj any) error {
	item, ok := obj.(*VirtualKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*VirtualKey{}
	}
	cloned := *item
	s.items[key] = &cloned
	return nil
}

func (s *testManagedVirtualKeyStore) Update(_ context.Context, key string, obj any) error {
	if _, ok := s.items[key]; !ok {
		return configstoreintf.ErrNotFound
	}
	return s.Create(context.Background(), key, "", obj)
}

func (s *testManagedVirtualKeyStore) Delete(_ context.Context, key string) error {
	delete(s.items, key)
	return nil
}

func (s *testManagedVirtualKeyStore) Get(_ context.Context, key string) (any, error) {
	s.getCalls++
	item, ok := s.items[key]
	if !ok {
		return nil, configstoreintf.ErrNotFound
	}
	cloned := *item
	return &cloned, nil
}

func TestVirtualKeyManagerGetCachesDynamicKey(t *testing.T) {
	store := &testManagedVirtualKeyStore{
		items: map[string]*VirtualKey{
			"lk-test": {Key: "lk-test", Tag: "admin"},
		},
	}
	manager := NewVirtualKeyManager(store)

	got, err := manager.Get(context.Background(), "lk-test")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Tag != "admin" {
		t.Fatalf("Tag = %q, want admin", got.Tag)
	}

	if _, err := manager.Get(context.Background(), "lk-test"); err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if store.getCalls != 1 {
		t.Fatalf("store get calls = %d, want 1", store.getCalls)
	}
}

func TestVirtualKeyManagerGetPrefersStaticKey(t *testing.T) {
	store := &testManagedVirtualKeyStore{
		items: map[string]*VirtualKey{
			"lk-test": {Key: "lk-test", Name: "dynamic"},
		},
	}
	manager := NewVirtualKeyManager(store)
	manager.InitStaticKeys([]VirtualKey{{Key: "lk-test", Name: "static"}})

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

func TestVirtualKeyManagerCreateUpdateDeleteManageCache(t *testing.T) {
	store := &testManagedVirtualKeyStore{items: map[string]*VirtualKey{}}
	manager := NewVirtualKeyManager(store)

	if err := manager.Create(context.Background(), VirtualKey{
		Key:  "lk-test",
		Tag:  "admin",
		Name: "created",
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

	if err := manager.Update(context.Background(), "lk-test", VirtualKey{
		Tag:  "admin",
		Name: "updated",
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
	if _, err := manager.Get(context.Background(), "lk-test"); !errors.Is(err, ErrVirtualKeyNotConfigured) {
		t.Fatalf("Get after delete error = %v, want ErrVirtualKeyNotConfigured", err)
	}
}

func TestVirtualKeyManagerRejectsStaticKeyMutation(t *testing.T) {
	manager := NewVirtualKeyManager(&testManagedVirtualKeyStore{items: map[string]*VirtualKey{}})
	manager.InitStaticKeys([]VirtualKey{{Key: "lk-test"}})

	if err := manager.Update(context.Background(), "lk-test", VirtualKey{}); !errors.Is(err, ErrStaticVirtualKeyReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticVirtualKeyReadOnly", err)
	}
	if err := manager.Delete(context.Background(), "lk-test"); !errors.Is(err, ErrStaticVirtualKeyReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticVirtualKeyReadOnly", err)
	}
}
