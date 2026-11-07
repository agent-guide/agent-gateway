package routecore

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type mutationRouteStore struct{ testRouteStore }

func (s *mutationRouteStore) Create(_ context.Context, obj any) error {
	r := obj.(storedRouteConfig).route
	if s.items[r.ID] != nil {
		return errors.New("already exists")
	}
	s.items[r.ID] = &r
	return nil
}
func (s *mutationRouteStore) Update(_ context.Context, obj any) error {
	r := obj.(storedRouteConfig).route
	if s.items[r.ID] == nil {
		return configstore.ErrNotFound
	}
	s.items[r.ID] = &r
	return nil
}

func TestConcurrentRouteWritesRejectTies(t *testing.T) {
	ctx := context.Background()
	store := &mutationRouteStore{testRouteStore{items: map[string]*RouteConfig{}}}
	m := NewRouteConfigManager(store)
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := RouteConfig{ID: string(rune('a' + i)), MatchPolicy: RouteMatchPolicy{PathPrefix: "/api"}}
			if err := m.Create(ctx, r, ""); err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrAmbiguousRoute) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 || len(store.items) != 1 {
		t.Fatalf("published %d routes, persisted %d", accepted.Load(), len(store.items))
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshRetainsLastValidGenerationAndListCannotPublish(t *testing.T) {
	ctx := context.Background()
	store := &mutationRouteStore{testRouteStore{items: map[string]*RouteConfig{"a": {ID: "a", MatchPolicy: RouteMatchPolicy{PathPrefix: "/api"}}}}}
	m := NewRouteConfigManager(store)
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	store.items["b"] = &RouteConfig{ID: "b", Kind: RouteKindMCP, Disabled: true, MatchPolicy: RouteMatchPolicy{PathPrefix: "/api"}}
	err := m.Refresh(ctx)
	if !errors.Is(err, ErrAmbiguousRoute) || !strings.Contains(err.Error(), "repair-architecture-config") {
		t.Fatalf("error = %v", err)
	}
	// Listing externally changed persistence must not seed unvalidated runtime
	// entries. Requests continue to see the last valid generation.
	if _, err := m.List(ctx, RouteListOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, "b"); !errors.Is(err, ErrRouteNotConfigured) {
		t.Fatalf("unpublished record became visible: %v", err)
	}
	for range 100 {
		got, ok, err := m.Match(ctx, httptest.NewRequest("POST", "http://example.org/api", nil))
		if err != nil || !ok || got.ID != "a" {
			t.Fatalf("match = %v, %v, %v", got, ok, err)
		}
	}
	delete(store.items, "b")
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStaticDynamicTieAndMutationRejection(t *testing.T) {
	ctx := context.Background()
	store := &mutationRouteStore{testRouteStore{items: map[string]*RouteConfig{}}}
	m := NewRouteConfigManager(store)
	static := RouteConfig{ID: "static", MatchPolicy: RouteMatchPolicy{PathPrefix: "/api", Methods: []string{"GET", "POST"}}}
	if err := m.InitStaticRoutes([]RouteConfig{static}); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	dynamic := RouteConfig{ID: "dynamic", Disabled: true, MatchPolicy: RouteMatchPolicy{PathPrefix: "/api", Methods: []string{"POST"}}}
	if err := m.Create(ctx, dynamic, ""); !errors.Is(err, ErrAmbiguousRoute) {
		t.Fatalf("static/dynamic tie accepted: %v", err)
	}
	dynamic.MatchPolicy.PathPrefix = "/other"
	if err := m.Create(ctx, dynamic, ""); err != nil {
		t.Fatal(err)
	}
	dynamic.MatchPolicy.PathPrefix = "/api"
	if err := m.Update(ctx, dynamic.ID, dynamic); !errors.Is(err, ErrAmbiguousRoute) {
		t.Fatalf("update tie accepted: %v", err)
	}
	if got := store.items[dynamic.ID].MatchPolicy.PathPrefix; got != "/other" {
		t.Fatal("rejected update persisted")
	}
	// Existing conflicting rows reject first startup; no partial data plane.
	store.items[dynamic.ID] = &dynamic
	fresh := NewRouteConfigManager(store)
	if err := fresh.InitStaticRoutes([]RouteConfig{static}); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Refresh(ctx); !errors.Is(err, ErrAmbiguousRoute) {
		t.Fatalf("startup accepted ambiguity: %v", err)
	}
}

func TestRuntimeTieErrorIsDeterministic(t *testing.T) {
	a := RouteConfig{ID: "a", MatchPolicy: RouteMatchPolicy{PathPrefix: "/api"}}
	b := a
	b.ID = "b"
	request := httptest.NewRequest("GET", "http://example.org/api", nil)
	_, _, first := MatchRouteConfigs([]RouteConfig{a, b}, request)
	_, _, second := MatchRouteConfigs([]RouteConfig{b, a}, request)
	if !errors.Is(first, ErrAmbiguousRoute) || first.Error() != second.Error() {
		t.Fatalf("unstable ambiguity: %v / %v", first, second)
	}
	// A more-specific route makes an existing broad tie irrelevant to this
	// request; publication still rejects the ambiguous configuration globally.
	c := a
	c.ID = "c"
	c.MatchPolicy.PathPrefix = "/api/chat"
	got, ok, err := MatchRouteConfigs([]RouteConfig{a, b, c}, httptest.NewRequest("GET", "http://example.org/api/chat", nil))
	if err != nil || !ok || got.ID != "c" {
		t.Fatalf("match = %v, %v, %v", got, ok, err)
	}
}

func TestRouteReadsCannotMutatePublishedConfiguration(t *testing.T) {
	ctx := context.Background()
	m := NewRouteConfigManager(nil)
	r := RouteConfig{ID: "a", MatchPolicy: RouteMatchPolicy{PathPrefix: "/api", Methods: []string{"POST"}}, TargetPolicy: []byte(`{"provider_target":{"provider_id":"p"}}`)}
	if err := m.InitStaticRoutes([]RouteConfig{r}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://example.org/api", nil)
	got, ok, err := m.Match(ctx, request)
	if err != nil || !ok {
		t.Fatal(err)
	}
	got.MatchPolicy.Methods[0] = "DELETE"
	got.TargetPolicy[0] = '['
	again, err := m.Get(ctx, "a")
	if err != nil || again.MatchPolicy.Methods[0] != "POST" || again.TargetPolicy[0] != '{' {
		t.Fatalf("read mutated generation: %v, %v", again, err)
	}
}

func TestRemovedTargetFieldRejectedBeforePublication(t *testing.T) {
	ctx := context.Background()
	store := &mutationRouteStore{testRouteStore{items: map[string]*RouteConfig{}}}
	m := NewRouteConfigManager(store)
	r := RouteConfig{ID: "a", Kind: RouteKindLLM, TargetPolicy: []byte(`{"provider_id":"p"}`)}
	if err := m.Create(ctx, r, ""); err == nil {
		t.Fatal("removed field persisted")
	}
	if len(store.items) != 0 {
		t.Fatal("invalid create wrote storage")
	}
	if err := m.InitStaticRoutes([]RouteConfig{r}); err == nil {
		t.Fatal("static config accepted removed field")
	}
	if _, err := DecodeStoredRouteConfig([]byte(`{"id":"a","kind":"llm","target_policy":{"provider_id":"p"}}`)); err == nil {
		t.Fatal("stored decoder silently ignored removed field")
	}
}
