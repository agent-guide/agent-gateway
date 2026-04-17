package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/gateway"
	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"gorm.io/gorm"
)

const defaultRouteTag = ""

type LocalAPIKeyView struct {
	localapikeypkg.LocalAPIKey
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

type RouteView struct {
	routepkg.AgentRoute
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

type ProviderView struct {
	provider.ProviderConfig
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

// Route defines an admin API route.
type Route struct {
	Method      string
	Path        string
	Handler     http.HandlerFunc
	RequireAuth bool
}

// Routes returns all admin API routes.
func (h *Handler) Routes() []Route {
	return []Route{
		// Health — public
		{Method: http.MethodGet, Path: "/admin/health", Handler: h.handleHealth},

		// Auth — login is public; logout and me require a valid session
		{Method: http.MethodPost, Path: "/admin/auth/login", Handler: h.handleLogin},
		{Method: http.MethodPost, Path: "/admin/auth/logout", Handler: h.handleLogout, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/auth/me", Handler: h.handleMe, RequireAuth: true},

		// Providers
		{Method: http.MethodGet, Path: "/admin/providers", Handler: h.handleListProviders, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/providers", Handler: h.handleCreateProvider, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/providers/{id}", Handler: h.handleGetProvider, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/providers/{id}", Handler: h.handleUpdateProvider, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/providers/{id}/enable", Handler: h.handleEnableProvider, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/providers/{id}/disable", Handler: h.handleDisableProvider, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/providers/{id}", Handler: h.handleDeleteProvider, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/routes", Handler: h.handleListRoutes, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/routes", Handler: h.handleCreateRoute, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/routes/{id}", Handler: h.handleGetRoute, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/routes/{id}", Handler: h.handleUpdateRoute, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/routes/{id}/enable", Handler: h.handleEnableRoute, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/routes/{id}/disable", Handler: h.handleDisableRoute, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/routes/{id}", Handler: h.handleDeleteRoute, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/local_api_keys", Handler: h.handleListLocalAPIKeys, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/local_api_keys", Handler: h.handleCreateLocalAPIKey, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/local_api_keys/{key}", Handler: h.handleGetLocalAPIKey, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/local_api_keys/{key}", Handler: h.handleUpdateLocalAPIKey, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/local_api_keys/{key}/enable", Handler: h.handleEnableLocalAPIKey, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/local_api_keys/{key}/disable", Handler: h.handleDisableLocalAPIKey, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/local_api_keys/{key}", Handler: h.handleDeleteLocalAPIKey, RequireAuth: true},
		// Credentials (api_key and cliauth)
		{Method: http.MethodGet, Path: "/admin/credentials", Handler: h.handleListCredentials, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/credentials", Handler: h.handleCreateCredential, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/credentials/{credential_id}", Handler: h.handleGetCredential, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/credentials/{credential_id}", Handler: h.handleUpdateCredential, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/credentials/{credential_id}", Handler: h.handleDeleteCredential, RequireAuth: true},

		// CLI Auth Authenticators
		{Method: http.MethodGet, Path: "/admin/cliauth/authenticators", Handler: h.handleListCLIAuthAuthenticators, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/authenticators/{authenticator_name}/enable", Handler: h.handleEnableCLIAuthAuthenticator, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/authenticators/{authenticator_name}/disable", Handler: h.handleDisableCLIAuthAuthenticator, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/authenticators/{authenticator_name}/login", Handler: h.handleStartCLIAuthAuthenticatorLogin, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/cliauth/authenticators/{authenticator_name}/login/status", Handler: h.handleGetCLIAuthAuthenticatorLoginStatus, RequireAuth: true},

		// MCP
		{Method: http.MethodGet, Path: "/admin/mcp/clients", Handler: h.handleListMCPClients, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/mcp/clients", Handler: h.handleAddMCPClient, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/mcp/clients/{id}", Handler: h.handleGetMCPClient, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/mcp/clients/{id}", Handler: h.handleUpdateMCPClient, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/mcp/clients/{id}", Handler: h.handleRemoveMCPClient, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/mcp/clients/{id}/tools", Handler: h.handleListMCPTools, RequireAuth: true},

		// Memory
		{Method: http.MethodGet, Path: "/admin/memory/config", Handler: h.handleGetMemoryConfig, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/memory/config", Handler: h.handleSetMemoryConfig, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/memory/search", Handler: h.handleSearchMemory, RequireAuth: true},

		// Agents
		{Method: http.MethodGet, Path: "/admin/agents", Handler: h.handleListAgents, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/agents", Handler: h.handleCreateAgent, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/agents/{id}", Handler: h.handleGetAgent, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/agents/{id}", Handler: h.handleUpdateAgent, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/agents/{id}", Handler: h.handleDeleteAgent, RequireAuth: true},

		// Metrics
		{Method: http.MethodGet, Path: "/admin/metrics", Handler: h.handleMetrics, RequireAuth: true},
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleListProviders(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	items, err := manager.ListConfigs(r.Context(), gateway.ProviderListOptions{
		ProviderName: r.URL.Query().Get("provider_name"),
	})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers := make([]ProviderView, 0, len(items))
	for _, cfg := range items {
		providers = append(providers, providerViewFromConfig(manager, cfg))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": providers})
}

func (h *Handler) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	var cfg provider.ProviderConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if cfg.Id == "" || cfg.ProviderName == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "id and provider_name are required")
		return
	}
	if err := manager.CreateConfig(r.Context(), cfg); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := manager.GetConfig(r.Context(), cfg.Id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, providerViewFromConfig(manager, created))
}

func (h *Handler) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	id := r.PathValue("id")
	cfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, gateway.ErrProviderNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, cfg))
}

func (h *Handler) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	var cfg provider.ProviderConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if cfg.ProviderName == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "provider_name is required")
		return
	}

	id := r.PathValue("id")
	if cfg.Id != "" && cfg.Id != id {
		_ = httpjson.Error(w, http.StatusBadRequest, "body id must match path id")
		return
	}
	cfg.Id = id
	if err := manager.UpdateConfig(r.Context(), id, cfg); err != nil {
		if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		if errors.Is(err, gateway.ErrStaticProviderReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updatedCfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, updatedCfg))
}

func (h *Handler) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	id := r.PathValue("id")
	if err := manager.DeleteConfig(r.Context(), id); err != nil {
		if errors.Is(err, gateway.ErrStaticProviderReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleEnableProvider(w http.ResponseWriter, r *http.Request) {
	h.handleSetProviderDisabled(w, r, false)
}

func (h *Handler) handleDisableProvider(w http.ResponseWriter, r *http.Request) {
	h.handleSetProviderDisabled(w, r, true)
}

func (h *Handler) handleSetProviderDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	id := r.PathValue("id")
	cfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, gateway.ErrProviderNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		if errors.Is(err, gateway.ErrProviderDisabled) && !disabled {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.Disabled = disabled

	if err := manager.UpdateConfig(r.Context(), id, cfg); err != nil {
		if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		if errors.Is(err, gateway.ErrStaticProviderReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updatedCfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, gateway.ErrProviderDisabled) && disabled {
			_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, cfg))
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, updatedCfg))
}

func (h *Handler) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	manager := h.routeManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "route manager is not configured")
		return
	}

	tagPrefix := r.URL.Query().Get("tag_prefix")
	tag := r.URL.Query().Get("tag")
	if tag == "" && tagPrefix == "" {
		tag = defaultRouteTag
	}

	items, err := manager.List(r.Context(), routepkg.RouteListOptions{Tag: tag, TagPrefix: tagPrefix})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]RouteView, 0, len(items))
	for _, item := range items {
		views = append(views, routeViewFromRoute(manager, item))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "route manager is not configured")
		return
	}

	var route routepkg.AgentRoute
	if err := httpjson.Decode(r, &route); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if route.ID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "id is required")
		return
	}
	if len(route.Targets) == 0 {
		_ = httpjson.Error(w, http.StatusBadRequest, "at least one target is required")
		return
	}
	route.Policy.Defaults()
	if err := route.ValidateDefinition(); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	tag := r.URL.Query().Get("tag")
	if tag == "" {
		tag = defaultRouteTag
	}

	if err := manager.Create(r.Context(), route, tag); err != nil {
		if errors.Is(err, routepkg.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, route)
}

func (h *Handler) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "route manager is not configured")
		return
	}

	item, err := manager.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, routepkg.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, routeViewFromRoute(manager, item))
}

func (h *Handler) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "route manager is not configured")
		return
	}

	var route routepkg.AgentRoute
	if err := httpjson.Decode(r, &route); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	id := r.PathValue("id")
	if route.ID == "" {
		route.ID = id
	}
	if route.ID != id {
		_ = httpjson.Error(w, http.StatusBadRequest, "route id in body must match path")
		return
	}
	if len(route.Targets) == 0 {
		_ = httpjson.Error(w, http.StatusBadRequest, "at least one target is required")
		return
	}
	route.Policy.Defaults()
	if err := route.ValidateDefinition(); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := manager.Update(r.Context(), id, route); err != nil {
		if errors.Is(err, routepkg.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	item, err := manager.Get(r.Context(), id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, item)
}

func (h *Handler) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "route manager is not configured")
		return
	}

	id := r.PathValue("id")
	if err := manager.Delete(r.Context(), id); err != nil {
		if errors.Is(err, routepkg.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleEnableRoute(w http.ResponseWriter, r *http.Request) {
	h.handleSetRouteDisabled(w, r, false)
}

func (h *Handler) handleDisableRoute(w http.ResponseWriter, r *http.Request) {
	h.handleSetRouteDisabled(w, r, true)
}

func (h *Handler) handleSetRouteDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	manager := h.routeManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "route manager is not configured")
		return
	}

	id := r.PathValue("id")
	item, err := manager.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, routepkg.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	item.Disabled = disabled

	if err := manager.Update(r.Context(), id, item); err != nil {
		if errors.Is(err, routepkg.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err := manager.Get(r.Context(), id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, routeViewFromRoute(manager, updated))
}

func (h *Handler) handleListLocalAPIKeys(w http.ResponseWriter, r *http.Request) {
	manager := h.localAPIKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "local api key manager not configured")
		return
	}

	userID := r.URL.Query().Get("user_id")
	sessionUsername := h.sessionUsername(r)
	if sessionUsername == "" {
		_ = httpjson.Error(w, http.StatusForbidden, "forbidden")
		return
	}
	if userID != "" && userID != sessionUsername {
		_ = httpjson.Error(w, http.StatusForbidden, "forbidden")
		return
	}
	userID = sessionUsername

	items, err := manager.List(r.Context(), localapikeypkg.LocalAPIKeyListOptions{UserID: userID})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]LocalAPIKeyView, 0, len(items))
	for _, item := range items {
		views = append(views, localAPIKeyViewFromKey(manager, item))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handleCreateLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	manager := h.localAPIKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "local api key manager not configured")
		return
	}

	var key localapikeypkg.LocalAPIKey
	if err := httpjson.Decode(r, &key); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	sessionUsername := h.sessionUsername(r)
	if sessionUsername == "" {
		_ = httpjson.Error(w, http.StatusForbidden, "forbidden")
		return
	}
	if key.UserID != "" && key.UserID != sessionUsername {
		_ = httpjson.Error(w, http.StatusForbidden, "forbidden")
		return
	}
	key.UserID = sessionUsername

	if key.Key == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "key is required")
		return
	}

	if err := manager.Create(r.Context(), key); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, key)
}

func (h *Handler) handleGetLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	manager := h.localAPIKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "local api key manager not configured")
		return
	}

	item, err := manager.Get(r.Context(), r.PathValue("key"))
	if err != nil {
		if errors.Is(err, localapikeypkg.ErrLocalAPIKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "local api key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, localAPIKeyViewFromKey(manager, item))
}

func (h *Handler) handleUpdateLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	manager := h.localAPIKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "local api key manager not configured")
		return
	}

	var key localapikeypkg.LocalAPIKey
	if err := httpjson.Decode(r, &key); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	pathKey := r.PathValue("key")
	if key.Key == "" {
		key.Key = pathKey
	}
	if key.Key != pathKey {
		_ = httpjson.Error(w, http.StatusBadRequest, "local api key in body must match path")
		return
	}

	if _, err := manager.Get(r.Context(), pathKey); err != nil {
		if errors.Is(err, localapikeypkg.ErrLocalAPIKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "local api key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := manager.Update(r.Context(), key.Key, key); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, key)
}

func (h *Handler) handleDeleteLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	manager := h.localAPIKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "local api key manager not configured")
		return
	}

	key := r.PathValue("key")
	if err := manager.Delete(r.Context(), key); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}

func (h *Handler) handleEnableLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	h.handleSetLocalAPIKeyDisabled(w, r, false)
}

func (h *Handler) handleDisableLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	h.handleSetLocalAPIKeyDisabled(w, r, true)
}

func (h *Handler) handleSetLocalAPIKeyDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	manager := h.localAPIKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "local api key manager not configured")
		return
	}

	keyID := r.PathValue("key")
	key, err := manager.Get(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, localapikeypkg.ErrLocalAPIKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "local api key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	key.Disabled = disabled

	if err := manager.Update(r.Context(), keyID, key); err != nil {
		if errors.Is(err, localapikeypkg.ErrStaticLocalAPIKeyReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "local api key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err := manager.Get(r.Context(), keyID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, localAPIKeyViewFromKey(manager, updated))
}

func (h *Handler) handleListMCPClients(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleAddMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleGetMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleUpdateMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleRemoveMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleListMCPTools(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleGetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleSetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleSearchMemory(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleListAgents(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) providerStore() intf.ProviderConfigStorer {
	if h.configStore == nil {
		return nil
	}
	providerConfigStore, err := h.configStore.GetProviderConfigStore(context.Background(), provider.DecodeStoredProviderConfig)
	if err != nil {
		return nil
	}
	return providerConfigStore
}

func (h *Handler) providerManagerForRoutes() *gateway.ProviderManager {
	if h.providerManager != nil {
		if !h.providerManager.IsConfigured() {
			return nil
		}
		return h.providerManager
	}

	store := h.providerStore()
	if store == nil {
		return nil
	}
	return gateway.NewProviderManager(store)
}

func (h *Handler) routeStore() intf.RouteStorer {
	if h.configStore == nil {
		return nil
	}
	store, err := h.configStore.GetRouteStore(context.Background(), routepkg.DecodeStoredRoute)
	if err != nil {
		return nil
	}
	return store
}

func (h *Handler) routeManagerForRoutes() *routepkg.AgentRouteManager {
	if h.routeManager != nil {
		return h.routeManager
	}

	store := h.routeStore()
	if store == nil {
		return nil
	}
	return routepkg.NewAgentRouteManager(store)
}

func (h *Handler) localAPIKeyStore() intf.LocalAPIKeyStorer {
	if h.configStore == nil {
		return nil
	}
	store, err := h.configStore.GetLocalAPIKeyStore(context.Background(), localapikeypkg.DecodeStoredLocalAPIKey)
	if err != nil {
		return nil
	}
	return store
}

func (h *Handler) localAPIKeyManagerForRoutes() *localapikeypkg.LocalAPIKeyManager {
	if h.localAPIKeyManager != nil {
		return h.localAPIKeyManager
	}

	store := h.localAPIKeyStore()
	if store == nil {
		return nil
	}
	return localapikeypkg.NewLocalAPIKeyManager(store)
}

func localAPIKeyViewFromKey(manager *localapikeypkg.LocalAPIKeyManager, key localapikeypkg.LocalAPIKey) LocalAPIKeyView {
	view := LocalAPIKeyView{
		LocalAPIKey: key,
		Source:      "store",
		ReadOnly:    false,
	}
	if manager != nil && manager.IsStatic(key.Key) {
		view.Source = "caddyfile"
		view.ReadOnly = true
	}
	return view
}

func routeViewFromRoute(manager *routepkg.AgentRouteManager, route routepkg.AgentRoute) RouteView {
	view := RouteView{
		AgentRoute: route,
		Source:     "store",
		ReadOnly:   false,
	}
	if manager != nil && manager.IsStatic(route.ID) {
		view.Source = "caddyfile"
		view.ReadOnly = true
	}
	return view
}

func providerViewFromConfig(manager *gateway.ProviderManager, cfg provider.ProviderConfig) ProviderView {
	view := ProviderView{
		ProviderConfig: cfg,
		Source:         "store",
		ReadOnly:       false,
	}
	if manager != nil && manager.IsStatic(cfg.Id) {
		view.Source = "caddyfile"
		view.ReadOnly = true
	}
	return view
}
