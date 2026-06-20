package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

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

func acpAgent(id, serviceID string, acpRouteIDs ...string) Agent {
	return Agent{
		ID:      id,
		Name:    id,
		Runtime: Runtime{Type: RuntimeTypeACP, ACP: &ACPRuntime{ServiceID: serviceID}},
		Routes:  Routes{ACPRouteIDs: acpRouteIDs},
	}
}

func TestCreateRejectsDuplicateServiceBinding(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()
	if err := m.Create(ctx, acpAgent("a1", "codex-main")); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	err := m.Create(ctx, acpAgent("a2", "codex-main"))
	if err == nil {
		t.Fatalf("expected duplicate service binding to be rejected")
	}
}

func TestCreateRejectsDuplicateRouteBinding(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()
	if err := m.Create(ctx, acpAgent("a1", "codex-main", "route-1")); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	a2 := acpAgent("a2", "codex-other")
	a2.Routes.LLMRouteIDs = []string{"route-1"}
	err := m.Create(ctx, a2)
	if err == nil {
		t.Fatalf("expected duplicate route binding to be rejected")
	}
}

func TestCreateIgnoresClientOwnedServiceProvenance(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()
	a := acpAgent("a1", "codex-main")
	a.OwnsService = true
	if err := m.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.Get(ctx, "a1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OwnsService {
		t.Fatalf("OwnsService = true, want false because no auto-create path set provenance")
	}
}

func TestCreateConcurrentServiceBindingIsExclusive(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	errsCh := make(chan error, n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			// Distinct agent ids, identical service id: only one may win the P0
			// one-runtime-one-agent race.
			errsCh <- m.Create(ctx, acpAgent(fmt.Sprintf("a%d", i), "codex-main"))
		}(i)
	}
	wg.Wait()
	close(errsCh)

	success := 0
	for err := range errsCh {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one concurrent create to bind the service, got %d", success)
	}

	agents, err := m.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	bound := 0
	for _, a := range agents {
		if a.ACPServiceID() == "codex-main" {
			bound++
		}
	}
	if bound != 1 {
		t.Fatalf("expected exactly one agent bound to the service, got %d", bound)
	}
}

func TestRefreshDropsAmbiguousRouteMapping(t *testing.T) {
	store := newTestConfigStore()
	m := NewManager(store)
	ctx := context.Background()
	if err := store.Create(ctx, &Agent{ID: "a1", Name: "a1", Runtime: Runtime{Type: RuntimeTypeACP, ACP: &ACPRuntime{ServiceID: "svc-1"}}, Routes: Routes{LLMRouteIDs: []string{"shared-route"}}}); err != nil {
		t.Fatalf("seed a1: %v", err)
	}
	if err := store.Create(ctx, &Agent{ID: "a2", Name: "a2", Runtime: Runtime{Type: RuntimeTypeACP, ACP: &ACPRuntime{ServiceID: "svc-2"}}, Routes: Routes{MCPRouteIDs: []string{"shared-route"}}}); err != nil {
		t.Fatalf("seed a2: %v", err)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if id, ok := m.ResolveAgentID("shared-route", "", ""); ok {
		t.Fatalf("ambiguous route mapping resolved to %q, want ok=false", id)
	}
}

func TestUpdateKeepsOwnServiceBinding(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()
	if err := m.Create(ctx, acpAgent("a1", "codex-main")); err != nil {
		t.Fatalf("create: %v", err)
	}
	upd := acpAgent("a1", "codex-main")
	upd.Description = "updated"
	if err := m.Update(ctx, "a1", upd); err != nil {
		t.Fatalf("update with same service must succeed: %v", err)
	}
}

func TestValidateRuntime(t *testing.T) {
	bad := Agent{ID: "x", Name: "x", Runtime: Runtime{Type: RuntimeTypeACP}}
	if err := bad.Validate(); err == nil {
		t.Fatalf("acp runtime without service_id must fail")
	}
	httpOK := Agent{ID: "x", Name: "x", Runtime: Runtime{Type: RuntimeTypeHTTP, HTTP: &HTTPRuntime{Endpoint: "https://x"}}}
	if err := httpOK.Validate(); err != nil {
		t.Fatalf("valid http agent rejected: %v", err)
	}
}

func TestResolveAgentIDIndex(t *testing.T) {
	m := NewManager(newTestConfigStore())
	ctx := context.Background()
	if err := m.Create(ctx, acpAgent("a1", "codex-main", "route-1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if id, ok := m.ResolveAgentID("route-1", "", ""); !ok || id != "a1" {
		t.Fatalf("route mapping = (%q,%v), want a1,true", id, ok)
	}
	if id, ok := m.ResolveAgentID("", "codex-main", ""); !ok || id != "a1" {
		t.Fatalf("service mapping = (%q,%v), want a1,true", id, ok)
	}
	if _, ok := m.ResolveAgentID("unknown", "unknown", ""); ok {
		t.Fatalf("unknown mapping must be ok=false")
	}
	if err := m.Delete(ctx, "a1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := m.ResolveAgentID("route-1", "codex-main", ""); ok {
		t.Fatalf("mapping must be cleared after delete")
	}
}
