package llmroute

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type testManagedRouteStore struct {
	items    map[string]*routecore.AgentRouteConfig
	getCalls int
}

func testLLMRoute(cfg AgentRouteConfig, protocol RouteProtocol, targetPolicy RouteTargetPolicy) LLMRoute {
	cfg.Protocol = protocol
	return LLMRoute{
		AgentRouteConfig: cfg,
		TargetPolicy:     targetPolicy,
	}
}

type testRouteProviderResolver struct {
	configured map[string]bool
}

func (r testRouteProviderResolver) ResolveProvider(_ context.Context, providerID string) (provider.Provider, string, error) {
	if r.configured[providerID] {
		return nil, providerID, nil
	}
	return nil, "", fmt.Errorf("provider %q is not configured", providerID)
}

func (s *testManagedRouteStore) List(ctx context.Context) ([]any, error) {
	return s.ListByTag(ctx, "")
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

func (s *testManagedRouteStore) Create(_ context.Context, obj any) error {
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

func (s *testManagedRouteStore) Update(_ context.Context, obj any) error {
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
	return s.Create(context.Background(), obj)
}

func (s *testManagedRouteStore) Delete(_ context.Context, keyParts ...any) error {
	id, _ := keyParts[0].(string)
	delete(s.items, id)
	return nil
}

func (s *testManagedRouteStore) Get(_ context.Context, keyParts ...any) (any, error) {
	s.getCalls++
	id, _ := keyParts[0].(string)
	item, ok := s.items[id]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	cloned := *item
	return &cloned, nil
}

func (s *testManagedRouteStore) GetByIndex(context.Context, string, any) (any, error) {
	return nil, configstore.ErrNotFound
}

func mustConfigFromRoute(t *testing.T, route LLMRoute) routecore.AgentRouteConfig {
	t.Helper()
	cfg, err := route.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig returned error: %v", err)
	}
	return cfg
}

func TestLLMRouteResolverGetCachesDecodedRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.AgentRouteConfig{
			"chat-prod": func() *routecore.AgentRouteConfig {
				cfg := mustConfigFromRoute(t, testLLMRoute(AgentRouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteDirectProviderPolicy{
					ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
				}))
				return &cfg
			}(),
		},
	}
	manager := routecore.NewAgentRouteConfigManager(store)
	resolver := NewLLMRouteResolver(manager)

	_, err := resolver.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	_, err = resolver.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if store.getCalls != 1 {
		t.Fatalf("store get calls = %d, want 1", store.getCalls)
	}
}

func TestLLMRouteResolverGetPrefersStaticRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.AgentRouteConfig{
			"chat-prod": func() *routecore.AgentRouteConfig {
				cfg := mustConfigFromRoute(t, testLLMRoute(AgentRouteConfig{ID: "chat-prod"}, RouteProtocolAnthropic, &RouteDirectProviderPolicy{
					ProviderTarget: DirectProviderTarget{ProviderID: "dynamic-provider"},
				}))
				return &cfg
			}(),
		},
	}
	manager := routecore.NewAgentRouteConfigManager(store)
	manager.InitStaticRoutes([]routecore.AgentRouteConfig{
		mustConfigFromRoute(t, testLLMRoute(AgentRouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteDirectProviderPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "static-provider"},
		})),
	})
	resolver := NewLLMRouteResolver(manager)

	got, err := resolver.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.TargetPolicy.(*RouteDirectProviderPolicy).ProviderTarget.ProviderID != "static-provider" {
		t.Fatalf("provider = %q, want static-provider", got.TargetPolicy.(*RouteDirectProviderPolicy).ProviderTarget.ProviderID)
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0", store.getCalls)
	}
}

func TestAgentRouteConfigManagerCreateUpdateDeleteManageCache(t *testing.T) {
	store := &testManagedRouteStore{items: map[string]*routecore.AgentRouteConfig{}}
	manager := routecore.NewAgentRouteConfigManager(store)
	resolver := NewLLMRouteResolver(manager)

	createCfg := mustConfigFromRoute(t, LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod"},
		TargetPolicy: &RouteDirectProviderPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai"},
		},
	})
	if err := manager.Create(context.Background(), createCfg, ""); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	store.items["chat-prod"].Description = "stale-store-value"
	got, err := resolver.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Description != "" {
		t.Fatalf("Description = %q, want empty cached value", got.Description)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero after create")
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt is zero after create")
	}
	createdAt := got.CreatedAt
	firstUpdatedAt := got.UpdatedAt

	updateCfg := mustConfigFromRoute(t, LLMRoute{
		AgentRouteConfig: AgentRouteConfig{Description: "updated"},
		TargetPolicy: &RouteDirectProviderPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "anthropic"},
		},
	})
	if err := manager.Update(context.Background(), "chat-prod", updateCfg); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, err = resolver.Get(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get after update returned error: %v", err)
	}
	if got.Description != "updated" {
		t.Fatalf("Description = %q, want updated", got.Description)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt changed on update: got %v want %v", got.CreatedAt, createdAt)
	}
	if got.UpdatedAt.Before(firstUpdatedAt) {
		t.Fatalf("UpdatedAt moved backwards: got %v want >= %v", got.UpdatedAt, firstUpdatedAt)
	}
	if got.UpdatedAt.Equal(time.Time{}) {
		t.Fatal("UpdatedAt is zero after update")
	}

	if err := manager.Delete(context.Background(), "chat-prod"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := resolver.Get(context.Background(), "chat-prod"); !errors.Is(err, routecore.ErrRouteNotConfigured) {
		t.Fatalf("Get after delete error = %v, want ErrRouteNotConfigured", err)
	}
}

func TestAgentRouteConfigManagerRejectsStaticRouteMutation(t *testing.T) {
	manager := routecore.NewAgentRouteConfigManager(&testManagedRouteStore{items: map[string]*routecore.AgentRouteConfig{}})
	manager.InitStaticRoutes([]routecore.AgentRouteConfig{
		mustConfigFromRoute(t, testLLMRoute(AgentRouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteDirectProviderPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
		})),
	})

	cfg := mustConfigFromRoute(t, testLLMRoute(AgentRouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteDirectProviderPolicy{
		ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
	}))
	if err := manager.Update(context.Background(), "chat-prod", cfg); !errors.Is(err, routecore.ErrStaticRouteReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticRouteReadOnly", err)
	}
	if err := manager.Delete(context.Background(), "chat-prod"); !errors.Is(err, routecore.ErrStaticRouteReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticRouteReadOnly", err)
	}
}

func TestLLMRouteResolverValidateAcceptsDirectProviderRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.AgentRouteConfig{
			"chat-prod": func() *routecore.AgentRouteConfig {
				cfg := mustConfigFromRoute(t, LLMRoute{
					AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
					TargetPolicy: &RouteDirectProviderPolicy{
						ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
					},
				})
				return &cfg
			}(),
		},
	}
	resolver := NewLLMRouteResolver(routecore.NewAgentRouteConfigManager(store))

	err := resolver.Validate(context.Background(), "chat-prod", testRouteProviderResolver{
		configured: map[string]bool{"openai-main": true},
	})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestLLMRouteResolverValidateChecksAllReferencedProviderIDs(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.AgentRouteConfig{
			"chat-prod": func() *routecore.AgentRouteConfig {
				cfg := mustConfigFromRoute(t, LLMRoute{
					AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
					TargetPolicy: &RouteLogicalModelTargetPolicy{
						ModelTargets: []RouteModelTarget{{
							Name: "chat-fast",
							Candidates: []RouteModelCandidate{{
								ProviderID:    "missing-provider",
								UpstreamModel: "gpt-4.1-mini",
							}},
						}},
					},
				})
				return &cfg
			}(),
		},
	}
	resolver := NewLLMRouteResolver(routecore.NewAgentRouteConfigManager(store))

	err := resolver.Validate(context.Background(), "chat-prod", testRouteProviderResolver{
		configured: map[string]bool{"openai-main": true},
	})
	if err == nil {
		t.Fatal("Validate returned nil error, want missing provider rejection")
	}
}
