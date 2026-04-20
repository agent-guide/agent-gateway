package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

// ProviderResolver resolves a provider ID into an executable provider instance.
type ProviderResolver interface {
	ResolveProvider(ctx context.Context, providerID string) (provider.Provider, string, error)
}

// ProviderResolverFunc adapts a function into ProviderResolver.
type ProviderResolverFunc func(ctx context.Context, providerID string) (provider.Provider, string, error)

func (f ProviderResolverFunc) ResolveProvider(ctx context.Context, providerID string) (provider.Provider, string, error) {
	return f(ctx, providerID)
}

var (
	ErrProviderNotConfigured  = errors.New("provider is not configured")
	ErrProviderDisabled       = errors.New("provider is disabled")
	ErrStaticProviderReadOnly = errors.New("static provider is read-only")
)

type ProviderListOptions struct {
	ProviderType string
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

func (m *ProviderManager) IsStatic(providerID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticProviders[providerID]
	return ok
}

func (m *ProviderManager) IsConfigured() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.store != nil || len(m.staticProviders) > 0
}

func (m *ProviderManager) GetConfig(ctx context.Context, providerID string) (provider.ProviderConfig, error) {
	if providerID == "" {
		return provider.ProviderConfig{}, fmt.Errorf("provider id is required")
	}

	m.mu.RLock()
	staticProvider, ok := m.staticProviders[providerID]
	m.mu.RUnlock()
	if ok {
		return provider.NormalizeConfig(staticProvider.Config(), providerID, ""), nil
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return provider.ProviderConfig{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, providerID)
	}

	tag, obj, err := store.Get(ctx, providerID)
	if err != nil {
		if errors.Is(err, configstoreintf.ErrNotFound) {
			return provider.ProviderConfig{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, providerID)
		}
		return provider.ProviderConfig{}, fmt.Errorf("load provider %q: %w", providerID, err)
	}

	cfg, err := decodeProviderConfigItem(providerID, tag, obj)
	if err != nil {
		return provider.ProviderConfig{}, err
	}
	return cfg, nil
}

func (m *ProviderManager) ListConfigs(ctx context.Context, opts ProviderListOptions) ([]provider.ProviderConfig, error) {
	m.mu.RLock()
	store := m.store
	staticProviders := make(map[string]provider.Provider, len(m.staticProviders))
	for providerID, prov := range m.staticProviders {
		staticProviders[providerID] = prov
	}
	m.mu.RUnlock()

	out := make(map[string]provider.ProviderConfig, len(staticProviders))
	for providerID, prov := range staticProviders {
		cfg := provider.NormalizeConfig(prov.Config(), providerID, "")
		if opts.ProviderType != "" && cfg.ProviderType != opts.ProviderType {
			continue
		}
		out[providerID] = cfg
	}

	if store == nil {
		return mapProviderConfigs(out), nil
	}

	items, err := store.ListByType(ctx, opts.ProviderType)
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
	if cfg.ProviderType == "" {
		return fmt.Errorf("provider_type is required")
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
	if _, err := store.Create(ctx, cfg.Id, cfg.ProviderType, &cfg); err != nil {
		return err
	}

	m.deleteDynamicCache(cfg.Id)
	return nil
}

func (m *ProviderManager) UpdateConfig(ctx context.Context, providerID string, cfg provider.ProviderConfig) error {
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	if cfg.ProviderType == "" {
		return fmt.Errorf("provider_type is required")
	}
	if err := m.ensureWritable(providerID); err != nil {
		return err
	}

	cfg.Id = providerID

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("provider store is not configured")
	}
	if err := store.Update(ctx, providerID, &cfg); err != nil {
		return err
	}

	m.deleteDynamicCache(providerID)
	return nil
}

func (m *ProviderManager) DeleteConfig(ctx context.Context, providerID string) error {
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	if err := m.ensureWritable(providerID); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("provider store is not configured")
	}
	if err := store.Delete(ctx, providerID); err != nil {
		return err
	}

	m.deleteDynamicCache(providerID)
	return nil
}

// ResolveProvider resolves a provider ID to a live provider instance.
//
// For dynamic (store-backed) providers, the store is consulted on every call to detect
// config changes at runtime. If the config fingerprint has not changed since the last
// call, the cached provider instance is reused to avoid re-establishing connections.
// This is intentionally different from AgentRouteManager/VirtualKeyManager, which skip the
// store on cache hit, because provider config changes (API keys, base URLs) must take
// effect without a gateway restart.
func (m *ProviderManager) ResolveProvider(ctx context.Context, providerID string) (provider.Provider, string, error) {
	if providerID == "" {
		return nil, "", fmt.Errorf("provider id is required")
	}

	m.mu.RLock()
	staticProvider, ok := m.staticProviders[providerID]
	m.mu.RUnlock()
	if ok {
		cfg := provider.NormalizeConfig(staticProvider.Config(), providerID, "")
		if cfg.Disabled {
			return nil, "", fmt.Errorf("%w: %q", ErrProviderDisabled, providerID)
		}
		return staticProvider, providerID, nil
	}

	m.mu.RLock()
	cached, ok := m.dynamicCache[providerID]
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return nil, "", fmt.Errorf("%w: %q", ErrProviderNotConfigured, providerID)
	}

	item, err := m.loadDynamicProviderConfig(ctx, providerID, store)
	if err != nil {
		return nil, "", err
	}
	if ok && cached.cfgJSON == item.fingerprint {
		return cached.provider, cached.name, nil
	}

	entry, err := m.buildDynamicProvider(providerID, item)
	if err != nil {
		return nil, "", err
	}
	m.cacheDynamicProvider(providerID, entry)
	return entry.provider, entry.name, nil
}

// cachedProviderEntry holds a cached provider instance and the config fingerprint
// used to detect config changes.
type cachedProviderEntry struct {
	cfgJSON  string
	provider provider.Provider
	name     string
}

type dynamicProviderConfigItem struct {
	tag         string
	obj         any
	fingerprint string
}

func (m *ProviderManager) cacheDynamicProvider(providerID string, entry cachedProviderEntry) {
	if providerID == "" || entry.provider == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]cachedProviderEntry{}
	}
	m.dynamicCache[providerID] = entry
}

func (m *ProviderManager) loadDynamicProviderConfig(ctx context.Context, providerID string, store configstoreintf.ProviderConfigStorer) (dynamicProviderConfigItem, error) {
	tag, obj, err := store.Get(ctx, providerID)
	if err != nil {
		if errors.Is(err, configstoreintf.ErrNotFound) {
			return dynamicProviderConfigItem{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, providerID)
		}
		return dynamicProviderConfigItem{}, err
	}

	fingerprint, err := fingerprintProviderConfig(providerID, obj)
	if err != nil {
		return dynamicProviderConfigItem{}, err
	}

	return dynamicProviderConfigItem{
		tag:         tag,
		obj:         obj,
		fingerprint: fingerprint,
	}, nil
}

func (m *ProviderManager) buildDynamicProvider(providerID string, item dynamicProviderConfigItem) (cachedProviderEntry, error) {
	resolvedCfg, err := decodeProviderConfigItem(providerID, item.tag, item.obj)
	if err != nil {
		return cachedProviderEntry{}, fmt.Errorf("normalize provider config %q: %w", providerID, err)
	}
	if resolvedCfg.Disabled {
		return cachedProviderEntry{}, fmt.Errorf("%w: %q", ErrProviderDisabled, providerID)
	}

	prov, err := provider.NewProvider(resolvedCfg)
	if err != nil {
		return cachedProviderEntry{}, err
	}

	return cachedProviderEntry{
		cfgJSON:  item.fingerprint,
		provider: prov,
		name:     providerID,
	}, nil
}

func (m *ProviderManager) ensureWritable(providerID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticProviders[providerID]; ok {
		return fmt.Errorf("%w: %q", ErrStaticProviderReadOnly, providerID)
	}
	return nil
}

func (m *ProviderManager) deleteDynamicCache(providerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dynamicCache, providerID)
}

func fingerprintProviderConfig(providerID string, obj any) (string, error) {
	cfgJSON, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("fingerprint provider config %q: %w", providerID, err)
	}
	return string(cfgJSON), nil
}

func decodeProviderConfigItem(providerID string, fallbackName string, item any) (provider.ProviderConfig, error) {
	normalized, err := provider.NormalizeStoredProviderConfig(providerID, fallbackName, item)
	if err != nil {
		return provider.ProviderConfig{}, err
	}
	if normalized.Id == "" {
		return provider.ProviderConfig{}, fmt.Errorf("provider %q id is required", providerID)
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
