package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLocalAndGatewayCLIAuthHelpAreDistinct(t *testing.T) {
	localOut, localErr, err := executeAGWCTL(t, "cliauth", "--help")
	if err != nil {
		t.Fatalf("local cliauth help: %v\nstderr=%s", err, localErr)
	}
	if !strings.Contains(localOut, "local CLI auth credentials on the agwctl machine") {
		t.Fatalf("local help missing local wording:\n%s", localOut)
	}

	remoteOut, remoteErr, err := executeAGWCTL(t, "gateway", "cliauth", "--help")
	if err != nil {
		t.Fatalf("gateway cliauth help: %v\nstderr=%s", err, remoteErr)
	}
	if !strings.Contains(remoteOut, "remote gateway CLI auth runtime via the admin API") {
		t.Fatalf("gateway help missing remote wording:\n%s", remoteOut)
	}
}

func TestGatewayProviderTypesListCommand(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/provider_types":
			gotAuthHeader = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"provider_type": "openai", "enabled": true},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	stdout, stderr, err := executeAGWCTL(
		t,
		"--output", "json",
		"gateway",
		"--addr", srv.URL,
		"--user", "admin",
		"--password", "secret",
		"provider-types", "list",
	)
	if err != nil {
		t.Fatalf("provider-types list: %v\nstderr=%s", err, stderr)
	}
	if gotAuthHeader != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", gotAuthHeader)
	}
	if !strings.Contains(stdout, `"provider_type": "openai"`) {
		t.Fatalf("stdout missing provider type:\n%s", stdout)
	}
}

func TestGatewayCLIAuthAuthenticatorsListCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/cliauth/authenticators":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"name":          "codex",
						"provider_type": "openai",
						"enabled":       true,
						"config": map[string]any{
							"no_browser": true,
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	stdout, stderr, err := executeAGWCTL(
		t,
		"--output", "json",
		"gateway",
		"--addr", srv.URL,
		"--user", "admin",
		"--password", "secret",
		"cliauth", "authenticators", "list",
	)
	if err != nil {
		t.Fatalf("gateway cliauth authenticators list: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, `"name": "codex"`) {
		t.Fatalf("stdout missing authenticator:\n%s", stdout)
	}
}

func executeAGWCTL(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	oldArgs := os.Args
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	oldOutputFormat := outputFormat
	defer func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		outputFormat = oldOutputFormat
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(oldStdout)
		rootCmd.SetErr(oldStderr)
	}()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stdoutBuf, stdoutR)
		close(stdoutDone)
	}()
	go func() {
		_, _ = io.Copy(&stderrBuf, stderrR)
		close(stderrDone)
	}()

	os.Stdout = stdoutW
	os.Stderr = stderrW
	rootCmd.SetOut(stdoutW)
	rootCmd.SetErr(stderrW)
	rootCmd.SetArgs(args)

	runErr := rootCmd.Execute()

	_ = stdoutW.Close()
	_ = stderrW.Close()
	<-stdoutDone
	<-stderrDone

	return stdoutBuf.String(), stderrBuf.String(), runErr
}
