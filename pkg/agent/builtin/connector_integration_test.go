package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/cloudwego/eino/schema"
)

type changingGenerationCaller struct {
	*fakeToolCaller
	generation string
	afterRead  func()
}

func (c *changingGenerationCaller) GenerationVector([]string) string {
	generation := c.generation
	if f := c.afterRead; f != nil {
		c.afterRead = nil
		f()
	}
	return generation
}

func TestPermissionResumeRejectsReplacementConnectorGraph(t *testing.T) {
	a := interactiveAgent("gated")
	tools := &changingGenerationCaller{fakeToolCaller: fetchDocCaller("replacement"), generation: "original"}
	host := NewHost(Config{Agents: &fakeAgentSource{agents: map[string]agent.Agent{a.ID: a}}, Models: &fakeModelResolver{model: gatedToolModel()}, Tools: tools})
	defer host.CloseContext(context.Background())
	session, payload := suspendOneTurn(t, host, a.ID)
	// Publish a replacement after the initial resume check has sampled the old
	// generation, before materialization selects the graph used for resumption.
	tools.afterRead = func() { tools.generation = "replacement" }
	err := host.ServeTurn(t.Context(), a.ID, TurnRequest{SessionID: session, Permission: &TurnPermission{RequestID: payload.RequestID, Decisions: []TurnPermissionDecision{{CallID: payload.Calls[0].CallID, Outcome: "allow"}}}}, (&collectedEvents{}).sink)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("replacement graph resumed old approval: %v", err)
	}
	if tools.calls != 0 {
		t.Fatal("old approval executed replacement tools")
	}
	if _, ok, _ := host.checkpoints.Get(t.Context(), payload.RequestID); ok {
		t.Fatal("rejected permission retained its checkpoint")
	}
	if _, ok := host.permissions.byRequest[payload.RequestID]; ok {
		t.Fatal("rejected permission remains resumable")
	}
}

func TestPermissionSuspensionKeepsExecutingGraphGeneration(t *testing.T) {
	for _, rotateAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("interrupt-%d", rotateAt), func(t *testing.T) {
			a := interactiveAgent("gated")
			tools := &changingGenerationCaller{fakeToolCaller: fetchDocCaller("original"), generation: "original"}
			step := 0
			model := &fakeChatModel{script: func([]*schema.Message) *schema.Message {
				step++
				if step == rotateAt {
					// Replace the source while the existing graph is generating
					// the next gated call, before the checkpoint is registered.
					tools.generation = "replacement"
				}
				return schema.AssistantMessage("", []schema.ToolCall{{ID: fmt.Sprintf("call-%d", step), Type: "function", Function: schema.FunctionCall{Name: "fetch_doc", Arguments: `{"id":"x"}`}}})
			}}
			host := NewHost(Config{Agents: &fakeAgentSource{agents: map[string]agent.Agent{a.ID: a}}, Models: &fakeModelResolver{model: model}, Tools: tools})
			defer host.CloseContext(context.Background())
			session, payload := suspendOneTurn(t, host, a.ID)
			resume := func(out *collectedEvents) error {
				return host.ServeTurn(t.Context(), a.ID, TurnRequest{SessionID: session, Permission: &TurnPermission{RequestID: payload.RequestID, Decisions: []TurnPermissionDecision{{CallID: payload.Calls[0].CallID, Outcome: "allow"}}}}, out.sink)
			}
			if rotateAt == 2 {
				out := &collectedEvents{}
				if err := resume(out); err != nil {
					t.Fatal(err)
				}
				permissions := out.byName(EventPermission)
				if len(permissions) != 1 {
					t.Fatalf("expected another approval: %v", out.names())
				}
				if err := json.Unmarshal(permissions[0].Data, &payload); err != nil {
					t.Fatal(err)
				}
				payload.RequestID = permissions[0].RequestID
			}
			if err := resume(&collectedEvents{}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("approval from retired graph resumed: %v", err)
			}
			if tools.calls != rotateAt-1 {
				t.Fatalf("stale approval executed a tool: calls=%d", tools.calls)
			}
			if _, ok, _ := host.checkpoints.Get(t.Context(), payload.RequestID); ok {
				t.Fatal("rejected approval retained its checkpoint")
			}
		})
	}
}


func TestBuiltinDefinitionRetiresGraphsAndOnlyOldPermissions(t *testing.T) {
	a := interactiveAgent("gated")
	source := &fakeAgentSource{agents: map[string]agent.Agent{a.ID: a}}
	host := NewHost(Config{Agents: source, Models: &fakeModelResolver{model: gatedToolModel()}, Tools: fetchDocCaller("x")})
	defer host.CloseContext(context.Background())
	host.PrepareDefinitions(t.Context(), []agent.Agent{a})()
	_, old := suspendOneTurn(t, host, a.ID)
	a.UpdatedAt = a.UpdatedAt.Add(time.Second)
	source.agents[a.ID] = a
	cleanup := host.PrepareDefinitions(t.Context(), []agent.Agent{a})()
	if len(host.entries) != 0 {
		t.Fatal("old graph remains cached")
	}
	_, next := suspendOneTurn(t, host, a.ID)
	cleanup(t.Context())
	if _, ok := host.permissions.byRequest[old.RequestID]; ok {
		t.Fatal("old checkpoint survives definition cleanup")
	}
	if _, ok := host.checkpoints.data[old.RequestID]; ok {
		t.Fatal("old ADK checkpoint retained")
	}
	if _, ok := host.permissions.byRequest[next.RequestID]; !ok {
		t.Fatal("cleanup discarded new permission")
	}
	// Cleanup may also lag behind more than one committed definition.
	a.UpdatedAt = a.UpdatedAt.Add(time.Second)
	source.agents[a.ID] = a
	lastCleanup := host.PrepareDefinitions(t.Context(), []agent.Agent{a})()
	_, last := suspendOneTurn(t, host, a.ID)
	cleanup(t.Context())
	lastCleanup(t.Context())
	if _, ok := host.permissions.byRequest[last.RequestID]; !ok {
		t.Fatal("older cleanup discarded newest permission")
	}
	if _, ok := host.checkpoints.data[next.RequestID]; ok {
		t.Fatal("intermediate checkpoint retained")
	}
	if err := host.GuardContinuation(a.ID, a.UpdatedAt.Add(-time.Second), func() error { t.Fatal("stale definition registered continuation"); return nil }); err == nil {
		t.Fatal("stale registration accepted")
	}
	cleanup = host.PrepareDefinitions(t.Context(), nil)()
	cleanup(t.Context())
	if len(host.entries) != 0 || len(host.permissions.byRequest) != 0 || len(host.checkpoints.data) != 0 || len(host.sessions.agents) != 0 {
		t.Fatal("deleted Agent retained runtime state")
	}
}

func TestBuiltinCloseCancelsTurnsAndRejectsAdmission(t *testing.T) {
	model := &fakeChatModel{block: make(chan struct{}), started: make(chan struct{}, 1)}
	a := testAgent("blocked")
	host := NewHost(Config{Agents: &fakeAgentSource{agents: map[string]agent.Agent{a.ID: a}}, Models: &fakeModelResolver{model: model}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		host.ServeTurn(t.Context(), a.ID, TurnRequest{Input: "wait"}, (&collectedEvents{}).sink)
	}()
	<-model.started
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := host.CloseContext(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := host.ServeTurn(t.Context(), a.ID, TurnRequest{Input: "again"}, (&collectedEvents{}).sink); err == nil {
		t.Fatal("closed host accepted a turn")
	}
}
