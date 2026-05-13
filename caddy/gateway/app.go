package gateway

import (
	"context"
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"go.uber.org/zap"

	configstoresqlite "github.com/agent-guide/agent-gateway/caddy/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	runtimegateway "github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	credentialmgrscheduler "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func init() {
	caddy.RegisterModule(App{})
}

// App is the Caddy app module for the Agent Gateway.
// It manages providers, MCP clients, memory stores, and configuration.
type App struct {
	// Providers lists the configured LLM providers.
	ProvidersRaw caddy.ModuleMap `json:"providers,omitempty" caddy:"namespace=llm.providers inline_key=provider_type"`
	// ConfigStore configures persistent admin/auth state storage.
	ConfigStoreRaw caddy.ModuleMap `json:"config_store,omitempty" caddy:"namespace=agent_gateway.config_store_backends"`
	// Routes lists statically configured gateway routes from the Caddyfile app block.
	Routes []routepkg.AgentRoute `json:"routes,omitempty"`
	// Models lists statically configured managed concrete models derived from the Caddyfile app block.
	Models []modelcatalog.ManagedModel `json:"models,omitempty"`

	logger           *zap.Logger
	cliauthManager   *cliauth.Manager
	cliauthRefresher *cliauth.AutoRefresher
	credentialMgr    *credentialmgr.Manager
	credentialSched  credentialmgrscheduler.CredentialScheduler
	configBackend    configstore.ConfigStoreBackend
	providers        map[string]provider.Provider
	agentGateway     *runtimegateway.AgentGateway
}

// CaddyModule returns the Caddy module information.
func (App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_gateway",
		New: func() caddy.Module { return new(App) },
	}
}

// Provision sets up the app.
func (a *App) Provision(ctx caddy.Context) error {
	a.logger = ctx.Logger(a)
	a.agentGateway = runtimegateway.NewAgentGateway()

	if err := a.provisionConfigStore(ctx); err != nil {
		return fmt.Errorf("init config store: %w", err)
	}
	credentialStore, err := a.configBackend.Get(schema.StoreCredentials)
	if err != nil {
		return fmt.Errorf("get credential store: %w", err)
	}

	credentialScheduler := credentialmgrscheduler.NewScheduler(nil)
	a.credentialSched = credentialScheduler
	a.credentialMgr = credentialmgr.NewManager(credentialStore)
	if schedulerListener, ok := credentialScheduler.(credentialmgr.CredentialLifecycleListener); ok {
		a.credentialMgr.AddListener(schedulerListener)
	}
	a.cliauthManager = cliauth.NewManager()
	a.cliauthManager.SetCredentialManager(a.credentialMgr)
	a.cliauthRefresher = cliauth.NewAutoRefresher(cliauth.WrapSharedCredentialManager(a.credentialMgr), a.cliauthManager)
	if err := a.provisionProviders(ctx); err != nil {
		return fmt.Errorf("provision providers: %w", err)
	}
	if err := a.credentialMgr.Load(ctx); err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}
	if err := a.cliauthRefresher.Load(ctx); err != nil {
		return fmt.Errorf("load cliauth credentials: %w", err)
	}

	if err := a.agentGateway.Bootstrap(ctx, runtimegateway.BootstrapOptions{
		StaticRoutes:        a.Routes,
		StaticProviders:     a.providers,
		StaticModels:        a.Models,
		ConfigStoreBackend:  a.configBackend,
		CLIAuthManager:      a.cliauthManager,
		CLIAuthRefresher:    a.cliauthRefresher,
		CredentialManager:   a.credentialMgr,
		CredentialScheduler: a.credentialSched,
		Logger:              a.logger,
	}); err != nil {
		return fmt.Errorf("configure agent gateway: %w", err)
	}

	a.logger.Info("Agent Gateway provisioned")
	return nil
}

// CLIAuthManager returns the CLI authenticator manager shared across the gateway.
func (a *App) CLIAuthManager() *cliauth.Manager {
	return a.cliauthManager
}

// CLIAuthRefresher returns the CLI credential refresher shared across the gateway.
func (a *App) CLIAuthRefresher() *cliauth.AutoRefresher {
	return a.cliauthRefresher
}

// CredentialManager returns the shared upstream credential manager.
func (a *App) CredentialManager() *credentialmgr.Manager {
	return a.credentialMgr
}

// AgentGateway returns the gateway instance owned by this app.
// It returns nil if called before Provision completes.
func (a *App) AgentGateway() *runtimegateway.AgentGateway {
	return a.agentGateway
}

func (a *App) ConfigStore() configstore.ConfigStoreBackend {
	return a.configBackend
}

// Provider returns a configured provider by name.
func (a *App) Provider(name string) (provider.Provider, bool) {
	if a.providers == nil {
		return nil, false
	}
	prov, ok := a.providers[name]
	return prov, ok
}

// Validate validates the app configuration.
func (a *App) Validate() error {
	return nil
}

// Start starts the app.
func (a *App) Start() error {
	if a.cliauthRefresher != nil {
		a.cliauthRefresher.Start(context.Background())
	}
	a.logger.Info("Agent Gateway started")
	return nil
}

// Stop stops the app.
func (a *App) Stop() error {
	if a.cliauthRefresher != nil {
		a.cliauthRefresher.Stop()
	}
	return nil
}

// GetApp retrieves the agent gateway app from the Caddy context.
func GetApp(ctx caddy.Context) (*App, error) {
	appIface, err := ctx.App("agent_gateway")
	if err != nil {
		return nil, err
	}
	app, ok := appIface.(*App)
	if !ok {
		return nil, fmt.Errorf("agent_gateway app is not *gateway.App")
	}
	return app, nil
}

func (a *App) provisionConfigStore(ctx caddy.Context) error {
	if len(a.ConfigStoreRaw) == 0 {
		a.ConfigStoreRaw = caddy.ModuleMap{
			"sqlite": caddyconfig.JSON(&configstoresqlite.SQLiteConfigStoreBackend{}, nil),
		}
	}

	modules, err := ctx.LoadModule(a, "ConfigStoreRaw")
	if err != nil {
		return err
	}

	loaded, ok := modules.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected config store backend module type %T", modules)
	}
	if len(loaded) != 1 {
		return fmt.Errorf("expected exactly one config store backend module, got %d", len(loaded))
	}

	for name, mod := range loaded {
		backend, ok := mod.(configstore.ConfigStoreBackend)
		if !ok {
			return fmt.Errorf("config store backend module %q does not implement configstore.ConfigStoreBackend", name)
		}
		if err := schema.RegisterDefaultStores(backend); err != nil {
			return err
		}
		a.configBackend = backend
		return nil
	}

	return fmt.Errorf("no config store backend module loaded")
}

func (a *App) provisionProviders(ctx caddy.Context) error {
	if len(a.ProvidersRaw) == 0 {
		a.providers = map[string]provider.Provider{}
		return nil
	}

	modules, err := ctx.LoadModule(a, "ProvidersRaw")
	if err != nil {
		return err
	}

	loaded, ok := modules.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected provider module type %T", modules)
	}

	a.providers = make(map[string]provider.Provider, len(loaded))
	for name, mod := range loaded {
		prov, ok := mod.(provider.Provider)
		if !ok {
			return fmt.Errorf("provider module %q does not implement provider.Provider", name)
		}
		a.providers[name] = prov
	}
	return nil
}

// Interface guards
var (
	_ caddy.App         = (*App)(nil)
	_ caddy.Provisioner = (*App)(nil)
	_ caddy.Validator   = (*App)(nil)
)
