package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/runtimecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// ProviderResolver resolves a provider ID into an executable provider instance.
type ProviderResolver interface {
	ResolveProvider(ctx context.Context, providerID string) (provider.Provider, error)
}

// ProviderResolverFunc adapts a function into ProviderResolver.
type ProviderResolverFunc func(ctx context.Context, providerID string) (provider.Provider, error)

func (f ProviderResolverFunc) ResolveProvider(ctx context.Context, providerID string) (provider.Provider, error) {
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

func (o ProviderListOptions) listQuery() configmgr.ListQuery {
	return configmgr.ListQuery{Tag: o.ProviderType}
}

type ProviderManager struct {
	mu sync.RWMutex

	configs         *configmgr.BaseConfigManager[provider.ProviderConfig]
	staticProviders map[string]provider.Provider
	resolver        *runtimecore.Resolver[provider.ProviderConfig, provider.Provider, ProviderListOptions]

	store configstore.ConfigStore
}

func NewProviderManager(store configstore.ConfigStore) *ProviderManager {
	manager := &ProviderManager{
		configs: configmgr.NewBaseConfigManager(store, configmgr.Definition[provider.ProviderConfig]{
			GetID:  providerConfigID,
			Decode: decodeProviderConfigManagerItem,
			Clone:  cloneProviderConfig,
			PrepareCreate: func(cfg provider.ProviderConfig) (any, provider.ProviderConfig, error) {
				if cfg.Id == "" {
					return nil, provider.ProviderConfig{}, fmt.Errorf("provider id is required")
				}
				if cfg.ProviderType == "" {
					return nil, provider.ProviderConfig{}, fmt.Errorf("provider_type is required")
				}
				cfg = provider.NormalizeConfig(cfg, cfg.Id, "")
				return &cfg, cfg, nil
			},
			PrepareUpdate: func(providerID string, _ provider.ProviderConfig, cfg provider.ProviderConfig) (any, provider.ProviderConfig, error) {
				if cfg.ProviderType == "" {
					return nil, provider.ProviderConfig{}, fmt.Errorf("provider_type is required")
				}
				cfg.Id = providerID
				cfg = provider.NormalizeConfig(cfg, providerID, "")
				return &cfg, cfg, nil
			},
			MatchesListQuery: func(cfg provider.ProviderConfig, query configmgr.ListQuery) bool {
				return query.Tag == "" || cfg.ProviderType == query.Tag
			},
			NotConfiguredErr: func(id string) error {
				return fmt.Errorf("%w: %q", ErrProviderNotConfigured, id)
			},
			ReadOnlyErr: func(id string) error {
				return fmt.Errorf("%w: %q", ErrStaticProviderReadOnly, id)
			},
			StoreNilErr: func() error {
				return fmt.Errorf("provider store is not configured")
			},
		}),
		staticProviders: map[string]provider.Provider{},
		store:           store,
	}
	manager.resolver = newDynamicProviderRuntimeResolver(manager)
	return manager
}

func (m *ProviderManager) Reset() {
	m.configs.Reset()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticProviders = map[string]provider.Provider{}
	m.resolver = newDynamicProviderRuntimeResolver(m)
}

func (m *ProviderManager) InitStaticProviders(providers map[string]provider.Provider) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticProviders = make(map[string]provider.Provider, len(providers))
	staticConfigs := make([]provider.ProviderConfig, 0, len(providers))
	for name, prov := range providers {
		if name == "" || prov == nil {
			continue
		}
		cfg := prov.Config()
		if cfg.Id == "" {
			return fmt.Errorf("static provider %q: provider id is required", name)
		}
		if cfg.Id != name {
			return fmt.Errorf("static provider %q: provider config id %q must match registration id", name, cfg.Id)
		}
		staticConfigs = append(staticConfigs, provider.NormalizeConfig(cfg, name, ""))
		m.staticProviders[name] = prov
	}
	m.configs.InitStatic(staticConfigs)
	return nil
}

func (m *ProviderManager) IsStatic(providerID string) bool {
	return m.configs.IsStatic(providerID)
}

func (m *ProviderManager) GetConfig(ctx context.Context, providerID string) (provider.ProviderConfig, error) {
	if providerID == "" {
		return provider.ProviderConfig{}, fmt.Errorf("provider id is required")
	}

	cfg, err := m.configs.Get(ctx, providerID)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return provider.ProviderConfig{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, providerID)
		}
		return provider.ProviderConfig{}, fmt.Errorf("load provider %q: %w", providerID, err)
	}
	return cfg, nil
}

func (m *ProviderManager) ListConfigs(ctx context.Context, opts ProviderListOptions) ([]provider.ProviderConfig, error) {
	return m.configs.List(ctx, opts.listQuery())
}

func (m *ProviderManager) CreateConfig(ctx context.Context, cfg provider.ProviderConfig) error {
	if err := m.configs.Create(ctx, cfg); err != nil {
		return err
	}

	m.deleteDynamicCache(cfg.Id)
	return nil
}

func (m *ProviderManager) UpdateConfig(ctx context.Context, providerID string, cfg provider.ProviderConfig) error {
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	if err := m.configs.Update(ctx, providerID, cfg); err != nil {
		return err
	}

	m.deleteDynamicCache(providerID)
	return nil
}

func (m *ProviderManager) DeleteConfig(ctx context.Context, providerID string) error {
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	if err := m.configs.Delete(ctx, providerID); err != nil {
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
// This is intentionally different from the route config manager and virtual key manager, which skip the
// store on cache hit, because provider config changes (API keys, base URLs) must take
// effect without a gateway restart.
func (m *ProviderManager) ResolveProvider(ctx context.Context, providerID string) (provider.Provider, error) {
	if providerID == "" {
		return nil, fmt.Errorf("provider id is required")
	}

	m.mu.RLock()
	staticProvider, ok := m.staticProviders[providerID]
	m.mu.RUnlock()
	if ok {
		cfg := provider.NormalizeConfig(staticProvider.Config(), providerID, "")
		if cfg.Disabled {
			return nil, fmt.Errorf("%w: %q", ErrProviderDisabled, providerID)
		}
		return staticProvider, nil
	}

	m.mu.RLock()
	resolver := m.resolver
	m.mu.RUnlock()
	if resolver == nil {
		return nil, fmt.Errorf("provider runtime resolver is not configured")
	}

	return resolver.Get(ctx, providerID)
}

func newDynamicProviderRuntimeResolver(manager *ProviderManager) *runtimecore.Resolver[provider.ProviderConfig, provider.Provider, ProviderListOptions] {
	return runtimecore.NewResolver(
		runtimecore.FuncSource[provider.ProviderConfig, ProviderListOptions]{
			GetFunc: func(ctx context.Context, providerID string) (provider.ProviderConfig, error) {
				if manager == nil || manager.configs == nil {
					return provider.ProviderConfig{}, fmt.Errorf("provider manager is not configured")
				}
				cfg, err := manager.configs.GetFresh(ctx, providerID)
				if err != nil {
					if errors.Is(err, configstore.ErrNotFound) {
						return provider.ProviderConfig{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, providerID)
					}
					return provider.ProviderConfig{}, fmt.Errorf("load provider %q: %w", providerID, err)
				}
				return cfg, nil
			},
			ListFunc: func(ctx context.Context, opts ProviderListOptions) ([]provider.ProviderConfig, error) {
				if manager == nil || manager.configs == nil {
					return nil, fmt.Errorf("provider manager is not configured")
				}
				return manager.configs.List(ctx, opts.listQuery())
			},
		},
		func(cfg provider.ProviderConfig) string {
			return cfg.Id
		},
		func(cfg provider.ProviderConfig) (string, error) {
			return runtimecore.FingerprintJSON(cfg.Id, "provider config", cfg)
		},
		func(cfg provider.ProviderConfig) (provider.Provider, error) {
			return buildProvider(cfg)
		},
	)
}

func (m *ProviderManager) deleteDynamicCache(providerID string) {
	m.mu.RLock()
	resolver := m.resolver
	m.mu.RUnlock()
	if resolver != nil {
		resolver.Invalidate(providerID)
	}
}

func buildProvider(cfg provider.ProviderConfig) (provider.Provider, error) {
	if cfg.Disabled {
		return nil, fmt.Errorf("%w: %q", ErrProviderDisabled, cfg.Id)
	}
	return provider.NewProvider(cfg)
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

func decodeProviderConfigManagerItem(providerID string, item any) (provider.ProviderConfig, error) {
	return decodeProviderConfigItem(providerID, "", item)
}

func cloneProviderConfig(cfg provider.ProviderConfig) provider.ProviderConfig {
	if len(cfg.Options) > 0 {
		cloned := make(map[string]any, len(cfg.Options))
		for k, v := range cfg.Options {
			cloned[k] = v
		}
		cfg.Options = cloned
	}
	return cfg
}

func providerConfigID(cfg provider.ProviderConfig) string {
	return cfg.Id
}
