package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"gorm.io/gorm"
)

// ProviderResolver resolves a provider reference into an executable provider instance.
type ProviderResolver interface {
	ResolveProvider(ctx context.Context, ref string) (provider.Provider, string, error)
}

// ProviderResolverFunc adapts a function into ProviderResolver.
type ProviderResolverFunc func(ctx context.Context, ref string) (provider.Provider, string, error)

func (f ProviderResolverFunc) ResolveProvider(ctx context.Context, ref string) (provider.Provider, string, error) {
	return f(ctx, ref)
}

var (
	ErrProviderNotConfigured  = errors.New("provider is not configured")
	ErrStaticProviderReadOnly = errors.New("static provider is read-only")
)

type ProviderListOptions struct {
	ProviderName string
}

type ProviderManager struct {
	mu sync.RWMutex

	staticProviders map[string]provider.Provider
	dynamicCache    map[string]cachedProviderEntry

	store configstoreintf.ProviderConfigStorer
}

func NewProviderManager(store configstoreintf.ProviderConfigStorer) *ProviderManager {
	return &ProviderManager{
		staticProviders: map[string]provider.Provider{},
		dynamicCache:    map[string]cachedProviderEntry{},
		store:           store,
	}
}

func (m *ProviderManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticProviders = map[string]provider.Provider{}
	m.dynamicCache = map[string]cachedProviderEntry{}
}

func (m *ProviderManager) InitStaticProviders(providers map[string]provider.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticProviders = make(map[string]provider.Provider, len(providers))
	for name, prov := range providers {
		if name == "" || prov == nil {
			continue
		}
		m.staticProviders[name] = prov
	}
}

func (m *ProviderManager) IsStatic(ref string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticProviders[ref]
	return ok
}

func (m *ProviderManager) IsConfigured() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store != nil || len(m.staticProviders) > 0
}

func (m *ProviderManager) GetConfig(ctx context.Context, ref string) (provider.ProviderConfig, error) {
	if ref == "" {
		return provider.ProviderConfig{}, fmt.Errorf("provider ref is required")
	}

	m.mu.RLock()
	staticProvider, ok := m.staticProviders[ref]
	m.mu.RUnlock()
	if ok {
		return provider.NormalizeConfig(staticProvider.Config(), ref, ""), nil
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return provider.ProviderConfig{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, ref)
	}

	tag, obj, err := store.Get(ctx, ref)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return provider.ProviderConfig{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, ref)
		}
		return provider.ProviderConfig{}, fmt.Errorf("load provider %q: %w", ref, err)
	}

	cfg, err := decodeProviderConfigItem(ref, tag, obj)
	if err != nil {
		return provider.ProviderConfig{}, err
	}
	return cfg, nil
}

func (m *ProviderManager) ListConfigs(ctx context.Context, opts ProviderListOptions) ([]provider.ProviderConfig, error) {
	m.mu.RLock()
	store := m.store
	staticProviders := make(map[string]provider.Provider, len(m.staticProviders))
	for ref, prov := range m.staticProviders {
		staticProviders[ref] = prov
	}
	m.mu.RUnlock()

	out := make(map[string]provider.ProviderConfig, len(staticProviders))
	for ref, prov := range staticProviders {
		cfg := provider.NormalizeConfig(prov.Config(), ref, "")
		if opts.ProviderName != "" && cfg.ProviderName != opts.ProviderName {
			continue
		}
		out[ref] = cfg
	}

	if store == nil {
		return mapProviderConfigs(out), nil
	}

	items, err := store.ListByName(ctx, opts.ProviderName)
	if err != nil {
		return nil, err
	}

	for _, item := range items {
		cfg, err := decodeProviderConfigItem("", "", item)
		if err != nil {
			return nil, err
		}
		if _, ok := out[cfg.Id]; !ok {
			out[cfg.Id] = cfg
		}
	}
	return mapProviderConfigs(out), nil
}

func (m *ProviderManager) CreateConfig(ctx context.Context, cfg provider.ProviderConfig) error {
	if cfg.Id == "" {
		return fmt.Errorf("provider id is required")
	}
	if cfg.ProviderName == "" {
		return fmt.Errorf("provider_name is required")
	}
	if err := m.ensureWritable(cfg.Id); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("provider store is not configured")
	}
	if _, err := store.Create(ctx, cfg.Id, cfg.ProviderName, &cfg); err != nil {
		return err
	}

	m.deleteDynamicCache(cfg.Id)
	return nil
}

func (m *ProviderManager) UpdateConfig(ctx context.Context, ref string, cfg provider.ProviderConfig) error {
	if ref == "" {
		return fmt.Errorf("provider ref is required")
	}
	if cfg.ProviderName == "" {
		return fmt.Errorf("provider_name is required")
	}
	if err := m.ensureWritable(ref); err != nil {
		return err
	}

	cfg.Id = ref

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("provider store is not configured")
	}
	if err := store.Update(ctx, ref, &cfg); err != nil {
		return err
	}

	m.deleteDynamicCache(ref)
	return nil
}

func (m *ProviderManager) DeleteConfig(ctx context.Context, ref string) error {
	if ref == "" {
		return fmt.Errorf("provider ref is required")
	}
	if err := m.ensureWritable(ref); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("provider store is not configured")
	}
	if err := store.Delete(ctx, ref); err != nil {
		return err
	}

	m.deleteDynamicCache(ref)
	return nil
}

func (m *ProviderManager) ResolveProvider(ctx context.Context, ref string) (provider.Provider, string, error) {
	if ref == "" {
		return nil, "", fmt.Errorf("provider ref is required")
	}

	m.mu.RLock()
	staticProvider, ok := m.staticProviders[ref]
	m.mu.RUnlock()
	if ok {
		return staticProvider, ref, nil
	}

	m.mu.RLock()
	cached, ok := m.dynamicCache[ref]
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return nil, "", fmt.Errorf("%w: %q", ErrProviderNotConfigured, ref)
	}

	entry, err := m.loadDynamicProvider(ctx, ref, store)
	if err != nil {
		return nil, "", err
	}
	if ok && cached.cfgJSON == entry.cfgJSON {
		return cached.provider, cached.name, nil
	}

	m.cacheDynamicProvider(ref, entry)
	return entry.provider, entry.name, nil
}

// cachedProviderEntry holds a cached provider instance and the config fingerprint
// used to detect config changes.
type cachedProviderEntry struct {
	cfgJSON  string
	provider provider.Provider
	name     string
}

func (m *ProviderManager) cacheDynamicProvider(ref string, entry cachedProviderEntry) {
	if ref == "" || entry.provider == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]cachedProviderEntry{}
	}
	m.dynamicCache[ref] = entry
}

func (m *ProviderManager) loadDynamicProvider(ctx context.Context, ref string, store configstoreintf.ProviderConfigStorer) (cachedProviderEntry, error) {
	tag, obj, err := store.Get(ctx, ref)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cachedProviderEntry{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, ref)
		}
		return cachedProviderEntry{}, err
	}

	fingerprint, err := fingerprintProviderConfig(ref, obj)
	if err != nil {
		return cachedProviderEntry{}, err
	}

	resolvedCfg, err := decodeProviderConfigItem(ref, tag, obj)
	if err != nil {
		return cachedProviderEntry{}, fmt.Errorf("normalize provider config %q: %w", ref, err)
	}

	prov, err := provider.NewProvider(resolvedCfg)
	if err != nil {
		return cachedProviderEntry{}, err
	}

	return cachedProviderEntry{
		cfgJSON:  fingerprint,
		provider: prov,
		name:     resolvedCfg.ProviderName,
	}, nil
}

func (m *ProviderManager) ensureWritable(ref string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticProviders[ref]; ok {
		return fmt.Errorf("%w: %q", ErrStaticProviderReadOnly, ref)
	}
	return nil
}

func (m *ProviderManager) deleteDynamicCache(ref string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dynamicCache, ref)
}

func fingerprintProviderConfig(ref string, obj any) (string, error) {
	cfgJSON, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("fingerprint provider config %q: %w", ref, err)
	}
	return string(cfgJSON), nil
}

func decodeProviderConfigItem(ref string, fallbackName string, item any) (provider.ProviderConfig, error) {
	normalized, err := provider.NormalizeStoredProviderConfig(ref, fallbackName, item)
	if err != nil {
		return provider.ProviderConfig{}, nil
	}
	if normalized.Id == "" {
		return provider.ProviderConfig{}, fmt.Errorf("provider %q id is required", ref)
	}
	return normalized, nil
}

func mapProviderConfigs(configs map[string]provider.ProviderConfig) []provider.ProviderConfig {
	out := make([]provider.ProviderConfig, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, cfg)
	}
	return out
}
