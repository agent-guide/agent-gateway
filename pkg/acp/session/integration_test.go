package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	acplauncher "github.com/agent-guide/agent-gateway/pkg/acp/launcher"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

// This file drives the full runtime stack (manager pool -> instance driver ->
// real stdio transport -> acpupdate parsing) against a real OS subprocess that
// speaks ACP. The subprocess is this test binary re-executed into
// TestFakeACPAgentMain, so the test is deterministic and needs no network,
// credentials, or external binary. It is the CI-safe counterpart to the gated
// real-agent smoke tests in smoke_test.go.

const fakeBinAgent = "fake-acp-bin"

func fakeProcessConfig(owner, cwd string, permissions bool) Config {
	cfg := Config{
		OwnerID: owner, Launcher: fakeBinAgent, CWD: cwd,
		Env: map[string]string{"AGW_ACP_FAKE_AGENT": "1"},
	}
	if permissions {
		cfg.Env["AGW_ACP_FAKE_PERMISSION"] = "1"
	}
	cfg.Normalize()
	cfg, err := cfg.ResolveLauncher(acplauncher.Launchers{fakeBinAgent: {Command: os.Args[0], Args: []string{"-test.run=TestFakeACPAgentMain", "--"}}})
	if err != nil {
		panic(err)
	}
	return cfg
}

func TestIntegrationRejectsNonACPProcessDuringHandshake(t *testing.T) {
	for _, mode := range []string{"empty", "version", "envelope"} {
		t.Run(mode, func(t *testing.T) {
			cfg := fakeProcessConfig("fake", t.TempDir(), false)
			cfg.Env["AGW_ACP_FAKE_INVALID_HANDSHAKE"] = mode
			m := newTestManager()
			defer m.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err := m.resolveInstance(ctx, "fake", cfg, TurnRequest{ThreadID: "test"})
			if err == nil || errors.Is(err, context.DeadlineExceeded) || len(m.ListInstances()) != 0 {
				t.Fatalf("handshake error = %v, instances = %v", err, m.ListInstances())
			}
		})
	}
}

func TestIntegrationFakeBinaryFullLifecycle(t *testing.T) {
	cwd := t.TempDir()
	cfg := fakeProcessConfig("fake", cwd, false)
	cfg, err := cfg.ResolveLauncher(acplauncher.Launchers{fakeBinAgent: {Command: cfg.Command, Args: cfg.Args}})
	if err != nil {
		t.Fatal(err)
	}

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

// TestIntegrationInteractivePermissionRoundTrip drives an interactive
// permission request through the full stack: the fake agent (a real stdio
// subprocess) asks mid-turn, the driver surfaces it as a "permission" event,
// the test answers through Manager.ResolvePermission exactly like the
// dispatcher decision endpoint, and the agent echoes the wire outcome it
// observed back as the reply text.
func TestReadOnlyConnectionsCancelPermissionsAndObserveCapabilities(t *testing.T) {
	cfg := fakeProcessConfig("acp-reader", t.TempDir(), false)
	cfg.PermissionMode = baseacp.PermissionModeAutoApprove
	cfg.Env["AGW_ACP_FAKE_READ_PERMISSION"] = "1"
	ownerID := cfg.OwnerID
	fingerprint, err := cfg.Fingerprint(ownerID)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager()
	defer m.Close()
	m.RetireOwner(ownerID, fingerprint)
	if _, known := m.ConfiguredSessionCapabilities(ownerID, fingerprint); known {
		t.Fatal("claimed capabilities before a handshake")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	list, err := m.ListConfiguredSessions(ctx, ownerID, cfg, ListSessionsRequest{})
	if err != nil || len(list.Sessions) != 1 || list.Sessions[0].Title != "cancelled" {
		t.Fatalf("session/list permission outcome = %+v, err=%v", list, err)
	}
	m.RetireOwner(ownerID, fingerprint)
	if caps, known := m.ConfiguredSessionCapabilities(ownerID, fingerprint); !known || !caps.List || !caps.Load {
		t.Fatalf("observed capabilities = %+v, known=%t", caps, known)
	}

	replay, err := m.LoadConfiguredTranscript(ctx, ownerID, cfg, TranscriptRequest{SessionID: "sess-fake"})
	if err != nil || len(replay.Messages) != 1 || replay.Messages[0].Text != "cancelled" {
		t.Fatalf("session/load permission outcome = %+v, err=%v", replay, err)
	}

	oldObserver := m.sessionObserver(ownerID, fingerprint)
	m.RetireOwner(ownerID, "different-fingerprint")
	oldObserver(SessionCapabilities{List: true, Load: true})
	if _, known := m.ConfiguredSessionCapabilities(ownerID, "different-fingerprint"); known {
		t.Fatal("a retired generation published handshake evidence")
	}
	if _, known := m.ConfiguredSessionCapabilities(ownerID, fingerprint); known {
		t.Fatal("a retired generation still exposed handshake evidence")
	}
}

func TestIntegrationInteractivePermissionRoundTrip(t *testing.T) {
	cwd := t.TempDir()
	cfg := fakeProcessConfig("fake-perm", cwd, true)
	cfg.PermissionMode = baseacp.PermissionModeInteractive
	cfg.Normalize()

	m := newTestManager()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	inst, err := m.resolveInstance(ctx, "scope", cfg, TurnRequest{ThreadID: "t1", Input: "write it"})
	if err != nil {
		t.Fatalf("resolveInstance: %v", err)
	}

	var permissionEvents int
	var deltas string
	stop, err := inst.prompt(ctx, TurnRequest{Input: "write it"}, func(ev TurnEvent) error {
		switch ev.Event {
		case "permission":
			permissionEvents++
			if ev.RequestID == "" || len(ev.Data) == 0 {
				t.Errorf("permission event missing request id or raw params: %+v", ev)
			}
			// Pick the allow option from the raw ACP params like a real client.
			var params struct {
				Options []struct {
					OptionID string `json:"optionId"`
					Kind     string `json:"kind"`
				} `json:"options"`
			}
			if err := json.Unmarshal(ev.Data, &params); err != nil {
				t.Errorf("decode permission params: %v", err)
			}
			optionID := ""
			for _, opt := range params.Options {
				if opt.Kind == "allow_once" {
					optionID = opt.OptionID
				}
			}
			if err := m.ResolvePermission(PermissionDecision{RequestID: ev.RequestID, Outcome: "selected", OptionID: optionID}); err != nil {
				t.Errorf("ResolvePermission: %v", err)
			}
		case "delta":
			deltas += ev.Text
		}
		return nil
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if stop != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", stop)
	}
	if permissionEvents != 1 {
		t.Fatalf("permission events = %d, want 1", permissionEvents)
	}
	// The agent echoes the exact nested outcome it received on the wire.
	if deltas != "perm:selected:allow-once" {
		t.Fatalf("agent observed %q, want %q", deltas, "perm:selected:allow-once")
	}
	if got := m.ListPendingPermissions(); len(got) != 0 {
		t.Fatalf("pending permissions after the turn = %+v, want empty", got)
	}
}

func TestIntegrationExactRunCancelSendsNativeSessionCancel(t *testing.T) {
	cwd := t.TempDir()
	cfg := fakeProcessConfig("reviewer", cwd, true)
	cfg.PermissionMode = baseacp.PermissionModeInteractive
	cfg.MaxInstances = 2
	cfg.Normalize()
	m := newTestManager()
	defer m.Close()
	cancelFrames := make(chan json.RawMessage, 2)
	m.notifyObserver = func(method string, params json.RawMessage) {
		if method == "session/cancel" {
			cancelFrames <- params
		}
	}
	type turnResult struct {
		runID string
		err   error
	}
	events := make(chan struct {
		runID string
		event TurnEvent
	}, 32)
	done := make(chan turnResult, 2)
	runA := "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	runB := "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	start := func(runID, threadID string) {
		go func() {
			err := m.serveTurn(context.Background(), cfg, TurnRequest{RunID: runID, ThreadID: threadID, Input: "wait"}, func(ev TurnEvent) error {
				events <- struct {
					runID string
					event TurnEvent
				}{runID: runID, event: ev}
				return nil
			}, false)
			done <- turnResult{runID: runID, err: err}
		}()
	}
	start(runA, "thread-cancel-a")
	start(runB, "thread-cancel-b")
	deadline := time.After(10 * time.Second)
	pending := map[string]bool{}
	for len(pending) != 2 {
		select {
		case observed := <-events:
			if observed.event.Event == "permission" {
				pending[observed.runID] = true
			}
		case result := <-done:
			t.Fatalf("run %s ended before both permissions: %v", result.runID, result.err)
		case <-deadline:
			t.Fatalf("permission events did not arrive for both runs: %+v", pending)
		}
	}
	if got := m.ListActiveRuns("reviewer"); len(got) != 2 || got[0].RunID != runA || got[1].RunID != runB {
		t.Fatalf("active runs=%+v", got)
	}
	if err := m.CancelRun("reviewer", runA); err != nil {
		t.Fatal(err)
	}
	assertNativeCancelFrame(t, cancelFrames, "sess-fake")
	select {
	case result := <-done:
		if result.runID != runA || result.err != nil {
			t.Fatalf("first completed run=%s error=%v, want cancelled %s", result.runID, result.err, runA)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn did not terminate")
	}
	if got := m.ListActiveRuns("reviewer"); len(got) != 1 || got[0].RunID != runB {
		t.Fatalf("unrelated run was affected by exact cancel: %+v", got)
	}
	if len(m.ListInstances()) != 2 {
		t.Fatalf("pool entries after exact cancel=%d, want 2", len(m.ListInstances()))
	}
	if err := m.CancelRun("reviewer", runB); err != nil {
		t.Fatal(err)
	}
	assertNativeCancelFrame(t, cancelFrames, "sess-fake")
	select {
	case result := <-done:
		if result.runID != runB || result.err != nil {
			t.Fatalf("cleanup run=%s error=%v, want %s", result.runID, result.err, runB)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unrelated run did not terminate during cleanup")
	}
}

func TestExactRunCancelBeforeInstanceBindIsRetryable(t *testing.T) {
	runs := newActiveRunRegistry()
	ctx, run := runs.begin(t.Context(), "owner", "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	if run == nil {
		t.Fatal("begin returned nil run")
	}
	if err := runs.cancel("owner", run.runID); !errors.Is(err, ErrRunNotReady) {
		t.Fatalf("cancel before bind error=%v, want ErrRunNotReady", err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("retryable pre-bind cancellation cancelled the run context")
	default:
	}
	runs.finish(run)
}

func TestExactRunCancelMarksNativeFrameAsSent(t *testing.T) {
	runs := newActiveRunRegistry()
	ctx, run := runs.begin(t.Context(), "owner", "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	inst := &instance{agent: stubAgent{}, t: &scriptedTransport{}, sessionID: "sess-1"}
	if err := runs.bind(run, inst); err != nil {
		t.Fatal(err)
	}
	if err := runs.cancel("owner", run.runID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(ctx), errNativeCancelSent) {
		t.Fatalf("cancel cause=%v, want errNativeCancelSent", context.Cause(ctx))
	}
	runs.finish(run)
}

func TestRetirementCancelBeforeBindStopsRunBeforePrompt(t *testing.T) {
	runs := newActiveRunRegistry()
	ctx, run := runs.begin(t.Context(), "owner", "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	if err := runs.requestCancel("owner", run.runID); err != nil {
		t.Fatalf("requestCancel before bind: %v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("retirement cancellation ended context before native cancel frame")
	default:
	}
	inst := &instance{agent: stubAgent{}, t: &scriptedTransport{}, sessionID: "sess-1"}
	if err := runs.bind(run, inst); !errors.Is(err, ErrTurnCancelled) {
		t.Fatalf("bind after retirement cancellation = %v, want ErrTurnCancelled", err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("bind did not finish the retirement-cancelled run context")
	}
	runs.finish(run)
}

type failingCancelAgent struct{ stubAgent }

func (failingCancelAgent) Cancel(context.Context, acptransport.Transport, string) error {
	return errors.New("notify failed")
}

func TestExactRunCancelWriteFailureLeavesContextRetryable(t *testing.T) {
	runs := newActiveRunRegistry()
	ctx, run := runs.begin(t.Context(), "owner", "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	inst := &instance{agent: failingCancelAgent{}, t: &scriptedTransport{}, sessionID: "sess-1"}
	if err := runs.bind(run, inst); err != nil {
		t.Fatal(err)
	}
	if err := runs.cancel("owner", run.runID); !errors.Is(err, ErrRunNotReady) {
		t.Fatalf("cancel write error=%v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("failed session/cancel write cancelled the run context")
	default:
	}
	runs.finish(run)
}

func assertNativeCancelFrame(t *testing.T, frames <-chan json.RawMessage, wantSessionID string) {
	t.Helper()
	select {
	case raw := <-frames:
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatalf("decode session/cancel params %s: %v", raw, err)
		}
		if params.SessionID != wantSessionID {
			t.Fatalf("session/cancel sessionId=%q, want %q", params.SessionID, wantSessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session/cancel frame was not written")
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
			switch os.Getenv("AGW_ACP_FAKE_INVALID_HANDSHAKE") {
			case "empty":
				fakeResult(out, req.ID, map[string]any{})
			case "version":
				fakeResult(out, req.ID, map[string]any{"protocolVersion": 2})
			case "envelope":
				fakeWrite(out, map[string]any{"jsonrpc": "1.0", "id": req.ID, "result": map[string]any{"protocolVersion": 1}})
			default:
				caps := map[string]any{}
				if os.Getenv("AGW_ACP_FAKE_READ_PERMISSION") != "" {
					caps["loadSession"] = true
					caps["sessionCapabilities"] = map[string]any{"list": map[string]any{}}
				}
				fakeResult(out, req.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": caps})
			}
		case "session/list":
			outcome, _ := fakeRequestPermission(in, out)
			cwd, _ := os.Getwd()
			fakeResult(out, req.ID, map[string]any{"sessions": []map[string]any{{"sessionId": "sess-fake", "cwd": cwd, "title": outcome}}})
		case "session/load":
			outcome, _ := fakeRequestPermission(in, out)
			fakeNotify(out, "sess-fake", map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": outcome}})
			fakeResult(out, req.ID, map[string]any{})
		case "session/new":
			fakeResult(out, req.ID, map[string]any{"sessionId": "sess-fake"})
		case "session/prompt":
			if os.Getenv("AGW_ACP_FAKE_PERMISSION") != "" {
				// Server-initiated permission round-trip mid-turn: ask, block
				// until the client answers, then echo the decision as the
				// reply text so the driver test can assert the exact wire
				// outcome the agent observed.
				outcome, optionID := fakeRequestPermission(in, out)
				fakeNotify(out, "sess-fake", map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "perm:" + outcome + ":" + optionID}})
				fakeResult(out, req.ID, map[string]any{"stopReason": "end_turn"})
				continue
			}
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

// fakeRequestPermission sends a spec-shaped session/request_permission to the
// client and blocks until the response for it arrives, returning the nested
// ACP outcome discriminator and selected option id.
func fakeRequestPermission(in *bufio.Scanner, out *bufio.Writer) (outcome, optionID string) {
	fakeWrite(out, map[string]any{
		"jsonrpc": "2.0",
		"id":      "perm-req-1",
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": "sess-fake",
			"toolCall":  map[string]any{"toolCallId": "tc1", "title": "Write file"},
			"options": []map[string]any{
				{"optionId": "allow-once", "id": "allow-once", "kind": "allow_once", "name": "Allow once"},
				{"optionId": "reject-once", "id": "reject-once", "kind": "reject_once", "name": "Reject"},
			},
		},
	})
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result struct {
				Outcome struct {
					Outcome  string `json:"outcome"`
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &resp) != nil {
			continue
		}
		if strings.Trim(string(resp.ID), `"`) != "perm-req-1" {
			continue
		}
		return resp.Result.Outcome.Outcome, resp.Result.Outcome.OptionID
	}
	os.Exit(3)
	return "", ""
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
