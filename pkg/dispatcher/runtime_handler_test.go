package dispatcher

import (
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	agentexecution "github.com/agent-guide/agent-gateway/pkg/agent/execution"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

func TestNormalizeAgentLookupError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want agentexecution.ErrorCode
	}{
		{name: "agent missing", err: agent.ErrAgentNotConfigured, want: agentexecution.ErrorAgentNotFound},
		{name: "store missing", err: configstore.ErrNotFound, want: agentexecution.ErrorAgentNotFound},
		{name: "store failure", err: errors.New("sqlite temporarily unavailable"), want: agentexecution.ErrorBackendUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := agentexecution.ErrorCodeOf(normalizeAgentLookupError(tt.err))
			if !ok || got != tt.want {
				t.Fatalf("error code = %q, %v; want %q", got, ok, tt.want)
			}
		})
	}
}
