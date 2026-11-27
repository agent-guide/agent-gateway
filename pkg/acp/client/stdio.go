package client

import (
	"context"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

const ProtocolVersion = 1

type stdioAgent struct {
	name string
	OpenRequest
}

// NewStdio creates the standard ACP client for a configured subprocess.
func NewStdio(name string, req OpenRequest) Agent {
	return &stdioAgent{name: name, OpenRequest: req}
}

func (a *stdioAgent) Name() string { return a.name }

func (a *stdioAgent) Open(ctx context.Context, h transport.Handlers) (transport.Transport, error) {
	return transport.Open(ctx, transport.ProcessConfig{
		Command: a.Command,
		Args:    a.Args,
		Dir:     a.CWD,
		Env:     MergeEnv(a.Env),
	}, h)
}

func (a *stdioAgent) InitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}
}

func (a *stdioAgent) SessionNewParams() map[string]any {
	return map[string]any{
		"cwd":        a.CWD,
		"mcpServers": []any{},
	}
}

func (a *stdioAgent) SessionLoadParams(sessionID string) map[string]any {
	return map[string]any{
		"sessionId":  sessionID,
		"cwd":        a.CWD,
		"mcpServers": []any{},
	}
}

func (a *stdioAgent) SessionListParams(cursor string) map[string]any {
	params := map[string]any{}
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		params["cursor"] = cursor
	}
	return params
}

func (a *stdioAgent) PromptParams(sessionID, input string) map[string]any {
	return map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": input,
		}},
	}
}

func (a *stdioAgent) Cancel(_ context.Context, t transport.Transport, sessionID string) error {
	if t == nil || sessionID == "" {
		return nil
	}
	return t.Notify("session/cancel", map[string]any{"sessionId": sessionID})
}
