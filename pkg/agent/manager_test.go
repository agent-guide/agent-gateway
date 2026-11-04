package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

// testConfigStore is a minimal in-memory ConfigStore for agent manager tests.
type testConfigStore struct {
	mu    sync.RWMutex
	items map[string]*Agent
}

func newTestConfigStore() configstore.ConfigStore {
	return &testConfigStore{items: make(map[string]*Agent)}
}

func (s *testConfigStore) unwrap(obj any) (*Agent, error) {
	if u, ok := obj.(configstore.ObjectUnwrapper); ok {
		obj = u.ConfigStoreObject()
	}
	cfg, ok := obj.(*Agent)
	if !ok {
		return nil, fmt.Errorf("testConfigStore: unexpected type %T", obj)
	}
	return cfg, nil
}

func (s *testConfigStore) Create(_ context.Context, obj any) error {
	cfg, err := s.unwrap(obj)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[cfg.ID]; exists {
		return fmt.Errorf("already exists: %s", cfg.ID)
	}
	cloned := *cfg
	s.items[cfg.ID] = &cloned
	return nil
}

func (s *testConfigStore) Update(_ context.Context, obj any) error {
	cfg, err := s.unwrap(obj)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *cfg
	s.items[cfg.ID] = &cloned
	return nil
}

func (s *testConfigStore) Delete(_ context.Context, keyParts ...any) error {
	if len(keyParts) == 0 {
		return fmt.Errorf("key required")
	}
	id := fmt.Sprint(keyParts[0])
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

func (s *testConfigStore) Get(_ context.Context, keyParts ...any) (any, error) {
	if len(keyParts) == 0 {
		return nil, fmt.Errorf("key required")
	}
	id := fmt.Sprint(keyParts[0])
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.items[id]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	cloned := *cfg
	return &cloned, nil
}

func (s *testConfigStore) List(_ context.Context) ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]any, 0, len(s.items))
	for _, cfg := range s.items {
		cloned := *cfg
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testConfigStore) ListByTag(_ context.Context, _ string) ([]any, error) {
	return s.List(context.Background())
}

func (s *testConfigStore) ListByTagPrefix(_ context.Context, _ string) ([]any, error) {
	return s.List(context.Background())
}

func (s *testConfigStore) GetByIndex(_ context.Context, _ string, _ any) (any, error) {
	return nil, configstore.ErrNotFound
}

func acpAgent(id string, routeIDs ...string) Agent {
	return Agent{
		ID:      id,
		Name:    id,
		Runtime: Runtime{Type: RuntimeTypeACP, ACP: &ACPRuntime{AgentType: "codex", CWD: "/tmp", AllowedRoots: []string{"/tmp"}}},
		Routes:  Routes{LLMRouteIDs: routeIDs},
	}
}

func TestCreateRejectsDuplicateMCPRouteBinding(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()
	if err := m.Create(ctx, Agent{ID: "a1", Name: "a1", Runtime: acpAgent("a1").Runtime, Routes: Routes{MCPRouteIDs: []string{"route-1"}}}); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	a2 := acpAgent("a2")
	a2.Routes.MCPRouteIDs = []string{"route-1"}
	err := m.Create(ctx, a2)
	if err == nil {
		t.Fatalf("expected duplicate route binding to be rejected")
	}
}

func TestRefreshDropsAmbiguousRouteMapping(t *testing.T) {
	store := newTestConfigStore()
	m := NewManager(store)
	ctx := context.Background()
	if err := store.Create(ctx, &Agent{ID: "a1", Name: "a1", Runtime: acpAgent("a1").Runtime, Routes: Routes{MCPRouteIDs: []string{"shared-route"}}}); err != nil {
		t.Fatalf("seed a1: %v", err)
	}
	if err := store.Create(ctx, &Agent{ID: "a2", Name: "a2", Runtime: acpAgent("a2").Runtime, Routes: Routes{MCPRouteIDs: []string{"shared-route"}}}); err != nil {
		t.Fatalf("seed a2: %v", err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if id, ok := m.ResolveAgentID("shared-route", "", ""); ok {
		t.Fatalf("ambiguous route mapping resolved to %q, want ok=false", id)
	}
}

func TestValidateRuntime(t *testing.T) {
	bad := Agent{ID: "x", Name: "x", Runtime: Runtime{Type: RuntimeTypeACP}}
	if err := bad.Validate(); err == nil {
		t.Fatalf("acp runtime without runtime.acp config must fail")
	}
	httpOK := Agent{ID: "x", Name: "x", Runtime: Runtime{Type: RuntimeTypeHTTP, HTTP: &HTTPRuntime{CardURL: "https://x/.well-known/agent-card.json", Protocol: "a2a"}}}
	if err := httpOK.Validate(); err != nil {
		t.Fatalf("valid http agent rejected: %v", err)
	}
}

func TestHTTPRuntimeValidation(t *testing.T) {
	valid := Agent{ID: "remote", Name: "Remote", Runtime: Runtime{Type: RuntimeTypeHTTP, HTTP: &HTTPRuntime{
		CardURL: "https://agent.example/.well-known/agent-card.json", Protocol: "a2a", TimeoutSeconds: 30,
	}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid HTTP runtime rejected: %v", err)
	}

	tests := []struct {
		name    string
		runtime HTTPRuntime
	}{
		{"missing card", HTTPRuntime{Protocol: "a2a"}},
		{"relative card", HTTPRuntime{CardURL: "/.well-known/agent-card.json", Protocol: "a2a"}},
		{"insecure remote", HTTPRuntime{CardURL: "http://agent.example/.well-known/agent-card.json", Protocol: "a2a"}},
		{"userinfo", HTTPRuntime{CardURL: "https://user@agent.example/card", Protocol: "a2a"}},
		{"missing protocol", HTTPRuntime{CardURL: "https://agent.example/card"}},
		{"custom protocol", HTTPRuntime{CardURL: "https://agent.example/card", Protocol: "custom"}},
		{"negative timeout", HTTPRuntime{CardURL: "https://agent.example/card", Protocol: "a2a", TimeoutSeconds: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := valid
			a.Runtime.HTTP = &tt.runtime
			if err := a.Validate(); err == nil {
				t.Fatal("Validate() returned nil")
			}
		})
	}

	loopback := valid
	loopback.Runtime.HTTP = &HTTPRuntime{CardURL: "http://127.0.0.1:8080/card", Protocol: "a2a"}
	if err := loopback.Validate(); err != nil {
		t.Fatalf("loopback HTTP rejected: %v", err)
	}
}

func TestResolveAgentIDIndex(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()
	if err := m.Create(ctx, Agent{ID: "a1", Name: "a1", Runtime: acpAgent("a1").Runtime, Routes: Routes{MCPRouteIDs: []string{"route-1"}}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if id, ok := m.ResolveAgentID("route-1", "", ""); !ok || id != "a1" {
		t.Fatalf("route mapping = (%q,%v), want a1,true", id, ok)
	}
	if _, ok := m.ResolveAgentID("unknown", "unknown", ""); ok {
		t.Fatalf("unknown mapping must be ok=false")
	}
	if err := m.Delete(ctx, "a1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := m.ResolveAgentID("route-1", "", ""); ok {
		t.Fatalf("mapping must be cleared after delete")
	}
}

func TestSharedLLMRoutesDoNotEstablishAgentIdentity(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := t.Context()
	for _, id := range []string{"a1", "a2"} {
		if err := m.Create(ctx, acpAgent(id, "shared")); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Update(ctx, "a2", acpAgent("a2", "shared")); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.ResolveAgentID("shared", "", ""); ok {
		t.Fatal("shared LLM route must not infer an Agent")
	}
	if err := m.Delete(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.ResolveAgentID("shared", "", ""); ok {
		t.Fatal("even one LLM reference does not identify direct callers")
	}
	if _, ok := m.GetSnapshot("a2"); !ok {
		t.Fatal("remaining Agent was removed")
	}
}

// Gate the first operation while the competing operation attempts to commit.
func TestResourceRouteDeletionSerializesWithAgentWrites(t *testing.T) {
	for _, kind := range []string{"llm", "mcp"} {
		for _, update := range []bool{false, true} {
			for _, deleteFirst := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/update=%v/deleteFirst=%v", kind, update, deleteFirst), func(t *testing.T) {
					ctx := t.Context()
					errMissingRoute := errors.New("test resource route is missing")
					m := NewManager(newTestConfigStore())
					if update {
						if err := m.Create(ctx, acpAgent("a")); err != nil {
							t.Fatal(err)
						}
					}
					routeExists := true
					entered, release := make(chan struct{}), make(chan struct{})
					var releaseOnce sync.Once
					unblock := func() { releaseOnce.Do(func() { close(release) }) }
					defer unblock()
					m.SetResourceRouteValidator(func(_ context.Context, routes Routes) error {
						if !routeExists {
							return errMissingRoute
						}
						if !deleteFirst {
							close(entered)
							<-release
						}
						return nil
					})
					deleteRoute := m.DeleteUnreferencedLLMRoute
					errReferenced := ErrLLMRouteReferenced
					if kind == "mcp" {
						deleteRoute = m.DeleteUnreferencedMCPRoute
						errReferenced = ErrMCPRouteReferenced
					}
					writeResult, deleteResult := make(chan error, 1), make(chan error, 1)
					write := func() {
						a := acpAgent("a", "chat")
						if kind == "mcp" {
							a.Routes = Routes{MCPRouteIDs: []string{"chat"}}
						}
						if update {
							writeResult <- m.Update(ctx, a.ID, a)
						} else {
							writeResult <- m.Create(ctx, a)
						}
					}
					remove := func() {
						deleteResult <- deleteRoute(ctx, "chat", func(context.Context, string) error {
							if deleteFirst {
								close(entered)
								<-release
							}
							routeExists = false
							return nil
						})
					}
					if deleteFirst {
						go remove()
					} else {
						go write()
					}
					<-entered
					if deleteFirst {
						go write()
					} else {
						go remove()
					}
					select {
					case err := <-writeResult:
						t.Fatalf("write completed inside critical section: %v", err)
					case err := <-deleteResult:
						t.Fatalf("delete completed inside critical section: %v", err)
					case <-time.After(25 * time.Millisecond):
					}
					unblock()
					writeErr, deleteErr := <-writeResult, <-deleteResult
					if deleteFirst {
						if deleteErr != nil || !errors.Is(writeErr, errMissingRoute) {
							t.Fatalf("delete/write = %v/%v", deleteErr, writeErr)
						}
						agents, err := m.List(ctx)
						if err != nil {
							t.Fatal(err)
						}
						for _, a := range agents {
							if len(a.Routes.LLMRouteIDs) != 0 || len(a.Routes.MCPRouteIDs) != 0 {
								t.Fatalf("dangling reference: %+v", a)
							}
						}
					} else if writeErr != nil || !errors.Is(deleteErr, errReferenced) || !routeExists {
						t.Fatalf("write/delete/exists = %v/%v/%v", writeErr, deleteErr, routeExists)
					}
				})
			}
		}
	}

}

type attributionCaptureSink struct{ events []any }

func (s *attributionCaptureSink) Enqueue(event any) bool {
	s.events = append(s.events, event)
	return true
}

func TestObserverMCPFallbackCannotAttributeSharedLLMTraffic(t *testing.T) {
	m := NewManager(newTestConfigStore())
	for _, id := range []string{"a", "b"} {
		if err := m.Create(t.Context(), acpAgent(id, "chat")); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate an existing misconfigured definition loaded without route validation.
	c := acpAgent("c")
	c.Routes.MCPRouteIDs = []string{"chat"}
	if err := m.Create(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	holder := usage.NewAgentAttribution()
	holder.Set(m)
	for _, tc := range []struct{ kind, stamp, want string }{
		{"llm", "", ""},
		{"llm", "a", "a"},
		{"mcp", "", "c"},
		{"mcp", "b", "b"},
	} {
		t.Run(tc.kind+"/"+tc.stamp, func(t *testing.T) {
			sink := &attributionCaptureSink{}
			span, ctx := usage.NewObserverWithAttribution(sink, holder).Begin(t.Context(), usage.InteractionDimensions{
				RouteID: "chat", RouteKind: tc.kind, AgentID: tc.stamp,
			})
			dims, _ := usage.DimensionsFromContext(ctx)
			if dims.AgentID != tc.want {
				t.Fatalf("context agent = %q, want %q", dims.AgentID, tc.want)
			}
			span.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
			if len(sink.events) != 1 {
				t.Fatalf("events = %d", len(sink.events))
			}
			var got string
			switch event := sink.events[0].(type) {
			case usage.LLMUsageEvent:
				got = event.AgentID
			case usage.MCPUsageEvent:
				got = event.AgentID
			default:
				t.Fatalf("unexpected event %T", event)
			}
			if got != tc.want {
				t.Fatalf("event agent = %q, want %q", got, tc.want)
			}
		})
	}
}
