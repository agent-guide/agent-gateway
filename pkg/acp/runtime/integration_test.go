package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

// This file drives the full runtime stack (manager pool -> instance driver ->
// real stdio transport -> acpupdate parsing) against a real OS subprocess that
// speaks ACP. The subprocess is this test binary re-executed into
// TestFakeACPAgentMain, so the test is deterministic and needs no network,
// credentials, or external binary. It is the CI-safe counterpart to the gated
// real-agent smoke tests in smoke_test.go.

const fakeBinAgent = "fake-acp-bin"

func init() {
	agentspi.Register(fakeBinAgent, func(req agentspi.OpenRequest) (agentspi.Agent, error) {
		return &fakeBinAgentImpl{cwd: req.CWD}, nil
	})
}

type fakeBinAgentImpl struct{ cwd string }

func (a *fakeBinAgentImpl) Name() string { return fakeBinAgent }

func (a *fakeBinAgentImpl) Open(ctx context.Context, h acptransport.Handlers) (acptransport.Transport, error) {
	return acptransport.Open(ctx, acptransport.ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestFakeACPAgentMain", "--"},
		Env:     append(os.Environ(), "AGW_ACP_FAKE_AGENT=1"),
		Dir:     a.cwd,
	}, h)
}

func (a *fakeBinAgentImpl) InitializeParams() map[string]any {
	return map[string]any{"protocolVersion": 1}
}

func (a *fakeBinAgentImpl) SessionNewParams(string) map[string]any {
	return map[string]any{"cwd": a.cwd, "mcpServers": []any{}}
}

func (a *fakeBinAgentImpl) SessionLoadParams(id string) map[string]any {
	return map[string]any{"sessionId": id, "cwd": a.cwd}
}

func (a *fakeBinAgentImpl) PromptParams(id, input string, _ string) map[string]any {
	return map[string]any{"sessionId": id, "prompt": []map[string]any{{"type": "text", "text": input}}}
}

func (a *fakeBinAgentImpl) Cancel(_ context.Context, t acptransport.Transport, id string) {
	if t != nil && id != "" {
		_ = t.Notify("session/cancel", map[string]any{"sessionId": id})
	}
}

func TestIntegrationFakeBinaryFullLifecycle(t *testing.T) {
	cwd := t.TempDir()
	cfg := acpservice.ServiceConfig{ID: "fake", Name: "fake", AgentType: fakeBinAgent, CWD: cwd, AllowedRoots: []string{cwd}}
	cfg.Normalize()

	m := newTestManager()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	inst, err := m.resolveInstance(ctx, "scope", cfg, TurnRequest{ThreadID: "t1", Input: "ping"})
	if err != nil {
		t.Fatalf("resolveInstance over real subprocess (initialize + session/new): %v", err)
	}
	if inst.sessionID != "sess-fake" {
		t.Fatalf("sessionID = %q, want sess-fake", inst.sessionID)
	}

	var events []TurnEvent
	stop, err := inst.prompt(ctx, TurnRequest{Input: "ping"}, func(ev TurnEvent) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("prompt over real subprocess: %v", err)
	}
	if stop != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", stop)
	}

	text := map[string]string{}
	var toolCallData bool
	for _, ev := range events {
		switch ev.Event {
		case "delta", "reasoning":
			text[ev.Event] += ev.Text
		case "tool_call":
			if len(ev.Data) > 0 {
				toolCallData = true
			}
		}
	}
	if text["delta"] != "pong" {
		t.Fatalf("delta text = %q, want pong", text["delta"])
	}
	if text["reasoning"] != "thinking" {
		t.Fatalf("reasoning text = %q, want thinking", text["reasoning"])
	}
	if !toolCallData {
		t.Fatalf("expected a structured tool_call event, got %+v", events)
	}
}

// TestFakeACPAgentMain is the re-executed subprocess: a minimal, spec-shaped ACP
// agent over stdio. It is a no-op unless AGW_ACP_FAKE_AGENT is set, so a normal
// `go test` run does nothing here.
func TestFakeACPAgentMain(t *testing.T) {
	if os.Getenv("AGW_ACP_FAKE_AGENT") == "" {
		return
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), 1024*1024)
	out := bufio.NewWriter(os.Stdout)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		switch strings.TrimSpace(req.Method) {
		case "initialize":
			fakeResult(out, req.ID, map[string]any{"protocolVersion": 1})
		case "session/new":
			fakeResult(out, req.ID, map[string]any{"sessionId": "sess-fake"})
		case "session/prompt":
			// session/update notifications precede the prompt result.
			fakeNotify(out, "sess-fake", map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "thinking"}})
			fakeNotify(out, "sess-fake", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tc1", "title": "Read"})
			fakeNotify(out, "sess-fake", map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "pong"}})
			fakeResult(out, req.ID, map[string]any{"stopReason": "end_turn"})
		case "session/cancel":
			// notification: no response
		default:
			fakeResult(out, req.ID, map[string]any{})
		}
	}
	os.Exit(0)
}

func fakeResult(w *bufio.Writer, id json.RawMessage, result map[string]any) {
	fakeWrite(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func fakeNotify(w *bufio.Writer, sessionID string, update map[string]any) {
	fakeWrite(w, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": update}})
}

func fakeWrite(w *bufio.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		os.Exit(2)
	}
	_, _ = w.Write(append(data, '\n'))
	_ = w.Flush()
}
