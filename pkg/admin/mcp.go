package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/internal/strictjson"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	connectorpkg "github.com/agent-guide/agent-gateway/pkg/connector"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	mcproute "github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	mcptracking "github.com/agent-guide/agent-gateway/pkg/mcp/tracking"
)

type MCPRouteView struct {
	mcproute.MCPRoute
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

// MarshalJSON merges the view fields into the embedded config JSON. Without
// this the embedded MCPRoute.MarshalJSON is promoted and silently drops
// source and read_only from admin responses.
func (v MCPRouteView) MarshalJSON() ([]byte, error) {
	return marshalRouteView(v.MCPRoute, v.Source, v.ReadOnly)
}

func (v *MCPRouteView) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	delete(obj, "source")
	delete(obj, "read_only")
	body, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &v.MCPRoute); err != nil {
		return err
	}
	return unmarshalRouteViewExtras(data, &v.Source, &v.ReadOnly)
}

// marshalRouteView marshals a route config through its own MarshalJSON and
// grafts the admin view fields onto the resulting object.
func marshalRouteView(config any, source string, readOnly bool) ([]byte, error) {
	base, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	readOnlyJSON, err := json.Marshal(readOnly)
	if err != nil {
		return nil, err
	}
	merged["source"] = sourceJSON
	merged["read_only"] = readOnlyJSON
	return json.Marshal(merged)
}

func unmarshalRouteViewExtras(data []byte, source *string, readOnly *bool) error {
	var extras struct {
		Source   string `json:"source"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := json.Unmarshal(data, &extras); err != nil {
		return err
	}
	*source = extras.Source
	*readOnly = extras.ReadOnly
	return nil
}

type ConnectorView struct {
	connectorpkg.ConnectorConfig
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

type MCPDispatcherRuntimeView struct {
	InFlight []mcptracking.InFlightRequest      `json:"in_flight"`
	Progress []mcptracking.ProgressNotification `json:"progress"`
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

func (h *Handler) handleGetConnectorCapabilities(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	item, err := manager.Initialize(r.Context(), id)
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, item)
}

func (h *Handler) handlerListConnectors(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := manager.List(r.Context())
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]ConnectorView, 0, len(items))
	for _, item := range items {
		views = append(views, ConnectorView{ConnectorConfig: item, Source: "config_store"})
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handlerAddConnector(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var cfg connectorpkg.ConnectorConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	cfg.Normalize()
	if err := manager.Create(r.Context(), cfg); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := manager.Get(r.Context(), cfg.ID)
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, ConnectorView{ConnectorConfig: created, Source: "config_store"})
}

func (h *Handler) handlerGetConnector(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cfg, err := manager.Get(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, ConnectorView{ConnectorConfig: cfg, Source: "config_store"})
}

func (h *Handler) handlerUpdateConnector(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	clientID := strings.TrimSpace(r.PathValue("id"))
	var cfg connectorpkg.ConnectorConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if err := manager.Update(r.Context(), clientID, cfg); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) || errors.Is(err, configstore.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := manager.Get(r.Context(), clientID)
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, ConnectorView{ConnectorConfig: updated, Source: "config_store"})
}

func (h *Handler) handlerRemoveConnector(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err := manager.Delete(r.Context(), strings.TrimSpace(r.PathValue("id"))); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
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
	var route mcproute.MCPRoute
	if err := httpjson.Decode(r, &route); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if !route.CreatedAt.IsZero() || !route.UpdatedAt.IsZero() {
		_ = httpjson.Error(w, http.StatusBadRequest, "created_at and updated_at are managed by the server and must be omitted")
		return
	}
	route.Normalize()
	if route.ConnectorID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "connector_id is required")
		return
	}
	cfg, err := route.ToConfig()
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := resolver.CreateConfig(r.Context(), cfg, ""); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, mcproute.ErrStaticRouteReadOnly) || errors.Is(err, routecore.ErrAmbiguousRoute) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, mcproute.ErrInvalidRouteID) {
			_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
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
	var route mcproute.MCPRoute
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
	if route.ConnectorID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "connector_id is required")
		return
	}
	cfg, err := route.ToConfig()
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := resolver.UpdateConfig(r.Context(), id, cfg); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, mcproute.ErrStaticRouteReadOnly) || errors.Is(err, routecore.ErrAmbiguousRoute) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, configstore.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp route not found")
			return
		}
		if errors.Is(err, mcproute.ErrInvalidRouteID) {
			_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
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
	remove := resolver.DeleteConfig
	if h.agentManager != nil {
		remove = func(ctx context.Context, id string) error {
			return h.agentManager.DeleteUnreferencedMCPRoute(ctx, id, resolver.DeleteConfig)
		}
	}
	if err := remove(r.Context(), id); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, mcproute.ErrStaticRouteReadOnly) || errors.Is(err, routecore.ErrAmbiguousRoute) || errors.Is(err, agentpkg.ErrMCPRouteReferenced) {
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

func (h *Handler) handleListMCPDispatcherHistory(w http.ResponseWriter, r *http.Request) {
	if h.mcpRuntimeRegistry == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "mcp dispatcher runtime is not configured")
		return
	}
	routeID := strings.TrimSpace(r.URL.Query().Get("route_id"))
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"items": h.mcpRuntimeRegistry.ListHistory(routeID),
	})
}

func (h *Handler) handleListMCPTools(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	tools, err := manager.ListTools(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": tools})
}

func (h *Handler) handleCallMCPTool(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var req MCPToolCallRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	lease, err := manager.Acquire(r.Context(), id)
	if err != nil {
		// Execution errors deliberately hide management details. Use the
		// manager's current view to distinguish absence from recovery/admission.
		_, lookupErr := manager.Get(r.Context(), id)
		if writeControlPlaneError(w, lookupErr) {
			return
		}
		if errors.Is(lookupErr, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "connector not found")
			return
		}
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "connector unavailable")
		return
	}
	defer lease.Close()
	outcome, err := lease.CallTool(r.Context(), cr.ToolCall{Name: strings.TrimSpace(req.Name), Arguments: req.Arguments})
	result := outcome.Result
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		status := http.StatusBadGateway
		var callErr *cr.CallError
		if errors.As(err, &callErr) {
			switch callErr.Category {
			case "invalid_arguments", "unknown_tool", "unsupported_capability":
				status = http.StatusBadRequest
			}
		}
		_ = httpjson.Error(w, status, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, result)
}

func (h *Handler) handleListMCPResources(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := manager.ListResources(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, rewriteMCPDiscoveryError(err, "resources"))
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleListMCPResourceTemplates(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := manager.ListResourceTemplates(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, rewriteMCPDiscoveryError(err, "resource_templates"))
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleReadMCPResource(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var req MCPResourceReadRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	result, err := manager.ReadResource(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(req.URI), nil)
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, result)
}

func (h *Handler) handleListMCPPrompts(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	items, err := manager.ListPrompts(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetMCPPrompt(w http.ResponseWriter, r *http.Request) {
	manager, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var req MCPPromptGetRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	result, err := manager.GetPrompt(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(req.Name), req.Arguments, nil)
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, connectorpkg.ErrConnectorNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "mcp service not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, result)
}

// GatewaySessionView is the Admin API representation of a GatewaySession.
// The upstream_session_id field is masked to hide transport-internal identifiers.
type GatewaySessionView struct {
	ID                string                     `json:"id"`
	ConnectorID       string                     `json:"connector_id"`
	UpstreamSessionID string                     `json:"upstream_session_id,omitempty"`
	Transport         connectorpkg.TransportType `json:"transport"`
	State             connectormcp.SessionState  `json:"state"`
	CreatedAt         time.Time                  `json:"created_at"`
	LastUsedAt        time.Time                  `json:"last_used_at"`
}

func gatewaySessionView(s connectormcp.GatewaySession) GatewaySessionView {
	masked := s.UpstreamSessionID
	if len(masked) > 8 {
		masked = masked[:8] + "****"
	}
	return GatewaySessionView{
		ID:                s.ID,
		ConnectorID:       s.ConnectorID,
		UpstreamSessionID: masked,
		Transport:         s.Transport,
		State:             s.State,
		CreatedAt:         s.CreatedAt,
		LastUsedAt:        s.LastUsedAt,
	}
}

func (h *Handler) handleListConnectorSessions(w http.ResponseWriter, r *http.Request) {
	manager := h.sharedConnectorManager
	id := strings.TrimSpace(r.PathValue("id"))
	var view *GatewaySessionView
	if manager != nil {
		cfg, err := manager.Get(r.Context(), id)
		if err != nil {
			if writeControlPlaneError(w, err) {
				return
			}
			_ = httpjson.Error(w, 404, "connector not found")
			return
		}
		if cfg.Type != "mcp" {
			_ = httpjson.Error(w, 501, "HTTP connectors have no upstream MCP sessions")
			return
		}
		if s := manager.GetGatewaySession(id); s != nil {
			v := gatewaySessionView(*s)
			view = &v
		}
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"session": view})
}

func mcpRouteViewFromConfig(resolver *mcproute.MCPRouteResolver, cfg mcproute.RouteConfig) MCPRouteView {
	item, _ := mcproute.NewMCPRouteFromConfig(cfg)
	view := MCPRouteView{
		MCPRoute: item,
		Source:   "store",
		ReadOnly: false,
	}
	if resolver != nil {
		if configManager := resolver.ConfigManager(); configManager != nil && configManager.IsStatic(cfg.ID) {
			view.Source = "caddyfile"
			view.ReadOnly = true
		}
	}
	return view
}

func rewriteMCPDiscoveryError(err error, family string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if !strings.Contains(msg, "Method not found") {
		return msg
	}
	switch family {
	case "resources", "resource_templates":
		return msg + "; upstream MCP service does not expose resource discovery methods, try tools/list instead"
	default:
		return msg
	}
}

func (v *ConnectorView) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	if raw, ok := obj["source"]; ok {
		if err := json.Unmarshal(raw, &v.Source); err != nil {
			return err
		}
		delete(obj, "source")
	}
	if raw, ok := obj["read_only"]; ok {
		if err := json.Unmarshal(raw, &v.ReadOnly); err != nil {
			return err
		}
		delete(obj, "read_only")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &v.ConnectorConfig)
}

func (h *Handler) handleConnectorState(w http.ResponseWriter, r *http.Request) {
	m, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, 503, "connector manager unavailable")
		return
	}
	state, err := m.State(r.Context(), r.PathValue("id"))
	if err != nil {
		_ = httpjson.Error(w, 404, "connector not found")
		return
	}
	_ = httpjson.Write(w, 200, state)
}

func (h *Handler) handleConnectorMutation(w http.ResponseWriter, r *http.Request) {
	m, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, 503, "connector manager unavailable")
		return
	}
	var pending any
	if p, ok := m.PendingMutation(r.PathValue("id")); ok {
		pending = p
	}
	_ = httpjson.Write(w, 200, map[string]any{"id": r.PathValue("id"), "pending_mutation": pending})
}
func (h *Handler) handleConnectorRecover(w http.ResponseWriter, r *http.Request) {
	m, err := h.connectorManager()
	if err != nil {
		_ = httpjson.Error(w, 503, "connector manager unavailable")
		return
	}
	if err = m.Recover(r.Context(), r.PathValue("id")); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		_ = httpjson.Error(w, 500, "connector recovery failed")
		return
	}
	h.handleConnectorMutation(w, r)
}

func (r *MCPToolCallRequest) UnmarshalJSON(data []byte) error {
	type plain MCPToolCallRequest
	p := plain{Arguments: map[string]any{}}
	if err := strictjson.Decode(data, &p, 4<<20, 64); err != nil {
		return err
	}
	if p.Arguments == nil {
		return fmt.Errorf("arguments must be object")
	}
	*r = MCPToolCallRequest(p)
	return nil
}
