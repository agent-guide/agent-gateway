package admin

import (
	"fmt"
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	caddygateway "github.com/agent-guide/caddy-agent-gateway/caddy/gateway"
)

func init() {
	caddy.RegisterModule(AgentGatewayAdminHandler{})
	httpcaddyfile.RegisterHandlerDirective("agent_gateway_admin", parseAgentGatewayAdmin)
}

// AgentGatewayAdminHandler is the Caddy HTTP middleware for the agent gateway admin API.
type AgentGatewayAdminHandler struct {
	handler           *Handler
	AdminUsername     string `json:"admin_username,omitempty"`
	AdminPasswordHash string `json:"admin_password_hash,omitempty"`
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
	h.handler = NewHandler(app.AgentGateway(), ctx.Logger(h), h.AdminUsername, h.AdminPasswordHash)
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (h AgentGatewayAdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	h.handler.ServeHTTP(w, r)
	return nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
//	agent_gateway_admin {
//	    admin_user     <username>
//	    admin_password_hash  <bcrypt-hash>
//	}
func (h *AgentGatewayAdminHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "admin_user":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.AdminUsername = d.Val()
			case "admin_password_hash":
				if !d.NextArg() {
					return d.ArgErr()
				}
				h.AdminPasswordHash = d.Val()
			default:
				return d.Errf("unrecognized agent_gateway_admin option: %s", d.Val())
			}
		}
	}
	return nil
}

// ParseAgentGatewayAdminForTest exposes the parser to external tests.
func ParseAgentGatewayAdminForTest(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	return parseAgentGatewayAdmin(h)
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
