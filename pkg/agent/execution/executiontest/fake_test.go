package executiontest

import (
	"context"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	agentexecution "github.com/agent-guide/agent-gateway/pkg/agent/execution"
)

func TestBackendCapabilitiesCanBeReplacedConcurrently(t *testing.T) {
	t.Parallel()

	backend := NewBackend("fake")
	var wait sync.WaitGroup
	for i := 0; i < 100; i++ {
		wait.Add(2)
		go func(executable bool) {
			defer wait.Done()
			backend.SetCapabilities(agentexecution.Capabilities{Executable: executable}, nil)
		}(i%2 == 0)
		go func() {
			defer wait.Done()
			_, _ = backend.Capabilities(context.Background(), agent.Agent{})
		}()
	}
	wait.Wait()
}

func TestFakeBackendSupportsRunScopedMultiSegmentProof(t *testing.T) {
	backend := NewBackend("fake")
	backend.ServeTurnFunc = func(_ context.Context, _ agent.Agent, _ agentexecution.TurnRequest, emit agentexecution.EventSink) error {
		return emit(agentexecution.TurnEvent{Event: agentexecution.EventDone})
	}
	run, err := agentexecution.NewRunSequencer("agent-1", "fake")
	if err != nil {
		t.Fatal(err)
	}
	var events []agentexecution.TurnEvent
	collect := func(ev agentexecution.TurnEvent) error { events = append(events, ev); return nil }
	if _, err := run.ServeSegment(t.Context(), backend, agent.Agent{ID: "agent-1"}, agentexecution.TurnRequest{}, collect); err != nil {
		t.Fatal(err)
	}
	resumed, err := agentexecution.RestoreRunSequencer("agent-1", "fake", run.Cursor())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.ServeSegment(t.Context(), backend, agent.Agent{ID: "agent-1"}, agentexecution.TurnRequest{}, collect); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].RunID != events[1].RunID || events[0].Sequence != 1 || events[1].Sequence != 2 || events[1].SegmentIndex != 1 {
		t.Fatalf("multi-segment fake events = %+v", events)
	}
}
