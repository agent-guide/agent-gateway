package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	a2acard "github.com/agent-guide/agent-gateway/pkg/a2a/card"
	a2ajsonrpc "github.com/agent-guide/agent-gateway/pkg/a2a/jsonrpc"
	a2aproxy "github.com/agent-guide/agent-gateway/pkg/a2a/proxy"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	agentroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
)

const a2aCardPath = "/.well-known/agent-card.json"

func (h *Handler) dispatchA2A(w http.ResponseWriter, r *http.Request, route *agentroutepkg.AgentRoute) error {
	switch r.URL.Path {
	case a2aCardPath:
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			return httpjson.Error(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return h.serveA2ACard(w, r, route)
	case "/":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			return httpjson.Error(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return h.serveA2ARequest(w, r, route)
	default:
		return httpjson.Error(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) serveA2ACard(w http.ResponseWriter, r *http.Request, route *agentroutepkg.AgentRoute) error {
	a, target, status := h.resolveA2ATarget(route)
	if status != 0 {
		message := "agent route not found"
		if status == http.StatusServiceUnavailable {
			message = "agent runtime is not ready"
		}
		return httpjson.Error(w, status, message)
	}
	setA2AOperationExtension(r.Context(), a, "card", "")
	publicURL := a2aPublicRouteURL(r, route)
	card, err := a2acard.RewritePublicCard(target.CardTemplate, publicURL, target.Interface.Tenant, route.AuthPolicy.RequireVirtualKey)
	if err != nil {
		return httpjson.Error(w, http.StatusServiceUnavailable, "agent runtime is not ready")
	}
	w.Header().Set("Cache-Control", "no-store")
	return httpjson.Write(w, http.StatusOK, card)
}

func (h *Handler) serveA2ARequest(w http.ResponseWriter, r *http.Request, route *agentroutepkg.AgentRoute) error {
	if err := validateA2AContentType(r.Header.Get("Content-Type")); err != nil {
		return httpjson.Error(w, http.StatusUnsupportedMediaType, "unsupported media type")
	}
	if r.ContentLength > a2aproxy.MaxBodyBytes {
		return httpjson.Error(w, http.StatusRequestEntityTooLarge, "request body too large")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, a2aproxy.MaxBodyBytes+1))
	if err != nil {
		return httpjson.Error(w, http.StatusBadRequest, "failed to read request body")
	}
	if int64(len(body)) > a2aproxy.MaxBodyBytes {
		return httpjson.Error(w, http.StatusRequestEntityTooLarge, "request body too large")
	}
	meta, inspectErr := a2ajsonrpc.InspectRequest(body)
	if inspectErr != nil {
		if !json.Valid(body) {
			return writeA2AError(w, nil, false, -32700, "Parse error")
		}
		return writeA2AError(w, nil, false, -32600, "Invalid Request")
	}
	if meta.Notification {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	streaming := meta.Method == a2ajsonrpc.MethodSendStreamingMessage
	if len(bytes.TrimSpace(meta.ID)) == 0 || bytes.Equal(bytes.TrimSpace(meta.ID), []byte("null")) {
		return writeA2AError(w, nil, streaming, -32600, "Invalid Request")
	}
	if err := a2ajsonrpc.ValidateServiceVersion(r.Header, r.URL.Query()); err != nil {
		return writeA2AError(w, meta.ID, streaming, -32009, "Version not supported")
	}
	if !a2ajsonrpc.AllowedMethod(meta.Method) {
		return writeA2AError(w, meta.ID, streaming, -32601, "Method not found")
	}
	streaming = meta.Method == a2ajsonrpc.MethodSendStreamingMessage || meta.Method == a2ajsonrpc.MethodSubscribeToTask
	if meta.EmbeddedPushConfig {
		return writeA2AError(w, meta.ID, streaming, -32602, "Invalid params")
	}
	a, target, _ := h.resolveA2ATarget(route)
	if a.ID == "" || target == nil {
		return writeA2AError(w, meta.ID, streaming, -32000, "Server error")
	}
	if err := a2ajsonrpc.ValidateTenant(meta, target.Interface.Tenant); err != nil {
		return writeA2AError(w, meta.ID, streaming, -32602, "Invalid params")
	}
	setA2AOperationExtension(r.Context(), a, meta.Method, "")
	resp, err := target.Proxy.Do(r.Context(), r, body, meta)
	if err != nil {
		if a2aproxy.IsInvalidResponse(err) {
			return writeA2AError(w, meta.ID, streaming, -32006, "Invalid agent response")
		}
		return writeA2AError(w, meta.ID, streaming, -32000, "Server error")
	}
	defer resp.Close()
	a2aproxy.CopyHeaders(w.Header(), resp.Header)
	if !resp.Streaming {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(resp.Buffered)))
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(resp.Buffered)
		return err
	}
	w.WriteHeader(http.StatusOK)
	if err := a2aproxy.CopyStream(w, resp.Body, meta.ID); err != nil {
		usage.SpanFromContext(r.Context()).AddAnnotation("error_type", "a2a_stream_failed")
		return err
	}
	return nil
}

func (h *Handler) resolveA2ATarget(route *agentroutepkg.AgentRoute) (agentpkg.Agent, *gateway.HTTPProxyTarget, int) {
	if route == nil || h.gateway.AgentManager() == nil {
		return agentpkg.Agent{}, nil, http.StatusNotFound
	}
	a, ok := h.gateway.AgentManager().GetSnapshot(route.AgentID)
	if !ok {
		return agentpkg.Agent{}, nil, http.StatusNotFound
	}
	if a.Disabled || a.Runtime.Type != agentpkg.RuntimeTypeHTTP || a.Runtime.HTTP == nil || a.Runtime.HTTP.Protocol != "a2a" {
		return a, nil, http.StatusServiceUnavailable
	}
	manager := h.gateway.HTTPRuntimeManager()
	if manager == nil {
		return a, nil, http.StatusServiceUnavailable
	}
	target, err := manager.ResolveProxyTarget(a.ID)
	if err != nil {
		return a, nil, http.StatusServiceUnavailable
	}
	return a, target, 0
}

func a2aPublicRouteURL(r *http.Request, route *agentroutepkg.AgentRoute) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	path := strings.TrimSpace(route.MatchPolicy.PathPrefix)
	if path == "" {
		path = "/"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return scheme + "://" + route.MatchPolicy.Host + path
}

func validateA2AContentType(value string) error {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return fmt.Errorf("invalid Content-Type")
	}
	if len(params) == 0 {
		return nil
	}
	charset, ok := params["charset"]
	if len(params) != 1 || !ok || !strings.EqualFold(charset, "utf-8") {
		return fmt.Errorf("unsupported Content-Type parameters")
	}
	return nil
}

type a2aErrorEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeA2AError(w http.ResponseWriter, id json.RawMessage, streaming bool, code int, message string) error {
	if len(bytes.TrimSpace(id)) == 0 {
		id = json.RawMessage("null")
	}
	envelope := a2aErrorEnvelope{JSONRPC: a2ajsonrpc.Version, ID: id}
	envelope.Error.Code, envelope.Error.Message = code, message
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if streaming {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, err = fmt.Fprintf(w, "data: %s\n\n", payload)
		if flushErr := http.NewResponseController(w).Flush(); err == nil && flushErr != nil {
			err = flushErr
		}
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(payload)
	return err
}

func setA2AOperationExtension(ctx context.Context, a agentpkg.Agent, operation, outcome string) {
	span := usage.SpanFromContext(ctx)
	span.SetExtension(usage.CommonExtension{AgentID: a.ID, RuntimeType: a.Runtime.Type})
	span.AddAnnotation("a2a_operation", operation)
	if outcome != "" {
		span.AddAnnotation("a2a_outcome", outcome)
	}
}
