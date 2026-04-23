package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	virtualkeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/virtualkey"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

type BootstrapOptions struct {
	StaticRoutes      []routepkg.AgentRoute
	StaticVirtualKeys []virtualkeypkg.VirtualKey
	StaticProviders   map[string]provider.Provider
	ConfigStore       configstoreintf.ConfigStorer
	CLIAuthManager    *cliauth.Manager
	CredentialManager *credentialmgr.Manager
	Selector          routepkg.RouteTargetSelector
}

type AgentGateway struct {
	mu sync.RWMutex

	configured        bool
	configStore       configstoreintf.ConfigStorer
	routeManager      *routepkg.AgentRouteManager
	virtualKeyManager *virtualkeypkg.VirtualKeyManager
	providerManager   *ProviderManager
	cliauthManager    *cliauth.Manager
	credentialManager *credentialmgr.Manager
	Selector          routepkg.RouteTargetSelector
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
	if err := g.configureVirtualKeyManager(ctx, opts.ConfigStore, opts.StaticVirtualKeys); err != nil {
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
	g.virtualKeyManager = nil
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

func (g *AgentGateway) VirtualKeyManager() *virtualkeypkg.VirtualKeyManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.virtualKeyManager
}

func (g *AgentGateway) ProviderManager() *ProviderManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.providerManager
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

func (g *AgentGateway) resolveProviderID(route routepkg.AgentRoute, routeResolveReq routepkg.RouteResolveRequest) (string, error) {
	if err := route.ValidateRequestPolicy(routeResolveReq); err != nil {
		return "", err
	}

	selector := g.selector()
	target, err := selector.SelectTarget(route, routeResolveReq)
	if err != nil {
		return "", err
	}
	if target == nil || target.ProviderID == "" {
		return "", statuserr.New(http.StatusBadGateway, fmt.Sprintf("route %q has no eligible targets", route.ID))
	}
	return target.ProviderID, nil
}

func (g *AgentGateway) ResolveProvider(ctx context.Context, route routepkg.AgentRoute, routeResolveReq routepkg.RouteResolveRequest) (provider.Provider, error) {
	resolver := g.providerResolver()
	if resolver == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "provider resolver is not configured")
	}

	providerID, err := g.resolveProviderID(route, routeResolveReq)
	if err != nil {
		return nil, err
	}

	prov, err := resolver.ResolveProvider(ctx, providerID)
	if err != nil || prov == nil {
		if errors.Is(err, ErrProviderDisabled) {
			return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("route target provider %q is disabled", providerID))
		}
		return nil, statuserr.New(http.StatusBadGateway, fmt.Sprintf("route target provider %q is not configured", providerID))
	}
	prov = g.wrapProvider(prov)

	return prov, nil
}

func (g *AgentGateway) ResolveVirtualKey(ctx context.Context, httpReq *http.Request, r routepkg.AgentRoute) (*virtualkeypkg.VirtualKey, error) {
	rawKey := virtualkeypkg.ExtractAPIKey(httpReq)
	if rawKey == "" {
		if r.Policy.Auth.RequireVirtualKey {
			return nil, statuserr.New(http.StatusUnauthorized, "virtual key is required")
		}
		return nil, nil
	}

	g.mu.RLock()
	virtualKeyManager := g.virtualKeyManager
	g.mu.RUnlock()
	if virtualKeyManager == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "virtual key manager is not configured")
	}

	virtualKey, err := virtualKeyManager.Get(ctx, rawKey)
	if err != nil {
		return nil, statuserr.New(http.StatusUnauthorized, "invalid virtual key")
	}
	if err := virtualKey.ValidateForRoute(r.ID); err != nil {
		return nil, err
	}
	return &virtualKey, nil
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

func (g *AgentGateway) wrapProvider(prov provider.Provider) provider.Provider {
	g.mu.RLock()
	credMgr := g.credentialManager
	g.mu.RUnlock()
	return provider.WrapWithCredentialManager(prov, credMgr)
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

func (g *AgentGateway) configureVirtualKeyManager(ctx context.Context, configStore configstoreintf.ConfigStorer, staticVirtualKeys []virtualkeypkg.VirtualKey) error {
	if g.virtualKeyManager != nil {
		return fmt.Errorf("virtual key manager is not nil")
	}

	var virtualKeyStore configstoreintf.VirtualKeyStorer
	if configStore != nil {
		var err error
		virtualKeyStore, err = configStore.GetVirtualKeyStore(ctx, virtualkeypkg.DecodeStoredVirtualKey)
		if err != nil {
			return fmt.Errorf("get virtual key store: %w", err)
		}
	}

	g.virtualKeyManager = virtualkeypkg.NewVirtualKeyManager(virtualKeyStore)
	g.virtualKeyManager.InitStaticKeys(staticVirtualKeys)
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
	if err := providerManager.InitStaticProviders(staticProviders); err != nil {
		return fmt.Errorf("init static providers: %w", err)
	}
	g.providerManager = providerManager
	return nil
}
