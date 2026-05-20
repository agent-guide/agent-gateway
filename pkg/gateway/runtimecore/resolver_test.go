package runtimecore

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

type testRuntimeRoute struct {
	Config routecore.AgentRouteConfig
}

type testRuntimeRouteStore struct {
	items    map[string]*routecore.AgentRouteConfig
	getCalls int
}

func (s *testRuntimeRouteStore) List(ctx context.Context) ([]any, error) {
	return s.ListByTag(ctx, "")
}

func (s *testRuntimeRouteStore) ListByTag(_ context.Context, _ string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testRuntimeRouteStore) ListByTagPrefix(ctx context.Context, _ string) ([]any, error) {
	return s.ListByTag(ctx, "")
}

func (s *testRuntimeRouteStore) Create(_ context.Context, obj any) error {
	if unwrapper, ok := obj.(interface{ ConfigStoreObject() any }); ok {
		obj = unwrapper.ConfigStoreObject()
	}
	cfg, ok := obj.(*routecore.AgentRouteConfig)
	if !ok || cfg == nil {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*routecore.AgentRouteConfig{}
	}
	cloned := *cfg
	s.items[cloned.ID] = &cloned
	return nil
}

func (s *testRuntimeRouteStore) Update(_ context.Context, obj any) error {
	if unwrapper, ok := obj.(interface{ ConfigStoreObject() any }); ok {
		obj = unwrapper.ConfigStoreObject()
	}
	cfg, ok := obj.(*routecore.AgentRouteConfig)
	if !ok || cfg == nil {
		return errors.New("unexpected type")
	}
	if _, ok := s.items[cfg.ID]; !ok {
		return configstore.ErrNotFound
	}
	cloned := *cfg
	s.items[cloned.ID] = &cloned
	return nil
}

func (s *testRuntimeRouteStore) Delete(_ context.Context, keyParts ...any) error {
	id, _ := keyParts[0].(string)
	delete(s.items, id)
	return nil
}

func (s *testRuntimeRouteStore) Get(_ context.Context, keyParts ...any) (any, error) {
	s.getCalls++
	id, _ := keyParts[0].(string)
	item, ok := s.items[id]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	cloned := *item
	return &cloned, nil
}

func (s *testRuntimeRouteStore) GetByIndex(context.Context, string, any) (any, error) {
	return nil, configstore.ErrNotFound
}

func TestResolverGetCachesDecodedRoute(t *testing.T) {
	store := &testRuntimeRouteStore{
		items: map[string]*routecore.AgentRouteConfig{
			"route-1": {ID: "route-1", Protocol: "openai"},
		},
	}
	manager := routecore.NewAgentRouteConfigManager(store)
	decodeCalls := 0
	resolver := NewResolver(
		FuncSource[routecore.AgentRouteConfig, routecore.RouteListOptions]{
			GetFunc:  manager.Get,
			ListFunc: manager.List,
		},
		func(cfg routecore.AgentRouteConfig) string {
			return cfg.ID
		},
		func(cfg routecore.AgentRouteConfig) (string, error) {
			return FingerprintJSON(cfg.ID, "route config", cfg)
		},
		func(cfg routecore.AgentRouteConfig) (*testRuntimeRoute, error) {
			decodeCalls++
			return &testRuntimeRoute{Config: cfg}, nil
		},
	)

	first, err := resolver.Get(context.Background(), "route-1")
	if err != nil {
		t.Fatalf("first Get() error = %v", err)
	}
	second, err := resolver.Get(context.Background(), "route-1")
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}

	if first != second {
		t.Fatalf("cached route mismatch: first=%p second=%p", first, second)
	}
	if store.getCalls != 1 {
		t.Fatalf("store get calls = %d, want 1", store.getCalls)
	}
	if decodeCalls != 1 {
		t.Fatalf("decode calls = %d, want 1", decodeCalls)
	}
}

func TestResolverGetRebuildsWhenConfigChanges(t *testing.T) {
	store := &testRuntimeRouteStore{
		items: map[string]*routecore.AgentRouteConfig{
			"route-1": {ID: "route-1", Protocol: "openai"},
		},
	}
	manager := routecore.NewAgentRouteConfigManager(store)
	decodeCalls := 0
	resolver := NewResolver(
		FuncSource[routecore.AgentRouteConfig, routecore.RouteListOptions]{
			GetFunc:  manager.Get,
			ListFunc: manager.List,
		},
		func(cfg routecore.AgentRouteConfig) string {
			return cfg.ID
		},
		func(cfg routecore.AgentRouteConfig) (string, error) {
			return FingerprintJSON(cfg.ID, "route config", cfg)
		},
		func(cfg routecore.AgentRouteConfig) (*testRuntimeRoute, error) {
			decodeCalls++
			return &testRuntimeRoute{Config: cfg}, nil
		},
	)

	first, err := resolver.Get(context.Background(), "route-1")
	if err != nil {
		t.Fatalf("first Get() error = %v", err)
	}

	store.items["route-1"] = &routecore.AgentRouteConfig{ID: "route-1", Protocol: "anthropic"}
	manager.Reset()

	second, err := resolver.Get(context.Background(), "route-1")
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}

	if first == second {
		t.Fatal("Get() reused cached route after config change")
	}
	if decodeCalls != 2 {
		t.Fatalf("decode calls = %d, want 2", decodeCalls)
	}
	if second == nil || second.Config.Protocol != "anthropic" {
		t.Fatalf("second protocol = %q, want anthropic", second.Config.Protocol)
	}
}

func TestResolverInvalidateForcesRebuild(t *testing.T) {
	store := &testRuntimeRouteStore{
		items: map[string]*routecore.AgentRouteConfig{
			"route-1": {ID: "route-1", Protocol: "openai"},
		},
	}
	manager := routecore.NewAgentRouteConfigManager(store)
	decodeCalls := 0
	resolver := NewResolver(
		FuncSource[routecore.AgentRouteConfig, routecore.RouteListOptions]{
			GetFunc:  manager.Get,
			ListFunc: manager.List,
		},
		func(cfg routecore.AgentRouteConfig) string {
			return cfg.ID
		},
		func(cfg routecore.AgentRouteConfig) (string, error) {
			return FingerprintJSON(cfg.ID, "route config", cfg)
		},
		func(cfg routecore.AgentRouteConfig) (*testRuntimeRoute, error) {
			decodeCalls++
			return &testRuntimeRoute{Config: cfg}, nil
		},
	)

	_, err := resolver.Get(context.Background(), "route-1")
	if err != nil {
		t.Fatalf("first Get() error = %v", err)
	}

	resolver.Invalidate("route-1")

	_, err = resolver.Get(context.Background(), "route-1")
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}

	if decodeCalls != 2 {
		t.Fatalf("decode calls = %d, want 2", decodeCalls)
	}
}
