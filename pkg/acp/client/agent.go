package client

import (
	"context"

	"github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

// OpenRequest supplies the resolved process launch settings.
type OpenRequest struct {
	CWD     string
	Env     map[string]string
	Command string
	Args    []string
}

type Factory func(OpenRequest) (Agent, error)

type Agent interface {
	Name() string
	Open(ctx context.Context, h transport.Handlers) (transport.Transport, error)
	InitializeParams() map[string]any
	SessionNewParams() map[string]any
	SessionLoadParams(sessionID string) map[string]any
	SessionListParams(cursor string) map[string]any
	PromptParams(sessionID, input string) map[string]any
	Cancel(ctx context.Context, t transport.Transport, sessionID string) error
}
