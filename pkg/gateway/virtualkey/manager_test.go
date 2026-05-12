package virtualkey

import (
	"context"
	"errors"
	"testing"

	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
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

func (s *testManagedVirtualKeyStore) Create(_ context.Context, id string, _ string, obj any) error {
	item, ok := obj.(*VirtualKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*VirtualKey{}
	}
	cloned := *item
	s.items[id] = &cloned
	return nil
}

func (s *testManagedVirtualKeyStore) Update(_ context.Context, id string, obj any) error {
	if _, ok := s.items[id]; !ok {
		return configstoreintf.ErrNotFound
	}
	return s.Create(context.Background(), id, "", obj)
}

func (s *testManagedVirtualKeyStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func (s *testManagedVirtualKeyStore) Get(_ context.Context, id string) (any, error) {
	s.getCalls++
	item, ok := s.items[id]
	if !ok {
		return nil, configstoreintf.ErrNotFound
	}
	cloned := *item
	return &cloned, nil
}

func (s *testManagedVirtualKeyStore) GetByKey(_ context.Context, key string) (any, error) {
	s.getCalls++
	for _, item := range s.items {
		if item.Key == key {
			cloned := *item
			return &cloned, nil
		}
	}
	return nil, configstoreintf.ErrNotFound
}

func TestVirtualKeyManagerGetCachesDynamicKey(t *testing.T) {
	store := &testManagedVirtualKeyStore{
		items: map[string]*VirtualKey{
			"vk-test": {ID: "vk-test", Key: "lk-test", Tag: "admin"},
		},
	}
	manager := NewVirtualKeyManager(store)

	got, err := manager.GetByKey(context.Background(), "lk-test")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Tag != "admin" {
		t.Fatalf("Tag = %q, want admin", got.Tag)
	}

	if _, err := manager.GetByKey(context.Background(), "lk-test"); err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if store.getCalls != 1 {
		t.Fatalf("store get calls = %d, want 1", store.getCalls)
	}
}

func TestVirtualKeyManagerGetPrefersStaticKey(t *testing.T) {
	store := &testManagedVirtualKeyStore{
		items: map[string]*VirtualKey{
			"vk-test": {ID: "vk-test", Key: "lk-test", Tag: "dynamic"},
		},
	}
	manager := NewVirtualKeyManager(store)
	manager.InitStaticKeys([]VirtualKey{{ID: "vk-test", Key: "lk-test", Tag: "static"}})

	got, err := manager.GetByKey(context.Background(), "lk-test")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Tag != "static" {
		t.Fatalf("Tag = %q, want static", got.Tag)
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0", store.getCalls)
	}
}

func TestVirtualKeyManagerCreateUpdateDeleteManageCache(t *testing.T) {
	store := &testManagedVirtualKeyStore{items: map[string]*VirtualKey{}}
	manager := NewVirtualKeyManager(store)

	if err := manager.Create(context.Background(), VirtualKey{
		ID:  "vk-test",
		Key: "lk-test",
		Tag: "created",
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	store.items["vk-test"].Tag = "stale-store-value"
	got, err := manager.GetByID(context.Background(), "vk-test")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Tag != "created" {
		t.Fatalf("Tag = %q, want created", got.Tag)
	}

	if err := manager.Update(context.Background(), "vk-test", VirtualKey{
		Tag: "updated",
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, err = manager.GetByID(context.Background(), "vk-test")
	if err != nil {
		t.Fatalf("Get after update returned error: %v", err)
	}
	if got.Tag != "updated" {
		t.Fatalf("Tag = %q, want updated", got.Tag)
	}

	if err := manager.Delete(context.Background(), "vk-test"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := manager.GetByID(context.Background(), "vk-test"); !errors.Is(err, ErrVirtualKeyNotConfigured) {
		t.Fatalf("Get after delete error = %v, want ErrVirtualKeyNotConfigured", err)
	}
}

func TestVirtualKeyManagerRejectsStaticKeyMutation(t *testing.T) {
	manager := NewVirtualKeyManager(&testManagedVirtualKeyStore{items: map[string]*VirtualKey{}})
	manager.InitStaticKeys([]VirtualKey{{ID: "vk-test", Key: "lk-test"}})

	if err := manager.Update(context.Background(), "vk-test", VirtualKey{}); !errors.Is(err, ErrStaticVirtualKeyReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticVirtualKeyReadOnly", err)
	}
	if err := manager.Delete(context.Background(), "vk-test"); !errors.Is(err, ErrStaticVirtualKeyReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticVirtualKeyReadOnly", err)
	}
}
