package api

import (
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// ParseLLMAPIForTest exposes the parser to external tests.
func ParseLLMAPIForTest(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	return parseLLMAPI(h)
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (h *Handler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	if !d.Next() {
		return d.Err("expected directive name")
	}
	if !d.NextArg() {
		return d.Err("expected llm api name")
	}
	apiName := strings.Trim(d.Val(), "\"`")
	if d.NextArg() {
		return d.ArgErr()
	}

	for d.NextBlock(0) {
		switch d.Val() {
		case "llm_route_id":
			args := d.RemainingArgsRaw()
			if len(args) != 1 {
				return d.ArgErr()
			}
			h.RouteID = strings.Trim(args[0], "\"`")
		default:
			return d.Errf("unknown subdirective: %s", d.Val())
		}
	}
	if h.RouteID == "" {
		return d.Err("llm_route_id is required")
	}

	moduleID := "http.handlers.llm_api." + apiName
	mod, err := caddy.GetModule(moduleID)
	if err != nil {
		return d.Errf("getting module named '%s': %v", moduleID, err)
	}
	h.APIHandlerRaw = caddyconfig.JSONModuleObject(mod.New(), "handler", mod.ID.Name(), nil)
	return nil
}

func parseLLMAPI(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	handler := &Handler{}
	if err := handler.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return handler, nil
}
