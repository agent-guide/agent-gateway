package llmroute

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type testManagedRouteStore struct {
	items    map[string]*routecore.RouteConfig
	getCalls int
}

func testLLMRoute(cfg RouteConfig, protocol RouteProtocol, targetPolicy *RouteModelPolicy) LLMRoute {
	cfg.Protocol = protocol
	return LLMRoute{
		RouteMetadata: cfg.Metadata(),
		TargetPolicy:  targetPolicy,
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
	cfg, ok := obj.(*routecore.RouteConfig)
	if !ok || cfg == nil {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*routecore.RouteConfig{}
	}
	cloned := *cfg
	s.items[cloned.ID] = &cloned
	return nil
}

func (s *testManagedRouteStore) Update(_ context.Context, obj any) error {
	if unwrapper, ok := obj.(interface{ ConfigStoreObject() any }); ok {
		obj = unwrapper.ConfigStoreObject()
	}
	cfg, ok := obj.(*routecore.RouteConfig)
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

func mustConfigFromRoute(t *testing.T, route LLMRoute) routecore.RouteConfig {
	t.Helper()
	cfg, err := route.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig returned error: %v", err)
	}
	return cfg
}

func TestLLMRouteResolverGetCachesDecodedRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.RouteConfig{
			"chat-prod": func() *routecore.RouteConfig {
				cfg := mustConfigFromRoute(t, testLLMRoute(RouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteModelPolicy{
					DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
				}))
				return &cfg
			}(),
		},
	}
	manager := routecore.NewRouteConfigManager(store)
	resolver := NewLLMRouteResolver(manager)

	_, err := resolver.ResolveByID(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	_, err = resolver.ResolveByID(context.Background(), "chat-prod")
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0 (validated snapshot)", store.getCalls)
	}
}

func TestLLMRouteResolverResolveCachesByFingerprint(t *testing.T) {
	resolver := NewLLMRouteResolver(routecore.NewRouteConfigManager(nil))

	base := mustConfigFromRoute(t, testLLMRoute(RouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteModelPolicy{
		DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
	}))
	base.UpdatedAt = time.Unix(1000, 0).UTC()

	first, err := resolver.Resolve(context.Background(), base)
	if err != nil {
		t.Fatalf("first Resolve returned error: %v", err)
	}
	second, err := resolver.Resolve(context.Background(), base)
	if err != nil {
		t.Fatalf("second Resolve returned error: %v", err)
	}
	if first == second || first.ID != second.ID {
		t.Fatal("resolved requests must own separate copies of the same generation")
	}

	// A changed UpdatedAt must rebuild even without an explicit Invalidate, so a
	// resolver that missed invalidation still self-heals on the next request.
	bumped := base
	bumped.UpdatedAt = time.Unix(2000, 0).UTC()
	third, err := resolver.Resolve(context.Background(), bumped)
	if err != nil {
		t.Fatalf("third Resolve returned error: %v", err)
	}
	if third == first {
		t.Fatal("changed UpdatedAt reused the stale cached route instance")
	}
}

func TestLLMRouteResolverGetRejectsStaticDynamicCollision(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.RouteConfig{
			"chat-prod": func() *routecore.RouteConfig {
				cfg := mustConfigFromRoute(t, testLLMRoute(RouteConfig{ID: "chat-prod"}, RouteProtocolAnthropic, &RouteModelPolicy{
					DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "dynamic-provider", UpstreamModel: "gpt-4.1"}},
				}))
				return &cfg
			}(),
		},
	}
	manager := routecore.NewRouteConfigManager(store)
	manager.InitStaticRoutes([]routecore.RouteConfig{
		mustConfigFromRoute(t, testLLMRoute(RouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "static-provider", UpstreamModel: "gpt-4.1"}},
		})),
	})
	resolver := NewLLMRouteResolver(manager)

	_, err := resolver.ResolveByID(context.Background(), "chat-prod")
	if err == nil {
		t.Fatal("static/dynamic collision was accepted")
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0", store.getCalls)
	}
}

func TestRouteConfigManagerCreateUpdateDeleteManageCache(t *testing.T) {
	store := &testManagedRouteStore{items: map[string]*routecore.RouteConfig{}}
	manager := routecore.NewRouteConfigManager(store)
	resolver := NewLLMRouteResolver(manager)

	createCfg := mustConfigFromRoute(t, LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai", UpstreamModel: "gpt-4.1"}},
		},
	})
	if err := manager.Create(context.Background(), createCfg, ""); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	store.items["chat-prod"].Description = "stale-store-value"
	got, err := resolver.ResolveByID(context.Background(), "chat-prod")
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
		RouteMetadata: RouteMetadata{Description: "updated", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "anthropic", UpstreamModel: "gpt-4.1"}},
		},
	})
	if err := manager.Update(context.Background(), "chat-prod", updateCfg); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	got, err = resolver.ResolveByID(context.Background(), "chat-prod")
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
	if _, err := resolver.ResolveByID(context.Background(), "chat-prod"); !errors.Is(err, routecore.ErrRouteNotConfigured) {
		t.Fatalf("Get after delete error = %v, want ErrRouteNotConfigured", err)
	}
}

func TestRouteConfigManagerRejectsStaticRouteMutation(t *testing.T) {
	manager := routecore.NewRouteConfigManager(&testManagedRouteStore{items: map[string]*routecore.RouteConfig{}})
	manager.InitStaticRoutes([]routecore.RouteConfig{
		mustConfigFromRoute(t, testLLMRoute(RouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
		})),
	})

	cfg := mustConfigFromRoute(t, testLLMRoute(RouteConfig{ID: "chat-prod"}, RouteProtocolOpenAI, &RouteModelPolicy{
		DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
	}))
	if err := manager.Update(context.Background(), "chat-prod", cfg); !errors.Is(err, routecore.ErrStaticRouteReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticRouteReadOnly", err)
	}
	if err := manager.Delete(context.Background(), "chat-prod"); !errors.Is(err, routecore.ErrStaticRouteReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticRouteReadOnly", err)
	}
}

func TestLLMRouteResolverValidateAcceptsProviderModelRoute(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.RouteConfig{
			"chat-prod": func() *routecore.RouteConfig {
				cfg := mustConfigFromRoute(t, LLMRoute{
					RouteMetadata: RouteMetadata{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
					TargetPolicy: &RouteModelPolicy{
						DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
					},
				})
				return &cfg
			}(),
		},
	}
	resolver := NewLLMRouteResolver(routecore.NewRouteConfigManager(store))

	err := resolver.Validate(context.Background(), "chat-prod", testRouteProviderResolver{
		configured: map[string]bool{"openai-main": true},
	}, nil)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestLLMRouteResolverValidateChecksAllReferencedProviderIDs(t *testing.T) {
	store := &testManagedRouteStore{
		items: map[string]*routecore.RouteConfig{
			"chat-prod": func() *routecore.RouteConfig {
				cfg := mustConfigFromRoute(t, LLMRoute{
					RouteMetadata: RouteMetadata{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
					TargetPolicy: &RouteModelPolicy{
						ModelTargets: []RouteModelTarget{{
							Name: "gpt-4.1-mini",
							Kind: "provider-model", ProviderID: "missing-provider", UpstreamModel: "gpt-4.1-mini",
						}},
					},
				})
				return &cfg
			}(),
		},
	}
	resolver := NewLLMRouteResolver(routecore.NewRouteConfigManager(store))

	err := resolver.Validate(context.Background(), "chat-prod", testRouteProviderResolver{
		configured: map[string]bool{"openai-main": true},
	}, nil)
	if err == nil {
		t.Fatal("Validate returned nil error, want missing provider rejection")
	}
}

type validationModelCatalog struct {
	testModelCatalogResolver
	model *modelcatalog.LogicalModel
	err   error
}

func (c validationModelCatalog) GetLogicalModel(context.Context, string) (*modelcatalog.LogicalModel, bool, error) {
	return c.model, c.model != nil, c.err
}

func TestLLMRouteResolverValidateLogicalCandidateProviders(t *testing.T) {
	cfg := mustConfigFromRoute(t, LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteModelPolicy{ModelTargets: []RouteModelTarget{
			{Name: "chat", Kind: "logical-model", LogicalModelID: "shared"},
		}},
	})
	resolver := NewLLMRouteResolver(routecore.NewRouteConfigManager(&testManagedRouteStore{
		items: map[string]*routecore.RouteConfig{"chat-prod": &cfg},
	}))
	logical := modelcatalog.LogicalModel{ID: "shared", Candidates: []modelcatalog.ModelCandidate{
		{ProviderID: "first", UpstreamModel: "m"}, {ProviderID: "second", UpstreamModel: "n"},
	}}
	disabled := logical.Clone()
	disabled.Disabled = true
	for _, tt := range []struct {
		name      string
		catalog   ModelCatalogResolver
		providers map[string]bool
		want      string
	}{
		{name: "all providers configured", catalog: validationModelCatalog{model: &logical}, providers: map[string]bool{"first": true, "second": true}},
		{name: "later candidate unavailable", catalog: validationModelCatalog{model: &logical}, providers: map[string]bool{"first": true}, want: `provider "second"`},
		{name: "all candidates unavailable", catalog: validationModelCatalog{model: &logical}, want: `provider "first"`},
		{name: "catalog required", want: "model catalog is not configured"},
		{name: "missing logical model", catalog: validationModelCatalog{}, want: `logical model "shared" is not configured`},
		{name: "disabled logical model", catalog: validationModelCatalog{model: &disabled}, want: `logical model "shared" is disabled`},
		{name: "catalog failure", catalog: validationModelCatalog{err: errors.New("catalog unavailable")}, want: "catalog unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := resolver.Validate(t.Context(), "chat-prod", testRouteProviderResolver{configured: tt.providers}, tt.catalog)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolvedRouteOwnsTargetsAndSameTimestampChanges(t *testing.T) {
	resolver := NewLLMRouteResolver(routecore.NewRouteConfigManager(nil))
	cfg := mustConfigFromRoute(t, testLLMRoute(RouteConfig{ID: "route", Protocol: RouteProtocolOpenAI}, RouteProtocolOpenAI, &RouteModelPolicy{ModelTargets: []RouteModelTarget{{Name: "original", Kind: "provider-model", ProviderID: "p", UpstreamModel: "original"}}}))
	first, err := resolver.Resolve(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	first.TargetPolicy.ModelTargets[0].UpstreamModel = "caller-mutated"
	first.MatchPolicy.Methods = append(first.MatchPolicy.Methods, "DELETE")
	second, err := resolver.Resolve(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second.TargetPolicy.ModelTargets[0].UpstreamModel != "original" || len(second.MatchPolicy.Methods) != 0 {
		t.Fatal("caller mutated cached generation")
	}
	changed := cfg
	changed.TargetPolicy = bytes.ReplaceAll(cfg.TargetPolicy, []byte("original"), []byte("new-model"))
	third, err := resolver.Resolve(t.Context(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.TargetPolicy.ModelTargets[0].UpstreamModel != "new-model" {
		t.Fatal("timestamp collision reused stale target")
	}
	fourth, err := resolver.Resolve(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.TargetPolicy.ModelTargets[0].UpstreamModel != "original" {
		t.Fatal("detached generation poisoned original target")
	}
}
