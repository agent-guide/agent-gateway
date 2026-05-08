package dispatcher

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/pkg/gateway"
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
}

// NewHandler constructs a runtime dispatcher handler.
func NewHandler(agentGateway *gateway.AgentGateway, apiHandlers map[string]LLMApiHandler, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if apiHandlers == nil {
		apiHandlers = map[string]LLMApiHandler{}
	}
	return &Handler{
		apiHandlers: apiHandlers,
		gateway:     agentGateway,
		logger:      logger,
	}
}

// Validate verifies the dispatcher has at least one configured LLM API handler.
func (h *Handler) Validate() error {
	if h == nil || len(h.apiHandlers) == 0 {
		return fmt.Errorf("agent_route_dispatcher requires at least one llm_api")
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

	route, err := h.gateway.ResolveRoute(r.Context(), r)
	if err != nil {
		status := statuserr.StatusCode(err, http.StatusBadGateway)
		return WriteLoggedError(h.logger, ErrorContext{}, w, r, status, "failed to resolve route", err)
	}
	if route.ID == "" {
		return serveNextOrNotFound(next, w, r)
	}

	apiName := strings.TrimSpace(route.LLMAPI)
	if enabled, ok := IsLLMApiHandlerTypeEnabled(apiName); ok && !enabled {
		return WriteLoggedError(h.logger, ErrorContext{RouteID: route.ID}, w, r, http.StatusServiceUnavailable, "route llm_api is disabled", fmt.Errorf("%w: %s", ErrLLMApiHandlerTypeDisabled, apiName))
	}
	apiHandler := h.apiHandlers[apiName]
	if apiHandler == nil {
		return WriteLoggedError(h.logger, ErrorContext{RouteID: route.ID}, w, r, http.StatusServiceUnavailable, "route llm_api is not configured", fmt.Errorf("route %q llm_api %q is not configured", route.ID, apiName))
	}

	rewritten := RewriteRoutePath(r, route.Match.PathPrefix)
	if !apiHandler.MatchLLMApi(rewritten) {
		return serveNextOrNotFound(next, w, r)
	}

	if _, err := h.gateway.ResolveVirtualKey(r.Context(), r, route); err != nil {
		return WriteError(h.logger, apiHandler.Name(), route.ID, "", w, rewritten, err, "resolve virtual key")
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

// RewriteRoutePath returns a cloned request with the matched route prefix stripped.
func RewriteRoutePath(r *http.Request, prefix string) *http.Request {
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
