package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeEnvPriority(t *testing.T) {
	t.Setenv("AGW_ADMIN_BASIC_AUTH_HASH", "shell:hash")

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), "AGW_ADMIN_BASIC_AUTH_HASH=dotenv:hash\n")
	writeFile(t, filepath.Join(dir, ".env.local"), "AGW_ADMIN_BASIC_AUTH_HASH=dotenv-local:hash\n")

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir() error = %v", err)
	}
	defer func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore cwd error = %v", err)
		}
	}()

	if err := loadRuntimeEnv(); err != nil {
		t.Fatalf("loadRuntimeEnv() error = %v", err)
	}

	if got := os.Getenv("AGW_ADMIN_BASIC_AUTH_HASH"); got != "shell:hash" {
		t.Fatalf("AGW_ADMIN_BASIC_AUTH_HASH = %q, want %q", got, "shell:hash")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	prev, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("os.Unsetenv(%q) error = %v", key, err)
	}
	t.Cleanup(func() {
		var err error
		if ok {
			err = os.Setenv(key, prev)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			t.Fatalf("restore env %q error = %v", key, err)
		}
	})
}
