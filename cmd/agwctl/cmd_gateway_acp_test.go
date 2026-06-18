package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGatewayACPServiceCommands(t *testing.T) {
	var deleteCalled atomic.Bool
	var gotSessionsQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/acp/services":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":              "codex-main",
						"name":            "Codex",
						"agent_type":      "codex",
						"cwd":             "/tmp/acp-codex-test",
						"permission_mode": "auto_approve",
						"disabled":        false,
						"source":          "config_store",
					},
				},
			})
		case "/admin/acp/services/codex-main":
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":         "codex-main",
					"name":       "Codex",
					"agent_type": "codex",
					"cwd":        "/tmp/acp-codex-test",
					"source":     "config_store",
				})
			case http.MethodDelete:
				deleteCalled.Store(true)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "deleted"})
			default:
				t.Fatalf("unexpected method: %s", r.Method)
			}
		case "/admin/acp/services/codex-main/sessions":
			gotSessionsQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessions": []map[string]any{
					{"session_id": "sess-1", "cwd": "/tmp/acp-codex-test", "title": "hello"},
				},
			})
		case "/admin/acp/services/codex-main/sessions/sess-1/transcript":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session_id": "sess-1",
				"messages": []map[string]any{
					{"role": "user", "text": "ping"},
					{"role": "assistant", "text": "pong"},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	gatewayArgs := func(args ...string) []string {
		return append([]string{
			"gateway",
			"--admin-addr", srv.URL,
			"--admin-basic-auth", "admin:secret",
		}, args...)
	}

	stdout, stderr, err := executeAGWCTL(t, gatewayArgs("acp-service", "list")...)
	if err != nil {
		t.Fatalf("gateway acp-service list: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "AGENT-TYPE") || !strings.Contains(stdout, "codex-main") {
		t.Fatalf("stdout missing acp service table data:\n%s", stdout)
	}

	stdout, stderr, err = executeAGWCTL(t, append([]string{"--output", "json"}, gatewayArgs("acp-service", "get", "codex-main")...)...)
	if err != nil {
		t.Fatalf("gateway acp-service get: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, `"id": "codex-main"`) {
		t.Fatalf("stdout missing acp service id:\n%s", stdout)
	}

	stdout, stderr, err = executeAGWCTL(t, gatewayArgs("acp-service", "sessions", "codex-main", "--cwd", "/tmp/acp-codex-test")...)
	if err != nil {
		t.Fatalf("gateway acp-service sessions: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(gotSessionsQuery, "cwd=%2Ftmp%2Facp-codex-test") {
		t.Fatalf("sessions query = %q, want cwd filter", gotSessionsQuery)
	}
	if !strings.Contains(stdout, "sess-1") {
		t.Fatalf("stdout missing session id:\n%s", stdout)
	}

	stdout, stderr, err = executeAGWCTL(t, append([]string{"--output", "json"}, gatewayArgs("acp-service", "transcript", "codex-main", "sess-1")...)...)
	if err != nil {
		t.Fatalf("gateway acp-service transcript: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, `"text": "pong"`) {
		t.Fatalf("stdout missing transcript message:\n%s", stdout)
	}

	stdout, stderr, err = executeAGWCTL(t, append([]string{"--output", "json"}, gatewayArgs("acp-service", "delete", "codex-main")...)...)
	if err != nil {
		t.Fatalf("gateway acp-service delete: %v\nstderr=%s", err, stderr)
	}
	if !deleteCalled.Load() {
		t.Fatal("expected acp service delete request")
	}
	if !strings.Contains(stdout, `"status": "deleted"`) {
		t.Fatalf("stdout missing deleted status:\n%s", stdout)
	}
}

func TestGatewayACPRouteCommands(t *testing.T) {
	var deleteCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/acp/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":           "acp-codex",
						"kind":         "acp",
						"protocol":     "acp",
						"service_id":   "codex-main",
						"match_policy": map[string]any{"path_prefix": "/acp/codex"},
						"auth_policy":  map[string]any{"require_virtual_key": false},
						"source":       "store",
					},
				},
			})
		case "/admin/acp/routes/acp-codex":
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":         "acp-codex",
					"kind":       "acp",
					"protocol":   "acp",
					"service_id": "codex-main",
					"source":     "store",
				})
			case http.MethodDelete:
				deleteCalled.Store(true)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "deleted"})
			default:
				t.Fatalf("unexpected method: %s", r.Method)
			}
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	gatewayArgs := func(args ...string) []string {
		return append([]string{
			"gateway",
			"--admin-addr", srv.URL,
			"--admin-basic-auth", "admin:secret",
		}, args...)
	}

	stdout, stderr, err := executeAGWCTL(t, gatewayArgs("acp-route", "list")...)
	if err != nil {
		t.Fatalf("gateway acp-route list: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "PATH-PREFIX") || !strings.Contains(stdout, "/acp/codex") {
		t.Fatalf("stdout missing acp route table data:\n%s", stdout)
	}

	stdout, stderr, err = executeAGWCTL(t, append([]string{"--output", "json"}, gatewayArgs("acp-route", "get", "acp-codex")...)...)
	if err != nil {
		t.Fatalf("gateway acp-route get: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, `"id": "acp-codex"`) {
		t.Fatalf("stdout missing acp route id:\n%s", stdout)
	}

	_, stderr, err = executeAGWCTL(t, append([]string{"--output", "json"}, gatewayArgs("acp-route", "delete", "acp-codex")...)...)
	if err != nil {
		t.Fatalf("gateway acp-route delete: %v\nstderr=%s", err, stderr)
	}
	if !deleteCalled.Load() {
		t.Fatal("expected acp route delete request")
	}
}

func TestGatewayACPRuntimeCommands(t *testing.T) {
	var closeCalled atomic.Bool
	var permissionBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":    "test-token",
				"username": "admin",
			})
		case "/admin/acp/runtime":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"in_flight": []map[string]any{{"scope": "codex-main\x00/tmp\x00t1\x00\x00"}},
				"instances": []map[string]any{
					{
						"scope":      "codex-main\x00/tmp\x00t1\x00\x00",
						"session_id": "sess-1",
						"alive":      true,
						"active":     false,
						"last_used":  "2026-06-12T00:00:00Z",
						"metadata":   map[string]any{},
					},
				},
				"pending_permissions": []map[string]any{},
			})
		case "/admin/acp/runtime/inflight":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"scope": "codex-main\x00/tmp\x00t1\x00\x00"}},
			})
		case "/admin/acp/runtime/threads/codex-main/t1":
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			closeCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{"closed": 1})
		case "/admin/acp/runtime/permissions/perm-1":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			permissionBody, _ = io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "resolved"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	gatewayArgs := func(args ...string) []string {
		return append([]string{
			"gateway",
			"--admin-addr", srv.URL,
			"--admin-basic-auth", "admin:secret",
		}, args...)
	}

	stdout, stderr, err := executeAGWCTL(t, gatewayArgs("acp-runtime", "get")...)
	if err != nil {
		t.Fatalf("gateway acp-runtime get: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "sess-1") {
		t.Fatalf("stdout missing instance session id:\n%s", stdout)
	}

	stdout, stderr, err = executeAGWCTL(t, gatewayArgs("acp-runtime", "inflight")...)
	if err != nil {
		t.Fatalf("gateway acp-runtime inflight: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "SCOPE") {
		t.Fatalf("stdout missing inflight table:\n%s", stdout)
	}

	stdout, stderr, err = executeAGWCTL(t, append([]string{"--output", "json"}, gatewayArgs("acp-runtime", "close-thread", "codex-main", "t1")...)...)
	if err != nil {
		t.Fatalf("gateway acp-runtime close-thread: %v\nstderr=%s", err, stderr)
	}
	if !closeCalled.Load() {
		t.Fatal("expected close-thread request")
	}
	if !strings.Contains(stdout, `"closed": 1`) {
		t.Fatalf("stdout missing closed status:\n%s", stdout)
	}

	_, stderr, err = executeAGWCTL(t, append([]string{"--output", "json"}, gatewayArgs(
		"acp-runtime", "resolve-permission", "perm-1",
		"--outcome", "selected",
		"--option-id", "allow-once",
	)...)...)
	if err != nil {
		t.Fatalf("gateway acp-runtime resolve-permission: %v\nstderr=%s", err, stderr)
	}
	var decision struct {
		RequestID string `json:"request_id"`
		Outcome   string `json:"outcome"`
		OptionID  string `json:"option_id"`
	}
	if err := json.Unmarshal(permissionBody, &decision); err != nil {
		t.Fatalf("decode permission body: %v\nbody=%s", err, permissionBody)
	}
	if decision.RequestID != "perm-1" || decision.Outcome != "selected" || decision.OptionID != "allow-once" {
		t.Fatalf("permission decision = %+v, want perm-1/selected/allow-once", decision)
	}

	// Package-level flag vars persist across executeAGWCTL calls in one test
	// process; reset to simulate a fresh invocation without --outcome.
	gatewayACPPermissionOutcome = ""
	_, _, err = executeAGWCTL(t, gatewayArgs("acp-runtime", "resolve-permission", "perm-1")...)
	if err == nil || !strings.Contains(err.Error(), "--outcome is required") {
		t.Fatalf("resolve-permission without outcome: err = %v, want --outcome required", err)
	}
}
