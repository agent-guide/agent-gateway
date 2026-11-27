package session

import (
	"path/filepath"
	"slices"
	"testing"

	acplauncher "github.com/agent-guide/agent-gateway/pkg/acp/launcher"
)

func TestConfigFingerprintMatchesPoolConfigWithoutMutatingInput(t *testing.T) {
	root := t.TempDir()
	cfg := Config{
		Launcher:     "codex",
		Command:      "codex-acp",
		CWD:          root,
		AllowedRoots: []string{" " + root + " "},
	}
	published, err := cfg.Fingerprint("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowedRoots[0] != " "+root+" " || cfg.Command != "codex-acp" {
		t.Fatal("fingerprinting mutated the caller's config")
	}
	poolConfig, err := ownerRuntimeConfig(cfg, "agent-2")
	if err != nil {
		t.Fatal(err)
	}
	pooled, err := configFingerprint(poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	if pooled != published {
		t.Fatalf("pool fingerprint = %q, published fingerprint = %q", pooled, published)
	}
}

func TestConfigOpenRequestProjectsLaunchSettings(t *testing.T) {
	root := t.TempDir()
	cfg := Config{
		Launcher: "codex",
		CWD:      root,
		Env:      map[string]string{"CODEX_HOME": root},
		Command:  filepath.Join(root, "codex-acp"),
		Args:     []string{"--profile", "work"},
		CWDFlag:  "--workspace",
	}
	cwd := filepath.Join(root, "project")
	req := cfg.openRequest(cwd)
	if req.CWD != cwd || req.Env["CODEX_HOME"] != root ||
		req.Command != cfg.Command || !slices.Equal(req.Args, []string{"--profile", "work", "--workspace", cwd}) {
		t.Fatal("open request lost the effective cwd, environment or adapter command/args")
	}
	req.Env["CODEX_HOME"] = "changed"
	req.Args[0] = "changed"
	if cfg.Env["CODEX_HOME"] != root || cfg.Args[0] != "--profile" {
		t.Fatal("open request aliases the host config")
	}
}

func TestConfigValidateRequiresResolvedCommand(t *testing.T) {
	cfg := Config{Launcher: "codex", CWD: t.TempDir(), Command: ""}
	cfg.Normalize()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted an unresolved command")
	}
}

func TestLaunchersRejectUnknownName(t *testing.T) {
	root := t.TempDir()
	config := Config{
		Launcher:     "qwen",
		CWD:          root,
		AllowedRoots: []string{root},
	}
	config.Normalize()
	if _, err := config.ResolveLauncher(acplauncher.DefaultLaunchers()); err == nil {
		t.Fatal("Resolve returned nil, want unknown launcher error")
	}
}

func TestConfigNormalizeTrimsEnvKeys(t *testing.T) {
	root := t.TempDir()
	config := Config{
		Launcher:     "codex",
		Command:      "codex-acp",
		CWD:          root,
		AllowedRoots: []string{root},
		Env:          map[string]string{"  CODEX_HOME  ": "/home/.codex"},
	}
	config.Normalize()
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if got := config.Env["CODEX_HOME"]; got != "/home/.codex" {
		t.Fatalf("normalized CODEX_HOME = %q, want /home/.codex", got)
	}
}

func TestConfigValidateRejectsEnvKeyContainingEquals(t *testing.T) {
	root := t.TempDir()
	config := Config{
		Launcher:     "codex",
		Command:      "codex-acp",
		CWD:          root,
		AllowedRoots: []string{root},
		Env:          map[string]string{"BAD=KEY": "value"},
	}
	config.Normalize()
	if err := config.Validate(); err == nil {
		t.Fatal("Validate returned nil, want invalid env key error")
	}
}

func TestValidateCWDAllowedRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "project")
	if err := validateCWDAllowed(outside, []string{root}); err == nil {
		t.Fatal("ValidateCWDAllowed returned nil, want outside allowed_roots error")
	}
}
