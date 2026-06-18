package admin

import (
	"fmt"
	"net/http"

	adminpkg "github.com/agent-guide/agent-gateway/pkg/admin"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	caddygateway "github.com/agent-guide/agent-gateway/caddy/gateway"
)

func init() {
	caddy.RegisterModule(AgentGatewayAdminHandler{})
	httpcaddyfile.RegisterHandlerDirective("agent_gateway_admin", parseAgentGatewayAdmin)
}

// AgentGatewayAdminHandler is the Caddy HTTP middleware for the agent gateway admin API.
type AgentGatewayAdminHandler struct {
	handler *adminpkg.Handler
}

// CaddyModule returns the Caddy module information.
func (AgentGatewayAdminHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.agent_gateway_admin",
		New: func() caddy.Module { return new(AgentGatewayAdminHandler) },
	}
}

// Provision sets up the handler.
func (h *AgentGatewayAdminHandler) Provision(ctx caddy.Context) error {
	app, err := caddygateway.GetApp(ctx)
	if err != nil {
		return fmt.Errorf("agent_gateway_admin: get agent_gateway app: %w", err)
	}
	h.handler = adminpkg.NewHandler(app.AgentGateway(), ctx.Logger(h))
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (h AgentGatewayAdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	h.handler.ServeHTTP(w, r)
	return nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
//	agent_gateway_admin
func (h *AgentGatewayAdminHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			return d.Errf("unrecognized agent_gateway_admin option: %s", d.Val())
		}
	}
	return nil
}

func parseAgentGatewayAdmin(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var handler AgentGatewayAdminHandler
	if err := handler.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return &handler, nil
}

var (
	_ caddy.Module                = (*AgentGatewayAdminHandler)(nil)
	_ caddy.Provisioner           = (*AgentGatewayAdminHandler)(nil)
	_ caddyhttp.MiddlewareHandler = (*AgentGatewayAdminHandler)(nil)
	_ caddyfile.Unmarshaler       = (*AgentGatewayAdminHandler)(nil)
)
