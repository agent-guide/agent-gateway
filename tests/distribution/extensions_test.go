package distribution_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/extension/contracttest"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExtensionProcesses(t *testing.T) {
	dir := os.Getenv("AGW_DISTRIBUTION_BIN_DIR")
	if dir == "" {
		t.Skip("consumer binaries required")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(filepath.Join(dir, "agw"), "manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var linked distribution.Assembly
	if err = json.Unmarshal(raw, &linked); err != nil {
		t.Fatal(err)
	}
	fixture := false
	for _, d := range linked.Modules {
		if d.ID == "example.fixture.mutable" {
			fixture = true
		}
	}
	if !fixture {
		t.Skip("external configuration fixture is not linked")
	}
	for _, binary := range []string{"agw", "agwd"} {
		t.Run(binary, func(t *testing.T) {
			work := t.TempDir()
			eventPath := filepath.Join(work, "events.jsonl")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			_ = listener.Close()
			initial := extension.Envelope{ModuleID: "example.fixture.entries", SchemaVersion: 1, Config: json.RawMessage(`{"message":"persisted","token":{"$secret_ref":"env:AGW_FIXTURE_SECRET"}}`)}
			db := filepath.Join(work, "config.db")
			path := filepath.Join(work, "config.json")
			hash, err := bcrypt.GenerateFromPassword([]byte("fixture-pass"), bcrypt.MinCost)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--addr", addr, "--admin-addr", addr, "--admin-basic-auth-hash", "fixture:" + string(hash), "--config-store", db, "--static-config", path}
			var config any = map[string]any{"apiVersion": "gateway.agw/v1alpha1", "kind": "GatewayBundle"}
			if binary == "agw" {
				args = []string{"run", "--config", path}
				config = map[string]any{"admin": map[string]any{"disabled": true}, "apps": map[string]any{
					"agent_gateway": map[string]any{"config_store": map[string]any{"sqlite": map[string]string{"sqlite_path": db}}},
					"http": map[string]any{"servers": map[string]any{"test": map[string]any{"listen": []string{addr}, "routes": []any{
						map[string]any{"match": []any{map[string]any{"path": []string{"/admin/*"}}}, "handle": []any{map[string]any{"handler": "authentication", "providers": map[string]any{"http_basic": map[string]any{"accounts": []any{map[string]string{"username": "fixture", "password": string(hash)}}}}}, map[string]string{"handler": "agent_gateway_admin"}}},
						map[string]any{"handle": []any{map[string]any{"handler": "agent_route_dispatcher", "api_handlers": map[string]any{"cc": map[string]any{}}}}},
					}}}},
				}}
			}
			encoded, _ := json.Marshal(config)
			if err = os.WriteFile(path, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(work, "process.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, filepath.Join(dir, binary), args...)
			command.Dir = work
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "AGW_FIXTURE_EVENT_FILE=") && !strings.HasPrefix(entry, "AGW_FIXTURE_SECRET=") && !strings.HasPrefix(entry, "AGW_ADMIN_BASIC_AUTH_HASH=") {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "AGW_FIXTURE_SECRET=process-secret-never-export", "AGW_ADMIN_BASIC_AUTH_HASH=", "AGW_FIXTURE_EVENT_FILE="+eventPath)
			command.Stdout = log
			command.Stderr = log
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			finished := false
			defer func() {
				if !finished {
					_ = command.Process.Kill()
					<-done
				}
			}()
			client := adminclient.New(adminclient.Config{BaseURL: "http://" + addr, BasicAuth: "fixture:fixture-pass", HTTPClient: &http.Client{Timeout: time.Second}})
			deadline := time.After(10 * time.Second)
			var manifest *distribution.Assembly
			for {
				manifest, err = client.Manifest(ctx)
				if err == nil {
					break
				}
				select {
				case err := <-done:
					finished = true
					logs, _ := os.ReadFile(logPath)
					t.Fatalf("startup: %v %s", err, logs)
				case <-deadline:
					t.Fatal("startup timeout")
				case <-time.After(25 * time.Millisecond):
				}
			}
			if !reflect.DeepEqual(linked, *manifest) {
				t.Fatal("Caddy/standalone manifest mismatch")
			}

			if _, err = client.PutExtension(ctx, initial); err != nil {
				t.Fatal(err)
			}
			httpClient := &http.Client{Timeout: 3 * time.Second}
			send := func(req *http.Request) (*http.Response, error) {
				req.SetBasicAuth("fixture", "fixture-pass")
				return httpClient.Do(req)
			}
			moduleURL := "http://" + addr + "/admin/extensions/" + initial.ModuleID
			unauth, err := httpClient.Get(moduleURL + "/status")
			if err != nil {
				t.Fatal(err)
			}
			unauth.Body.Close()
			if unauth.StatusCode != 401 {
				t.Fatalf("contributor bypassed mount authentication: %d", unauth.StatusCode)
			}
			status := func() struct {
				Message string `json:"message"`
				Resets  int64  `json:"resets"`
			} {
				req, _ := http.NewRequest("GET", moduleURL+"/status", nil)
				resp, err := send(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				var value struct {
					Message string `json:"message"`
					Resets  int64  `json:"resets"`
				}
				if resp.StatusCode != 200 {
					t.Fatalf("contributor status: %d", resp.StatusCode)
				}
				if err = json.NewDecoder(resp.Body).Decode(&value); err != nil {
					t.Fatal(err)
				}
				if value.Message != "persisted" {
					t.Fatalf("prepared config missing: %+v", value)
				}
				return value
			}
			before := status()
			contracttest.AdminBody(t, contracttest.AdminBodyFixture{URL: moduleURL + "/reset", Method: "POST", Body: []byte(`{}`), SuccessStatus: 200, Send: send, CheckUnchanged: func(t *testing.T) {
				if status().Resets != before.Resets {
					t.Fatal("oversized contributed write committed")
				}
			}})
			if status().Resets != before.Resets+1 {
				t.Fatal("supported contributed mutation missing")
			}
			contracttest.AdminBody(t, contracttest.AdminBodyFixture{URL: "http://" + addr + "/admin/llm/providers", Method: "POST", Body: []byte(`{"id":"body-proof","provider_type":"openai","base_url":"https://example.com"}`), SuccessStatus: 201, Send: send, CheckUnchanged: func(t *testing.T) {
				req, _ := http.NewRequest("GET", "http://"+addr+"/admin/llm/providers/body-proof", nil)
				resp, err := send(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 404 {
					t.Fatalf("oversized Community write committed: %d", resp.StatusCode)
				}
			}})
			current, err := client.GetExtension(ctx, initial.ModuleID)
			if err != nil || !bytes.Equal(current.Config, initial.Config) {
				t.Fatalf("persisted config: %v %+v", err, current)
			}
			if _, err = client.PutExtension(ctx, initial); err != nil {
				t.Fatal(err)
			}
			unknown := initial
			unknown.ModuleID = "example.fixture.unlinked"
			if _, err = client.PutExtension(ctx, unknown); err == nil {
				t.Fatal("unknown module accepted")
			}
			dynamic := initial
			dynamic.ModuleID = "example.fixture.mutable"
			dynamic.Config = json.RawMessage(`{"message":"dynamic","token":{"$secret_ref":"env:AGW_FIXTURE_SECRET"}}`)
			if _, err = client.PutExtension(ctx, dynamic); err != nil {
				t.Fatal(err)
			}
			checkDynamicRoute := func(wantStatus int, wantMessage string) {
				t.Helper()
				req, _ := http.NewRequest("GET", "http://"+addr+"/admin/extensions/"+dynamic.ModuleID+"/status", nil)
				resp, err := send(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != wantStatus || (wantMessage != "" && !bytes.Contains(body, []byte(wantMessage))) {
					t.Fatalf("dynamic route generation: %d %s", resp.StatusCode, body)
				}
			}
			checkDynamicRoute(200, "dynamic")
			applyPath := filepath.Join(work, "apply.yaml")
			apply := `apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
extensions:
- module_id: example.fixture.entries
  schema_version: 1
  config:
    message: persisted
    token: {"$secret_ref": "env:AGW_FIXTURE_SECRET"}
- module_id: example.fixture.mutable
  schema_version: 1
  config:
    message: applied
    token: {"$secret_ref": "env:AGW_FIXTURE_SECRET"}
`
			if err = os.WriteFile(applyPath, []byte(apply), 0600); err != nil {
				t.Fatal(err)
			}
			runCLI := func(args ...string) []byte {
				t.Helper()
				cmd := exec.CommandContext(ctx, filepath.Join(dir, "agwctl"), append([]string{"--admin-addr", "http://" + addr, "--admin-basic-auth", "fixture:fixture-pass"}, args...)...)
				cmd.Dir = work
				result, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("CLI %v: %v %s", args, err, result)
				}
				return result
			}
			runCLI("validate", "--file", applyPath)
			runCLI("apply", "--file", applyPath)
			exported := runCLI("export")
			if strings.Contains(string(exported), "process-secret-never-export") || !strings.Contains(string(exported), "env:AGW_FIXTURE_SECRET") || strings.Contains(string(exported), "source:") {
				t.Fatalf("unsafe or incomplete export: %s", exported)
			}
			if err = command.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				finished = true
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("restart shutdown timeout")
			}
			processEnv := command.Env
			command = exec.CommandContext(ctx, filepath.Join(dir, binary), args...)
			command.Dir = work
			command.Env = processEnv
			command.Stdout, command.Stderr = log, log
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			finished = false
			go func() { done <- command.Wait() }()
			restartDeadline := time.After(10 * time.Second)
			for {
				if _, err = client.Manifest(ctx); err == nil {
					break
				}
				select {
				case err = <-done:
					finished = true
					t.Fatalf("restart: %v", err)
				case <-restartDeadline:
					t.Fatal("restart startup timeout")
				case <-time.After(25 * time.Millisecond):
				}
			}
			if status().Message != "persisted" {
				t.Fatal("initial extension not restored")
			}
			current, err = client.GetExtension(ctx, dynamic.ModuleID)
			if err != nil || !strings.Contains(string(current.Config), "applied") {
				t.Fatalf("apply missing: %v", err)
			}
			checkDynamicRoute(200, "applied")
			if err = client.DeleteExtension(ctx, initial.ModuleID); err == nil {
				t.Fatal("required dependency deleted")
			}
			if err = client.DeleteExtension(ctx, dynamic.ModuleID); err != nil {
				t.Fatal(err)
			}
			checkDynamicRoute(503, "extension_unavailable")
			checkLimiterProcess(t, ctx, "http://"+addr, client, send)
			if err = command.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				finished = true
				if err != nil {
					logs, _ := os.ReadFile(logPath)
					t.Fatalf("shutdown: %v %s", err, logs)
				}
			case <-ctx.Done():
				t.Fatal("shutdown timeout")
			}

			raw, err := os.ReadFile(eventPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"process-secret-never-export", "env:AGW_FIXTURE_SECRET", "$secret_ref", "\"message\"", "\"token\""} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("private configuration escaped public DTO")
				}
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			seen := map[string]bool{}
			count := 0
			limiterSeen := map[string]bool{}
			resetSeen := false
			for {
				var e event.Envelope
				err := decoder.Decode(&e)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := event.Validate(e, attribution.Policy{}); err != nil {
					t.Fatal(err)
				}
				if e.Family == "limiter.admission" {
					limiterSeen[e.Outcome+"/"+e.Reason] = true
					if e.Attribution.Tenant != "" || e.Attribution.VirtualKeyID != "limiter-proof" || e.Attribution.RouteID != "limiter-proof" {
						t.Fatalf("admission attribution %+v", e.Attribution)
					}
					continue
				}
				if e.Family != "extension.mutation" {
					t.Fatal("unexpected public family")
				}
				resetSeen = resetSeen || e.Mutation.StateReset
				seen[e.Outcome+"/"+e.Mutation.Operation] = true
				count++
			}
			if !resetSeen || !limiterSeen["allow/"] || !limiterSeen["deny/rate_limited"] || !limiterSeen["deny/limiter_unavailable"] || !limiterSeen["degraded/limiter_unavailable"] {
				t.Fatalf("limiter event evidence %v reset=%v", limiterSeen, resetSeen)
			}
			if count < 4 || !seen["success/put"] || !seen["failure/put"] || !seen["success/delete"] || !seen["failure/delete"] || !seen["success/reset"] {
				t.Fatalf("event lifecycle evidence %d %v", count, seen)
			}
		})
	}
}
