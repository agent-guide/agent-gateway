package gateway

import (
	"context"
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

	routes     map[string]routepkg.Route
	configured bool

	RouteLoader      routepkg.RouteLoader
	ProviderResolver ProviderResolver
	LocalAPIKeyStore configstoreintf.LocalAPIKeyStorer
	cliauthManager   *manager.Manager
	Selector         routepkg.RouteTargetSelector
}

func NewAgentGateway() *AgentGateway {
	return &AgentGateway{
		routes:     map[string]routepkg.Route{},
		configured: false,
	}
}

func (g *AgentGateway) Bootstrap(ctx context.Context, opts BootstrapOptions) error {
	routeLoader, providerResolver, localAPIKeyStore, err := g.buildDependencies(ctx, opts.StaticProviders, opts.ConfigStore)
	if err != nil {
		return err
	}

	g.Configure(routeLoader, providerResolver, localAPIKeyStore, opts.CLIAuthManager, opts.Selector)
	g.SetRoutes(opts.StaticRoutes)
	return nil
}

func (g *AgentGateway) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.routes = map[string]routepkg.Route{}
	g.configured = false
	g.RouteLoader = nil
	g.ProviderResolver = nil
	g.LocalAPIKeyStore = nil
	g.cliauthManager = nil
	g.Selector = nil
}

func (g *AgentGateway) Configure(routeLoader routepkg.RouteLoader, providerResolver ProviderResolver, localAPIKeyStore configstoreintf.LocalAPIKeyStorer, cliauthMgr *manager.Manager, selector routepkg.RouteTargetSelector) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.RouteLoader = routeLoader
	g.ProviderResolver = providerResolver
	g.LocalAPIKeyStore = localAPIKeyStore
	g.cliauthManager = cliauthMgr
	g.Selector = selector
	g.configured = true
}

func (g *AgentGateway) SetRoutes(routes []routepkg.Route) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.routes = make(map[string]routepkg.Route, len(routes))
	for _, r := range routes {
		if r.ID == "" {
			continue
		}
		r.Normalize()
		g.routes[r.ID] = r
	}
}

func (g *AgentGateway) EnsureRoute(r routepkg.Route) {
	if r.ID == "" {
		return
	}

	r.Normalize()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.routes == nil {
		g.routes = map[string]routepkg.Route{}
	}
	g.routes[r.ID] = r
}

func (g *AgentGateway) Routes() []routepkg.Route {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]routepkg.Route, 0, len(g.routes))
	for _, r := range g.routes {
		out = append(out, r)
	}
	return out
}

func (g *AgentGateway) Route(routeID string) (routepkg.Route, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	r, ok := g.routes[routeID]
	return r, ok
}

func (g *AgentGateway) ValidateRoute(ctx context.Context, routeID string) error {
	r, err := g.resolveRouteForValidation(ctx, routeID)
	if err != nil {
		return err
	}
	if err := r.ValidateDefinition(); err != nil {
		return err
	}

	resolver := g.providerResolver()
	if resolver == nil {
		return fmt.Errorf("provider resolver is not configured")
	}

	for _, ref := range r.ProviderRefs() {
		if _, _, err := resolver.ResolveProvider(ctx, ref); err != nil {
			return fmt.Errorf("provider %q is not configured", ref)
		}
	}
	return nil
}

func (g *AgentGateway) Resolve(ctx context.Context, routeID string, req routepkg.ResolveRequest) (*ResolvedRequest, error) {
	r, err := g.resolveRoute(ctx, routeID)
	if err != nil {
		return nil, err
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

func (g *AgentGateway) resolveRoute(ctx context.Context, routeID string) (routepkg.Route, error) {
	if routeID == "" {
		return routepkg.Route{}, utils.NewHTTPError(http.StatusServiceUnavailable, "route id is not configured")
	}

	g.mu.RLock()
	r, ok := g.routes[routeID]
	loader := g.RouteLoader
	g.mu.RUnlock()

	if loader != nil {
		latest, err := loader(ctx, routeID)
		if err != nil {
			return routepkg.Route{}, utils.NewHTTPError(http.StatusServiceUnavailable, fmt.Sprintf("route %q is unavailable", routeID))
		}
		if latest != nil {
			latest.Normalize()
			g.EnsureRoute(*latest)
			return *latest, nil
		}
	}

	if !ok {
		return routepkg.Route{}, utils.NewHTTPError(http.StatusServiceUnavailable, fmt.Sprintf("route %q is not configured", routeID))
	}
	r.Normalize()
	return r, nil
}

func (g *AgentGateway) resolveRouteForValidation(ctx context.Context, routeID string) (routepkg.Route, error) {
	if routeID == "" {
		return routepkg.Route{}, fmt.Errorf("route_id is required")
	}

	r, ok := g.Route(routeID)
	if ok {
		r.Normalize()
		return r, nil
	}

	g.mu.RLock()
	loader := g.RouteLoader
	g.mu.RUnlock()
	if loader == nil {
		return routepkg.Route{}, fmt.Errorf("route %q is not configured", routeID)
	}

	loaded, err := loader(ctx, routeID)
	if err != nil || loaded == nil {
		return routepkg.Route{}, fmt.Errorf("route %q is not configured", routeID)
	}
	loaded.Normalize()
	return *loaded, nil
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

func (g *AgentGateway) buildDependencies(ctx context.Context, staticProviders map[string]provider.Provider, configStore configstoreintf.ConfigStorer) (routepkg.RouteLoader, ProviderResolver, configstoreintf.LocalAPIKeyStorer, error) {
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

	var routeLoader routepkg.RouteLoader
	if routeStore != nil {
		routeLoader = func(ctx context.Context, routeID string) (*routepkg.Route, error) {
			item, err := routeStore.Get(ctx, routeID)
			if err != nil {
				return nil, err
			}
			r, ok := item.(*routepkg.Route)
			if !ok || r == nil {
				return nil, fmt.Errorf("route %q has unexpected type %T", routeID, item)
			}
			return r, nil
		}
	}

	return routeLoader, ChainProviderResolvers(dynamicResolver, staticResolver), localAPIKeyStore, nil
}
