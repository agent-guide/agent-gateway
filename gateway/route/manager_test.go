package route

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
)

type testManagedRouteStore struct {
	items    map[string]*AgentRoute
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
	route, ok := obj.(*AgentRoute)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*AgentRoute{}
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

func TestAgentRouteManagerGetCachesDynamicRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*AgentRoute{
			"chat-prod": {ID: "chat-prod"},
		},
	}
	manager := NewAgentRouteManager(store)

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

func TestAgentRouteManagerGetPrefersStaticRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*AgentRoute{
			"chat-prod": {ID: "chat-prod"},
		},
	}
	manager := NewAgentRouteManager(store)
	manager.InitStaticRoutes([]AgentRoute{{ID: "chat-prod"}})

	got, err := manager.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != "chat-prod" {
		t.Fatalf("ID = %q, want chat-prod", got.ID)
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0", store.getCalls)
	}
}

func TestAgentRouteManagerCreateUpdateDeleteManageCache(t *testing.T) {
	store := &testManagedRouteStore{items: map[string]*AgentRoute{}}
	manager := NewAgentRouteManager(store)

	if err := manager.Create(context.Background(), AgentRoute{
		ID:      "chat-prod",
		Targets: []RouteTarget{{ProviderID: "openai"}},
	}, ""); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	store.items["chat-prod"].Description = "stale-store-value"
	got, err := manager.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Description != "" {
		t.Fatalf("Description = %q, want empty cached value", got.Description)
	}

	if err := manager.Update(context.Background(), "chat-prod", AgentRoute{
		Description: "updated",
		Targets:     []RouteTarget{{ProviderID: "anthropic"}},
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, err = manager.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get after update returned error: %v", err)
	}
	if got.Description != "updated" {
		t.Fatalf("Description = %q, want updated", got.Description)
	}

	if err := manager.Delete(context.Background(), "chat-prod"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := manager.Get(context.Background(), "chat-prod"); !errors.Is(err, ErrRouteNotConfigured) {
		t.Fatalf("Get after delete error = %v, want ErrRouteNotConfigured", err)
	}
}

func TestAgentRouteManagerRejectsStaticRouteMutation(t *testing.T) {
	manager := NewAgentRouteManager(&testManagedRouteStore{items: map[string]*AgentRoute{}})
	manager.InitStaticRoutes([]AgentRoute{{ID: "chat-prod"}})

	if err := manager.Update(context.Background(), "chat-prod", AgentRoute{}); !errors.Is(err, ErrStaticRouteReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticRouteReadOnly", err)
	}
	if err := manager.Delete(context.Background(), "chat-prod"); !errors.Is(err, ErrStaticRouteReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticRouteReadOnly", err)
	}
}

func TestAgentRouteManagerMatchPrefersMoreSpecificPath(t *testing.T) {
	manager := NewAgentRouteManager(&testManagedRouteStore{items: map[string]*AgentRoute{
		"root": {
			ID:     "root",
			LLMAPI: "openai",
			Match:  RouteMatch{PathPrefix: "/"},
		},
		"tenant": {
			ID:     "tenant",
			LLMAPI: "anthropic",
			Match:  RouteMatch{Host: "api.example.test", PathPrefix: "/tenant", Methods: []string{http.MethodPost}},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "http://api.example.test/tenant/v1/messages", nil)
	got, ok, err := manager.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("Match returned error: %v", err)
	}
	if !ok {
		t.Fatal("Match returned ok=false")
	}
	if got.ID != "tenant" {
		t.Fatalf("matched route = %q, want tenant", got.ID)
	}
}

func TestAgentRouteManagerMatchRejectsMethod(t *testing.T) {
	manager := NewAgentRouteManager(nil)
	manager.InitStaticRoutes([]AgentRoute{{
		ID:    "post-only",
		Match: RouteMatch{PathPrefix: "/v1", Methods: []string{http.MethodPost}},
	}})

	req := httptest.NewRequest(http.MethodGet, "http://example.test/v1/chat/completions", nil)
	if _, ok, err := manager.Match(context.Background(), req); err != nil {
		t.Fatalf("Match returned error: %v", err)
	} else if ok {
		t.Fatal("Match returned ok=true, want false")
	}
}

func TestAgentRouteManagerMatchReturnsDisabledRoute(t *testing.T) {
	manager := NewAgentRouteManager(nil)
	manager.InitStaticRoutes([]AgentRoute{{
		ID:       "disabled",
		Disabled: true,
		Match:    RouteMatch{PathPrefix: "/v1"},
	}})

	req := httptest.NewRequest(http.MethodPost, "http://example.test/v1/chat/completions", nil)
	got, ok, err := manager.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("Match returned error: %v", err)
	}
	if !ok {
		t.Fatal("Match returned ok=false")
	}
	if got.ID != "disabled" {
		t.Fatalf("matched route = %q, want disabled", got.ID)
	}
}
