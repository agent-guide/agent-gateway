package anthropic

import (
	runtimeanthropic "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/anthropic"
	"github.com/caddyserver/caddy/v2"
)

func init() {
	caddy.RegisterModule(Handler{})
}

// Handler is the Caddy module adapter for the Anthropic LLM API handler.
type Handler struct {
	*runtimeanthropic.Handler
}

// CaddyModule returns the Caddy module information.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_route_dispatcher.llm_apis.anthropic",
		New: func() caddy.Module { return &Handler{Handler: runtimeanthropic.NewHandler(nil)} },
	}
}

func (h *Handler) Provision(ctx caddy.Context) error {
	if h.Handler == nil {
		h.Handler = runtimeanthropic.NewHandler(nil)
	}
	h.SetLogger(ctx.Logger(h))
	return nil
}

var (
	_ caddy.Module      = (*Handler)(nil)
	_ caddy.Provisioner = (*Handler)(nil)
)
