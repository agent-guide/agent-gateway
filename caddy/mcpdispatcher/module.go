package mcpdispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	caddygateway "github.com/agent-guide/agent-gateway/caddy/gateway"
	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	mcpruntime "github.com/agent-guide/agent-gateway/pkg/mcp/runtime"
	mcpservice "github.com/agent-guide/agent-gateway/pkg/mcp/service"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Dispatcher{})
	httpcaddyfile.RegisterHandlerDirective("agent_mcp_dispatcher", parseMCPDispatcher)
}

type Dispatcher struct {
	Route mcproute.MCPRoute `json:"route,omitempty"`

	routeManager      *mcproute.Manager
	serviceManager    *mcpservice.Manager
	virtualKeyManager *virtualkeypkg.VirtualKeyManager
	runtimeRegistry   *mcpruntime.Registry
}

func (Dispatcher) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.agent_mcp_dispatcher",
		New: func() caddy.Module { return new(Dispatcher) },
	}
}

func (h *Dispatcher) Provision(ctx caddy.Context) error {
	app, err := caddygateway.GetApp(ctx)
	if err != nil {
		return fmt.Errorf("agent_mcp_dispatcher: get agent_gateway app: %w", err)
	}
	backend := app.ConfigStore()
	if backend == nil {
		return fmt.Errorf("agent_mcp_dispatcher: config store backend is not configured")
	}
	store, err := backend.Get(schema.StoreMCPServices)
	if err != nil {
		return fmt.Errorf("agent_mcp_dispatcher: get mcp service store: %w", err)
	}
	routeStore, err := backend.Get(schema.StoreMCPRoutes)
	if err != nil {
		return fmt.Errorf("agent_mcp_dispatcher: get mcp route store: %w", err)
	}
	h.Route.Normalize()
	h.routeManager = mcproute.NewManager(routeStore)
	h.routeManager.InitStaticRoutes([]mcproute.MCPRoute{h.Route})
	h.serviceManager = mcpservice.NewManager(store)
	h.virtualKeyManager = app.AgentGateway().VirtualKeyManager()
	h.runtimeRegistry = app.AgentGateway().MCPRuntimeRegistry()
	return nil
}

func (h Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if h.routeManager == nil {
		return next.ServeHTTP(w, r)
	}
	route, ok, err := h.routeManager.Match(r.Context(), r)
	if err != nil {
		return httpjson.Error(w, http.StatusBadGateway, err.Error())
	}
	if !ok {
		return next.ServeHTTP(w, r)
	}
	return h.dispatchJSONRPC(w, r, route)
}

func (h *Dispatcher) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "service_id":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.Route.ServiceID = d.Val()
			case "route_id":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.Route.ID = d.Val()
			case "path_prefix":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.Route.Match.PathPrefix = d.Val()
			case "require_virtual_key":
				h.Route.AuthPolicy.RequireVirtualKey = true
			default:
				return d.Errf("unrecognized agent_mcp_dispatcher option: %s", d.Val())
			}
		}
	}
	h.Route.Normalize()
	if h.Route.ServiceID == "" {
		return d.Err("agent_mcp_dispatcher requires service_id")
	}
	return nil
}

func parseMCPDispatcher(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var dispatcher Dispatcher
	if err := dispatcher.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return &dispatcher, nil
}

func (h Dispatcher) dispatchJSONRPC(w http.ResponseWriter, r *http.Request, route mcproute.MCPRoute) error {
	if r.Method != http.MethodPost {
		return httpjson.Error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
	if err := h.validateVirtualKey(r, route); err != nil {
		return writeMCPError(w, nil, http.StatusUnauthorized, -32001, err.Error())
	}

	var msg transport.Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		return writeMCPError(w, nil, http.StatusBadRequest, -32700, fmt.Sprintf("decode json-rpc request: %v", err))
	}
	if msg.JSONRPC == "" {
		msg.JSONRPC = "2.0"
	}
	if msg.JSONRPC != "2.0" {
		return writeMCPError(w, msg.ID, http.StatusBadRequest, -32600, "jsonrpc must be \"2.0\"")
	}
	if strings.TrimSpace(msg.Method) == "" {
		return writeMCPError(w, msg.ID, http.StatusBadRequest, -32600, "method is required")
	}
	if isNotification(msg) {
		return h.dispatchNotification(w, r, route, msg)
	}
	requestCtx, finishRequest := h.beginRequest(r.Context(), route, msg)
	defer finishRequest()

	switch msg.Method {
	case "initialize":
		result, err := h.serviceManager.Initialize(requestCtx, route.ServiceID)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "ping":
		return writeMCPResult(w, msg.ID, map[string]any{})
	case "roots/list":
		return writeMCPResult(w, msg.ID, map[string]any{"roots": []any{}})
	case "tools/list":
		cursor, err := stringParamFromParams(msg.Params, "cursor", false)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.ListToolsPage(requestCtx, route.ServiceID, cursor)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "resources/list":
		cursor, err := stringParamFromParams(msg.Params, "cursor", false)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.ListResourcesPage(requestCtx, route.ServiceID, cursor)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "resources/templates/list":
		cursor, err := stringParamFromParams(msg.Params, "cursor", false)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.ListResourceTemplatesPage(requestCtx, route.ServiceID, cursor)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "prompts/list":
		cursor, err := stringParamFromParams(msg.Params, "cursor", false)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.ListPromptsPage(requestCtx, route.ServiceID, cursor)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "tools/call":
		params, err := objectParams(msg.Params)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		name, err := requiredStringParam(params, "name")
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		args, err := mapParam(params, "arguments")
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.CallTool(requestCtx, route.ServiceID, strings.TrimSpace(name), args)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "resources/read":
		params, err := objectParams(msg.Params)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		uri, err := requiredStringParam(params, "uri")
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.ReadResource(requestCtx, route.ServiceID, strings.TrimSpace(uri))
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "prompts/get":
		params, err := objectParams(msg.Params)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		name, err := requiredStringParam(params, "name")
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		args, err := mapParam(params, "arguments")
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.GetPrompt(requestCtx, route.ServiceID, strings.TrimSpace(name), args)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	case "completion/complete":
		params, err := objectParams(msg.Params)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		ref, argument, args, err := decodeCompletionParams(params)
		if err != nil {
			return writeMCPError(w, msg.ID, http.StatusBadRequest, -32602, err.Error())
		}
		result, err := h.serviceManager.Complete(requestCtx, route.ServiceID, ref, argument, args)
		if err != nil {
			return h.writeRequestError(w, route, msg, err)
		}
		return writeMCPResult(w, msg.ID, result)
	default:
		return writeMCPError(w, msg.ID, http.StatusNotImplemented, -32601, fmt.Sprintf("mcp method %q is not implemented by this dispatcher", msg.Method))
	}
}

func (h Dispatcher) dispatchNotification(w http.ResponseWriter, r *http.Request, route mcproute.MCPRoute, msg transport.Message) error {
	switch msg.Method {
	case "notifications/initialized":
		if _, err := h.serviceManager.Initialize(r.Context(), route.ServiceID); err != nil {
			return httpjson.Error(w, http.StatusBadGateway, err.Error())
		}
	case "notifications/cancelled":
		cancelled, err := h.handleCancelledNotification(route, msg.Params)
		if err != nil {
			return httpjson.Error(w, http.StatusBadRequest, err.Error())
		}
		if cancelled {
			w.WriteHeader(http.StatusAccepted)
			return nil
		}
	case "notifications/progress":
		if err := h.handleProgressNotification(route, msg.Params); err != nil {
			return httpjson.Error(w, http.StatusBadRequest, err.Error())
		}
	case "notifications/message":
		// Accepted for now; there is no gateway-side log sink yet.
	default:
		// Unknown notifications are accepted but ignored to keep the dispatcher permissive.
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (h *Dispatcher) beginRequest(parent context.Context, route mcproute.MCPRoute, msg transport.Message) (context.Context, func()) {
	if h.runtimeRegistry == nil || msg.ID == nil {
		return parent, func() {}
	}
	return h.runtimeRegistry.BeginRequest(
		parent,
		resolvedRouteID(route),
		msg.ID,
		strings.TrimSpace(msg.Method),
		extractProgressToken(msg.Params),
	)
}

func (h *Dispatcher) handleCancelledNotification(route mcproute.MCPRoute, params any) (bool, error) {
	object, err := objectParams(params)
	if err != nil {
		return false, err
	}
	requestID, ok := object["requestId"]
	if !ok || requestID == nil {
		return false, fmt.Errorf("requestId is required")
	}
	reason, err := optionalStringParam(object, "reason")
	if err != nil {
		return false, err
	}
	return h.cancelRequest(route, requestID, reason)
}

func (h *Dispatcher) handleProgressNotification(route mcproute.MCPRoute, params any) error {
	object, err := objectParams(params)
	if err != nil {
		return err
	}
	token, ok := object["progressToken"]
	if !ok || token == nil {
		return fmt.Errorf("progressToken is required")
	}
	progress, err := numericParam(object, "progress", true)
	if err != nil {
		return err
	}
	total, err := numericParamPointer(object, "total")
	if err != nil {
		return err
	}
	message, err := optionalStringParam(object, "message")
	if err != nil {
		return err
	}
	h.storeProgressNotification(route, token, progress, total, message)
	return nil
}

func (h *Dispatcher) cancelRequest(route mcproute.MCPRoute, requestID any, reason string) (bool, error) {
	if h.runtimeRegistry == nil {
		return false, nil
	}
	return h.runtimeRegistry.Cancel(resolvedRouteID(route), requestID, reason)
}

func (h *Dispatcher) storeProgressNotification(route mcproute.MCPRoute, token any, progress float64, total *float64, message string) {
	if h.runtimeRegistry != nil {
		h.runtimeRegistry.StoreProgress(resolvedRouteID(route), token, progress, total, message)
	}
}

func (h *Dispatcher) writeRequestError(w http.ResponseWriter, route mcproute.MCPRoute, msg transport.Message, err error) error {
	if errors.Is(err, context.Canceled) {
		reason := h.cancelReason(route, msg.ID)
		message := "request cancelled"
		if reason != "" {
			message += ": " + reason
		}
		return writeMCPErrorWithData(w, msg.ID, http.StatusOK, -32000, message, map[string]any{
			"cancelled": true,
			"method":    msg.Method,
		})
	}
	return writeMCPError(w, msg.ID, http.StatusBadGateway, -32002, err.Error())
}

func (h *Dispatcher) cancelReason(route mcproute.MCPRoute, requestID any) string {
	if h.runtimeRegistry != nil {
		return h.runtimeRegistry.CancelReason(resolvedRouteID(route), requestID)
	}
	return ""
}

func (h Dispatcher) validateVirtualKey(r *http.Request, route mcproute.MCPRoute) error {
	if !route.AuthPolicy.RequireVirtualKey {
		return nil
	}
	if h.virtualKeyManager == nil {
		return fmt.Errorf("virtual key manager is not configured")
	}
	rawKey := virtualkeypkg.ExtractAPIKey(r)
	if rawKey == "" {
		return fmt.Errorf("virtual key is required")
	}
	key, err := h.virtualKeyManager.GetByKey(r.Context(), rawKey)
	if err != nil {
		return fmt.Errorf("invalid virtual key")
	}
	routeID := strings.TrimSpace(route.ID)
	if routeID == "" {
		routeID = "mcp:" + route.ServiceID
	}
	return key.ValidateForRoute(routeID)
}

func writeMCPResult(w http.ResponseWriter, id any, result any) error {
	return httpjson.Write(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func isNotification(msg transport.Message) bool {
	return msg.ID == nil && strings.HasPrefix(strings.TrimSpace(msg.Method), "notifications/")
}

func objectParams(src any) (map[string]any, error) {
	if src == nil {
		return map[string]any{}, nil
	}
	params, ok := src.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("params must be an object")
	}
	return params, nil
}

func stringParamFromParams(src any, key string, required bool) (string, error) {
	params, err := objectParams(src)
	if err != nil {
		return "", err
	}
	if required {
		return requiredStringParam(params, key)
	}
	if _, ok := params[key]; !ok {
		return "", nil
	}
	return optionalStringParam(params, key)
}

func requiredStringParam(params map[string]any, key string) (string, error) {
	value, err := optionalStringParam(params, key)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func optionalStringParam(params map[string]any, key string) (string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(value), nil
}

func mapParam(params map[string]any, key string) (map[string]any, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return value, nil
}

func numericParam(params map[string]any, key string, required bool) (float64, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		if required {
			return 0, fmt.Errorf("%s is required", key)
		}
		return 0, nil
	}
	value, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	return value, nil
}

func numericParamPointer(params map[string]any, key string) (*float64, error) {
	if _, ok := params[key]; !ok {
		return nil, nil
	}
	value, err := numericParam(params, key, false)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func extractProgressToken(params any) any {
	object, err := objectParams(params)
	if err != nil {
		return nil
	}
	meta, err := mapParam(object, "_meta")
	if err != nil || meta == nil {
		return nil
	}
	token, ok := meta["progressToken"]
	if !ok || token == nil {
		return nil
	}
	return token
}

func resolvedRouteID(route mcproute.MCPRoute) string {
	routeID := strings.TrimSpace(route.ID)
	if routeID != "" {
		return routeID
	}
	return "mcp:" + strings.TrimSpace(route.ServiceID)
}

func decodeCompletionParams(params map[string]any) (basemcp.CompletionReference, basemcp.CompletionArgument, map[string]string, error) {
	refMap, err := mapParam(params, "ref")
	if err != nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, err
	}
	if refMap == nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("ref is required")
	}
	refType, err := requiredStringParam(refMap, "type")
	if err != nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("ref: %w", err)
	}
	ref := basemcp.CompletionReference{Type: refType}
	switch refType {
	case "ref/prompt":
		ref.Name, err = requiredStringParam(refMap, "name")
		if err != nil {
			return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("ref: %w", err)
		}
	case "ref/resource":
		ref.URI, err = requiredStringParam(refMap, "uri")
		if err != nil {
			return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("ref: %w", err)
		}
	default:
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("ref.type %q is not supported", refType)
	}

	argumentMap, err := mapParam(params, "argument")
	if err != nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, err
	}
	if argumentMap == nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("argument is required")
	}
	argumentName, err := requiredStringParam(argumentMap, "name")
	if err != nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("argument: %w", err)
	}
	argumentValue, err := optionalStringParam(argumentMap, "value")
	if err != nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("argument: %w", err)
	}

	argumentsMap, err := mapParam(params, "arguments")
	if err != nil {
		return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, err
	}
	arguments := make(map[string]string, len(argumentsMap))
	for key, raw := range argumentsMap {
		value, ok := raw.(string)
		if !ok {
			return basemcp.CompletionReference{}, basemcp.CompletionArgument{}, nil, fmt.Errorf("arguments.%s must be a string", key)
		}
		arguments[key] = value
	}

	return ref, basemcp.CompletionArgument{
		Name:  argumentName,
		Value: argumentValue,
	}, arguments, nil
}

func writeMCPError(w http.ResponseWriter, id any, status int, code int, message string) error {
	return writeMCPErrorWithData(w, id, status, code, message, nil)
}

func writeMCPErrorWithData(w http.ResponseWriter, id any, status int, code int, message string, data any) error {
	return httpjson.Write(w, status, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": &transport.Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
	})
}

var (
	_ caddy.Module                = (*Dispatcher)(nil)
	_ caddy.Provisioner           = (*Dispatcher)(nil)
	_ caddyhttp.MiddlewareHandler = (*Dispatcher)(nil)
	_ caddyfile.Unmarshaler       = (*Dispatcher)(nil)
)
