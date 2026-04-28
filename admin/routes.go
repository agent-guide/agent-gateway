package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	dispatcherpkg "github.com/agent-guide/caddy-agent-gateway/dispatcher"
	"github.com/agent-guide/caddy-agent-gateway/gateway"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	virtualkeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/virtualkey"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"gorm.io/gorm"
)

const defaultRouteTag = ""
const generatedVirtualKeyPrefix = "vk-"

type VirtualKeyView struct {
	virtualkeypkg.VirtualKey
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

type ProviderTypeView struct {
	ProviderType string `json:"provider_type"`
	Enabled      bool   `json:"enabled"`
}

type LLMApiHandlerTypeView struct {
	LLMApiHandlerType string `json:"llm_api_handler_type"`
	Enabled           bool   `json:"enabled"`
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

		// Provider names
		{Method: http.MethodGet, Path: "/admin/provider_types", Handler: h.handleListProviderTypes, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/provider_types/{provider_type}/enable", Handler: h.handleEnableProviderType, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/provider_types/{provider_type}/disable", Handler: h.handleDisableProviderType, RequireAuth: true},

		// LLM API handler types
		{Method: http.MethodGet, Path: "/admin/llm_api_handler_types", Handler: h.handleListLLMApiHandlerTypes, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/llm_api_handler_types/{llm_api_handler_type}/enable", Handler: h.handleEnableLLMApiHandlerType, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/llm_api_handler_types/{llm_api_handler_type}/disable", Handler: h.handleDisableLLMApiHandlerType, RequireAuth: true},

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
		{Method: http.MethodGet, Path: "/admin/virtual_keys", Handler: h.handleListVirtualKeys, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/virtual_keys", Handler: h.handleCreateVirtualKey, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/virtual_keys/{key}", Handler: h.handleGetVirtualKey, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/virtual_keys/{key}", Handler: h.handleUpdateVirtualKey, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/virtual_keys/{key}/enable", Handler: h.handleEnableVirtualKey, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/virtual_keys/{key}/disable", Handler: h.handleDisableVirtualKey, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/virtual_keys/{key}", Handler: h.handleDeleteVirtualKey, RequireAuth: true},
		// Credentials (api_key and cliauth)
		{Method: http.MethodGet, Path: "/admin/credentials", Handler: h.handleListCredentials, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/credentials", Handler: h.handleCreateCredential, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/credentials/{credential_id}", Handler: h.handleGetCredential, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/credentials/{credential_id}", Handler: h.handleUpdateCredential, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/credentials/{credential_id}", Handler: h.handleDeleteCredential, RequireAuth: true},

		// CLI Auth Authenticators
		{Method: http.MethodGet, Path: "/admin/cliauth/authenticators", Handler: h.handleListCLIAuthAuthenticators, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/cliauth/authenticators/{authenticator_name}", Handler: h.handleGetCLIAuthAuthenticator, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/authenticators/{authenticator_name}/enable", Handler: h.handleEnableCLIAuthAuthenticator, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/authenticators/{authenticator_name}/disable", Handler: h.handleDisableCLIAuthAuthenticator, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/authenticators/{authenticator_name}/login", Handler: h.handleStartCLIAuthAuthenticatorLogin, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/cliauth/refresher", Handler: h.handleGetCLIAuthRefresherStatus, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/refresher/enable", Handler: h.handleEnableCLIAuthRefresher, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/cliauth/refresher/disable", Handler: h.handleDisableCLIAuthRefresher, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/cliauth/logins/{login_id}", Handler: h.handleGetCLIAuthLoginStatus, RequireAuth: true},

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
		ProviderType: r.URL.Query().Get("provider_type"),
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

func (h *Handler) handleListProviderTypes(w http.ResponseWriter, r *http.Request) {
	names := provider.ListProviderTypes()
	items := make([]ProviderTypeView, 0, len(names))
	for _, name := range names {
		enabled, ok := provider.IsProviderTypeEnabled(name)
		if !ok {
			continue
		}
		items = append(items, ProviderTypeView{
			ProviderType: name,
			Enabled:      enabled,
		})
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleListLLMApiHandlerTypes(w http.ResponseWriter, r *http.Request) {
	types := dispatcherpkg.ListLLMApiHandlerTypes()
	items := make([]LLMApiHandlerTypeView, 0, len(types))
	for _, handlerType := range types {
		enabled, ok := dispatcherpkg.IsLLMApiHandlerTypeEnabled(handlerType)
		if !ok {
			continue
		}
		items = append(items, LLMApiHandlerTypeView{
			LLMApiHandlerType: handlerType,
			Enabled:           enabled,
		})
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleEnableLLMApiHandlerType(w http.ResponseWriter, r *http.Request) {
	h.handleSetLLMApiHandlerTypeEnabled(w, r, true)
}

func (h *Handler) handleDisableLLMApiHandlerType(w http.ResponseWriter, r *http.Request) {
	h.handleSetLLMApiHandlerTypeEnabled(w, r, false)
}

func (h *Handler) handleSetLLMApiHandlerTypeEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	handlerType := strings.ToLower(strings.TrimSpace(r.PathValue("llm_api_handler_type")))
	if handlerType == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "llm_api_handler_type is required")
		return
	}

	var err error
	if enabled {
		err = dispatcherpkg.EnableLLMApiHandlerType(handlerType)
	} else {
		err = dispatcherpkg.DisableLLMApiHandlerType(handlerType)
	}
	if err != nil {
		_ = httpjson.Error(w, http.StatusNotFound, err.Error())
		return
	}

	status := "disabled"
	if enabled {
		status = "enabled"
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"status":               status,
		"llm_api_handler_type": handlerType,
		"enabled":              enabled,
	})
}

func (h *Handler) handleEnableProviderType(w http.ResponseWriter, r *http.Request) {
	h.handleSetProviderTypeEnabled(w, r, true)
}

func (h *Handler) handleDisableProviderType(w http.ResponseWriter, r *http.Request) {
	h.handleSetProviderTypeEnabled(w, r, false)
}

func (h *Handler) handleSetProviderTypeEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("provider_type")))
	if name == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "provider_type is required")
		return
	}

	var err error
	if enabled {
		err = provider.EnableProviderType(name)
	} else {
		err = provider.DisableProviderType(name)
	}
	if err != nil {
		_ = httpjson.Error(w, http.StatusNotFound, err.Error())
		return
	}

	status := "disabled"
	if enabled {
		status = "enabled"
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"status":        status,
		"provider_type": name,
		"enabled":       enabled,
	})
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
	if cfg.Id == "" || cfg.ProviderType == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "id and provider_type are required")
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
	if cfg.ProviderType == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "provider_type is required")
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

func (h *Handler) handleListVirtualKeys(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	tag := r.URL.Query().Get("tag")
	sessionUsername := h.sessionUsername(r)
	if sessionUsername == "" {
		_ = httpjson.Error(w, http.StatusForbidden, "forbidden")
		return
	}

	items, err := manager.List(r.Context(), virtualkeypkg.VirtualKeyListOptions{Tag: tag})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]VirtualKeyView, 0, len(items))
	for _, item := range items {
		views = append(views, virtualKeyViewFromKey(manager, item))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handleCreateVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	var key virtualkeypkg.VirtualKey
	if err := httpjson.Decode(r, &key); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	if h.sessionUsername(r) == "" {
		_ = httpjson.Error(w, http.StatusForbidden, "forbidden")
		return
	}

	generatedKey, err := generateVirtualKey()
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, "failed to generate virtual key")
		return
	}
	key.Key = generatedKey

	if err := manager.Create(r.Context(), key); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, key)
}

func generateVirtualKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return generatedVirtualKeyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func (h *Handler) handleGetVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	item, err := manager.Get(r.Context(), r.PathValue("key"))
	if err != nil {
		if errors.Is(err, virtualkeypkg.ErrVirtualKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, virtualKeyViewFromKey(manager, item))
}

func (h *Handler) handleUpdateVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	var key virtualkeypkg.VirtualKey
	if err := httpjson.Decode(r, &key); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	pathKey := r.PathValue("key")
	if key.Key == "" {
		key.Key = pathKey
	}
	if key.Key != pathKey {
		_ = httpjson.Error(w, http.StatusBadRequest, "virtual key in body must match path")
		return
	}

	if _, err := manager.Get(r.Context(), pathKey); err != nil {
		if errors.Is(err, virtualkeypkg.ErrVirtualKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
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

func (h *Handler) handleDeleteVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	key := r.PathValue("key")
	if err := manager.Delete(r.Context(), key); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}

func (h *Handler) handleEnableVirtualKey(w http.ResponseWriter, r *http.Request) {
	h.handleSetVirtualKeyDisabled(w, r, false)
}

func (h *Handler) handleDisableVirtualKey(w http.ResponseWriter, r *http.Request) {
	h.handleSetVirtualKeyDisabled(w, r, true)
}

func (h *Handler) handleSetVirtualKeyDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	keyID := r.PathValue("key")
	key, err := manager.Get(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, virtualkeypkg.ErrVirtualKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	key.Disabled = disabled

	if err := manager.Update(r.Context(), keyID, key); err != nil {
		if errors.Is(err, virtualkeypkg.ErrStaticVirtualKeyReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
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
	_ = httpjson.Write(w, http.StatusOK, virtualKeyViewFromKey(manager, updated))
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

func (h *Handler) virtualKeyStore() intf.VirtualKeyStorer {
	if h.configStore == nil {
		return nil
	}
	store, err := h.configStore.GetVirtualKeyStore(context.Background(), virtualkeypkg.DecodeStoredVirtualKey)
	if err != nil {
		return nil
	}
	return store
}

func (h *Handler) virtualKeyManagerForRoutes() *virtualkeypkg.VirtualKeyManager {
	if h.virtualKeyManager != nil {
		return h.virtualKeyManager
	}

	store := h.virtualKeyStore()
	if store == nil {
		return nil
	}
	return virtualkeypkg.NewVirtualKeyManager(store)
}

func virtualKeyViewFromKey(manager *virtualkeypkg.VirtualKeyManager, key virtualkeypkg.VirtualKey) VirtualKeyView {
	view := VirtualKeyView{
		VirtualKey: key,
		Source:     "store",
		ReadOnly:   false,
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
