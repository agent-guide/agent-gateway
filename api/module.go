package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/gateway"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(Handler{})
	httpcaddyfile.RegisterHandlerDirective("llm_api", parseLLMAPI)
}

// Handler exposes a single LLM API dialect under the HTTP app.
type Handler struct {
	RouteID string `json:"llm_route_id,omitempty"`

	APIHandlerRaw json.RawMessage `json:"api_handler,omitempty" caddy:"namespace=http.handlers.llm_api inline_key=handler"`

	apiHandler LLMApiHandler
	gateway    *gateway.AgentGateway
	logger     *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.llm_api",
		New: func() caddy.Module { return new(Handler) },
	}
}

func (h *Handler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()

	app, err := gateway.GetApp(ctx)
	if err != nil {
		return fmt.Errorf("llm_api: get agent_gateway app: %w", err)
	}
	h.gateway = app.AgentGateway()

	if h.APIHandlerRaw == nil {
		return fmt.Errorf("llm_api api handler is required")
	}

	mod, err := ctx.LoadModule(h, "APIHandlerRaw")
	if err != nil {
		return fmt.Errorf("llm_api: load child handler: %w", err)
	}

	apiHandler, ok := mod.(LLMApiHandler)
	if !ok {
		return fmt.Errorf("llm_api: child handler does not implement api.LLMApiHandler: %T", mod)
	}
	h.apiHandler = apiHandler
	return nil
}

func (h *Handler) Validate() error {
	if h.RouteID == "" {
		return fmt.Errorf("llm_route_id is required")
	}
	if h.APIHandlerRaw == nil {
		return fmt.Errorf("llm_api api handler is required")
	}
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if h.apiHandler == nil || !h.apiHandler.MatchLLMApi(r) {
		return next.ServeHTTP(w, r)
	}

	prepared, err := h.apiHandler.PrepareLLMApiRequest(r)
	if err != nil {
		model := ""
		if prepared != nil && prepared.GenerateRequest != nil {
			model = prepared.GenerateRequest.Model
		}
		return WriteError(h.logger, h.apiHandler.Name(), h.RouteID, model, w, r, err, "prepare request")
	}
	if prepared == nil || prepared.GenerateRequest == nil {
		return WriteError(h.logger, h.apiHandler.Name(), h.RouteID, "", w, r, fmt.Errorf("llm api handler returned nil generate request"), "prepare request")
	}

	resolved, err := h.gateway.ResolveProvider(r.Context(), h.RouteID, gatewayResolveRequest(r, prepared))
	if err != nil {
		return WriteError(h.logger, h.apiHandler.Name(), h.RouteID, prepared.GenerateRequest.Model, w, r, err, "resolve provider")
	}

	return h.apiHandler.ServeLLMApi(w, r, resolved.Provider, prepared)
}

func gatewayResolveRequest(r *http.Request, prepared *PreparedLLMApiRequest) routepkg.ResolveRequest {
	model := ""
	stream := false
	if prepared != nil {
		stream = prepared.Stream
		if prepared.GenerateRequest != nil {
			model = prepared.GenerateRequest.Model
		}
	}
	return routepkg.ResolveRequest{
		HTTPRequest: r,
		Model:       model,
		Stream:      stream,
	}
}

var (
	_ caddy.Module                = (*Handler)(nil)
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.Validator             = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
	_ caddyfile.Unmarshaler       = (*Handler)(nil)
)
