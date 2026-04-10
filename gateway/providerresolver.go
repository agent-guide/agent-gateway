package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
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
		return nil, "", fmt.Errorf("provider %q is not configured", ref)
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
		return cachedProviderEntry{}, err
	}

	fingerprint, err := fingerprintProviderConfig(ref, obj)
	if err != nil {
		return cachedProviderEntry{}, err
	}

	resolvedCfg, err := provider.NormalizeStoredProviderConfig(tag, obj)
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

func fingerprintProviderConfig(ref string, obj any) (string, error) {
	cfgJSON, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("fingerprint provider config %q: %w", ref, err)
	}
	return string(cfgJSON), nil
}
