package service

import (
	"path/filepath"
	"testing"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
)

func TestServiceConfigValidateSupportsFirstVersionAgents(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "opencode-main",
		Name:         "Opencode",
		AgentType:    baseacp.AgentTypeOpencode,
		CWD:          root,
		AllowedRoots: []string{root},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestServiceConfigValidateRejectsUnsupportedAgent(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "qwen-main",
		Name:         "Qwen",
		AgentType:    "qwen",
		CWD:          root,
		AllowedRoots: []string{root},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want error")
	}
}

func TestValidateCWDAllowedRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "project")
	if err := ValidateCWDAllowed(outside, []string{root}); err == nil {
		t.Fatal("ValidateCWDAllowed returned nil, want error")
	}
}

func TestServiceConfigNormalizeTrimsEnvKeys(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "codex-main",
		Name:         "Codex",
		AgentType:    baseacp.AgentTypeCodex,
		CWD:          root,
		AllowedRoots: []string{root},
		Env:          map[string]string{"  CODEX_HOME  ": "/home/.codex"},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if got := cfg.Env["CODEX_HOME"]; got != "/home/.codex" {
		t.Fatalf("env key not trimmed: got %q", got)
	}
}

func TestServiceConfigValidateRejectsBadEnvKey(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "codex-main",
		Name:         "Codex",
		AgentType:    baseacp.AgentTypeCodex,
		CWD:          root,
		AllowedRoots: []string{root},
		Env:          map[string]string{"BAD=KEY": "x"},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want error for env key containing '='")
	}
}

func TestServiceConfigValidateRejectsEmptyEnvKey(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "codex-main",
		Name:         "Codex",
		AgentType:    baseacp.AgentTypeCodex,
		CWD:          root,
		AllowedRoots: []string{root},
		Env:          map[string]string{"   ": "ignored"},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want error for empty env key")
	}
}

func TestServiceConfigValidateRejectsEnvValueNUL(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "codex-main",
		Name:         "Codex",
		AgentType:    baseacp.AgentTypeCodex,
		CWD:          root,
		AllowedRoots: []string{root},
		Env:          map[string]string{"CODEX_HOME": "bad\x00value"},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want error for env value containing NUL")
	}
}

func TestServiceConfigNormalizeCodexDefaultsToAdapter(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "codex-main",
		Name:         "Codex",
		AgentType:    baseacp.AgentTypeCodex,
		CWD:          root,
		AllowedRoots: []string{root},
		Codex:        &CodexConfig{},
	}
	cfg.Normalize()
	if cfg.Codex.Mode != CodexModeAdapter {
		t.Fatalf("codex mode = %q, want %q", cfg.Codex.Mode, CodexModeAdapter)
	}
	if cfg.Codex.AdapterCommand != "codex-acp" {
		t.Fatalf("adapter command = %q, want codex-acp", cfg.Codex.AdapterCommand)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestServiceConfigValidateRejectsArbitraryCodexAdapterCommand(t *testing.T) {
	root := t.TempDir()
	cfg := ServiceConfig{
		ID:           "codex-main",
		Name:         "Codex",
		AgentType:    baseacp.AgentTypeCodex,
		CWD:          root,
		AllowedRoots: []string{root},
		Codex: &CodexConfig{
			Mode:           CodexModeAdapter,
			AdapterCommand: "sh",
		},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want error")
	}
}
