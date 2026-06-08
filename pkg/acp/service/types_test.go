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
