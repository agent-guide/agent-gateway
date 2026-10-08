package distribution_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Set AGW_DISTRIBUTION_BIN_DIR to test already-built binaries without rebuilding
// them in each package test. This suite also runs against the external consumer.
func TestCommandProcesses(t *testing.T) {
	dir := os.Getenv("AGW_DISTRIBUTION_BIN_DIR")
	if dir == "" {
		t.Skip("set AGW_DISTRIBUTION_BIN_DIR to built agw, agwd and agwctl")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"agw", "agwd", "agwctl"} {
		t.Run(bin, func(t *testing.T) {
			for _, tc := range []struct {
				args []string
				code int
				text string
			}{
				{[]string{"--help"}, 0, "Usage:"},
				{[]string{"--definitely-invalid-flag"}, 1, "unknown flag"},
			} {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, filepath.Join(dir, bin), tc.args...)
				cmd.Dir = t.TempDir()
				var out, stderr bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &stderr
				err := cmd.Run()
				code := 0
				if err != nil {
					ee, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatal(err)
					}
					code = ee.ExitCode()
				}
				if code != tc.code {
					t.Fatalf("%v: exit=%d, want %d; stderr=%s", tc.args, code, tc.code, &stderr)
				}
				if !strings.Contains(out.String()+stderr.String(), tc.text) {
					t.Fatalf("%v: missing %q in stdout=%s stderr=%s", tc.args, tc.text, &out, &stderr)
				}
				if tc.code != 0 && out.Len() != 0 {
					t.Fatalf("failure unexpectedly wrote stdout: %s", &out)
				}
			}
		})
	}
}

func TestDotenvProcessPrecedence(t *testing.T) {
	dir := os.Getenv("AGW_DISTRIBUTION_BIN_DIR")
	if dir == "" {
		t.Skip("set AGW_DISTRIBUTION_BIN_DIR")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"agwd", "agwctl"} {
		t.Run(bin, func(t *testing.T) {
			work := t.TempDir()
			for file, contents := range map[string]string{
				".env.local": "AGW_ADMIN_ADDR=http://local:1234\nAGW_ADMIN_BASIC_AUTH_HASH=local:hash\n",
				".env":       "AGW_ADMIN_ADDR=http://dotenv:2345\nAGW_ADMIN_BASIC_AUTH_HASH=dotenv:hash\n",
			} {
				if err := os.WriteFile(filepath.Join(work, file), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, shell := range []bool{false, true} {
				cmd := exec.Command(filepath.Join(dir, bin), "--help")
				cmd.Dir = work
				for _, e := range os.Environ() {
					if !strings.HasPrefix(e, "AGW_ADMIN_ADDR=") && !strings.HasPrefix(e, "AGW_ADMIN_BASIC_AUTH_HASH=") {
						cmd.Env = append(cmd.Env, e)
					}
				}
				want := "http://local:1234"
				if bin == "agwd" {
					want = "local:hash"
				}
				if shell {
					cmd.Env = append(cmd.Env, "AGW_ADMIN_ADDR=http://shell:3456", "AGW_ADMIN_BASIC_AUTH_HASH=shell:hash")
					want = "http://shell:3456"
					if bin == "agwd" {
						want = "shell:hash"
					}
				}
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("help: %v: %s", err, out)
				}
				if !strings.Contains(string(out), want) {
					t.Fatalf("want %q in %s", want, out)
				}
			}
			if err := os.WriteFile(filepath.Join(work, ".env.local"), []byte("invalid='unterminated"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(filepath.Join(dir, bin), "--help")
			cmd.Dir = work
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil || out.Len() != 0 || !strings.Contains(stderr.String(), "load .env.local:") {
				t.Fatalf("malformed dotenv: err=%v stdout=%s stderr=%s", err, &out, &stderr)
			}
		})
	}
}

func TestServerSignals(t *testing.T) {
	dir := os.Getenv("AGW_DISTRIBUTION_BIN_DIR")
	if dir == "" {
		t.Skip("set AGW_DISTRIBUTION_BIN_DIR")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A caller's daemon auth configuration must not configure this anonymous fixture.
	t.Setenv("AGW_ADMIN_BASIC_AUTH_HASH", "review-invalid-parent-auth")
	for _, bin := range []string{"agw", "agwd"} {
		for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
			t.Run(fmt.Sprintf("%s/%s", bin, sig), func(t *testing.T) {
				work := t.TempDir()
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				addr := listener.Addr().String()
				listener.Close()
				args := []string{"--addr", addr, "--admin-addr", addr, "--admin-basic-auth-hash", "", "--config-store", filepath.Join(work, "config.db")}
				readyURL := "http://" + addr + "/admin/virtual_keys"
				if bin == "agw" {
					config := fmt.Sprintf(`{"admin":{"disabled":true},"apps":{"http":{"servers":{"test":{"listen":[%q],"routes":[{"handle":[{"handler":"static_response","body":"ready"}]}]}}}}}`, addr)
					path := filepath.Join(work, "caddy.json")
					if err := os.WriteFile(path, []byte(config), 0600); err != nil {
						t.Fatal(err)
					}
					args = []string{"run", "--config", path}
					readyURL = "http://" + addr + "/"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, filepath.Join(dir, bin), args...)
				cmd.Dir = work
				var out, stderr bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				defer func() { _ = cmd.Process.Kill() }()
				client := http.Client{Timeout: 200 * time.Millisecond}
				deadline := time.After(10 * time.Second)
				ready := false
				for !ready {
					res, err := client.Get(readyURL)
					if err == nil {
						res.Body.Close()
						ready = res.StatusCode == http.StatusOK
					}
					if ready {
						break
					}
					select {
					case err := <-done:
						t.Fatalf("exited before ready: %v: %s", err, &stderr)
					case <-deadline:
						_ = cmd.Process.Kill()
						<-done
						t.Fatalf("startup timeout: %s", &stderr)
					case <-time.After(25 * time.Millisecond):
					}
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("signal shutdown: %v: %s", err, &stderr)
					}
				case <-ctx.Done():
					_ = cmd.Process.Kill()
					<-done
					t.Fatalf("shutdown deadline: %s", &stderr)
				}
			})
		}
	}
}
