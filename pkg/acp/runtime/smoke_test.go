package runtime

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	// Register the real agents so the runtime can resolve them by type.
	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	_ "github.com/agent-guide/agent-gateway/pkg/acp/agent/codex"
	_ "github.com/agent-guide/agent-gateway/pkg/acp/agent/opencode"
	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
)

// These tests exercise a real ACP agent binary. They are opt-in: set
// AGW_ACP_SMOKE=1 and have the agent binary on PATH. They only run the
// initialize + session/new handshake, which does not require model credentials
// or network access, so they prove real wire-protocol interop without hitting
// an LLM. Run with:
//
//	AGW_ACP_SMOKE=1 go test ./pkg/acp/runtime -run Smoke -v

func requireSmoke(t *testing.T, bin string) {
	t.Helper()
	if os.Getenv("AGW_ACP_SMOKE") == "" {
		t.Skip("set AGW_ACP_SMOKE=1 to run real-agent smoke tests")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%q not on PATH: %v", bin, err)
	}
}

func smokeHandshake(t *testing.T, cfg acpservice.ServiceConfig) {
	t.Helper()
	cfg.Normalize()
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
	t.Logf("%s session id: %s", cfg.AgentType, inst.sessionID)
}

func TestSmokeOpencodeHandshake(t *testing.T) {
	requireSmoke(t, "opencode")
	cwd := t.TempDir()
	smokeHandshake(t, acpservice.ServiceConfig{
		ID:           "opencode-smoke",
		Name:         "opencode",
		AgentType:    baseacp.AgentTypeOpencode,
		CWD:          cwd,
		AllowedRoots: []string{cwd},
	})
}

func TestSmokeCodexHandshake(t *testing.T) {
	requireSmoke(t, "codex-acp")
	cwd := t.TempDir()
	smokeHandshake(t, acpservice.ServiceConfig{
		ID:           "codex-smoke",
		Name:         "codex",
		AgentType:    baseacp.AgentTypeCodex,
		CWD:          cwd,
		AllowedRoots: []string{cwd},
		Codex:        &acpservice.CodexConfig{Mode: acpservice.CodexModeAdapter},
	})
}
