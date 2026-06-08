package opencode

import (
	"context"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
	"github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

func init() {
	agentspi.Register(baseacp.AgentTypeOpencode, New)
}

type Agent struct {
	cwd string
}

func New(req agentspi.OpenRequest) (agentspi.Agent, error) {
	return &Agent{cwd: req.CWD}, nil
}

func (a *Agent) Name() string { return baseacp.AgentTypeOpencode }

func (a *Agent) Open(ctx context.Context, h transport.Handlers) (transport.Transport, error) {
	return transport.Open(ctx, transport.ProcessConfig{
		Command: "opencode",
		Args:    []string{"acp", "--cwd", a.cwd},
		Dir:     a.cwd,
	}, h)
}

func (a *Agent) InitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}
}

// SessionNewParams omits model selection: ACP defines no model field on
// session/new and no session/set_model method. Per-session model selection, if
// ever supported, must go through the SessionModelSelector capability.
func (a *Agent) SessionNewParams(string) map[string]any {
	return map[string]any{
		"cwd":        a.cwd,
		"mcpServers": []any{},
	}
}

func (a *Agent) SessionLoadParams(sessionID string) map[string]any {
	return map[string]any{"sessionId": sessionID, "cwd": a.cwd}
}

func (a *Agent) PromptParams(sessionID, input string, _ string) map[string]any {
	return map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": input,
		}},
	}
}

func (a *Agent) Cancel(_ context.Context, t transport.Transport, sessionID string) {
	if t == nil || sessionID == "" {
		return
	}
	_ = t.Notify("session/cancel", map[string]any{"sessionId": sessionID})
}
