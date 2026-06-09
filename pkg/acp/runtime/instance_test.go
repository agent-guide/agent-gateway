package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
	"github.com/agent-guide/agent-gateway/pkg/acp/runtime/acpupdate"
	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
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

type recordedRequest struct {
	method string
	params any
}

type scriptedTransport struct {
	promptResult json.RawMessage
	promptErr    error
	updates      chan acptransport.Message

	mu       sync.Mutex
	requests []recordedRequest
}

func (s *scriptedTransport) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	s.requests = append(s.requests, recordedRequest{method: method, params: params})
	s.mu.Unlock()
	switch method {
	case "session/prompt":
		return s.promptResult, s.promptErr
	case "session/new":
		return json.RawMessage(`{"sessionId":"s1"}`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func (s *scriptedTransport) recorded(method string) []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recordedRequest
	for _, r := range s.requests {
		if r.method == method {
			out = append(out, r)
		}
	}
	return out
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

// modelSelectorAgent records SessionModelSelector invocations.
type modelSelectorAgent struct {
	stubAgent
	gotModelID string
	calls      int
}

func (a *modelSelectorAgent) SelectSessionModel(_ context.Context, _ acptransport.Transport, _, modelID string, _ []agentspi.ConfigOption) ([]agentspi.ConfigOption, error) {
	a.calls++
	a.gotModelID = modelID
	return nil, nil
}

func paramField(t *testing.T, params any, key string) string {
	t.Helper()
	m, ok := params.(map[string]any)
	if !ok {
		t.Fatalf("params is %T, want map", params)
	}
	v, _ := m[key].(string)
	return v
}

func TestInitializeAppliesServiceConfigOverrides(t *testing.T) {
	tr := &scriptedTransport{updates: make(chan acptransport.Message, 1)}
	inst := &instance{
		cfg:   acpservice.ServiceConfig{ConfigOverrides: map[string]string{"mode": "plan"}},
		agent: stubAgent{},
		t:     tr,
	}
	if err := inst.initialize(context.Background(), false); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	got := tr.recorded("session/set_config_option")
	if len(got) != 1 {
		t.Fatalf("set_config_option calls = %d, want 1", len(got))
	}
	if id := paramField(t, got[0].params, "configId"); id != "mode" {
		t.Fatalf("configId = %q, want mode", id)
	}
	if v := paramField(t, got[0].params, "value"); v != "plan" {
		t.Fatalf("value = %q, want plan", v)
	}
}

func TestInitializeInvokesModelSelector(t *testing.T) {
	tr := &scriptedTransport{updates: make(chan acptransport.Message, 1)}
	agent := &modelSelectorAgent{}
	inst := &instance{
		cfg:   acpservice.ServiceConfig{},
		model: "anthropic/claude-3-5-haiku-latest",
		agent: agent,
		t:     tr,
	}
	if err := inst.initialize(context.Background(), false); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if agent.calls != 1 {
		t.Fatalf("SelectSessionModel called %d times, want 1", agent.calls)
	}
	if agent.gotModelID != "anthropic/claude-3-5-haiku-latest" {
		t.Fatalf("model id = %q, want the configured model", agent.gotModelID)
	}
}

func TestPromptAppliesPerTurnConfigOverrides(t *testing.T) {
	tr := &scriptedTransport{
		promptResult: json.RawMessage(`{"stopReason":"end_turn"}`),
		updates:      make(chan acptransport.Message, 1),
	}
	inst := &instance{agent: stubAgent{}, t: tr, sessionID: "s1"}
	_, err := inst.prompt(context.Background(), TurnRequest{Input: "hi", ConfigOverrides: map[string]string{"mode": "build"}}, func(TurnEvent) error { return nil })
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	got := tr.recorded("session/set_config_option")
	if len(got) != 1 || paramField(t, got[0].params, "configId") != "mode" {
		t.Fatalf("expected one set_config_option for mode, got %+v", got)
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
