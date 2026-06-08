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
