package gateway

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	mcproutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	credentialmgrscheduler "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	mcpruntime "github.com/agent-guide/agent-gateway/pkg/mcp/runtime"
	mcpservice "github.com/agent-guide/agent-gateway/pkg/mcp/service"
	"go.uber.org/zap"
)

type BootstrapOptions struct {
	StaticRoutes        []routepkg.LLMRoute
	StaticVirtualKeys   []virtualkeypkg.VirtualKey
	StaticProviders     map[string]provider.Provider
	ConfigStoreBackend  configstore.ConfigStoreBackend
	CLIAuthManager      *cliauth.Manager
	CLIAuthRefresher    *cliauth.AutoRefresher
	CredentialManager   *credentialmgr.Manager
	CredentialScheduler credentialmgrscheduler.CredentialScheduler
	StaticModels        []modelcatalog.ManagedModel
	Logger              *zap.Logger
}

type AgentGateway struct {
	mu sync.RWMutex

	configured            bool
	configStoreBackend    configstore.ConfigStoreBackend
	routeConfigManager    *routecore.AgentRouteConfigManager
	routeResolver         *routepkg.LLMRouteResolver
	mcpRouteConfigManager *routecore.AgentRouteConfigManager
	mcpRouteResolver      *mcproutepkg.MCPRouteResolver
	virtualKeyManager     *virtualkeypkg.VirtualKeyManager
	providerManager       *ProviderManager
	cliauthManager        *cliauth.Manager
	cliauthRefresher      *cliauth.AutoRefresher
	credentialManager     *credentialmgr.Manager
	credentialScheduler   credentialmgrscheduler.CredentialScheduler
	modelCatalog          modelcatalog.Service
	mcpServiceManager     *mcpservice.Manager
	mcpRuntimeRegistry    *mcpruntime.Registry
}

func NewAgentGateway() *AgentGateway {
	return &AgentGateway{
		configured:         false,
		mcpRuntimeRegistry: mcpruntime.NewRegistry(),
	}
}

func (g *AgentGateway) Bootstrap(ctx context.Context, opts BootstrapOptions) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.configureConfigStoreBackend(opts.ConfigStoreBackend)
	if err := g.configureRouteResolver(ctx, opts.ConfigStoreBackend, opts.StaticRoutes); err != nil {
		return err
	}
	if err := g.configureMCPRouteResolver(ctx, opts.ConfigStoreBackend); err != nil {
		return err
	}
	if err := g.configureMCPServiceManager(opts.ConfigStoreBackend); err != nil {
		return err
	}
	if err := g.configureVirtualKeyManager(ctx, opts.ConfigStoreBackend, opts.StaticVirtualKeys); err != nil {
		return err
	}
	if err := g.configureProviderResolver(ctx, opts.ConfigStoreBackend, opts.StaticProviders); err != nil {
		return err
	}
	g.cliauthManager = opts.CLIAuthManager
	g.cliauthRefresher = opts.CLIAuthRefresher
	g.credentialManager = opts.CredentialManager
	g.credentialScheduler = opts.CredentialScheduler
	if err := g.configureModelCatalog(ctx, opts.ConfigStoreBackend, opts.StaticModels, opts.Logger); err != nil {
		return err
	}
	g.configured = true
	return nil
}

func (g *AgentGateway) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.configured = false
	g.configStoreBackend = nil
	g.routeConfigManager = nil
	g.routeResolver = nil
	g.mcpRouteConfigManager = nil
	g.mcpRouteResolver = nil
	g.virtualKeyManager = nil
	g.providerManager = nil
	g.cliauthManager = nil
	g.cliauthRefresher = nil
	g.credentialManager = nil
	g.credentialScheduler = nil
	g.modelCatalog = nil
	g.mcpServiceManager = nil
	g.mcpRuntimeRegistry = mcpruntime.NewRegistry()
}

func (g *AgentGateway) ConfigStoreBackend() configstore.ConfigStoreBackend {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.configStoreBackend
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

func (g *AgentGateway) AgentRouteConfigManager() *routecore.AgentRouteConfigManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.routeConfigManager
}

func (g *AgentGateway) LLMRouteResolver() *routepkg.LLMRouteResolver {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.routeResolver
}

func (g *AgentGateway) MCPRouteConfigManager() *routecore.AgentRouteConfigManager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.mcpRouteConfigManager
}

func (g *AgentGateway) MCPRouteResolver() *mcproutepkg.MCPRouteResolver {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.mcpRouteResolver
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

func (g *AgentGateway) MCPServiceManager() *mcpservice.Manager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.mcpServiceManager
}

func (g *AgentGateway) MCPRuntimeRegistry() *mcpruntime.Registry {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.mcpRuntimeRegistry
}

func (g *AgentGateway) Match(ctx context.Context, r *http.Request) (routecore.AgentRouteConfig, error) {
	g.mu.RLock()
	llmManager := g.routeConfigManager
	mcpManager := g.mcpRouteConfigManager
	g.mu.RUnlock()

	if llmManager == nil && mcpManager == nil {
		return routecore.AgentRouteConfig{}, statuserr.New(http.StatusServiceUnavailable, "route resolver is not configured")
	}

	route, ok, err := routecore.MatchManagers(ctx, r, llmManager, mcpManager)
	if err != nil {
		return routecore.AgentRouteConfig{}, statuserr.New(http.StatusInternalServerError, fmt.Sprintf("match route: %v", err))
	}
	if !ok {
		return routecore.AgentRouteConfig{}, nil
	}
	return route, nil
}

func (g *AgentGateway) ResolveRoute(ctx context.Context, r *http.Request) (*routepkg.LLMRoute, error) {
	cfg, err := g.Match(ctx, r)
	if err != nil {
		return nil, err
	}
	if cfg.ID == "" || cfg.Kind != routecore.RouteKindLLM {
		return nil, nil
	}

	routeResolver := g.LLMRouteResolver()
	if routeResolver == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "route resolver is not configured")
	}
	route, err := routeResolver.Get(ctx, cfg.ID)
	if err != nil {
		return nil, statuserr.New(http.StatusInternalServerError, fmt.Sprintf("get route %q: %v", cfg.ID, err))
	}
	if err := route.ValidateDefinition(); err != nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, err.Error())
	}
	if route.Disabled {
		return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("route %q is disabled", route.ID))
	}
	return route, nil
}

func (g *AgentGateway) RegisterStaticMCPRoute(route mcproutepkg.MCPRoute) error {
	route.Normalize()
	cfg, err := route.ToConfig()
	if err != nil {
		return err
	}

	g.mu.RLock()
	manager := g.mcpRouteConfigManager
	g.mu.RUnlock()
	if manager == nil {
		return statuserr.New(http.StatusServiceUnavailable, "mcp route resolver is not configured")
	}
	manager.UpsertStaticRoute(cfg)
	return nil
}

func (g *AgentGateway) NewRoutedProvider(route *routepkg.LLMRoute, requestRequirements routepkg.RequestRequirements) (*RoutedProvider, error) {
	resolver := g.providerResolver()
	if resolver == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "provider resolver is not configured")
	}
	if route == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "route is not configured")
	}
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

func (g *AgentGateway) ResolveVirtualKey(ctx context.Context, httpReq *http.Request, r routecore.AgentRouteConfig) (*virtualkeypkg.VirtualKey, error) {
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

	virtualKey, err := virtualKeyManager.GetByKey(ctx, rawKey)
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

func (g *AgentGateway) configureConfigStoreBackend(configStoreBackend configstore.ConfigStoreBackend) {
	g.configStoreBackend = configStoreBackend
}

func (g *AgentGateway) configureRouteResolver(ctx context.Context, configStoreBackend configstore.ConfigStoreBackend, staticRoutes []routepkg.LLMRoute) error {
	_ = ctx
	if g.routeConfigManager != nil || g.routeResolver != nil {
		return fmt.Errorf("route resolver is already configured")
	}

	var routeStore configstore.ConfigStore
	if configStoreBackend != nil {
		var err error
		routeStore, err = configStoreBackend.Get(schema.StoreRoutes)
		if err != nil {
			return fmt.Errorf("get route store: %w", err)
		}
	}
	g.routeConfigManager = routecore.NewAgentRouteConfigManager(routeStore)
	g.routeConfigManager.InitStaticRoutes(llmRoutesToConfigs(staticRoutes))
	g.routeResolver = routepkg.NewLLMRouteResolver(g.routeConfigManager)

	return nil
}

func (g *AgentGateway) configureMCPRouteResolver(ctx context.Context, configStoreBackend configstore.ConfigStoreBackend) error {
	_ = ctx
	if g.mcpRouteConfigManager != nil || g.mcpRouteResolver != nil {
		return fmt.Errorf("mcp route resolver is already configured")
	}

	var routeStore configstore.ConfigStore
	if configStoreBackend != nil {
		var err error
		routeStore, err = configStoreBackend.Get(schema.StoreMCPRoutes)
		if err != nil {
			return fmt.Errorf("get mcp route store: %w", err)
		}
	}
	g.mcpRouteConfigManager = routecore.NewAgentRouteConfigManager(routeStore)
	g.mcpRouteResolver = mcproutepkg.NewMCPRouteResolver(g.mcpRouteConfigManager)
	return nil
}

func (g *AgentGateway) configureMCPServiceManager(configStoreBackend configstore.ConfigStoreBackend) error {
	if g.mcpServiceManager != nil {
		return nil
	}
	if configStoreBackend == nil {
		return nil
	}
	store, err := configStoreBackend.Get(schema.StoreMCPServices)
	if err != nil {
		return err
	}
	g.mcpServiceManager = mcpservice.NewManager(store)
	return nil
}

func llmRoutesToConfigs(routes []routepkg.LLMRoute) []routecore.AgentRouteConfig {
	out := make([]routecore.AgentRouteConfig, 0, len(routes))
	for _, route := range routes {
		cfg, err := route.ToConfig()
		if err != nil || cfg.ID == "" {
			continue
		}
		out = append(out, cfg)
	}
	return out
}

func (g *AgentGateway) configureVirtualKeyManager(ctx context.Context, configStoreBackend configstore.ConfigStoreBackend, staticVirtualKeys []virtualkeypkg.VirtualKey) error {
	_ = ctx
	if g.virtualKeyManager != nil {
		return fmt.Errorf("virtual key manager is not nil")
	}

	var virtualKeyStore configstore.ConfigStore
	if configStoreBackend != nil {
		var err error
		virtualKeyStore, err = configStoreBackend.Get(schema.StoreVirtualKeys)
		if err != nil {
			return fmt.Errorf("get virtual key store: %w", err)
		}
	}

	g.virtualKeyManager = virtualkeypkg.NewVirtualKeyManager(virtualKeyStore)
	g.virtualKeyManager.InitStaticKeys(staticVirtualKeys)
	return nil
}

func (g *AgentGateway) configureProviderResolver(ctx context.Context, configStoreBackend configstore.ConfigStoreBackend, staticProviders map[string]provider.Provider) error {
	_ = ctx
	if g.providerManager != nil {
		return fmt.Errorf("provider resolver is not nil")
	}

	var providerStore configstore.ConfigStore
	if configStoreBackend != nil {
		var err error
		providerStore, err = configStoreBackend.Get(schema.StoreProviders)
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

func (g *AgentGateway) configureModelCatalog(ctx context.Context, configStoreBackend configstore.ConfigStoreBackend, staticModels []modelcatalog.ManagedModel, logger *zap.Logger) error {
	_ = ctx
	if g.modelCatalog != nil {
		return fmt.Errorf("model catalog is not nil")
	}

	var modelStore configstore.ConfigStore
	if configStoreBackend != nil {
		var err error
		modelStore, err = configStoreBackend.Get(schema.StoreManagedModels)
		if err != nil {
			return fmt.Errorf("get model store: %w", err)
		}
	}
	g.modelCatalog = modelcatalog.NewService(modelStore, g.providerManager, staticModels, logger)
	return nil
}
