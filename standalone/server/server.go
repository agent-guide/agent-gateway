package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/admin"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	configstoresqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher"
	anthropicapi "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/anthropic"
	openaiapi "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	credentialmgrscheduler "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const shutdownTimeout = 10 * time.Second

type Options struct {
	Addr              string
	AdminAddr         string
	ConfigStorePath   string
	StaticConfigPath  string
	AdminUser         string
	AdminPasswordHash string
}

func (o *Options) setDefaults() {
	if o.Addr == "" {
		o.Addr = "127.0.0.1:8080"
	}
	if o.AdminAddr == "" {
		o.AdminAddr = o.Addr
	}
	if o.ConfigStorePath == "" {
		o.ConfigStorePath = "./data/configstore.db"
	}
}

func Run(ctx context.Context, opts Options) error {
	opts.setDefaults()

	logger, err := zap.NewProduction()
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	agentGateway, cliauthRefresher, err := bootstrapGateway(ctx, opts, logger)
	if err != nil {
		return err
	}
	if cliauthRefresher != nil {
		cliauthRefresher.Start(ctx)
		defer cliauthRefresher.Stop()
	}

	adminHandler := admin.NewHandler(agentGateway, logger.Named("admin"), opts.AdminUser, opts.AdminPasswordHash)
	dispatchHandler := dispatcher.NewHandler(agentGateway, newLLMAPIHandlers(logger.Named("dispatcher")), logger.Named("dispatcher"))

	var servers []*http.Server
	if opts.AdminAddr == opts.Addr {
		servers = append(servers, &http.Server{
			Addr:    opts.Addr,
			Handler: newRouter(adminHandler, dispatchHandler),
		})
	} else {
		servers = append(servers,
			&http.Server{Addr: opts.Addr, Handler: newGatewayRouter(dispatchHandler)},
			&http.Server{Addr: opts.AdminAddr, Handler: newAdminRouter(adminHandler)},
		)
	}

	errCh := make(chan error, len(servers))
	for _, srv := range servers {
		server := srv
		go func() {
			logger.Info("standalone server listening", zap.String("addr", server.Addr))
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}()
	}

	select {
	case <-ctx.Done():
		return shutdownServers(context.Background(), servers)
	case err := <-errCh:
		if err != nil {
			_ = shutdownServers(context.Background(), servers)
			return err
		}
		return shutdownServers(context.Background(), servers)
	}
}

func bootstrapGateway(ctx context.Context, opts Options, logger *zap.Logger) (*gateway.AgentGateway, *cliauth.AutoRefresher, error) {
	staticConfig, err := loadStaticConfig(ctx, opts)
	if err != nil {
		return nil, nil, err
	}

	configStore, err := configstoresqlite.Open(ctx, configstoresqlite.Config{SQLitePath: opts.ConfigStorePath}, logger.Named("sqlite"))
	if err != nil {
		return nil, nil, fmt.Errorf("open config store: %w", err)
	}

	credentialStore, err := configStore.GetCredentialStore(ctx, credentialmgr.DecodeCredential)
	if err != nil {
		return nil, nil, fmt.Errorf("get credential store: %w", err)
	}
	credentialScheduler := credentialmgrscheduler.NewScheduler(nil)
	credentialManager := credentialmgr.NewManager(credentialStore)
	if schedulerListener, ok := credentialScheduler.(credentialmgr.CredentialLifecycleListener); ok {
		credentialManager.AddListener(schedulerListener)
	}

	cliauthManager := cliauth.NewManager()
	cliauthManager.SetCredentialManager(credentialManager)
	cliauthRefresher := cliauth.NewAutoRefresher(cliauth.WrapSharedCredentialManager(credentialManager), cliauthManager)

	if err := registerStaticProviderCredentials(ctx, credentialManager, staticConfig.Providers); err != nil {
		return nil, nil, err
	}

	if err := credentialManager.Load(ctx); err != nil {
		return nil, nil, fmt.Errorf("load credentials: %w", err)
	}
	if err := cliauthRefresher.Load(ctx); err != nil {
		return nil, nil, fmt.Errorf("load cliauth credentials: %w", err)
	}

	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(ctx, gateway.BootstrapOptions{
		StaticRoutes:        staticConfig.Routes,
		StaticProviders:     staticConfig.Providers,
		StaticModels:        staticConfig.ManagedModels,
		ConfigStore:         configStore,
		CLIAuthManager:      cliauthManager,
		CLIAuthRefresher:    cliauthRefresher,
		CredentialManager:   credentialManager,
		CredentialScheduler: credentialScheduler,
	}); err != nil {
		return nil, nil, fmt.Errorf("bootstrap agent gateway: %w", err)
	}
	return agentGateway, cliauthRefresher, nil
}

type staticConfig struct {
	Providers     map[string]provider.Provider
	ManagedModels []modelcatalog.ManagedModel
	Routes        []routepkg.AgentRoute
}

func loadStaticConfig(ctx context.Context, opts Options) (*staticConfig, error) {
	_ = ctx
	cfg := &staticConfig{
		Providers: map[string]provider.Provider{},
	}
	if opts.StaticConfigPath == "" {
		return cfg, nil
	}

	bundle, err := gatewaybundle.LoadFile(opts.StaticConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load static gateway bundle: %w", err)
	}
	if err := bundle.ValidateForStaticConfig(); err != nil {
		return nil, fmt.Errorf("validate static gateway bundle: %w", err)
	}
	providers, err := instantiateStaticProviders(bundle)
	if err != nil {
		return nil, err
	}
	cfg.Providers = providers
	cfg.ManagedModels = append([]modelcatalog.ManagedModel(nil), bundle.ManagedModels...)
	cfg.Routes = append([]routepkg.AgentRoute(nil), bundle.Routes...)
	return cfg, nil
}

func instantiateStaticProviders(bundle *gatewaybundle.GatewayBundle) (map[string]provider.Provider, error) {
	out := make(map[string]provider.Provider, len(bundle.Providers))
	for _, cfg := range bundle.Providers {
		cfg = provider.NormalizeConfig(cfg, cfg.Id, cfg.ProviderType)
		prov, err := provider.NewProvider(cfg)
		if err != nil {
			return nil, fmt.Errorf("init static provider %q: %w", cfg.Id, err)
		}
		out[cfg.Id] = prov
	}
	return out, nil
}

func registerStaticProviderCredentials(ctx context.Context, credentialManager *credentialmgr.Manager, providers map[string]provider.Provider) error {
	if credentialManager == nil {
		return nil
	}
	for providerID, prov := range providers {
		if prov == nil {
			continue
		}
		cred := provider.ProviderConfigAPIKeyCredential(prov.Config(), providerID)
		if cred == nil {
			continue
		}
		if err := credentialManager.RegisterCredential(credentialmgr.WithSkipPersist(ctx), cred); err != nil {
			return fmt.Errorf("register static credential for provider %q: %w", providerID, err)
		}
	}
	return nil
}

func newLLMAPIHandlers(logger *zap.Logger) map[string]dispatcher.LLMApiHandler {
	openAIHandler := openaiapi.NewHandler()
	openAIHandler.SetLogger(logger.Named("openai"))
	anthropicHandler := anthropicapi.NewHandler(nil)
	anthropicHandler.SetLogger(logger.Named("anthropic"))
	return map[string]dispatcher.LLMApiHandler{
		openAIHandler.Name():    openAIHandler,
		anthropicHandler.Name(): anthropicHandler,
	}
}

func newRouter(adminHandler http.Handler, dispatchHandler http.Handler) http.Handler {
	router := baseRouter()
	mountAdmin(router, adminHandler)
	router.NoRoute(gin.WrapH(dispatchHandler))
	return router
}

func newGatewayRouter(dispatchHandler http.Handler) http.Handler {
	router := baseRouter()
	router.NoRoute(gin.WrapH(dispatchHandler))
	return router
}

func newAdminRouter(adminHandler http.Handler) http.Handler {
	router := baseRouter()
	mountAdmin(router, adminHandler)
	return router
}

func baseRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	return router
}

func mountAdmin(router *gin.Engine, adminHandler http.Handler) {
	router.Any("/admin", gin.WrapH(adminHandler))
	router.Any("/admin/*path", gin.WrapH(adminHandler))
}

func shutdownServers(ctx context.Context, servers []*http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	var out error
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			out = errors.Join(out, err)
		}
	}
	return out
}
