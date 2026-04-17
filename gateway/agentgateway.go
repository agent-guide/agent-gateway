package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

type BootstrapOptions struct {
	StaticRoutes       []routepkg.AgentRoute
	StaticLocalAPIKeys []localapikeypkg.LocalAPIKey
	StaticProviders    map[string]provider.Provider
	ConfigStore        configstoreintf.ConfigStorer
	CLIAuthManager     *cliauth.Manager
	CredentialManager  *credentialmgr.Manager
	Selector           routepkg.RouteTargetSelector
}

type AgentGateway struct {
	mu sync.RWMutex

	configured         bool
	configStore        configstoreintf.ConfigStorer
	routeManager       *routepkg.AgentRouteManager
	localAPIKeyManager *localapikeypkg.LocalAPIKeyManager
	providerManager    *ProviderManager
	cliauthManager     *cliauth.Manager
	credentialManager  *credentialmgr.Manager
	Selector           routepkg.RouteTargetSelector
}

func NewAgentGateway() *AgentGateway {
	return &AgentGateway{
		configured: false,
	}
}

func (g *AgentGateway) Bootstrap(ctx context.Context, opts BootstrapOptions) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.configureConfigStore(opts.ConfigStore)
	if err := g.configureAgentRouteManager(ctx, opts.ConfigStore, opts.StaticRoutes); err != nil {
		return err
	}
	if err := g.configureLocalAPIKeyManager(ctx, opts.ConfigStore, opts.StaticLocalAPIKeys); err != nil {
		return err
	}
	if err := g.configureProviderResolver(ctx, opts.ConfigStore, opts.StaticProviders); err != nil {
		return err
	}
	g.cliauthManager = opts.CLIAuthManager
	g.credentialManager = opts.CredentialManager
	g.Selector = opts.Selector
	g.configured = true
	return nil
}

func (g *AgentGateway) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.configured = false
	g.configStore = nil
	g.routeManager = nil
	g.localAPIKeyManager = nil
	g.providerManager = nil
	g.cliauthManager = nil
	g.credentialManager = nil
	g.Selector = nil
}

func (g *AgentGateway) ConfigStore() configstoreintf.ConfigStorer {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.configStore
}

func (g *AgentGateway) CLIAuthManager() *cliauth.Manager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cliauthManager
}

func (g *AgentGateway) CredentialManager() *credentialmgr.Manager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.credentialManager
}

func (g *AgentGateway) AgentRouteManager() *routepkg.AgentRouteManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.routeManager
}

func (g *AgentGateway) LocalAPIKeyManager() *localapikeypkg.LocalAPIKeyManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.localAPIKeyManager
}

func (g *AgentGateway) ProviderManager() *ProviderManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.providerManager
}

func (g *AgentGateway) LookupRoute(ctx context.Context, routeID string) (routepkg.AgentRoute, error) {
	manager := g.AgentRouteManager()
	if manager == nil {
		return routepkg.AgentRoute{}, fmt.Errorf("route manager is not configured")
	}
	route, err := manager.Get(ctx, routeID)
	if err != nil {
		if errors.Is(err, routepkg.ErrRouteNotConfigured) {
			return routepkg.AgentRoute{}, fmt.Errorf("route %q is not configured", routeID)
		}
		return routepkg.AgentRoute{}, err
	}
	return route, nil
}

func (g *AgentGateway) ValidateRoute(ctx context.Context, routeID string) error {
	resolver := g.providerResolver()
	manager := g.AgentRouteManager()
	if manager == nil {
		return fmt.Errorf("route manager is not configured")
	}
	return manager.Validate(ctx, routeID, resolver)
}

func (g *AgentGateway) ResolveRoute(ctx context.Context, r *http.Request) (routepkg.AgentRoute, error) {
	routeManager := g.AgentRouteManager()
	if routeManager == nil {
		return routepkg.AgentRoute{}, statuserr.New(http.StatusServiceUnavailable, "route manager is not configured")
	}
	route, ok, err := routeManager.Match(ctx, r)
	if err != nil {
		return routepkg.AgentRoute{}, statuserr.New(http.StatusInternalServerError, fmt.Sprintf("match route: %v", err))
	}
	if !ok {
		return routepkg.AgentRoute{}, nil
	}
	if err := route.ValidateDefinition(); err != nil {
		return routepkg.AgentRoute{}, statuserr.New(http.StatusServiceUnavailable, err.Error())
	}
	if route.Disabled {
		return routepkg.AgentRoute{}, statuserr.New(http.StatusForbidden, fmt.Sprintf("route %q is disabled", route.ID))
	}
	return route, nil
}

func (g *AgentGateway) ResolveProviderRef(route routepkg.AgentRoute, resolveReq routepkg.ResolveRequest) (string, error) {
	if err := route.ValidateRequestPolicy(resolveReq); err != nil {
		return "", err
	}

	selector := g.selector()
	target, err := selector.SelectTarget(route, resolveReq)
	if err != nil {
		return "", err
	}
	if target == nil || target.ProviderRef == "" {
		return "", statuserr.New(http.StatusBadGateway, fmt.Sprintf("route %q has no eligible targets", route.ID))
	}
	return target.ProviderRef, nil
}

func (g *AgentGateway) ResolveProvider(ctx context.Context, providerRef string) (provider.Provider, error) {
	resolver := g.providerResolver()
	if resolver == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "provider resolver is not configured")
	}
	prov, providerName, err := resolver.ResolveProvider(ctx, providerRef)
	if err != nil || prov == nil {
		if errors.Is(err, ErrProviderDisabled) {
			return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("route target provider %q is disabled", providerRef))
		}
		return nil, statuserr.New(http.StatusBadGateway, fmt.Sprintf("route target provider %q is not configured", providerRef))
	}
	if providerName == "" {
		providerName = providerRef
	}
	prov = g.wrapProvider(prov, providerName)

	return prov, nil
}

func (g *AgentGateway) ResolveLocalAPIKey(ctx context.Context, httpReq *http.Request, r routepkg.AgentRoute) (*localapikeypkg.LocalAPIKey, error) {
	g.mu.RLock()
	localAPIKeyManager := g.localAPIKeyManager
	g.mu.RUnlock()
	if localAPIKeyManager == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "local api key manager is not configured")
	}

	localKey, err := localAPIKeyManager.Resolve(ctx, httpReq)
	if err == localapikeypkg.ErrLocalAPIKeyNotCarried {
		if r.Policy.Auth.RequireLocalAPIKey {
			return nil, statuserr.New(http.StatusUnauthorized, "local api key is required")
		}
		return nil, nil
	}
	if err != nil {
		return nil, statuserr.New(http.StatusUnauthorized, "invalid local api key")
	}
	if err := localKey.ValidateForRoute(r.ID); err != nil {
		return nil, err
	}
	return &localKey, nil
}

func (g *AgentGateway) providerResolver() ProviderResolver {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.providerManager
}

func (g *AgentGateway) selector() routepkg.RouteTargetSelector {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.Selector == nil {
		return routepkg.DefaultRouteSelector{}
	}
	return g.Selector
}

func (g *AgentGateway) wrapProvider(prov provider.Provider, providerName string) provider.Provider {
	g.mu.RLock()
	credMgr := g.credentialManager
	g.mu.RUnlock()
	return provider.WrapWithCredentialManager(prov, providerName, credMgr)
}

func (g *AgentGateway) configureConfigStore(configStore configstoreintf.ConfigStorer) {
	g.configStore = configStore
}

func (g *AgentGateway) configureAgentRouteManager(ctx context.Context, configStore configstoreintf.ConfigStorer, staticRoutes []routepkg.AgentRoute) error {
	if g.routeManager != nil {
		return fmt.Errorf("route manager is not nil")
	}

	var routeStore configstoreintf.RouteStorer
	if configStore != nil {
		var err error
		routeStore, err = configStore.GetRouteStore(ctx, routepkg.DecodeStoredRoute)
		if err != nil {
			return fmt.Errorf("get route store: %w", err)
		}
	}
	g.routeManager = routepkg.NewAgentRouteManager(routeStore)
	g.routeManager.InitStaticRoutes(staticRoutes)

	return nil
}

func (g *AgentGateway) configureLocalAPIKeyManager(ctx context.Context, configStore configstoreintf.ConfigStorer, staticLocalAPIKeys []localapikeypkg.LocalAPIKey) error {
	if g.localAPIKeyManager != nil {
		return fmt.Errorf("local api key manager is not nil")
	}

	var localAPIKeyStore configstoreintf.LocalAPIKeyStorer
	if configStore != nil {
		var err error
		localAPIKeyStore, err = configStore.GetLocalAPIKeyStore(ctx, localapikeypkg.DecodeStoredLocalAPIKey)
		if err != nil {
			return fmt.Errorf("get local api key store: %w", err)
		}
	}

	g.localAPIKeyManager = localapikeypkg.NewLocalAPIKeyManager(localAPIKeyStore)
	g.localAPIKeyManager.InitStaticKeys(staticLocalAPIKeys)
	return nil
}

func (g *AgentGateway) configureProviderResolver(ctx context.Context, configStore configstoreintf.ConfigStorer, staticProviders map[string]provider.Provider) error {
	if g.providerManager != nil {
		return fmt.Errorf("provider resolver is not nil")
	}

	var providerStore configstoreintf.ProviderConfigStorer
	if configStore != nil {
		var err error
		providerStore, err = configStore.GetProviderConfigStore(ctx, provider.DecodeStoredProviderConfig)
		if err != nil {
			return fmt.Errorf("get provider config store: %w", err)
		}
	}

	providerManager := NewProviderManager(providerStore)
	providerManager.InitStaticProviders(staticProviders)
	g.providerManager = providerManager
	return nil
}
