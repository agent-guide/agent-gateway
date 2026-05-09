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

func TestGatewayCLIAuthAuthenticatorsUpdateCommand(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/cliauth/authenticators/codex":
			gotMethod = r.Method
			gotPath = r.URL.Path
			gotBody, _ = io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "enabled",
				"authenticator": map[string]any{
					"name":          "codex",
					"provider_type": "openai",
					"enabled":       true,
					"config": map[string]any{
						"callback_port": 9002,
						"no_browser":    true,
						"device_flow":   true,
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
		"cliauth", "authenticators", "update", "codex",
		"--callback-port", "9002",
		"--no-browser",
		"--device-flow",
	)
	if err != nil {
		t.Fatalf("gateway cliauth authenticators update: %v\nstderr=%s", err, stderr)
	}
	if gotMethod != http.MethodPut || gotPath != "/admin/cliauth/authenticators/codex" {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(string(gotBody), `"enabled":true`) || !strings.Contains(string(gotBody), `"callback_port":9002`) {
		t.Fatalf("unexpected request body: %s", string(gotBody))
	}
	if !strings.Contains(stdout, `"name": "codex"`) {
		t.Fatalf("stdout missing authenticator:\n%s", stdout)
	}
}

func TestGatewayCLIAuthLoginCommand(t *testing.T) {
	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/cliauth/authenticators/codex/login":
			gotMethod = r.Method
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"login_id":           "login-123",
				"status":             "running",
				"authenticator_name": "codex",
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
		"cliauth", "login", "codex",
	)
	if err != nil {
		t.Fatalf("gateway cliauth login: %v\nstderr=%s", err, stderr)
	}
	if gotMethod != http.MethodPost || gotPath != "/admin/cliauth/authenticators/codex/login" {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(stdout, `"login_id": "login-123"`) {
		t.Fatalf("stdout missing login id:\n%s", stdout)
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
llmApiHandlerTypes:
  - llm_api_handler_type: openai
    enabled: true
providers:
  - id: openai-main
    provider_type: openai
routes:
  - id: chat-prod
    llm_api: openai
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
  - key: vk-local-test
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
  - key: vk-local-test
    allowed_route_ids: []
cliAuthAuthenticators:
  - name: codex
    enabled: true
    config:
      no_browser: true
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
		case r.URL.Path == "/admin/llm_api_handler_types":
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
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"key": "vk-local-test",
			})
		case r.URL.Path == "/admin/cliauth/authenticators" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}})
		case r.URL.Path == "/admin/cliauth/authenticators/codex" && r.Method == http.MethodPut:
			authenticatorUpdated.Store(true)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "enabled",
				"authenticator": map[string]any{
					"name":          "codex",
					"provider_type": "openai",
					"enabled":       true,
					"config": map[string]any{
						"no_browser": true,
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
		"--addr", srv.URL,
		"--user", "admin",
		"--password", "secret",
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
  - key: vk-local-test
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
		case r.URL.Path == "/admin/llm_api_handler_types":
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
						"key":               "vk-local-test",
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
		"--addr", srv.URL,
		"--user", "admin",
		"--password", "secret",
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
		case r.URL.Path == "/admin/llm_api_handler_types":
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
		"--addr", srv.URL,
		"--user", "admin",
		"--password", "secret",
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
		case "/admin/llm_api_handler_types":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"llm_api_handler_type": "openai", "enabled": true},
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
						"id":      "chat-prod",
						"llm_api": "openai",
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
		"--addr", srv.URL,
		"--user", "admin",
		"--password", "secret",
		"export",
	)
	if err != nil {
		t.Fatalf("gateway export: %v\nstderr=%s", err, stderr)
	}
	for _, want := range []string{
		"apiVersion: gateway.agw/v1alpha1",
		"kind: GatewayBundle",
		"providerTypes:",
		"llmApiHandlerTypes:",
		"providers:",
		"routes:",
		"virtualKeys:",
		"cliAuthAuthenticators:",
		"id: openai-main",
		"key: vk-local-test",
		"name: codex",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
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
		case "/admin/llm_api_handler_types":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"llm_api_handler_type": "openai", "enabled": true},
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
						"id":      "chat-prod",
						"llm_api": "openai",
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
		"--addr", srv.URL,
		"--user", "admin",
		"--password", "secret",
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
