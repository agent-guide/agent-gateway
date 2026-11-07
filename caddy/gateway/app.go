package gateway

import (
	"errors"
	"fmt"
	"github.com/agent-guide/agent-gateway/internal/distributionmetrics"
	"github.com/agent-guide/agent-gateway/internal/distributionruntime"
	"github.com/agent-guide/agent-gateway/internal/runtimeassembly"
	"github.com/agent-guide/agent-gateway/pkg/distribution"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"go.uber.org/zap"

	configstoresqlite "github.com/agent-guide/agent-gateway/caddy/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/credential"
	credentialscheduler "github.com/agent-guide/agent-gateway/pkg/credential/scheduler"
	runtimegateway "github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func init() {
	caddy.RegisterModule(App{})
}

// App is the Caddy app module for the Agent Gateway.
// It manages providers, MCP clients, memory stores, and configuration.
type App struct {
	// Providers lists the configured LLM providers.
	Providers map[string]provider.ProviderConfig `json:"providers,omitempty"`
	// ConfigStore configures persistent admin/auth state storage.
	ConfigStoreRaw caddy.ModuleMap `json:"config_store,omitempty" caddy:"namespace=agent_gateway.config_store_backends"`
	// ProviderTypes configures startup-only provider type availability.
	ProviderTypes []provider.ProviderTypeSetting `json:"provider_types,omitempty"`
	// CredentialRefreshCommand is the external executable used for
	// request-time credential refresh. It defaults to agw-auth.
	CredentialRefreshCommand string `json:"credential_refresh_command,omitempty"`
	// CredentialRefreshArgs are static arguments passed to the external
	// credential refresh executable. They default to ["refresh"] when the
	// command is omitted.
	CredentialRefreshArgs []string `json:"credential_refresh_args,omitempty"`
	// LLMRoutes lists statically configured gateway LLM route configs from the Caddyfile app block.
	LLMRoutes []routecore.RouteConfig `json:"llm_routes,omitempty"`
	// Metrics configures observability retention, agent-chain governance, and
	// optional OTLP span export of usage events.
	Metrics distribution.MetricsConfig `json:"metrics,omitzero"`

	logger          *zap.Logger
	credentialMgr   *credential.Manager
	credentialSched credentialscheduler.CredentialScheduler
	configBackend   configstore.ConfigStoreBackend
	providers       map[string]provider.Provider
	agentGateway    *runtimegateway.AgentGateway
	usageService    *usage.UsageService
	lifecycle       *runtimeassembly.Lifecycle
}

// CaddyModule returns the Caddy module information.
func (App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_gateway",
		New: func() caddy.Module { return new(App) },
	}
}

// Provision sets up the app.
func (a *App) Provision(ctx caddy.Context) (provisionErr error) {
	defer func() {
		if provisionErr != nil {
			provisionErr = errors.Join(provisionErr, a.lifecycle.Close())
		}
	}()
	a.logger = ctx.Logger(a)
	a.agentGateway = runtimegateway.NewAgentGateway()
	a.lifecycle = &runtimeassembly.Lifecycle{Runtime: a.agentGateway.CloseContext}

	if err := a.provisionConfigStore(ctx); err != nil {
		return fmt.Errorf("init config store: %w", err)
	}
	a.provisionUsageService()
	a.lifecycle.Usage = a.usageService
	assembly := distributionruntime.Current()
	policy, err := assembly.AttributionPolicy()
	if err != nil {
		return err
	}
	if err := a.usageService.ConfigurePublic(policy, assembly.EventSinks); err != nil {
		return err
	}
	// Provider type availability is startup-only. Always reconfigure so a
	// reload that omits provider_types resets to "all enabled" instead of
	// inheriting the previous process state.
	if len(a.ProviderTypes) > 0 {
		if err := provider.ConfigureProviderTypes(a.ProviderTypes, true); err != nil {
			return fmt.Errorf("configure provider types: %w", err)
		}
	} else {
		provider.EnableAllProviderTypes()
	}
	credentials, err := runtimeassembly.NewCredentials(a.configBackend, a.CredentialRefreshCommand, a.CredentialRefreshArgs)
	if err != nil {
		return err
	}
	a.credentialMgr, a.credentialSched = credentials.Manager, credentials.Scheduler

	if err := a.provisionProviders(ctx); err != nil {
		return fmt.Errorf("provision providers: %w", err)
	}
	if err := a.credentialMgr.Load(ctx); err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}
	registry, err := assembly.Registry()
	if err != nil {
		return err
	}
	if err := a.agentGateway.Bootstrap(ctx, runtimegateway.BootstrapOptions{
		ExtensionRegistry: registry, Distribution: assembly, EventPublisher: a.usageService,
		StaticLLMRoutes:     a.LLMRoutes,
		StaticProviders:     a.providers,
		ConfigStoreBackend:  a.configBackend,
		CredentialManager:   a.credentialMgr,
		CredentialScheduler: a.credentialSched,
		UsageObserver:       a.usageService.Observer(),
		UsageQuery:          a.usageService.Query(),
		UsageStats:          a.usageService,
		UsagePrometheus:     a.usageService.Prometheus(),
		UsageConfig:         distributionmetrics.From(a.Metrics).Normalized(),
		Logger:              a.logger,
	}); err != nil {
		return fmt.Errorf("configure agent gateway: %w", err)
	}
	// Inject the agent attributor now that the agent manager exists, so the usage
	// observer can stamp agent_id at write time.
	if attribution := a.usageService.Attribution(); attribution != nil {
		if manager := a.agentGateway.AgentManager(); manager != nil {
			attribution.Set(manager)
		}
	}

	a.logger.Info("Agent Gateway provisioned")
	return nil
}

// CredentialManager returns the shared upstream credential manager.
func (a *App) CredentialManager() *credential.Manager {
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
	if a.usageService != nil {
		a.usageService.Start()
	}
	a.logger.Info("Agent Gateway started")
	return nil
}

// Stop stops the app.
func (a *App) Stop() error { return a.lifecycle.Close() }

func (a *App) provisionUsageService() {
	a.usageService = runtimeassembly.NewUsage(a.configBackend, a.logger, distributionmetrics.From(a.Metrics))
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
		a.configBackend = backend
		a.lifecycle.Store, _ = backend.(interface{ Close() error })
		if err := schema.RegisterDefaultStores(backend); err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("no config store backend module loaded")
}

func (a *App) provisionProviders(_ caddy.Context) error {
	if len(a.Providers) == 0 {
		a.providers = map[string]provider.Provider{}
		return nil
	}

	a.providers = make(map[string]provider.Provider, len(a.Providers))
	for name, cfg := range a.Providers {
		cfg = provider.NormalizeConfig(cfg, name, cfg.ProviderType)
		if cfg.Id != name {
			return fmt.Errorf("provider %q config id %q must match registration id", name, cfg.Id)
		}
		prov, err := provider.NewProvider(cfg)
		if err != nil {
			return fmt.Errorf("init provider %q: %w", name, err)
		}
		a.providers[name] = prov
	}
	return nil
}

// Interface guards
var (
	_ caddy.App          = (*App)(nil)
	_ caddy.Provisioner  = (*App)(nil)
	_ caddy.Validator    = (*App)(nil)
	_ caddy.CleanerUpper = (*App)(nil)
)

// Cleanup also handles Caddy validation and failed provisioning.
func (a *App) Cleanup() error {
	return a.Stop()
}
