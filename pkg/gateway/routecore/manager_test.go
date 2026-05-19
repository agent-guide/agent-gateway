package routecore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentRouteConfigManagerMatchPrefersMoreSpecificPath(t *testing.T) {
	manager := NewAgentRouteConfigManager(&testRouteStore{
		items: map[string]*AgentRouteConfig{
			"root": {
				ID:          "root",
				MatchPolicy: RouteMatchPolicy{PathPrefix: "/"},
			},
			"tenant": {
				ID:          "tenant",
				MatchPolicy: RouteMatchPolicy{Host: "api.example.test", PathPrefix: "/tenant", Methods: []string{http.MethodPost}},
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "http://api.example.test/tenant/v1/messages", nil)
	got, ok, err := manager.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if !ok {
		t.Fatal("Match() ok = false, want true")
	}
	if got.ID != "tenant" {
		t.Fatalf("Match() route id = %q, want tenant", got.ID)
	}
}

func TestAgentRouteConfigManagerMatchRejectsMethod(t *testing.T) {
	manager := NewAgentRouteConfigManager(nil)
	manager.InitStaticRoutes([]AgentRouteConfig{{
		ID:          "post-only",
		MatchPolicy: RouteMatchPolicy{PathPrefix: "/v1", Methods: []string{http.MethodPost}},
	}})

	req := httptest.NewRequest(http.MethodGet, "http://example.test/v1/chat/completions", nil)
	if _, ok, err := manager.Match(context.Background(), req); err != nil {
		t.Fatalf("Match() error = %v", err)
	} else if ok {
		t.Fatal("Match() ok = true, want false")
	}
}

func TestAgentRouteConfigManagerMatchReturnsDisabledRoute(t *testing.T) {
	manager := NewAgentRouteConfigManager(nil)
	manager.InitStaticRoutes([]AgentRouteConfig{{
		ID:          "disabled",
		Disabled:    true,
		MatchPolicy: RouteMatchPolicy{PathPrefix: "/v1"},
	}})

	req := httptest.NewRequest(http.MethodPost, "http://example.test/v1/chat/completions", nil)
	got, ok, err := manager.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if !ok {
		t.Fatal("Match() ok = false, want true")
	}
	if got.ID != "disabled" {
		t.Fatalf("Match() route id = %q, want disabled", got.ID)
	}
}

type testRouteStore struct {
	items map[string]*AgentRouteConfig
}

func (s *testRouteStore) List(_ context.Context) ([]any, error) {
	return s.ListByTag(context.Background(), "")
}

func (s *testRouteStore) Get(_ context.Context, keyParts ...any) (any, error) {
	if len(keyParts) == 0 {
		return nil, nil
	}
	id, _ := keyParts[0].(string)
	if route, ok := s.items[id]; ok {
		copy := *route
		return &copy, nil
	}
	return nil, nil
}

func (s *testRouteStore) GetByIndex(context.Context, string, any) (any, error) { return nil, nil }

func (s *testRouteStore) ListByTag(_ context.Context, _ string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, route := range s.items {
		copy := *route
		out = append(out, &copy)
	}
	return out, nil
}

func (s *testRouteStore) ListByTagPrefix(_ context.Context, _ string) ([]any, error) {
	return s.ListByTag(context.Background(), "")
}

func (s *testRouteStore) Create(context.Context, any) error { return nil }

func (s *testRouteStore) Update(context.Context, any) error { return nil }

func (s *testRouteStore) Delete(context.Context, ...any) error { return nil }
