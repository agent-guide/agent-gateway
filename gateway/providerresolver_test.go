package gateway

import (
	"context"
	"sync"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testManagedProviderStore struct {
	mu       sync.Mutex
	items    map[string]*provider.ProviderConfig
	getCalls int
}

func (s *testManagedProviderStore) ListByName(_ context.Context, name string) ([]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if item.ProviderName != "" && item.ProviderName != name {
			continue
		}
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testManagedProviderStore) Create(_ context.Context, id string, name string, obj any) (string, error) {
	cfg, ok := obj.(*provider.ProviderConfig)
	if !ok {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items == nil {
		s.items = map[string]*provider.ProviderConfig{}
	}
	cloned := *cfg
	cloned.Id = id
	if cloned.ProviderName == "" {
		cloned.ProviderName = name
	}
	s.items[id] = &cloned
	return id, nil
}

func (s *testManagedProviderStore) Update(_ context.Context, id string, obj any) error {
	cfg, ok := obj.(*provider.ProviderConfig)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cloned := *cfg
	cloned.Id = id
	s.items[id] = &cloned
	return nil
}

func (s *testManagedProviderStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.items, id)
	return nil
}

func (s *testManagedProviderStore) Get(_ context.Context, id string) (string, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.getCalls++
	item := s.items[id]
	if item == nil {
		return "", nil, ErrRouteNotConfigured
	}
	cloned := *item
	tag := cloned.ProviderName
	cloned.ProviderName = ""
	return tag, &cloned, nil
}

var _ configstoreintf.ProviderConfigStorer = (*testManagedProviderStore)(nil)

type countingProvider struct {
	instance int
	cfg      provider.ProviderConfig
}

func (p *countingProvider) Generate(context.Context, *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	return nil, nil
}

func (p *countingProvider) Stream(context.Context, *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p *countingProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (p *countingProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (p *countingProvider) Config() provider.ProviderConfig {
	return p.cfg
}

var (
	registerCountingProviderOnce sync.Once
	countingProviderMu           sync.Mutex
	countingProviderNextID       int
)

func registerCountingProviderFactory() {
	registerCountingProviderOnce.Do(func() {
		provider.RegisterProvider("test-counting-provider", func(cfg provider.ProviderConfig) (provider.Provider, error) {
			countingProviderMu.Lock()
			defer countingProviderMu.Unlock()
			countingProviderNextID++
			return &countingProvider{instance: countingProviderNextID, cfg: cfg}, nil
		})
	})
}

func TestProviderManagerResolvePrefersStaticProvider(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider": {Id: "test-provider", ProviderName: "test-counting-provider", BaseURL: "https://dynamic.example"},
		},
	}
	manager := NewProviderManager(store)
	staticProvider := &countingProvider{instance: 999, cfg: provider.ProviderConfig{ProviderName: "static"}}
	manager.InitStaticProviders(map[string]provider.Provider{
		"test-provider": staticProvider,
	})

	got, name, err := manager.ResolveProvider(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("ResolveProvider returned error: %v", err)
	}
	if got != staticProvider {
		t.Fatalf("ResolveProvider returned %v, want static provider", got)
	}
	if name != "test-provider" {
		t.Fatalf("provider name = %q, want test-provider", name)
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0", store.getCalls)
	}
}

func TestProviderManagerResolveCachesDynamicProvider(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider": {Id: "test-provider", ProviderName: "test-counting-provider", BaseURL: "https://v1.example"},
		},
	}
	manager := NewProviderManager(store)

	first, name, err := manager.ResolveProvider(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("first ResolveProvider returned error: %v", err)
	}
	second, _, err := manager.ResolveProvider(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("second ResolveProvider returned error: %v", err)
	}

	if first != second {
		t.Fatalf("cached provider mismatch: first=%p second=%p", first, second)
	}
	if name != "test-counting-provider" {
		t.Fatalf("provider name = %q, want test-counting-provider", name)
	}
	if store.getCalls != 2 {
		t.Fatalf("store get calls = %d, want 2", store.getCalls)
	}
}

func TestProviderManagerResolveRefreshesProviderWhenConfigChanges(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider": {Id: "test-provider", ProviderName: "test-counting-provider", BaseURL: "https://v1.example"},
		},
	}
	manager := NewProviderManager(store)

	first, _, err := manager.ResolveProvider(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("first ResolveProvider returned error: %v", err)
	}

	store.mu.Lock()
	store.items["test-provider"] = &provider.ProviderConfig{Id: "test-provider", ProviderName: "test-counting-provider", BaseURL: "https://v2.example"}
	store.mu.Unlock()

	second, _, err := manager.ResolveProvider(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("second ResolveProvider returned error: %v", err)
	}
	if first == second {
		t.Fatal("provider instance was not refreshed after config change")
	}

	firstCfg := first.Config()
	secondCfg := second.Config()
	if firstCfg.BaseURL != "https://v1.example" {
		t.Fatalf("first provider base_url = %q, want https://v1.example", firstCfg.BaseURL)
	}
	if secondCfg.BaseURL != "https://v2.example" {
		t.Fatalf("second provider base_url = %q, want https://v2.example", secondCfg.BaseURL)
	}
}
