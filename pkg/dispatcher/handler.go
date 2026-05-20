package dispatcher

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	mcpruntime "github.com/agent-guide/agent-gateway/pkg/mcp/runtime"
	"go.uber.org/zap"
)

// NextHandler is the small subset of a middleware next handler needed by the dispatcher.
type NextHandler interface {
	ServeHTTP(http.ResponseWriter, *http.Request) error
}

// Handler dispatches gateway requests to the route-selected LLM API handler.
type Handler struct {
	apiHandlers map[string]LLMApiHandler
	gateway     *gateway.AgentGateway
	logger      *zap.Logger
	mcpEnabled  bool
}

type HandlerOptions struct {
	EnableMCP bool
}

// NewHandler constructs a runtime dispatcher handler.
func NewHandler(agentGateway *gateway.AgentGateway, apiHandlers map[string]LLMApiHandler, logger *zap.Logger, opts HandlerOptions) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if apiHandlers == nil {
		apiHandlers = map[string]LLMApiHandler{}
	}
	handler := &Handler{
		apiHandlers: apiHandlers,
		gateway:     agentGateway,
		logger:      logger,
		mcpEnabled:  opts.EnableMCP,
	}
	return handler
}

// Validate verifies the dispatcher has at least one configured ingress protocol handler.
func (h *Handler) Validate() error {
	if h == nil || (len(h.apiHandlers) == 0 && !h.mcpEnabled) {
		return fmt.Errorf("agent_route_dispatcher requires at least one llm_api or mcp")
	}
	return nil
}

// ServeHTTP implements http.Handler. Requests not handled by the dispatcher receive 404.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h.Dispatch(w, r, nil); err != nil {
		return
	}
}

// Dispatch handles a request and optionally delegates unmatched requests to next.
func (h *Handler) Dispatch(w http.ResponseWriter, r *http.Request, next NextHandler) error {
	if h == nil || h.gateway == nil {
		return WriteLoggedError(loggerOrNop(h), ErrorContext{}, w, r, http.StatusServiceUnavailable, "agent gateway is not configured", fmt.Errorf("agent gateway is not configured"))
	}

	cfg, err := h.gateway.Match(r.Context(), r)
	if err != nil {
		status := statuserr.StatusCode(err, http.StatusBadGateway)
		return WriteLoggedError(h.logger, ErrorContext{}, w, r, status, "failed to resolve matched route", err)
	}
	if cfg.ID == "" {
		return serveNextOrNotFound(next, w, r)
	}
	if cfg.Disabled {
		return httpjson.Error(w, http.StatusForbidden, fmt.Sprintf("route %q is disabled", cfg.ID))
	}

	if _, err := h.gateway.ResolveVirtualKey(r.Context(), r, cfg); err != nil {
		return WriteError(h.logger, "", cfg.ID, "", w, r, err, "resolve virtual key")
	}

	switch cfg.Kind {
	case routecore.RouteKindLLM:
		return h.dispatchLLM(w, r, next, cfg)
	case routecore.RouteKindMCP:
		return h.dispatchMCP(w, r, next, cfg)
	default:
		return WriteLoggedError(h.logger, ErrorContext{RouteID: cfg.ID, Protocol: string(cfg.Protocol)}, w, r, http.StatusServiceUnavailable, "route kind is not configured", fmt.Errorf("route %q kind %q is not configured", cfg.ID, cfg.Kind))
	}
}

func (h *Handler) dispatchLLM(w http.ResponseWriter, r *http.Request, next NextHandler, cfg routecore.AgentRouteConfig) error {
	routeResolver := h.gateway.LLMRouteResolver()
	if routeResolver == nil {
		return WriteLoggedError(h.logger, ErrorContext{RouteID: cfg.ID, Protocol: string(cfg.Protocol)}, w, r, http.StatusServiceUnavailable, "llm route resolver is not configured", fmt.Errorf("llm route resolver is not configured"))
	}
	route, err := routeResolver.Resolve(r.Context(), cfg)
	if err != nil {
		status := statuserr.StatusCode(err, http.StatusBadGateway)
		return WriteLoggedError(h.logger, ErrorContext{RouteID: cfg.ID, Protocol: string(cfg.Protocol)}, w, r, status, "failed to resolve llm route", err)
	}

	apiName := strings.TrimSpace(string(route.Protocol))
	apiHandler := h.apiHandlers[apiName]
	if apiHandler == nil {
		return WriteLoggedError(h.logger, ErrorContext{RouteID: route.ID, Protocol: apiName}, w, r, http.StatusServiceUnavailable, "llm route protocol is not configured", fmt.Errorf("llm route %q protocol %q is not configured", route.ID, apiName))
	}

	rewritten := RewriteLLMRoutePath(r, route.MatchPolicy.PathPrefix)
	if !apiHandler.MatchLLMApi(rewritten) {
		return serveNextOrNotFound(next, w, r)
	}

	prepared, requestRequirements, err := apiHandler.PrepareLLMApiRequest(rewritten)
	if err != nil {
		model := ""
		if prepared != nil {
			model = prepared.Model()
		}
		return WriteError(h.logger, apiHandler.Name(), route.ID, model, w, rewritten, err, "prepare request")
	}
	if !prepared.IsValid() {
		return WriteError(h.logger, apiHandler.Name(), route.ID, "", w, rewritten, fmt.Errorf("llm api handler returned invalid prepared request"), "prepare request")
	}

	routedProvider, err := h.gateway.NewRoutedProvider(route, requestRequirements)
	if err != nil {
		return WriteError(h.logger, apiHandler.Name(), route.ID, prepared.Model(), w, rewritten, err, "resolve provider")
	}

	return apiHandler.ServeLLMApi(w, rewritten, routedProvider, prepared)
}

// RewriteLLMRoutePath returns a cloned request with the matched LLM route prefix stripped.
func RewriteLLMRoutePath(r *http.Request, prefix string) *http.Request {
	rewritten := r.Clone(r.Context())
	urlCopy := *r.URL
	rewritten.URL = &urlCopy
	if prefix == "" || !strings.HasPrefix(rewritten.URL.Path, prefix) {
		return rewritten
	}

	path := strings.TrimPrefix(rewritten.URL.Path, prefix)
	if path == "" {
		path = "/"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	rewritten.URL.Path = path
	rewritten.URL.RawPath = ""
	return rewritten
}

func serveNextOrNotFound(next NextHandler, w http.ResponseWriter, r *http.Request) error {
	if next != nil {
		return next.ServeHTTP(w, r)
	}
	http.NotFound(w, r)
	return nil
}

func loggerOrNop(h *Handler) *zap.Logger {
	if h != nil && h.logger != nil {
		return h.logger
	}
	return zap.NewNop()
}

func (h *Handler) mcpRuntimeRegistry() *mcpruntime.Registry {
	if h == nil || h.gateway == nil {
		return nil
	}
	return h.gateway.MCPRuntimeRegistry()
}
