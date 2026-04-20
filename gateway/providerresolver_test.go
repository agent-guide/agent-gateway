package gateway

import (
	"context"
	"errors"
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

func (s *testManagedProviderStore) ListByType(_ context.Context, name string) ([]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if name != "" && item.ProviderType != name {
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
	if cloned.ProviderType == "" {
		cloned.ProviderType = name
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
	if _, ok := s.items[id]; !ok {
		return configstoreintf.ErrNotFound
	}
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
		return "", nil, configstoreintf.ErrNotFound
	}
	cloned := *item
	tag := cloned.ProviderType
	cloned.ProviderType = ""
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
		provider.RegisterProviderFactory("test-counting-provider", func(cfg provider.ProviderConfig) (provider.Provider, error) {
			countingProviderMu.Lock()
			defer countingProviderMu.Unlock()
			countingProviderNextID++
			return &countingProvider{instance: countingProviderNextID, cfg: cfg}, nil
		})
	})
}

func currentCountingProviderNextID() int {
	countingProviderMu.Lock()
	defer countingProviderMu.Unlock()
	return countingProviderNextID
}

func TestProviderManagerResolvePrefersStaticProvider(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider": {Id: "test-provider", ProviderType: "test-counting-provider", BaseURL: "https://dynamic.example"},
		},
	}
	manager := NewProviderManager(store)
	staticProvider := &countingProvider{instance: 999, cfg: provider.ProviderConfig{ProviderType: "static"}}
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
		t.Fatalf("provider id = %q, want test-provider", name)
	}
	if store.getCalls != 0 {
		t.Fatalf("store get calls = %d, want 0", store.getCalls)
	}
}

func TestProviderManagerResolveCachesDynamicProvider(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider": {Id: "test-provider", ProviderType: "test-counting-provider", BaseURL: "https://v1.example"},
		},
	}
	manager := NewProviderManager(store)
	before := currentCountingProviderNextID()

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
	if name != "test-provider" {
		t.Fatalf("provider id = %q, want test-provider", name)
	}
	if store.getCalls != 2 {
		t.Fatalf("store get calls = %d, want 2", store.getCalls)
	}
	if got := currentCountingProviderNextID() - before; got != 1 {
		t.Fatalf("provider factory calls = %d, want 1", got)
	}
}

func TestProviderManagerResolveRefreshesProviderWhenConfigChanges(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider": {Id: "test-provider", ProviderType: "test-counting-provider", BaseURL: "https://v1.example"},
		},
	}
	manager := NewProviderManager(store)

	first, _, err := manager.ResolveProvider(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("first ResolveProvider returned error: %v", err)
	}

	store.mu.Lock()
	store.items["test-provider"] = &provider.ProviderConfig{Id: "test-provider", ProviderType: "test-counting-provider", BaseURL: "https://v2.example"}
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

func TestProviderManagerGetAndListConfigPreferStaticProvider(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider":  {Id: "test-provider", ProviderType: "test-counting-provider", BaseURL: "https://dynamic.example"},
			"other-provider": {Id: "other-provider", ProviderType: "test-counting-provider", BaseURL: "https://other.example"},
		},
	}
	manager := NewProviderManager(store)
	manager.InitStaticProviders(map[string]provider.Provider{
		"test-provider": &countingProvider{
			instance: 999,
			cfg:      provider.ProviderConfig{Id: "test-provider", ProviderType: "test-counting-provider", BaseURL: "https://static.example"},
		},
	})

	got, err := manager.GetConfig(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("GetConfig returned error: %v", err)
	}
	if got.BaseURL != "https://static.example" {
		t.Fatalf("BaseURL = %q, want https://static.example", got.BaseURL)
	}

	items, err := manager.ListConfigs(context.Background(), ProviderListOptions{})
	if err != nil {
		t.Fatalf("ListConfigs returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("item count = %d, want 2", len(items))
	}
}

func TestProviderManagerResolveRejectsDisabledProvider(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{
		items: map[string]*provider.ProviderConfig{
			"test-provider": {
				Id:           "test-provider",
				ProviderType: "test-counting-provider",
				Disabled:     true,
			},
		},
	}
	manager := NewProviderManager(store)

	_, _, err := manager.ResolveProvider(context.Background(), "test-provider")
	if !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("ResolveProvider error = %v, want %v", err, ErrProviderDisabled)
	}
}

func TestProviderManagerCreateUpdateDeleteManageCache(t *testing.T) {
	registerCountingProviderFactory()

	store := &testManagedProviderStore{items: map[string]*provider.ProviderConfig{}}
	manager := NewProviderManager(store)

	if err := manager.CreateConfig(context.Background(), provider.ProviderConfig{
		Id:           "test-provider",
		ProviderType: "test-counting-provider",
		BaseURL:      "https://created.example",
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, _, err := manager.ResolveProvider(context.Background(), "test-provider"); err != nil {
		t.Fatalf("ResolveProvider returned error: %v", err)
	}

	store.mu.Lock()
	store.items["test-provider"] = &provider.ProviderConfig{
		Id:           "test-provider",
		ProviderType: "test-counting-provider",
		BaseURL:      "https://updated.example",
	}
	store.mu.Unlock()

	if err := manager.UpdateConfig(context.Background(), "test-provider", provider.ProviderConfig{
		ProviderType: "test-counting-provider",
		BaseURL:      "https://update-call.example",
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	cfg, err := manager.GetConfig(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("GetConfig returned error: %v", err)
	}
	if cfg.BaseURL != "https://update-call.example" {
		t.Fatalf("BaseURL = %q, want https://update-call.example", cfg.BaseURL)
	}

	if err := manager.DeleteConfig(context.Background(), "test-provider"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := manager.GetConfig(context.Background(), "test-provider"); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("GetConfig after delete error = %v, want ErrProviderNotConfigured", err)
	}
}

func TestProviderManagerRejectsStaticProviderMutation(t *testing.T) {
	registerCountingProviderFactory()

	manager := NewProviderManager(&testManagedProviderStore{items: map[string]*provider.ProviderConfig{}})
	manager.InitStaticProviders(map[string]provider.Provider{
		"test-provider": &countingProvider{
			instance: 999,
			cfg:      provider.ProviderConfig{Id: "test-provider", ProviderType: "test-counting-provider"},
		},
	})

	if err := manager.UpdateConfig(context.Background(), "test-provider", provider.ProviderConfig{ProviderType: "test-counting-provider"}); !errors.Is(err, ErrStaticProviderReadOnly) {
		t.Fatalf("Update error = %v, want ErrStaticProviderReadOnly", err)
	}
	if err := manager.DeleteConfig(context.Background(), "test-provider"); !errors.Is(err, ErrStaticProviderReadOnly) {
		t.Fatalf("Delete error = %v, want ErrStaticProviderReadOnly", err)
	}
}
