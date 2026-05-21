package service_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/mcp/service"
)

// testConfigStore is a minimal in-memory ConfigStore for use in unit tests.
// It stores *service.MCPServiceConfig objects keyed by ID.
type testConfigStore struct {
	mu    sync.RWMutex
	items map[string]*service.MCPServiceConfig
}

func newTestConfigStore(t *testing.T) configstore.ConfigStore {
	t.Helper()
	return &testConfigStore{items: make(map[string]*service.MCPServiceConfig)}
}

func (s *testConfigStore) unwrap(obj any) (*service.MCPServiceConfig, error) {
	if u, ok := obj.(configstore.ObjectUnwrapper); ok {
		obj = u.ConfigStoreObject()
	}
	cfg, ok := obj.(*service.MCPServiceConfig)
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
