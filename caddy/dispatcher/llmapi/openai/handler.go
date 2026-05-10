package openai

import (
	runtimeopenai "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/caddyserver/caddy/v2"
)

func init() {
	caddy.RegisterModule(Handler{})
}

// Handler is the Caddy module adapter for the OpenAI LLM API handler.
type Handler struct {
	*runtimeopenai.Handler
}

// CaddyModule returns the Caddy module information.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "agent_route_dispatcher.llm_apis.openai",
		New: func() caddy.Module { return &Handler{Handler: runtimeopenai.NewHandler()} },
	}
}

func (h *Handler) Provision(ctx caddy.Context) error {
	if h.Handler == nil {
		h.Handler = runtimeopenai.NewHandler()
	}
	h.SetLogger(ctx.Logger(h))
	return nil
}

var (
	_ caddy.Module      = (*Handler)(nil)
	_ caddy.Provisioner = (*Handler)(nil)
)
