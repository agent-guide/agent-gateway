package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	acplauncher "github.com/agent-guide/agent-gateway/pkg/acp/launcher"
)

// These tests exercise a real ACP agent binary. They are opt-in: set
// AGW_ACP_SMOKE=1 and have the agent binary on PATH. They only run the
// initialize + session/new handshake without sending a model prompt. Codex may
// require authentication before creating a session. Run with:
//
//	AGW_ACP_SMOKE=1 go test ./pkg/acp/session -run Smoke -v

func requireSmoke(t *testing.T, bin string) {
	t.Helper()
	if os.Getenv("AGW_ACP_SMOKE") == "" {
		t.Skip("set AGW_ACP_SMOKE=1 to run real-agent smoke tests")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%q not on PATH: %v", bin, err)
	}
}

func resolveSmokeConfig(t *testing.T, cfg Config) Config {
	t.Helper()
	cfg.Normalize()
	resolved, err := cfg.ResolveLauncher(acplauncher.DefaultLaunchers())
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func smokeHandshake(t *testing.T, cfg Config) {
	t.Helper()
	cfg = resolveSmokeConfig(t, cfg)
	m := newTestManager()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	inst, err := m.resolveInstance(ctx, "smoke", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("handshake (initialize + session/new): %v", err)
	}
	if inst.sessionID == "" {
		t.Fatal("agent returned an empty session id")
	}
	t.Logf("%s session id: %s", cfg.Launcher, inst.sessionID)
}

func TestSmokeOpencodeHandshake(t *testing.T) {
	requireSmoke(t, "opencode")
	cwd := t.TempDir()
	smokeHandshake(t, Config{
		OwnerID:      "opencode-smoke",
		Launcher:     "opencode",
		CWD:          cwd,
		AllowedRoots: []string{cwd},
	})
}

// TestSmokeOpencodeSessionLifecycle creates a real session, then verifies the
// transient-connection paths against the real binary: session/list must report
// the new session, transcript replay via session/load must succeed (empty: no
// prompt has run), and the metadata cache must capture the config options from
// session/new plus the available_commands_update opencode pushes after it.
func TestSmokeOpencodeSessionLifecycle(t *testing.T) {
	requireSmoke(t, "opencode")
	cwd := t.TempDir()
	cfg := Config{
		OwnerID:      "opencode-smoke",
		Launcher:     "opencode",
		CWD:          cwd,
		AllowedRoots: []string{cwd},
	}
	cfg = resolveSmokeConfig(t, cfg)
	m := newTestManager()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	inst, err := m.resolveInstance(ctx, "smoke", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	// Metadata cache: session/new config options are seeded synchronously; the
	// available_commands_update push arrives asynchronously right after.
	if snap := inst.meta.snapshot(); len(snap.ConfigOptions) == 0 {
		t.Fatal("config options from session/new were not cached")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if snap := inst.meta.snapshot(); len(snap.AvailableCommands) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("available_commands_update pushed after session/new was not cached")
		}
		time.Sleep(100 * time.Millisecond)
	}

	list, err := listAgentSessions(ctx, cfg, ListSessionsRequest{CWD: cwd}, nil)
	if err != nil {
		t.Fatalf("session/list: %v", err)
	}
	found := false
	for _, session := range list.Sessions {
		if session.SessionID == inst.sessionID {
			found = true
		}
	}
	if !found {
		t.Fatalf("session/list did not include the new session %s (got %d sessions)", inst.sessionID, len(list.Sessions))
	}

	transcript, err := loadAgentTranscript(ctx, cfg, TranscriptRequest{SessionID: inst.sessionID, CWD: cwd}, nil)
	if err != nil {
		t.Fatalf("transcript replay: %v", err)
	}
	t.Logf("opencode session %s: %d listed sessions, %d transcript messages, commands cached",
		inst.sessionID, len(list.Sessions), len(transcript.Messages))
}

func TestSmokeCodexSessionLifecycle(t *testing.T) {
	requireSmoke(t, "codex-acp")
	cwd := t.TempDir()
	cfg := Config{
		OwnerID:      "codex-smoke",
		Launcher:     "codex",
		CWD:          cwd,
		AllowedRoots: []string{cwd},
	}
	cfg = resolveSmokeConfig(t, cfg)
	m := newTestManager()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	inst, err := m.resolveInstance(ctx, "smoke", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	list, err := listAgentSessions(ctx, cfg, ListSessionsRequest{CWD: cwd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("codex-acp session/list returned %d sessions", len(list.Sessions))
	for _, session := range list.Sessions {
		if session.SessionID != inst.sessionID {
			continue
		}
		transcript, err := loadAgentTranscript(ctx, cfg, TranscriptRequest{SessionID: inst.sessionID, CWD: cwd}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("codex-acp transcript replay returned %d messages", len(transcript.Messages))
		return
	}
	t.Log("codex-acp has not persisted the turn-less session; transcript replay is covered by the prompt smoke")
}

// requirePromptSmoke gates the prompt-level smokes behind an extra env var:
// they send a real prompt to a real model and therefore spend tokens.
func requirePromptSmoke(t *testing.T, bin string) {
	t.Helper()
	requireSmoke(t, bin)
	if os.Getenv("AGW_ACP_SMOKE_PROMPT") == "" {
		t.Skip("set AGW_ACP_SMOKE_PROMPT=1 to run prompt-level smoke tests (spends model tokens)")
	}
}

// smokePromptTurn runs one real model turn and then replays the session
// transcript, verifying the full prompt path (streaming deltas, stop reason)
// and that the replayed transcript carries the real user and assistant
// messages.
func smokePromptTurn(t *testing.T, cfg Config) {
	t.Helper()
	cfg = resolveSmokeConfig(t, cfg)
	m := newTestManager()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	inst, err := m.resolveInstance(ctx, "smoke-prompt", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	const input = "Reply with exactly the single word: pong"
	var deltas, reasoning []string
	var usageRaw []string
	events := map[string]int{}
	emit := func(ev TurnEvent) error {
		events[ev.Event]++
		switch ev.Event {
		case "delta":
			deltas = append(deltas, ev.Text)
		case "reasoning":
			reasoning = append(reasoning, ev.Text)
		case "usage":
			usageRaw = append(usageRaw, string(ev.Data))
		}
		return nil
	}
	stopReason, err := inst.prompt(ctx, TurnRequest{ThreadID: "t1", Input: input}, emit)
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	text := strings.TrimSpace(strings.Join(deltas, ""))
	if text == "" {
		t.Fatalf("prompt produced no delta text (events: %v)", events)
	}
	if stopReason == "" {
		t.Fatal("prompt returned an empty stop reason")
	}
	t.Logf("%s prompt: stop_reason=%s, reply=%q, reasoning_chunks=%d, events=%v",
		cfg.Launcher, stopReason, text, len(reasoning), events)

	// Replay the persisted transcript after releasing the live session.
	m.Close()
	transcript, err := loadAgentTranscript(ctx, cfg, TranscriptRequest{SessionID: inst.sessionID, CWD: cfg.CWD}, nil)
	if err != nil {
		t.Fatalf("transcript replay after a real turn: %v", err)
	}
	roles := map[string]string{}
	for _, msg := range transcript.Messages {
		roles[msg.Role] += msg.Text
	}
	if !strings.Contains(roles["user"], input) {
		t.Fatalf("replayed transcript user text = %q, want it to contain the prompt", roles["user"])
	}
	if strings.TrimSpace(roles["assistant"]) == "" {
		t.Fatalf("replayed transcript has no assistant text: %+v", transcript.Messages)
	}
	t.Logf("%s transcript: %d messages, assistant=%q", cfg.Launcher, len(transcript.Messages), strings.TrimSpace(roles["assistant"]))

	if len(usageRaw) > 0 {
		t.Logf("%s streamed %d usage event(s) during the turn:", cfg.Launcher, len(usageRaw))
		for i, raw := range usageRaw {
			t.Logf("  usage[%d] = %s", i, raw)
		}
	} else {
		t.Logf("%s streamed no usage events during the turn", cfg.Launcher)
	}
	if snap := inst.meta.snapshot(); len(snap.Usage) > 0 {
		t.Logf("%s usage cached after turn: %s", cfg.Launcher, snap.Usage)
	} else {
		t.Logf("%s pushed no usage_update during the turn", cfg.Launcher)
	}
}

func TestSmokeOpencodePromptTurn(t *testing.T) {
	requirePromptSmoke(t, "opencode")
	cwd := t.TempDir()
	smokePromptTurn(t, Config{
		OwnerID:      "opencode-prompt-smoke",
		Launcher:     "opencode",
		CWD:          cwd,
		AllowedRoots: []string{cwd},
	})
}

// TestSmokeOpencodeInteractivePermission runs a real tool-using turn under
// permission_mode interactive and answers the agent's permission request the
// way the dispatcher decision endpoint does. If the agent's own policy does
// not ask for permission for the prompted action, the test logs that and only
// verifies the turn completes.
func TestSmokeOpencodeInteractivePermission(t *testing.T) {
	requirePromptSmoke(t, "opencode")
	cwd := t.TempDir()
	// Project-level opencode config forcing permission prompts for edits and
	// shell commands, so the agent actually raises session/request_permission.
	if err := os.WriteFile(cwd+"/opencode.json", []byte(`{"$schema":"https://opencode.ai/config.json","permission":{"edit":"ask","bash":"ask"}}`), 0o644); err != nil {
		t.Fatalf("write opencode.json: %v", err)
	}
	smokeInteractivePermission(t, Config{OwnerID: "opencode-perm-smoke", Launcher: "opencode", CWD: cwd})
}

func TestSmokeCodexInteractivePermission(t *testing.T) {
	requirePromptSmoke(t, "codex-acp")
	smokeInteractivePermission(t, Config{
		OwnerID: "codex-perm-smoke", Launcher: "codex", CWD: t.TempDir(),
		Env: map[string]string{"INITIAL_AGENT_MODE": "read-only"},
	})
}

func smokeInteractivePermission(t *testing.T, cfg Config) {
	t.Helper()
	cfg.PermissionMode = baseacp.PermissionModeInteractive
	cfg = resolveSmokeConfig(t, cfg)
	m := newTestManager()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	inst, err := m.resolveInstance(ctx, "smoke-perm", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	var permissionEvents int
	var resolved []string
	emit := func(ev TurnEvent) error {
		if ev.Event != "permission" {
			return nil
		}
		permissionEvents++
		var params struct {
			Options []struct {
				OptionID string `json:"optionId"`
				ID       string `json:"id"`
				Kind     string `json:"kind"`
			} `json:"options"`
		}
		if err := json.Unmarshal(ev.Data, &params); err != nil {
			t.Errorf("decode permission params: %v", err)
			return nil
		}
		optionID := ""
		for _, opt := range params.Options {
			if strings.Contains(opt.Kind, "allow") {
				optionID = opt.OptionID
				if optionID == "" {
					optionID = opt.ID
				}
				break
			}
		}
		if optionID == "" {
			t.Errorf("no allow option offered: %s", ev.Data)
			return nil
		}
		if err := m.ResolvePermission(PermissionDecision{RequestID: ev.RequestID, Outcome: "selected", OptionID: optionID}); err != nil {
			t.Errorf("ResolvePermission: %v", err)
		}
		resolved = append(resolved, optionID)
		return nil
	}
	stop, err := inst.prompt(ctx, TurnRequest{ThreadID: "t1", Input: "Create a file named hello.txt in the current directory containing exactly the word: hi"}, emit)
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if permissionEvents == 0 {
		t.Logf("%s turn completed with stop_reason=%s and no permission request", cfg.Launcher, stop)
		return
	}
	t.Logf("%s interactive permission: %d requests resolved with options %v, stop_reason=%s", cfg.Launcher, permissionEvents, resolved, stop)
}

func TestSmokeCodexPromptTurn(t *testing.T) {
	requirePromptSmoke(t, "codex-acp")
	cwd := t.TempDir()
	smokePromptTurn(t, Config{
		OwnerID:      "codex-prompt-smoke",
		Launcher:     "codex",
		CWD:          cwd,
		AllowedRoots: []string{cwd},
	})
}

func smokeExactRunCancel(t *testing.T, cfg Config) {
	t.Helper()
	cfg.MaxInstances = 2
	cfg = resolveSmokeConfig(t, cfg)
	m := newTestManager()
	defer m.Close()
	cancelFrames := make(chan json.RawMessage, 2)
	m.notifyObserver = func(method string, params json.RawMessage) {
		if method == "session/cancel" {
			cancelFrames <- params
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	type turnResult struct {
		runID string
		err   error
	}
	const runA = "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const runB = "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sessionSeen := make(chan string, 2)
	releases := map[string]chan struct{}{runA: make(chan struct{}), runB: make(chan struct{})}
	done := make(chan turnResult, 2)
	start := func(runID, threadID string) {
		go func() {
			err := m.serveTurn(ctx, cfg, TurnRequest{RunID: runID, ThreadID: threadID, Input: "Explain the repository in detail."}, func(ev TurnEvent) error {
				if ev.Event == "session" {
					sessionSeen <- runID
					select {
					case <-releases[runID]:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}, false)
			done <- turnResult{runID: runID, err: err}
		}()
	}
	waitForSession := func(runID string) {
		t.Helper()
		select {
		case got := <-sessionSeen:
			if got != runID {
				t.Fatalf("started session for %s, expected %s", got, runID)
			}
		case result := <-done:
			t.Fatalf("run %s ended before cancellation: %v", result.runID, result.err)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for session for %s", runID)
		}
	}
	start(runA, "cancel-smoke-a")
	waitForSession(runA)
	start(runB, "cancel-smoke-b")
	waitForSession(runB)
	active := m.ListActiveRuns(cfg.OwnerID)
	if len(active) != 2 || active[0].RunID != runA || active[1].RunID != runB || active[0].SessionID == "" || active[1].SessionID == "" {
		t.Fatalf("active runs missing native session bindings: %+v", active)
	}
	// ListActiveRuns exposes host-bound stable session ids. Native
	// session/cancel must use the corresponding raw protocol id. Snapshot it from each
	// exact run binding so this acceptance test verifies the wire frame targets
	// the intended run without conflating the two id domains.
	protocolSessions := activeRunProtocolSessions(m, cfg.OwnerID)
	if protocolSessions[runA] == "" || protocolSessions[runB] == "" {
		t.Fatalf("active runs missing protocol session bindings: %+v", protocolSessions)
	}
	t.Logf("active protocol sessions: %s=%q %s=%q", runA, protocolSessions[runA], runB, protocolSessions[runB])
	if err := m.CancelRun(cfg.OwnerID, runA); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	assertNativeCancelFrame(t, cancelFrames, protocolSessions[runA])
	close(releases[runA])
	select {
	case result := <-done:
		if result.runID != runA || result.err != nil {
			t.Fatalf("cancelled run=%s error=%v, want %s", result.runID, result.err, runA)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native cancellation did not terminate promptly")
	}
	if got := m.ListActiveRuns(cfg.OwnerID); len(got) != 1 || got[0].RunID != runB {
		t.Fatalf("unrelated run was affected by exact cancel: %+v", got)
	}
	if len(m.ListInstances()) != 2 {
		t.Fatalf("exact cancellation removed or replaced a pool entry")
	}
	select {
	case raw := <-cancelFrames:
		t.Fatalf("exact cancellation wrote a duplicate session/cancel frame: %s", raw)
	default:
	}
	if err := m.CancelRun(cfg.OwnerID, runB); err != nil {
		t.Fatalf("cleanup CancelRun: %v", err)
	}
	assertNativeCancelFrame(t, cancelFrames, protocolSessions[runB])
	close(releases[runB])
	select {
	case result := <-done:
		if result.runID != runB || result.err != nil {
			t.Fatalf("cleanup run=%s error=%v, want %s", result.runID, result.err, runB)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("unrelated run did not terminate during cleanup")
	}
}

func activeRunProtocolSessions(m *Manager, ownerID string) map[string]string {
	out := map[string]string{}
	m.runs.mu.Lock()
	defer m.runs.mu.Unlock()
	for _, run := range m.runs.runs {
		if run.ownerID == ownerID && run.instance != nil {
			out[run.runID] = run.instance.sessionID
		}
	}
	return out
}

func TestSmokeOpencodeExactRunCancel(t *testing.T) {
	requireSmoke(t, "opencode")
	cwd := t.TempDir()
	smokeExactRunCancel(t, Config{OwnerID: "opencode-cancel-smoke", Launcher: "opencode", CWD: cwd, AllowedRoots: []string{cwd}})
}
func TestSmokeCodexExactRunCancel(t *testing.T) {
	requireSmoke(t, "codex-acp")
	cwd := t.TempDir()
	smokeExactRunCancel(t, Config{OwnerID: "codex-cancel-smoke", Launcher: "codex", CWD: cwd, AllowedRoots: []string{cwd}})
}

func TestSmokeCodexHandshake(t *testing.T) {
	requireSmoke(t, "codex-acp")
	cwd := t.TempDir()
	smokeHandshake(t, Config{
		OwnerID:      "codex-smoke",
		Launcher:     "codex",
		CWD:          cwd,
		AllowedRoots: []string{cwd},
	})
}
