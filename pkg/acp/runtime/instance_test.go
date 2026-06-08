package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
	"github.com/agent-guide/agent-gateway/pkg/acp/runtime/acpupdate"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

// stubAgent is a minimal agentspi.Agent for driving instance.prompt directly.
// It deliberately does not implement TerminalUpdateDetector so the driver takes
// the buffered-drain path.
type stubAgent struct{}

func (stubAgent) Name() string { return "stub" }

func (stubAgent) Open(context.Context, acptransport.Handlers) (acptransport.Transport, error) {
	return nil, nil
}
func (stubAgent) InitializeParams() map[string]any        { return map[string]any{} }
func (stubAgent) SessionNewParams(string) map[string]any  { return map[string]any{} }
func (stubAgent) SessionLoadParams(string) map[string]any { return map[string]any{} }

func (stubAgent) PromptParams(sessionID, input string, _ string) map[string]any {
	return map[string]any{"sessionId": sessionID, "input": input}
}

func (stubAgent) Cancel(context.Context, acptransport.Transport, string) {}

type scriptedTransport struct {
	promptResult json.RawMessage
	promptErr    error
	updates      chan acptransport.Message
}

func (s *scriptedTransport) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == "session/prompt" {
		return s.promptResult, s.promptErr
	}
	return json.RawMessage(`{}`), nil
}

func (s *scriptedTransport) Notify(string, any) error { return nil }

func (s *scriptedTransport) Updates(int) (<-chan acptransport.Message, func()) {
	return s.updates, func() {}
}

func (s *scriptedTransport) Alive() bool  { return true }
func (s *scriptedTransport) Close() error { return nil }

func chunkMessage(sessionUpdate, text string) acptransport.Message {
	params, _ := json.Marshal(map[string]any{
		"update": map[string]any{
			"sessionUpdate": sessionUpdate,
			"content":       map[string]any{"type": "text", "text": text},
		},
	})
	return acptransport.Message{Method: "session/update", Params: params}
}

func deltaMessage(text string) acptransport.Message {
	return chunkMessage("agent_message_chunk", text)
}

// noReasoningAgent suppresses all reasoning updates via ReasoningUpdateFilter.
type noReasoningAgent struct{ stubAgent }

func (noReasoningAgent) AcceptReasoningUpdate(json.RawMessage) bool { return false }

func collectEvents(t *testing.T, agent agentspi.Agent, msgs ...acptransport.Message) []TurnEvent {
	t.Helper()
	updates := make(chan acptransport.Message, len(msgs)+1)
	for _, m := range msgs {
		updates <- m
	}
	tr := &scriptedTransport{promptResult: json.RawMessage(`{"stopReason":"end_turn"}`), updates: updates}
	inst := &instance{agent: agent, t: tr, sessionID: "s1"}
	var events []TurnEvent
	emit := func(ev TurnEvent) error {
		if ev.Event != "session" {
			events = append(events, ev)
		}
		return nil
	}
	if _, err := inst.prompt(context.Background(), TurnRequest{Input: "hi"}, emit); err != nil {
		t.Fatalf("prompt returned error: %v", err)
	}
	return events
}

func TestPromptParsesStopReasonAndDrainsBufferedUpdates(t *testing.T) {
	updates := make(chan acptransport.Message, 8)
	updates <- deltaMessage("hello ")
	updates <- deltaMessage("world")

	tr := &scriptedTransport{
		promptResult: json.RawMessage(`{"stopReason":"max_tokens"}`),
		updates:      updates,
	}
	inst := &instance{agent: stubAgent{}, t: tr, sessionID: "s1"}

	var deltas []string
	emit := func(ev TurnEvent) error {
		if ev.Event == "delta" {
			deltas = append(deltas, ev.Text)
		}
		return nil
	}

	stop, err := inst.prompt(context.Background(), TurnRequest{Input: "hi"}, emit)
	if err != nil {
		t.Fatalf("prompt returned error: %v", err)
	}
	if stop != "max_tokens" {
		t.Fatalf("stop reason = %q, want max_tokens", stop)
	}
	if got := strings.Join(deltas, ""); got != "hello world" {
		t.Fatalf("emitted deltas = %q, want %q (updates were dropped)", got, "hello world")
	}
}

func TestPromptDefaultsStopReasonWhenAbsent(t *testing.T) {
	tr := &scriptedTransport{
		promptResult: json.RawMessage(`{}`),
		updates:      make(chan acptransport.Message, 1),
	}
	inst := &instance{agent: stubAgent{}, t: tr, sessionID: "s1"}

	stop, err := inst.prompt(context.Background(), TurnRequest{Input: "hi"}, func(TurnEvent) error { return nil })
	if err != nil {
		t.Fatalf("prompt returned error: %v", err)
	}
	if stop != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", stop)
	}
}

func TestPromptEmitsReasoningSeparatelyFromText(t *testing.T) {
	events := collectEvents(t, stubAgent{},
		chunkMessage("agent_thought_chunk", "let me think"),
		chunkMessage("agent_message_chunk", "the answer"),
	)

	byEvent := map[string][]string{}
	for _, ev := range events {
		byEvent[ev.Event] = append(byEvent[ev.Event], ev.Text)
	}
	if got := strings.Join(byEvent["reasoning"], ""); got != "let me think" {
		t.Fatalf("reasoning event = %q, want %q", got, "let me think")
	}
	if got := strings.Join(byEvent["delta"], ""); got != "the answer" {
		t.Fatalf("delta event = %q, want %q", got, "the answer")
	}
}

func TestPromptDropsReasoningWhenFiltered(t *testing.T) {
	events := collectEvents(t, noReasoningAgent{},
		chunkMessage("agent_thought_chunk", "hidden"),
		chunkMessage("agent_message_chunk", "shown"),
	)
	for _, ev := range events {
		if ev.Event == "reasoning" {
			t.Fatalf("reasoning event should have been filtered, got %q", ev.Text)
		}
	}
	var deltas []string
	for _, ev := range events {
		if ev.Event == "delta" {
			deltas = append(deltas, ev.Text)
		}
	}
	if got := strings.Join(deltas, ""); got != "shown" {
		t.Fatalf("delta event = %q, want %q", got, "shown")
	}
}

func TestPromptEmitsStructuredToolCall(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tc1", "title": "Read"},
	})
	events := collectEvents(t, stubAgent{}, acptransport.Message{Method: "session/update", Params: params})

	var found bool
	for _, ev := range events {
		if ev.Event == "tool_call" {
			found = true
			if len(ev.Data) == 0 {
				t.Fatal("tool_call event carried no structured Data")
			}
		}
	}
	if !found {
		t.Fatalf("no tool_call event emitted, got %+v", events)
	}
}

func TestDrainBufferedEmitsAllWithoutBlocking(t *testing.T) {
	updates := make(chan acptransport.Message, 4)
	updates <- deltaMessage("a")
	updates <- deltaMessage("b")
	updates <- acptransport.Message{Method: "notification/ignored"}

	var deltas []string
	err := drainBuffered(updates, func(msg acptransport.Message) error {
		if msg.Method != "session/update" {
			return nil
		}
		for _, ev := range acpupdate.Parse(msg.Params) {
			if ev.Kind == acpupdate.KindText {
				deltas = append(deltas, ev.Text)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("drainBuffered returned error: %v", err)
	}
	if got := strings.Join(deltas, ""); got != "ab" {
		t.Fatalf("drained deltas = %q, want %q", got, "ab")
	}
}
