package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"gorm.io/gorm"
)

const defaultRouteTag = ""

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
		{Method: http.MethodDelete, Path: "/admin/providers/{id}", Handler: h.handleDeleteProvider, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/routes", Handler: h.handleListRoutes, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/routes", Handler: h.handleCreateRoute, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/routes/{id}", Handler: h.handleGetRoute, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/routes/{id}", Handler: h.handleUpdateRoute, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/routes/{id}", Handler: h.handleDeleteRoute, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/local_api_keys", Handler: h.handleListLocalAPIKeys, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/local_api_keys", Handler: h.handleCreateLocalAPIKey, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/local_api_keys/{key}", Handler: h.handleGetLocalAPIKey, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/local_api_keys/{key}", Handler: h.handleUpdateLocalAPIKey, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/local_api_keys/{key}", Handler: h.handleDeleteLocalAPIKey, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/credentials", Handler: h.handleListCredentials, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/credentials/{id}", Handler: h.handleGetCredential, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/credentials/{id}", Handler: h.handleDeleteCredential, RequireAuth: true},

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

		// CLI Auth
		{Method: http.MethodPost, Path: "/admin/cliauth/{cliname}", Handler: h.handleCLIAuth, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/cliauth/{cliname}/status", Handler: h.handleCLIAuthStatus, RequireAuth: true},

		// Caddy server management
		{Method: http.MethodGet, Path: "/admin/caddy/servers", Handler: h.handleListCaddyServers, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/caddy/servers", Handler: h.handleCreateCaddyServer, RequireAuth: true},
		{Method: http.MethodGet, Path: "/admin/caddy/servers/{id}", Handler: h.handleGetCaddyServer, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/caddy/servers/{id}", Handler: h.handleUpdateCaddyServer, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/caddy/servers/{id}", Handler: h.handleDeleteCaddyServer, RequireAuth: true},

		// Caddy route management (within a server)
		{Method: http.MethodGet, Path: "/admin/caddy/servers/{id}/routes", Handler: h.handleListCaddyRoutes, RequireAuth: true},
		{Method: http.MethodPost, Path: "/admin/caddy/servers/{id}/routes", Handler: h.handleAddCaddyRoute, RequireAuth: true},
		{Method: http.MethodPut, Path: "/admin/caddy/servers/{id}/routes/{routeId}", Handler: h.handleUpdateCaddyRoute, RequireAuth: true},
		{Method: http.MethodDelete, Path: "/admin/caddy/servers/{id}/routes/{routeId}", Handler: h.handleDeleteCaddyRoute, RequireAuth: true},
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleListProviders(w http.ResponseWriter, r *http.Request) {
	store := h.providerStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "provider store not configured")
		return
	}

	items, err := store.ListByName(r.Context(), r.URL.Query().Get("provider_name"))
	if err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers := make([]provider.ProviderConfig, 0, len(items))
	for _, item := range items {
		cfg, ok := item.(*provider.ProviderConfig)
		if !ok || cfg == nil {
			_ = utils.WriteError(w, http.StatusInternalServerError, "unexpected provider config type")
			return
		}
		providers = append(providers, *cfg)
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]any{"items": providers})
}

func (h *Handler) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	store := h.providerStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "provider store not configured")
		return
	}

	var cfg provider.ProviderConfig
	if err := utils.DecodeJSON(r, &cfg); err != nil {
		_ = utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if cfg.Id == "" || cfg.ProviderName == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "id and provider_name are required")
		return
	}
	id, err := store.Create(r.Context(), cfg.Id, cfg.ProviderName, &cfg)
	if err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.Id = id
	_ = utils.WriteJSON(w, http.StatusCreated, cfg)
}

func (h *Handler) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	store := h.providerStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "provider store not configured")
		return
	}

	id := r.PathValue("id")
	_, cfg, err := store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = utils.WriteError(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	providerCfg, ok := cfg.(*provider.ProviderConfig)
	if !ok || providerCfg == nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, "unexpected provider config type")
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, providerCfg)
}

func (h *Handler) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	store := h.providerStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "provider store not configured")
		return
	}

	var cfg provider.ProviderConfig
	if err := utils.DecodeJSON(r, &cfg); err != nil {
		_ = utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if cfg.ProviderName == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "provider_name is required")
		return
	}

	id := r.PathValue("id")
	if cfg.Id != "" && cfg.Id != id {
		_ = utils.WriteError(w, http.StatusBadRequest, "body id must match path id")
		return
	}
	cfg.Id = id
	if err := store.Update(r.Context(), id, &cfg); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = utils.WriteError(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, updatedCfg, err := store.Get(r.Context(), id)
	if err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	providerCfg, ok := updatedCfg.(*provider.ProviderConfig)
	if !ok || providerCfg == nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, "unexpected provider config type")
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, providerCfg)
}

func (h *Handler) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	store := h.providerStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "provider store not configured")
		return
	}

	id := r.PathValue("id")
	if err := store.Delete(r.Context(), id); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	store := h.routeStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "route store not configured")
		return
	}

	tagPrefix := r.URL.Query().Get("tag_prefix")
	tag := r.URL.Query().Get("tag")
	if tag == "" && tagPrefix == "" {
		tag = defaultRouteTag
	}

	var (
		items []any
		err   error
	)
	if tagPrefix != "" {
		items, err = store.ListByTagPrefix(r.Context(), tagPrefix)
	} else {
		items, err = store.ListByTag(r.Context(), tag)
	}
	if err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	store := h.routeStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "route store not configured")
		return
	}

	var route routepkg.Route
	if err := utils.DecodeJSON(r, &route); err != nil {
		_ = utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if route.ID == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "id is required")
		return
	}
	if route.Name == "" {
		route.Name = route.ID
	}
	if len(route.Targets) == 0 {
		_ = utils.WriteError(w, http.StatusBadRequest, "at least one target is required")
		return
	}
	route.Policy.Defaults()

	tag := r.URL.Query().Get("tag")
	if tag == "" {
		tag = defaultRouteTag
	}

	if err := store.Create(r.Context(), route.ID, tag, &route); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusCreated, route)
}

func (h *Handler) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	store := h.routeStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "route store not configured")
		return
	}

	item, err := store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = utils.WriteError(w, http.StatusNotFound, "route not found")
			return
		}
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	store := h.routeStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "route store not configured")
		return
	}

	var route routepkg.Route
	if err := utils.DecodeJSON(r, &route); err != nil {
		_ = utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	id := r.PathValue("id")
	if route.ID == "" {
		route.ID = id
	}
	if route.ID != id {
		_ = utils.WriteError(w, http.StatusBadRequest, "route id in body must match path")
		return
	}
	if route.Name == "" {
		route.Name = route.ID
	}
	if len(route.Targets) == 0 {
		_ = utils.WriteError(w, http.StatusBadRequest, "at least one target is required")
		return
	}
	route.Policy.Defaults()

	if err := store.Update(r.Context(), id, &route); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = utils.WriteError(w, http.StatusNotFound, "route not found")
			return
		}
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	item, err := store.Get(r.Context(), id)
	if err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	store := h.routeStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "route store not configured")
		return
	}

	id := r.PathValue("id")
	if err := store.Delete(r.Context(), id); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleListLocalAPIKeys(w http.ResponseWriter, r *http.Request) {
	store := h.localAPIKeyStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "local api key store not configured")
		return
	}

	userID := r.URL.Query().Get("user_id")
	sessionUsername := h.sessionUsername(r)
	if sessionUsername == "" {
		_ = utils.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if userID != "" && userID != sessionUsername {
		_ = utils.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	userID = sessionUsername

	items, err := store.ListByUserID(r.Context(), userID)
	if err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleCreateLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	store := h.localAPIKeyStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "local api key store not configured")
		return
	}

	var key routepkg.LocalAPIKey
	if err := utils.DecodeJSON(r, &key); err != nil {
		_ = utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	sessionUsername := h.sessionUsername(r)
	if sessionUsername == "" {
		_ = utils.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	if key.UserID != "" && key.UserID != sessionUsername {
		_ = utils.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	key.UserID = sessionUsername

	if key.Key == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "key is required")
		return
	}

	if err := store.Create(r.Context(), key.Key, key.UserID, &key); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusCreated, key)
}

func (h *Handler) handleGetLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	store := h.localAPIKeyStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "local api key store not configured")
		return
	}

	item, err := store.Get(r.Context(), r.PathValue("key"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = utils.WriteError(w, http.StatusNotFound, "local api key not found")
			return
		}
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) handleUpdateLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	store := h.localAPIKeyStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "local api key store not configured")
		return
	}

	var key routepkg.LocalAPIKey
	if err := utils.DecodeJSON(r, &key); err != nil {
		_ = utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	pathKey := r.PathValue("key")
	if key.Key == "" {
		key.Key = pathKey
	}
	if key.Key != pathKey {
		_ = utils.WriteError(w, http.StatusBadRequest, "local api key in body must match path")
		return
	}

	if _, err := store.Get(r.Context(), pathKey); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = utils.WriteError(w, http.StatusNotFound, "local api key not found")
			return
		}
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := store.Update(r.Context(), key.Key, &key); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, key)
}

func (h *Handler) handleDeleteLocalAPIKey(w http.ResponseWriter, r *http.Request) {
	store := h.localAPIKeyStore()
	if store == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "local api key store not configured")
		return
	}

	key := r.PathValue("key")
	if err := store.Delete(r.Context(), key); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}

func (h *Handler) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	provider := r.URL.Query().Get("provider")
	items := h.cliauthManager.List(provider)
	_ = utils.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetCredential(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	id := r.PathValue("id")
	item := h.cliauthManager.Get(id)
	if item == nil {
		_ = utils.WriteError(w, http.StatusNotFound, "credential not found")
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) handleListMCPClients(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleAddMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleGetMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleUpdateMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleRemoveMCPClient(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleListMCPTools(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleGetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleSetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleSearchMemory(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleListAgents(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}
func (h *Handler) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	_ = utils.WriteError(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	id := r.PathValue("id")
	if err := h.cliauthManager.Deregister(r.Context(), id); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
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

func (h *Handler) localAPIKeyStore() intf.LocalAPIKeyStorer {
	if h.configStore == nil {
		return nil
	}
	store, err := h.configStore.GetLocalAPIKeyStore(context.Background(), routepkg.DecodeStoredLocalAPIKey)
	if err != nil {
		return nil
	}
	return store
}
