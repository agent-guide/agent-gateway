package gateway

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	credentialmgrscheduler "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"go.uber.org/zap"
)

type BootstrapOptions struct {
	StaticRoutes        []routepkg.AgentRoute
	StaticVirtualKeys   []virtualkeypkg.VirtualKey
	StaticProviders     map[string]provider.Provider
	ConfigStore         configstoreintf.ConfigStorer
	CLIAuthManager      *cliauth.Manager
	CLIAuthRefresher    *cliauth.AutoRefresher
	CredentialManager   *credentialmgr.Manager
	CredentialScheduler credentialmgrscheduler.CredentialScheduler
	StaticModels        []modelcatalog.ManagedModel
	Logger              *zap.Logger
}

type AgentGateway struct {
	mu sync.RWMutex

	configured          bool
	configStore         configstoreintf.ConfigStorer
	routeManager        *routepkg.AgentRouteManager
	virtualKeyManager   *virtualkeypkg.VirtualKeyManager
	providerManager     *ProviderManager
	cliauthManager      *cliauth.Manager
	cliauthRefresher    *cliauth.AutoRefresher
	credentialManager   *credentialmgr.Manager
	credentialScheduler credentialmgrscheduler.CredentialScheduler
	modelCatalog        modelcatalog.Service
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
	g.cliauthRefresher = opts.CLIAuthRefresher
	g.credentialManager = opts.CredentialManager
	g.credentialScheduler = opts.CredentialScheduler
	if err := g.configureModelCatalog(ctx, opts.ConfigStore, opts.StaticModels, opts.Logger); err != nil {
		return err
	}
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
	g.cliauthRefresher = nil
	g.credentialManager = nil
	g.credentialScheduler = nil
	g.modelCatalog = nil
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

func (g *AgentGateway) CLIAuthRefresher() *cliauth.AutoRefresher {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cliauthRefresher
}

func (g *AgentGateway) CredentialManager() *credentialmgr.Manager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.credentialManager
}

func (g *AgentGateway) CredentialScheduler() credentialmgrscheduler.CredentialScheduler {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.credentialScheduler
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

func (g *AgentGateway) ModelCatalog() modelcatalog.Service {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.modelCatalog
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

func (g *AgentGateway) NewRoutedProvider(route routepkg.AgentRoute, requestRequirements routepkg.RequestRequirements) (*RoutedProvider, error) {
	resolver := g.providerResolver()
	if resolver == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "provider resolver is not configured")
	}
	route.Normalize()
	return &RoutedProvider{
		route:               route,
		requestRequirements: requestRequirements,
		providerResolver:    resolver,
		providerConfigs:     g.ProviderManager(),
		modelCatalog:        g.ModelCatalog(),
		credentialMgr:       g.CredentialManager(),
		scheduler:           g.CredentialScheduler(),
	}, nil
}

func (g *AgentGateway) ResolveVirtualKey(ctx context.Context, httpReq *http.Request, r routepkg.AgentRoute) (*virtualkeypkg.VirtualKey, error) {
	rawKey := virtualkeypkg.ExtractAPIKey(httpReq)
	if rawKey == "" {
		if r.AuthPolicy.RequireVirtualKey {
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

func (g *AgentGateway) configureModelCatalog(ctx context.Context, configStore configstoreintf.ConfigStorer, staticModels []modelcatalog.ManagedModel, logger *zap.Logger) error {
	if g.modelCatalog != nil {
		return fmt.Errorf("model catalog is not nil")
	}

	var modelStore configstoreintf.ModelStorer
	if configStore != nil {
		var err error
		modelStore, err = configStore.GetModelStore(ctx, modelcatalog.DecodeStoredManagedModel)
		if err != nil {
			return fmt.Errorf("get model store: %w", err)
		}
	}
	g.modelCatalog = modelcatalog.NewService(modelStore, g.providerManager, staticModels, logger)
	return nil
}
