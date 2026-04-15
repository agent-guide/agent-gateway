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
	StaticRoutes       []routepkg.Route
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
	routeManager       *RouteManager
	localAPIKeyManager *LocalAPIKeyManager
	providerManager    *ProviderManager
	cliauthManager     *cliauth.Manager
	credentialManager  *credentialmgr.Manager
	Selector           routepkg.RouteTargetSelector
}

// ResolvedRequest contains the route, consumer, and provider selected for a request.
type ResolvedRequest struct {
	Route        routepkg.Route
	LocalAPIKey  *localapikeypkg.LocalAPIKey
	ProviderName string
	Provider     provider.Provider
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
	if err := g.configureRouteManager(ctx, opts.ConfigStore, opts.StaticRoutes); err != nil {
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

func (g *AgentGateway) RouteManager() *RouteManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.routeManager
}

func (g *AgentGateway) LocalAPIKeyManager() *LocalAPIKeyManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.localAPIKeyManager
}

func (g *AgentGateway) ProviderManager() *ProviderManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.providerManager
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
		return nil, statuserr.New(http.StatusServiceUnavailable, "route id is not configured")
	}

	r, err := g.LookupRoute(ctx, routeID)
	if err != nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, err.Error())
	}
	if r.Disabled {
		return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("route %q is disabled", routeID))
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
		return nil, statuserr.New(http.StatusServiceUnavailable, "provider resolver is not configured")
	}
	prov, providerName, err := resolver.ResolveProvider(ctx, target.ProviderRef)
	if err != nil || prov == nil {
		if errors.Is(err, ErrProviderDisabled) {
			return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("route target provider %q is disabled", target.ProviderRef))
		}
		return nil, statuserr.New(http.StatusBadGateway, fmt.Sprintf("route target provider %q is not configured", target.ProviderRef))
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
	localAPIKeyManager := g.localAPIKeyManager
	g.mu.RUnlock()
	rawKey := localapikeypkg.ExtractAPIKey(httpReq)
	if rawKey == "" {
		return localapikeypkg.ValidateForRoute(r.ID, r.Policy.Auth.RequireLocalAPIKey, nil)
	}
	if localAPIKeyManager == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "local api key manager is not configured")
	}
	key, err := localAPIKeyManager.Get(ctx, rawKey)
	if err != nil {
		return nil, statuserr.New(http.StatusUnauthorized, "invalid local api key")
	}
	return localapikeypkg.ValidateForRoute(r.ID, r.Policy.Auth.RequireLocalAPIKey, &key)
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

func (g *AgentGateway) configureRouteManager(ctx context.Context, configStore configstoreintf.ConfigStorer, staticRoutes []routepkg.Route) error {
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
	g.routeManager = NewRouteManager(routeStore)
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

	g.localAPIKeyManager = NewLocalAPIKeyManager(localAPIKeyStore)
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
