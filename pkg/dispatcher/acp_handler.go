package dispatcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	acpruntime "github.com/agent-guide/agent-gateway/pkg/acp/runtime"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"go.uber.org/zap"
)

func (h *Handler) dispatchACP(w http.ResponseWriter, r *http.Request, next NextHandler, cfg routecore.AgentRouteConfig) error {
	if !h.acpEnabled {
		return serveNextOrNotFound(next, w, r)
	}
	routeResolver := h.gateway.ACPRouteResolver()
	if routeResolver == nil {
		return WriteDispatchError(h.logger, string(cfg.Protocol), cfg.ID, "", http.StatusServiceUnavailable, w, r, "resolve acp route", "acp route resolver is not configured", fmt.Errorf("acp route resolver is not configured"))
	}
	route, err := routeResolver.Resolve(r.Context(), cfg)
	if err != nil {
		return WriteDispatchError(h.logger, string(cfg.Protocol), cfg.ID, "", http.StatusBadGateway, w, r, "resolve acp route", "failed to resolve acp route", err)
	}
	if route == nil {
		return WriteDispatchError(h.logger, string(cfg.Protocol), cfg.ID, "", http.StatusServiceUnavailable, w, r, "resolve acp route", "acp route is not configured", fmt.Errorf("acp route %q is not configured", cfg.ID))
	}
	logRequestPhase(h.logger, "dispatcher: acp route resolved", r,
		zap.String("route_id", route.ID),
		zap.String("service_id", route.ServiceID),
		zap.String("path_prefix", route.MatchPolicy.PathPrefix),
	)

	rewritten := RewriteLLMRoutePath(r, route.MatchPolicy.PathPrefix)
	switch rewritten.URL.Path {
	case "/turn", "/permission":
	default:
		return serveNextOrNotFound(next, w, r)
	}
	if rewritten.Method != http.MethodPost {
		return httpjson.Error(w, http.StatusMethodNotAllowed, "method not allowed")
	}

	runtimeManager := h.gateway.ACPRuntimeManager()
	if runtimeManager == nil {
		return WriteDispatchError(h.logger, string(route.Protocol), route.ID, "", http.StatusServiceUnavailable, w, rewritten, "dispatch acp turn", "acp runtime manager is not configured", fmt.Errorf("acp runtime manager is not configured"))
	}

	if rewritten.URL.Path == "/permission" {
		return h.dispatchACPPermission(w, rewritten, runtimeManager)
	}

	var req acpruntime.TurnRequest
	if err := json.NewDecoder(rewritten.Body).Decode(&req); err != nil {
		return httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
	}
	if strings.TrimSpace(req.ThreadID) == "" {
		return httpjson.Error(w, http.StatusBadRequest, "thread_id is required")
	}
	if strings.TrimSpace(req.Input) == "" {
		return httpjson.Error(w, http.StatusBadRequest, "input is required")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	emit := newACPSSESink(w)

	if err := runtimeManager.ServeTurn(rewritten.Context(), route.ServiceID, req, emit); err != nil {
		_ = emit(acpruntime.TurnEvent{Event: "error", Message: err.Error()})
		return nil
	}
	return nil
}

// newACPSSESink builds the SSE EventSink used by the ACP turn handler. It writes
// one "event:/data:" frame per TurnEvent and flushes through the ResponseController
// Unwrap chain so frames reach the client incrementally; Caddy (v2.7+) wraps the
// ResponseWriter so a direct http.Flusher assertion no longer succeeds.
func newACPSSESink(w http.ResponseWriter) acpruntime.EventSink {
	flusher := NewResponseFlusher(w)
	return func(event acpruntime.TurnEvent) error {
		name := strings.TrimSpace(event.Event)
		if name == "" {
			name = "delta"
		}
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
}

// dispatchACPPermission answers one pending interactive permission request
// surfaced to the turn client as a "permission" SSE event.
func (h *Handler) dispatchACPPermission(w http.ResponseWriter, r *http.Request, runtimeManager *acpruntime.Manager) error {
	var decision acpruntime.PermissionDecision
	if err := json.NewDecoder(r.Body).Decode(&decision); err != nil {
		return httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
	}
	if err := runtimeManager.ResolvePermission(decision); err != nil {
		if errors.Is(err, acpruntime.ErrPermissionNotFound) {
			return httpjson.Error(w, http.StatusNotFound, "permission request not found (already answered or expired)")
		}
		return httpjson.Error(w, http.StatusBadRequest, err.Error())
	}
	return httpjson.Write(w, http.StatusOK, map[string]string{"status": "resolved"})
}
