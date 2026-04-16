package route

import (
	"context"
	"errors"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
)

type testManagedRouteStore struct {
	items    map[string]*Route
	getCalls int
}

func (s *testManagedRouteStore) ListByTag(_ context.Context, _ string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testManagedRouteStore) ListByTagPrefix(_ context.Context, _ string) ([]any, error) {
	return s.ListByTag(context.Background(), "")
}

func (s *testManagedRouteStore) Create(_ context.Context, id string, _ string, obj any) error {
	route, ok := obj.(*Route)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*Route{}
	}
	cloned := *route
	s.items[id] = &cloned
	return nil
}

func (s *testManagedRouteStore) Update(_ context.Context, id string, obj any) error {
	if _, ok := s.items[id]; !ok {
		return configstoreintf.ErrNotFound
	}
	return s.Create(context.Background(), id, "", obj)
}

func (s *testManagedRouteStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func (s *testManagedRouteStore) Get(_ context.Context, id string) (any, error) {
	s.getCalls++
	item, ok := s.items[id]
	if !ok {
		return nil, configstoreintf.ErrNotFound
	}
	cloned := *item
	return &cloned, nil
}

func TestRouteManagerGetCachesDynamicRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*Route{
			"chat-prod": {ID: "chat-prod"},
		},
	}
	manager := NewRouteManager(store)

	got, err := manager.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Policy.TimeoutSeconds != 120 {
		t.Fatalf("TimeoutSeconds = %d, want 120", got.Policy.TimeoutSeconds)
	}

	if _, err := manager.Get(context.Background(), "chat-prod"); err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if store.getCalls != 1 {
		t.Fatalf("store get calls = %d, want 1", store.getCalls)
	}
}

func TestRouteManagerGetPrefersStaticRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*Route{
			"chat-prod": {ID: "chat-prod", Name: "dynamic"},
		},
	}
	manager := NewRouteManager(store)
	manager.InitStaticRoutes([]Route{{ID: "chat-prod", Name: "static"}})

	got, err := manager.Get(context.Background(), "chat-prod")
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

func TestRouteManagerCreateUpdateDeleteManageCache(t *testing.T) {
	store := &testManagedRouteStore{items: map[string]*Route{}}
	manager := NewRouteManager(store)

	if err := manager.Create(context.Background(), Route{
		ID:      "chat-prod",
		Targets: []RouteTarget{{ProviderRef: "openai"}},
	}, ""); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	store.items["chat-prod"].Name = "stale-store-value"
	got, err := manager.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("Name = %q, want empty cached value", got.Name)
	}

	if err := manager.Update(context.Background(), "chat-prod", Route{
		Name:    "updated",
		Targets: []RouteTarget{{ProviderRef: "anthropic"}},
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, err = manager.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get after update returned error: %v", err)
	}
	if got.Name != "updated" {
		t.Fatalf("Name = %q, want updated", got.Name)
	}

	if err := manager.Delete(context.Background(), "chat-prod"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := manager.Get(context.Background(), "chat-prod"); !errors.Is(err, ErrRouteNotConfigured) {
		t.Fatalf("Get after delete error = %v, want ErrRouteNotConfigured", err)
	}
}

func TestRouteManagerRejectsStaticRouteMutation(t *testing.T) {
	manager := NewRouteManager(&testManagedRouteStore{items: map[string]*Route{}})
	manager.InitStaticRoutes([]Route{{ID: "chat-prod"}})

	if err := manager.Update(context.Background(), "chat-prod", Route{}); !errors.Is(err, ErrStaticRouteReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticRouteReadOnly", err)
	}
	if err := manager.Delete(context.Background(), "chat-prod"); !errors.Is(err, ErrStaticRouteReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticRouteReadOnly", err)
	}
}
