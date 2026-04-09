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
	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

type BootstrapOptions struct {
	StaticRoutes    []routepkg.Route
	StaticProviders map[string]provider.Provider
	ConfigStore     configstoreintf.ConfigStorer
	CLIAuthManager  *manager.Manager
	Selector        routepkg.RouteTargetSelector
}

type AgentGateway struct {
	mu sync.RWMutex

	configured       bool
	configStore      configstoreintf.ConfigStorer
	routeManager     *RouteManager
	ProviderResolver ProviderResolver
	LocalAPIKeyStore configstoreintf.LocalAPIKeyStorer
	cliauthManager   *manager.Manager
	Selector         routepkg.RouteTargetSelector
}

func NewAgentGateway() *AgentGateway {
	return &AgentGateway{
		routeManager: NewRouteManager(nil),
		configured:   false,
	}
}

func (g *AgentGateway) Bootstrap(ctx context.Context, opts BootstrapOptions) error {
	routeStore, providerResolver, localAPIKeyStore, err := g.buildDependencies(ctx, opts.StaticProviders, opts.ConfigStore)
	if err != nil {
		return err
	}

	routeManager := NewRouteManager(routeStore)
	routeManager.InitStaticRoutes(opts.StaticRoutes)
	g.Configure(opts.ConfigStore, routeManager, providerResolver, localAPIKeyStore, opts.CLIAuthManager, opts.Selector)
	return nil
}

func (g *AgentGateway) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.configured = false
	g.configStore = nil
	g.routeManager = NewRouteManager(nil)
	g.ProviderResolver = nil
	g.LocalAPIKeyStore = nil
	g.cliauthManager = nil
	g.Selector = nil
}

func (g *AgentGateway) Configure(configStore configstoreintf.ConfigStorer, routeManager *RouteManager, providerResolver ProviderResolver, localAPIKeyStore configstoreintf.LocalAPIKeyStorer, cliauthMgr *manager.Manager, selector routepkg.RouteTargetSelector) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if routeManager == nil {
		routeManager = NewRouteManager(nil)
	}
	g.configStore = configStore
	g.routeManager = routeManager
	g.ProviderResolver = providerResolver
	g.LocalAPIKeyStore = localAPIKeyStore
	g.cliauthManager = cliauthMgr
	g.Selector = selector
	g.configured = true
}

func (g *AgentGateway) ConfigStore() configstoreintf.ConfigStorer {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.configStore
}

func (g *AgentGateway) CLIAuthManager() *manager.Manager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cliauthManager
}

func (g *AgentGateway) RouteManager() *RouteManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.routeManager
}

func (g *AgentGateway) LookupRoute(ctx context.Context, routeID string) (routepkg.Route, error) {
	manager := g.RouteManager()
	if manager == nil {
		return routepkg.Route{}, fmt.Errorf("route manager is not configured")
	}
	route, err := manager.Get(ctx, routeID)
	if err != nil {
		if errors.Is(err, ErrRouteNotConfigured) {
			return routepkg.Route{}, fmt.Errorf("route %q is not configured", routeID)
		}
		return routepkg.Route{}, err
	}
	return route, nil
}

func (g *AgentGateway) ValidateRoute(ctx context.Context, routeID string) error {
	resolver := g.providerResolver()
	manager := g.RouteManager()
	if manager == nil {
		return fmt.Errorf("route manager is not configured")
	}
	return manager.Validate(ctx, routeID, resolver)
}

func (g *AgentGateway) Resolve(ctx context.Context, routeID string, req routepkg.ResolveRequest) (*ResolvedRequest, error) {
	if routeID == "" {
		return nil, utils.NewHTTPError(http.StatusServiceUnavailable, "route id is not configured")
	}

	r, err := g.LookupRoute(ctx, routeID)
	if err != nil {
		return nil, utils.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	}

	localKey, err := g.resolveLocalAPIKey(ctx, req.HTTPRequest, r)
	if err != nil {
		return nil, err
	}
	target, err := r.ResolveTarget(req, g.selector())
	if err != nil {
		return nil, err
	}

	resolver := g.providerResolver()
	if resolver == nil {
		return nil, utils.NewHTTPError(http.StatusServiceUnavailable, "provider resolver is not configured")
	}
	prov, providerName, err := resolver.ResolveProvider(ctx, target.ProviderRef)
	if err != nil || prov == nil {
		return nil, utils.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("route target provider %q is not configured", target.ProviderRef))
	}
	if providerName == "" {
		providerName = target.ProviderRef
	}
	prov = g.wrapProvider(prov, providerName)

	return &ResolvedRequest{
		Route:        r,
		LocalAPIKey:  localKey,
		ProviderName: providerName,
		Provider:     prov,
	}, nil
}

func (g *AgentGateway) ResolveProvider(ctx context.Context, routeID string, req routepkg.ResolveRequest) (provider.Provider, error) {
	resolved, err := g.Resolve(ctx, routeID, req)
	if err != nil {
		return nil, err
	}
	return resolved.Provider, nil
}

func (g *AgentGateway) resolveLocalAPIKey(ctx context.Context, httpReq *http.Request, r routepkg.Route) (*localapikeypkg.LocalAPIKey, error) {
	g.mu.RLock()
	store := g.LocalAPIKeyStore
	g.mu.RUnlock()
	return localapikeypkg.AuthenticateRequest(ctx, store, httpReq, r.ID, r.Policy.Auth.RequireLocalAPIKey)
}

func (g *AgentGateway) providerResolver() ProviderResolver {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.ProviderResolver
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
	cliauthMgr := g.cliauthManager
	g.mu.RUnlock()
	return provider.WrapWithAuthManager(prov, providerName, cliauthMgr)
}

func (g *AgentGateway) buildDependencies(ctx context.Context, staticProviders map[string]provider.Provider, configStore configstoreintf.ConfigStorer) (configstoreintf.RouteStorer, ProviderResolver, configstoreintf.LocalAPIKeyStorer, error) {
	staticResolver := NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		if staticProviders == nil {
			return nil, false
		}
		prov, ok := staticProviders[name]
		return prov, ok
	})

	if configStore == nil {
		return nil, staticResolver, nil, nil
	}

	localAPIKeyStore, err := configStore.GetLocalAPIKeyStore(ctx, localapikeypkg.DecodeStoredLocalAPIKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get local api key store: %w", err)
	}

	var dynamicResolver ProviderResolver
	providerStore, err := configStore.GetProviderConfigStore(ctx, provider.DecodeStoredProviderConfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get provider config store: %w", err)
	}
	if providerStore != nil {
		dynamicResolver = newCachedDynamicResolver(providerStore)
	}

	routeStore, err := configStore.GetRouteStore(ctx, routepkg.DecodeStoredRoute)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get route store: %w", err)
	}

	return routeStore, ChainProviderResolvers(dynamicResolver, staticResolver), localAPIKeyStore, nil
}
