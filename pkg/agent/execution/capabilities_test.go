package execution_test

import (
	"context"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	agentexecution "github.com/agent-guide/agent-gateway/pkg/agent/execution"
	"github.com/agent-guide/agent-gateway/pkg/agent/execution/executiontest"
)

type sessionBackend struct {
	*executiontest.Backend
}

func (sessionBackend) ListSessions(context.Context, agent.Agent, agentexecution.ListSessionsRequest) (agentexecution.ListSessionsResponse, error) {
	return agentexecution.ListSessionsResponse{}, nil
}

type fullBackend struct {
	sessionBackend
}

func (fullBackend) LoadTranscript(context.Context, agent.Agent, agentexecution.TranscriptRequest) (agentexecution.TranscriptResponse, error) {
	return agentexecution.TranscriptResponse{}, nil
}

func (fullBackend) ResolvePermission(context.Context, agent.Agent, agentexecution.PermissionDecision) error {
	return nil
}

func (fullBackend) CancelRun(context.Context, agent.Agent, agentexecution.CancelRequest) (agentexecution.CancelResult, error) {
	return agentexecution.CancelResult{}, nil
}

func (fullBackend) RuntimeSummary(context.Context, agent.Agent) (agentexecution.RuntimeSummary, error) {
	return agentexecution.RuntimeSummary{}, nil
}

func (fullBackend) Health(context.Context, agent.Agent) (agentexecution.Health, error) {
	return agentexecution.Health{}, nil
}

func TestDetectOptionalCapabilities(t *testing.T) {
	t.Parallel()

	base := executiontest.NewBackend("base")
	got := agentexecution.DetectOptionalCapabilities(base)
	if got != (agentexecution.OptionalCapabilities{}) {
		t.Fatalf("base optional capabilities = %+v, want none", got)
	}

	sessionOnly := sessionBackend{Backend: executiontest.NewBackend("sessions")}
	got = agentexecution.DetectOptionalCapabilities(sessionOnly)
	if !got.SessionList || got.Transcript || got.PermissionResolve || got.RunCancel || got.RuntimeInspect || got.HealthCheck {
		t.Fatalf("session optional capabilities = %+v", got)
	}

	full := fullBackend{sessionBackend{Backend: executiontest.NewBackend("full")}}
	got = agentexecution.DetectOptionalCapabilities(full)
	want := agentexecution.OptionalCapabilities{
		SessionList:       true,
		Transcript:        true,
		PermissionResolve: true,
		RunCancel:         true,
		RuntimeInspect:    true,
		HealthCheck:       true,
	}
	if got != want {
		t.Fatalf("full optional capabilities = %+v, want %+v", got, want)
	}

	if got := agentexecution.DetectOptionalCapabilities(nil); got != (agentexecution.OptionalCapabilities{}) {
		t.Fatalf("nil optional capabilities = %+v, want none", got)
	}
}
