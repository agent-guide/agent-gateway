package dispatcher

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/gateway"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(AgentRouteDispatcher{})
}

// AgentRouteDispatcher dynamically selects an AgentRoute and LLM API dialect per request.
type AgentRouteDispatcher struct {
	APIHandlersRaw caddy.ModuleMap `json:"api_handlers,omitempty" caddy:"namespace=agent_route_dispatcher.llm_apis"`

	apiHandlers map[string]LLMApiHandler
	gateway     *gateway.AgentGateway
	logger      *zap.Logger
}

func (AgentRouteDispatcher) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.agent_route_dispatcher",
		New: func() caddy.Module { return new(AgentRouteDispatcher) },
	}
}

func (h *AgentRouteDispatcher) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger(h)

	app, err := gateway.GetApp(ctx)
	if err != nil {
		return fmt.Errorf("agent_route_dispatcher: get agent_gateway app: %w", err)
	}
	h.gateway = app.AgentGateway()

	modules, err := ctx.LoadModule(h, "APIHandlersRaw")
	if err != nil {
		return fmt.Errorf("agent_route_dispatcher: load api handlers: %w", err)
	}
	loaded, ok := modules.(map[string]any)
	if !ok {
		return fmt.Errorf("agent_route_dispatcher: unexpected api handler module type %T", modules)
	}

	h.apiHandlers = make(map[string]LLMApiHandler, len(loaded))
	for name, mod := range loaded {
		apiHandler, ok := mod.(LLMApiHandler)
		if !ok {
			return fmt.Errorf("agent_route_dispatcher: api handler %q does not implement dispatcher.LLMApiHandler: %T", name, mod)
		}
		h.apiHandlers[name] = apiHandler
	}
	return nil
}

func (h *AgentRouteDispatcher) Validate() error {
	if len(h.apiHandlers) == 0 {
		return fmt.Errorf("agent_route_dispatcher requires at least one llm_api")
	}
	return nil
}

func (h AgentRouteDispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if h.gateway == nil {
		return WriteLoggedError(h.logger, ErrorContext{}, w, r, http.StatusServiceUnavailable, "agent gateway is not configured", fmt.Errorf("agent gateway is not configured"))
	}

	route, err := h.gateway.ResolveRoute(r.Context(), r)
	if err != nil {
		status := statuserr.StatusCode(err, http.StatusBadGateway)
		return WriteLoggedError(h.logger, ErrorContext{}, w, r, status, "failed to resolve route", err)
	}
	if route.ID == "" {
		return next.ServeHTTP(w, r)
	}

	apiName := strings.TrimSpace(route.LLMAPI)
	if enabled, ok := IsLLMApiHandlerTypeEnabled(apiName); ok && !enabled {
		return WriteLoggedError(h.logger, ErrorContext{RouteID: route.ID}, w, r, http.StatusServiceUnavailable, "route llm_api is disabled", fmt.Errorf("%w: %s", ErrLLMApiHandlerTypeDisabled, apiName))
	}
	apiHandler := h.apiHandlers[apiName]
	if apiHandler == nil {
		return WriteLoggedError(h.logger, ErrorContext{RouteID: route.ID}, w, r, http.StatusServiceUnavailable, "route llm_api is not configured", fmt.Errorf("route %q llm_api %q is not configured", route.ID, apiName))
	}

	rewritten := rewriteRoutePath(r, route.Match.PathPrefix)
	if !apiHandler.MatchLLMApi(rewritten) {
		return next.ServeHTTP(w, r)
	}

	if _, err := h.gateway.ResolveVirtualKey(r.Context(), r, route); err != nil {
		return WriteError(h.logger, apiHandler.Name(), route.ID, "", w, rewritten, err, "resolve virtual key")
	}

	prepared, err := apiHandler.PrepareLLMApiRequest(rewritten)
	if err != nil {
		return WriteError(h.logger, apiHandler.Name(), route.ID, prepared.Model(), w, rewritten, err, "prepare request")
	}
	if !prepared.IsValid() {
		return WriteError(h.logger, apiHandler.Name(), route.ID, "", w, rewritten, fmt.Errorf("llm api handler returned invalid prepared request"), "prepare request")
	}

	routeResolveReq := routeResolveRequest(prepared)
	exec, err := h.gateway.ResolveRouteExecution(rewritten.Context(), route, routeResolveReq)
	if err != nil {
		return WriteError(h.logger, apiHandler.Name(), route.ID, prepared.Model(), w, rewritten, err, "resolve provider")
	}
	if exec.UpstreamModel != "" {
		prepared.SetModel(exec.UpstreamModel)
	}

	return apiHandler.ServeLLMApi(w, rewritten, exec.Provider, prepared)
}

func rewriteRoutePath(r *http.Request, prefix string) *http.Request {
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

func routeResolveRequest(prepared *PreparedLLMApiRequest) routepkg.RouteResolveRequest {
	return routepkg.RouteResolveRequest{
		Model:            prepared.Model(),
		RequireStreaming: prepared.Stream(),
	}
}

var (
	_ caddy.Module                = (*AgentRouteDispatcher)(nil)
	_ caddy.Provisioner           = (*AgentRouteDispatcher)(nil)
	_ caddy.Validator             = (*AgentRouteDispatcher)(nil)
	_ caddyhttp.MiddlewareHandler = (*AgentRouteDispatcher)(nil)
	_ caddyfile.Unmarshaler       = (*AgentRouteDispatcher)(nil)
)
