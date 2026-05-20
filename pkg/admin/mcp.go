package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	mcproute "github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	mcpruntime "github.com/agent-guide/agent-gateway/pkg/mcp/runtime"
	mcpservice "github.com/agent-guide/agent-gateway/pkg/mcp/service"
)

type MCPRouteView struct {
	mcproute.MCPRouteConfig
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

type MCPServiceView struct {
	mcpservice.MCPServiceConfig
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

type MCPDispatcherRuntimeView struct {
	InFlight []mcpruntime.InFlightRequest      `json:"in_flight"`
	Progress []mcpruntime.ProgressNotification `json:"progress"`
}

type MCPToolCallRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type MCPResourceReadRequest struct {
	URI string `json:"uri"`
}

type MCPPromptGetRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

func (h *Handler) handlerListMCPServices(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := manager.List(r.Context())
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]MCPServiceView, 0, len(items))
	for _, item := range items {
		views = append(views, MCPServiceView{MCPServiceConfig: item, Source: "config_store"})
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handlerAddMCPService(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var cfg mcpservice.MCPServiceConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	cfg.Normalize()
	if err := manager.Create(r.Context(), cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := manager.Get(r.Context(), cfg.ID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, MCPServiceView{MCPServiceConfig: created, Source: "config_store"})
}

func (h *Handler) handlerGetMCPService(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cfg, err := manager.Get(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, MCPServiceView{MCPServiceConfig: cfg, Source: "config_store"})
}

func (h *Handler) handlerUpdateMCPService(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	clientID := strings.TrimSpace(r.PathValue("id"))
	var cfg mcpservice.MCPServiceConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if err := manager.Update(r.Context(), clientID, cfg); err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) || errors.Is(err, configstore.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := manager.Get(r.Context(), clientID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, MCPServiceView{MCPServiceConfig: updated, Source: "config_store"})
}

func (h *Handler) handlerRemoveMCPService(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err := manager.Delete(r.Context(), strings.TrimSpace(r.PathValue("id"))); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) handleListMCPRoutes(w http.ResponseWriter, r *http.Request) {
	resolver, err := h.mcpRouteResolver()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := resolver.ListConfigs(r.Context(), mcproute.RouteListOptions{})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	items = filterRouteConfigsByKind(items, mcproute.RouteKindMCP)
	views := make([]MCPRouteView, 0, len(items))
	for _, item := range items {
		views = append(views, mcpRouteViewFromConfig(resolver, item))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handleCreateMCPRoute(w http.ResponseWriter, r *http.Request) {
	resolver, err := h.mcpRouteResolver()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var route mcproute.MCPRouteConfig
	if err := httpjson.Decode(r, &route); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if !route.CreatedAt.IsZero() || !route.UpdatedAt.IsZero() {
		_ = httpjson.Error(w, http.StatusBadRequest, "created_at and updated_at are managed by the server and must be omitted")
		return
	}
	route.Normalize()
	if route.ServiceID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "service_id is required")
		return
	}
	cfg, err := route.ToConfig()
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := resolver.CreateConfig(r.Context(), cfg, ""); err != nil {
		if errors.Is(err, mcproute.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := resolver.GetConfig(r.Context(), route.ID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, mcpRouteViewFromConfig(resolver, created))
}

func (h *Handler) handleGetMCPRoute(w http.ResponseWriter, r *http.Request) {
	resolver, err := h.mcpRouteResolver()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	item, err := resolver.GetConfig(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, mcproute.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item.Kind != mcproute.RouteKindMCP {
		_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, mcpRouteViewFromConfig(resolver, item))
}

func (h *Handler) handleUpdateMCPRoute(w http.ResponseWriter, r *http.Request) {
	resolver, err := h.mcpRouteResolver()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var route mcproute.MCPRouteConfig
	if err := httpjson.Decode(r, &route); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	id := r.PathValue("id")
	current, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, mcproute.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if current.Kind != mcproute.RouteKindMCP {
		_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
		return
	}
	if route.ID == "" {
		route.ID = id
	}
	if route.ID != id {
		_ = httpjson.Error(w, http.StatusBadRequest, "route id in body must match path")
		return
	}
	if !route.CreatedAt.IsZero() || !route.UpdatedAt.IsZero() {
		_ = httpjson.Error(w, http.StatusBadRequest, "created_at and updated_at are managed by the server and must be omitted")
		return
	}
	route.Normalize()
	if route.ServiceID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "service_id is required")
		return
	}
	cfg, err := route.ToConfig()
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := resolver.UpdateConfig(r.Context(), id, cfg); err != nil {
		if errors.Is(err, mcproute.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, configstore.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	item, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, mcpRouteViewFromConfig(resolver, item))
}

func (h *Handler) handleDeleteMCPRoute(w http.ResponseWriter, r *http.Request) {
	resolver, err := h.mcpRouteResolver()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	id := r.PathValue("id")
	item, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, mcproute.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item.Kind != mcproute.RouteKindMCP {
		_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
		return
	}
	if err := resolver.DeleteConfig(r.Context(), id); err != nil {
		if errors.Is(err, mcproute.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleGetMCPDispatcherRuntime(w http.ResponseWriter, r *http.Request) {
	if h.mcpRuntimeRegistry == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "mcp dispatcher runtime is not configured")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, MCPDispatcherRuntimeView{
		InFlight: h.mcpRuntimeRegistry.ListInFlight(),
		Progress: h.mcpRuntimeRegistry.ListProgress(),
	})
}

func (h *Handler) handleListMCPDispatcherInFlight(w http.ResponseWriter, r *http.Request) {
	if h.mcpRuntimeRegistry == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "mcp dispatcher runtime is not configured")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"items": h.mcpRuntimeRegistry.ListInFlight(),
	})
}

func (h *Handler) handleListMCPDispatcherProgress(w http.ResponseWriter, r *http.Request) {
	if h.mcpRuntimeRegistry == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "mcp dispatcher runtime is not configured")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"items": h.mcpRuntimeRegistry.ListProgress(),
	})
}

func (h *Handler) handleListMCPTools(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	tools, err := manager.ListTools(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": tools})
}

func (h *Handler) handleCallMCPTool(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var req MCPToolCallRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	result, err := manager.CallTool(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(req.Name), req.Arguments)
	if err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, result)
}

func (h *Handler) handleListMCPResources(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := manager.ListResources(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleReadMCPResource(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var req MCPResourceReadRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	result, err := manager.ReadResource(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(req.URI))
	if err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, result)
}

func (h *Handler) handleListMCPPrompts(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := manager.ListPrompts(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetMCPPrompt(w http.ResponseWriter, r *http.Request) {
	manager, err := h.mcpServiceManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var req MCPPromptGetRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	result, err := manager.GetPrompt(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(req.Name), req.Arguments)
	if err != nil {
		if errors.Is(err, mcpservice.ErrServiceNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, result)
}

func mcpRouteViewFromConfig(resolver *mcproute.MCPRouteResolver, cfg mcproute.AgentRouteConfig) MCPRouteView {
	item, _ := mcproute.NewMCPRouteConfigFromConfig(cfg)
	view := MCPRouteView{
		MCPRouteConfig: item,
		Source:         "store",
		ReadOnly:       false,
	}
	if resolver != nil {
		if configManager := resolver.ConfigManager(); configManager != nil && configManager.IsStatic(cfg.ID) {
			view.Source = "caddyfile"
			view.ReadOnly = true
		}
	}
	return view
}
