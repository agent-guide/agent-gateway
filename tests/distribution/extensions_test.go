package distribution_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/extension"
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
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			_ = listener.Close()
			static := extension.Envelope{ModuleID: "example.fixture.entries", SchemaVersion: 1, Config: json.RawMessage(`{"message":"static","token":{"$secret_ref":"env:AGW_FIXTURE_SECRET"}}`)}
			db := filepath.Join(work, "config.db")
			path := filepath.Join(work, "config.json")
			args := []string{"--addr", addr, "--admin-addr", addr, "--admin-basic-auth-hash", "", "--config-store", db, "--static-config", path}
			var config any = map[string]any{"apiVersion": "gateway.agw/v1alpha1", "kind": "GatewayBundle", "extensions": []extension.Envelope{static}}
			if binary == "agw" {
				args = []string{"run", "--config", path}
				config = map[string]any{"admin": map[string]any{"disabled": true}, "apps": map[string]any{
					"agent_gateway": map[string]any{"config_store": map[string]any{"sqlite": map[string]string{"sqlite_path": db}}, "extensions": []extension.Envelope{static}},
					"http":          map[string]any{"servers": map[string]any{"test": map[string]any{"listen": []string{addr}, "routes": []any{map[string]any{"handle": []any{map[string]string{"handler": "agent_gateway_admin"}}}}}}},
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
				if !strings.HasPrefix(entry, "AGW_FIXTURE_SECRET=") && !strings.HasPrefix(entry, "AGW_ADMIN_BASIC_AUTH_HASH=") {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "AGW_FIXTURE_SECRET=process-secret-never-export", "AGW_ADMIN_BASIC_AUTH_HASH=")
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
			client := adminclient.New(adminclient.Config{BaseURL: "http://" + addr, HTTPClient: &http.Client{Timeout: time.Second}})
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
			current, err := client.GetExtension(ctx, static.ModuleID)
			if err != nil || current.Source != "static" {
				t.Fatalf("static: %v %+v", err, current)
			}
			_, err = client.PutExtension(ctx, static)
			var apiError *adminclient.Error
			if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusConflict {
				t.Fatalf("static writable: %v", err)
			}
			dynamic := static
			dynamic.ModuleID = "example.fixture.mutable"
			dynamic.Config = json.RawMessage(`{"message":"dynamic","token":{"$secret_ref":"env:AGW_FIXTURE_SECRET"}}`)
			if _, err = client.PutExtension(ctx, dynamic); err != nil {
				t.Fatal(err)
			}
			applyPath := filepath.Join(work, "apply.yaml")
			apply := `apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
extensions:
- module_id: example.fixture.entries
  schema_version: 1
  config:
    message: static
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
				cmd := exec.CommandContext(ctx, filepath.Join(dir, "agwctl"), append([]string{"--admin-addr", "http://" + addr}, args...)...)
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
			if strings.Contains(string(exported), "process-secret-never-export") || !strings.Contains(string(exported), "env:AGW_FIXTURE_SECRET") || !strings.Contains(string(exported), "source: static") {
				t.Fatalf("unsafe or incomplete export: %s", exported)
			}
			current, err = client.GetExtension(ctx, dynamic.ModuleID)
			if err != nil || !strings.Contains(string(current.Config), "applied") {
				t.Fatalf("apply missing: %v", err)
			}
			if err = client.DeleteExtension(ctx, static.ModuleID); !errors.As(err, &apiError) || apiError.StatusCode != http.StatusConflict {
				t.Fatalf("static delete: %v", err)
			}
			if err = client.DeleteExtension(ctx, dynamic.ModuleID); err != nil {
				t.Fatal(err)
			}
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
		})
	}
}
