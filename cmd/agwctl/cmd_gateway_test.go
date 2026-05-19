package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLocalAndGatewayCLIAuthHelpAreDistinct(t *testing.T) {
	localOut, localErr, err := executeAGWCTL(t, "cliauth", "--help")
	if err != nil {
		t.Fatalf("local cliauth help: %v\nstderr=%s", err, localErr)
	}
	if !strings.Contains(localOut, "gateway CLI auth login flows and local authenticator support") {
		t.Fatalf("local help missing gateway wording:\n%s", localOut)
	}
	if strings.Contains(localOut, "authenticators") {
		t.Fatalf("local help should not expose hidden authenticators command:\n%s", localOut)
	}
	if strings.Contains(localOut, "\n  list") || strings.Contains(localOut, "\n  get") || strings.Contains(localOut, "\n  delete") {
		t.Fatalf("local help still exposes removed credential commands:\n%s", localOut)
	}

	remoteOut, remoteErr, err := executeAGWCTL(t, "gateway", "cliauth", "--help")
	if err != nil {
		t.Fatalf("gateway cliauth help: %v\nstderr=%s", err, remoteErr)
	}
	if !strings.Contains(remoteOut, "remote gateway CLI auth runtime via the admin API") {
		t.Fatalf("gateway help missing remote wording:\n%s", remoteOut)
	}
}

func TestGatewayCredentialListCommandUsesTypeFilterAndDisplaysType(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/credentials":
			gotAuthHeader = r.Header.Get("Authorization")
			if r.URL.Query().Get("type") != "cliauth_token" {
				t.Fatalf("type query = %q, want cliauth_token", r.URL.Query().Get("type"))
			}
			if r.URL.Query().Get("provider_type") != "openai" {
				t.Fatalf("provider_type query = %q, want openai", r.URL.Query().Get("provider_type"))
			}
			if r.URL.Query().Get("provider_id") != "openai-main" {
				t.Fatalf("provider_id query = %q, want openai-main", r.URL.Query().Get("provider_id"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":            "cred-1",
						"provider_type": "openai",
						"provider_id":   "openai-main",
						"type":          "cliauth_token",
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
		"gateway",
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"credential", "list",
		"--type", "cliauth_token",
		"--provider-type", "openai",
		"--provider-id", "openai-main",
	)
	if err != nil {
		t.Fatalf("gateway credential list: %v\nstderr=%s", err, stderr)
	}
	if gotAuthHeader != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", gotAuthHeader)
	}
	if !strings.Contains(stdout, "TYPE") || !strings.Contains(stdout, "cliauth_token") {
		t.Fatalf("stdout missing type column or value:\n%s", stdout)
	}
}

func TestGatewayCredentialListCommandSurfacesAdminAuthErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid credentials",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	stdout, stderr, err := executeAGWCTL(
		t,
		"gateway",
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "wrong-secret",
		"credential", "list",
		"--type", "cliauth_token",
	)
	if err == nil {
		t.Fatalf("expected admin auth error, got nil\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "admin API error 401: invalid credentials") {
		t.Fatalf("stderr = %q, want 401 auth error", stderr)
	}
	if strings.Contains(stdout, "no credentials found") {
		t.Fatalf("stdout should not report empty credentials on auth error:\n%s", stdout)
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
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"provider-type", "list",
	)
	if err != nil {
		t.Fatalf("provider-type list: %v\nstderr=%s", err, stderr)
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
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"cliauth", "authenticators", "list",
	)
	if err != nil {
		t.Fatalf("gateway cliauth authenticators list: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, `"name": "codex"`) {
		t.Fatalf("stdout missing authenticator:\n%s", stdout)
	}
}

func TestCLIAuthLoginRequiresAuthenticatorWithClearUsage(t *testing.T) {
	stdout, stderr, err := executeAGWCTL(t, "cliauth", "login")
	if err == nil {
		t.Fatalf("expected login error\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	msg := err.Error()
	if !strings.Contains(msg, "--authenticator is required") {
		t.Fatalf("error = %q, want missing authenticator message", msg)
	}
	if !strings.Contains(msg, "supported authenticators: claudecode, codex, gemini") &&
		!strings.Contains(msg, "supported authenticators: codex, claudecode, gemini") &&
		!strings.Contains(msg, "supported authenticators: gemini, codex, claudecode") {
		t.Fatalf("error = %q, want supported authenticators", msg)
	}
	if !strings.Contains(msg, "Usage:\n  agwctl cliauth login") {
		t.Fatalf("error = %q, want login usage", msg)
	}
}

func TestCLIAuthLoginRejectsUnknownAuthenticatorWithClearUsage(t *testing.T) {
	stdout, stderr, err := executeAGWCTL(t, "cliauth", "login", "--authenticator", "bad-auth")
	if err == nil {
		t.Fatalf("expected login error\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	msg := err.Error()
	if !strings.Contains(msg, `unsupported --authenticator "bad-auth"`) {
		t.Fatalf("error = %q, want unsupported authenticator message", msg)
	}
	if !strings.Contains(msg, "supported authenticators:") {
		t.Fatalf("error = %q, want supported authenticators", msg)
	}
	if !strings.Contains(msg, "Usage:\n  agwctl cliauth login") {
		t.Fatalf("error = %q, want login usage", msg)
	}
}

func TestGatewayValidateCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providerTypes:
  - provider_type: openai
    enabled: true
providers:
  - id: openai-main
    provider_type: openai
routes:
  - id: chat-prod
    protocol: openai
    match:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: openai-main
virtualKeys:
  - id: vk-local-test
    allowed_route_ids:
      - chat-prod
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdout, stderr, err := executeAGWCTL(t, "gateway", "validate", "-f", path)
	if err != nil {
		t.Fatalf("gateway validate: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "gateway bundle is valid:") {
		t.Fatalf("stdout missing success message:\n%s", stdout)
	}
}

func TestGatewayValidateCommandAcceptsCodexProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway-codex.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providerTypes:
  - provider_type: codex
    enabled: true
providers:
  - id: codex-test
    provider_type: codex
routes:
  - id: code-codex
    protocol: openai
    match:
      path_prefix: /codecodex
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: codex-test
virtualKeys:
  - id: vk-local-code-codex
    allowed_route_ids:
      - code-codex
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdout, stderr, err := executeAGWCTL(t, "gateway", "validate", "-f", path)
	if err != nil {
		t.Fatalf("gateway validate: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "gateway bundle is valid:") {
		t.Fatalf("stdout missing success message:\n%s", stdout)
	}
}

func TestGatewayValidateCommandReturnsLocalValidationError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: dup
    provider_type: openai
  - id: dup
    provider_type: openai
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdout, stderr, err := executeAGWCTL(t, "gateway", "validate", "-f", path)
	if err == nil {
		t.Fatalf("gateway validate error = nil\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(err.Error(), `providers["dup"]: duplicate id`) {
		t.Fatalf("validate error = %v, want duplicate provider id", err)
	}
}

func TestGatewayApplyCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    default_model: gpt-4.1
virtualKeys:
  - id: vk-local-test
    allowed_route_ids: []
cliAuthAuthenticators:
  - name: claudecode
    enabled: true
    config:
      no_browser: true
      transport_profile: browser_like_tls
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var providerUpdated atomic.Bool
	var virtualKeyCreated atomic.Bool
	var authenticatorUpdated atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case r.URL.Path == "/admin/provider_types":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/providers" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":            "openai-main",
						"provider_type": "openai",
						"default_model": "gpt-4o-mini",
						"source":        "dynamic",
						"read_only":     false,
					},
				},
			})
		case r.URL.Path == "/admin/providers/openai-main" && r.Method == http.MethodPut:
			providerUpdated.Store(true)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            "openai-main",
				"provider_type": "openai",
				"default_model": "gpt-4.1",
			})
		case r.URL.Path == "/admin/models/managed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/virtual_keys" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/virtual_keys" && r.Method == http.MethodPost:
			virtualKeyCreated.Store(true)
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode virtual key create request: %v", err)
			}
			if _, ok := req["key"]; ok {
				t.Fatalf("virtual key create request unexpectedly carried key: %#v", req)
			}
			if req["id"] != "vk-local-test" {
				t.Fatalf("virtual key create request id = %#v, want %q", req["id"], "vk-local-test")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":  "vk-local-test",
				"key": "vk-generated",
			})
		case r.URL.Path == "/admin/cliauth/authenticators" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/cliauth/authenticators/claudecode" && r.Method == http.MethodPut:
			authenticatorUpdated.Store(true)
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode cliauth authenticator request: %v", err)
			}
			config, ok := req["config"].(map[string]any)
			if !ok {
				t.Fatalf("cliauth authenticator request config = %#v, want object", req["config"])
			}
			if got := config["transport_profile"]; got != "browser_like_tls" {
				t.Fatalf("transport_profile = %#v, want browser_like_tls", got)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "enabled",
				"authenticator": map[string]any{
					"name":    "claudecode",
					"enabled": true,
					"config": map[string]any{
						"no_browser":        true,
						"transport_profile": "browser_like_tls",
					},
				},
			})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	stdout, stderr, err := executeAGWCTL(
		t,
		"--output", "json",
		"gateway",
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"apply",
		"-f", path,
	)
	if err != nil {
		t.Fatalf("gateway apply: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	if !providerUpdated.Load() {
		t.Fatal("expected provider update request")
	}
	if !virtualKeyCreated.Load() {
		t.Fatal("expected virtual key create request")
	}
	if !authenticatorUpdated.Load() {
		t.Fatal("expected cliauth authenticator update request")
	}
	if !strings.Contains(stdout, `"status": "ok"`) {
		t.Fatalf("stdout missing ok status:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"action": "update"`) {
		t.Fatalf("stdout missing update action:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"action": "create"`) {
		t.Fatalf("stdout missing create action:\n%s", stdout)
	}
}

func TestGatewayApplyCommandSkipsUnchangedObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    default_model: gpt-4.1
virtualKeys:
  - id: vk-local-test
    allowed_route_ids: []
cliAuthAuthenticators:
  - name: codex
    enabled: false
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var providerWriteCount atomic.Int32
	var virtualKeyWriteCount atomic.Int32
	var authenticatorWriteCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case r.URL.Path == "/admin/provider_types":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/providers" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":            "openai-main",
						"provider_type": "openai",
						"default_model": "gpt-4.1",
						"source":        "dynamic",
						"read_only":     false,
					},
				},
			})
		case r.URL.Path == "/admin/providers/openai-main" && r.Method == http.MethodPut:
			providerWriteCount.Add(1)
			t.Fatalf("unexpected provider update request for unchanged object")
		case r.URL.Path == "/admin/models/managed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/virtual_keys" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":                "vk-local-test",
						"key":               "vk-generated",
						"allowed_route_ids": []string{},
						"source":            "dynamic",
						"read_only":         false,
					},
				},
			})
		case r.URL.Path == "/admin/virtual_keys" && r.Method == http.MethodPost:
			virtualKeyWriteCount.Add(1)
			t.Fatalf("unexpected virtual key create request for unchanged object")
		case r.URL.Path == "/admin/virtual_keys/vk-local-test" && r.Method == http.MethodPut:
			virtualKeyWriteCount.Add(1)
			t.Fatalf("unexpected virtual key update request for unchanged object")
		case r.URL.Path == "/admin/cliauth/authenticators" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"name":          "codex",
						"provider_type": "openai",
						"enabled":       false,
						"config":        map[string]any{},
					},
				},
			})
		case r.URL.Path == "/admin/cliauth/authenticators/codex" && r.Method == http.MethodPut:
			authenticatorWriteCount.Add(1)
			t.Fatalf("unexpected cliauth authenticator update request for unchanged object")
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	stdout, stderr, err := executeAGWCTL(
		t,
		"--output", "json",
		"gateway",
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"apply",
		"-f", path,
	)
	if err != nil {
		t.Fatalf("gateway apply unchanged: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	if providerWriteCount.Load() != 0 || virtualKeyWriteCount.Load() != 0 || authenticatorWriteCount.Load() != 0 {
		t.Fatalf("unexpected writes: provider=%d virtualKey=%d authenticator=%d", providerWriteCount.Load(), virtualKeyWriteCount.Load(), authenticatorWriteCount.Load())
	}
	if !strings.Contains(stdout, `"action": "skip"`) {
		t.Fatalf("stdout missing skip action:\n%s", stdout)
	}
}

func TestGatewayApplyCommandFailsOnReadOnlyObjectDrift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    default_model: gpt-4.1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case r.URL.Path == "/admin/provider_types":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/providers" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":            "openai-main",
						"provider_type": "openai",
						"default_model": "gpt-4o-mini",
						"source":        "static",
						"read_only":     true,
					},
				},
			})
		case r.URL.Path == "/admin/models/managed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/virtual_keys" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/cliauth/authenticators" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	stdout, stderr, err := executeAGWCTL(
		t,
		"--output", "json",
		"gateway",
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"apply",
		"-f", path,
	)
	if err == nil {
		t.Fatalf("gateway apply read-only drift error = nil\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(err.Error(), "gateway apply finished with 1 error") {
		t.Fatalf("apply error = %v", err)
	}
	if !strings.Contains(stdout, `"status": "error"`) {
		t.Fatalf("stdout missing error status:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"error": "provider \"openai-main\" is read-only"`) {
		t.Fatalf("stdout missing read-only provider error:\n%s", stdout)
	}
}

func TestGatewayExportCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/provider_types":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"provider_type": "openai", "enabled": true},
				},
			})
		case "/admin/providers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":            "openai-main",
						"provider_type": "openai",
						"default_model": "gpt-4.1",
						"source":        "dynamic",
						"read_only":     false,
					},
				},
			})
		case "/admin/models/managed":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"provider_id":    "openai-main",
						"upstream_model": "gpt-4.1",
						"enabled":        true,
					},
				},
			})
		case "/admin/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":       "chat-prod",
						"protocol": "openai",
						"match": map[string]any{
							"path_prefix": "/",
							"methods":     []string{"POST"},
						},
						"auth_policy": map[string]any{
							"require_virtual_key": true,
						},
						"target_policy": map[string]any{
							"provider_target": map[string]any{
								"provider_id": "openai-main",
							},
						},
						"source":    "dynamic",
						"read_only": false,
					},
				},
			})
		case "/admin/virtual_keys":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":                "vk-local-test",
						"key":               "vk-local-test",
						"allowed_route_ids": []string{"chat-prod"},
						"source":            "dynamic",
						"read_only":         false,
					},
				},
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
		"gateway",
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"export",
	)
	if err != nil {
		t.Fatalf("gateway export: %v\nstderr=%s", err, stderr)
	}
	for _, want := range []string{
		"apiVersion: gateway.agw/v1alpha1",
		"kind: GatewayBundle",
		"providerTypes:",
		"providers:",
		"routes:",
		"virtualKeys:",
		"cliAuthAuthenticators:",
		"id: openai-main",
		"id: vk-local-test",
		"name: codex",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "\n    key: ") || strings.Contains(stdout, "\n  key: ") {
		t.Fatalf("stdout unexpectedly included generated virtual key value:\n%s", stdout)
	}
}

func TestGatewayExportThenValidateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	exportPath := filepath.Join(dir, "exported.gateway.yaml")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/provider_types":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"provider_type": "openai", "enabled": true},
				},
			})
		case "/admin/providers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":            "openai-main",
						"provider_type": "openai",
						"default_model": "gpt-4.1",
						"source":        "dynamic",
						"read_only":     false,
					},
				},
			})
		case "/admin/models/managed":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case "/admin/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":       "chat-prod",
						"protocol": "openai",
						"match": map[string]any{
							"path_prefix": "/",
							"methods":     []string{"POST"},
						},
						"auth_policy": map[string]any{
							"require_virtual_key": true,
						},
						"target_policy": map[string]any{
							"provider_target": map[string]any{
								"provider_id": "openai-main",
							},
						},
						"source":    "dynamic",
						"read_only": false,
					},
				},
			})
		case "/admin/virtual_keys":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":                "vk-local-test",
						"key":               "vk-local-test",
						"allowed_route_ids": []string{"chat-prod"},
						"source":            "dynamic",
						"read_only":         false,
					},
				},
			})
		case "/admin/cliauth/authenticators":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"name":          "codex",
						"provider_type": "openai",
						"enabled":       true,
						"config": map[string]any{
							"device_flow": true,
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	_, stderr, err := executeAGWCTL(
		t,
		"gateway",
		"--admin-addr", srv.URL,
		"--admin-user", "admin",
		"--admin-password", "secret",
		"export",
		"-f", exportPath,
	)
	if err != nil {
		t.Fatalf("gateway export: %v\nstderr=%s", err, stderr)
	}

	stdout, stderr, err := executeAGWCTL(t, "gateway", "validate", "-f", exportPath)
	if err != nil {
		t.Fatalf("gateway validate exported file: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "gateway bundle is valid:") {
		t.Fatalf("stdout missing validation success:\n%s", stdout)
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
