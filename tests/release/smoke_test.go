package release_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const processTimeout = 15 * time.Second

type childProcess struct {
	name    string
	cmd     *exec.Cmd
	logPath string
	done    chan error
}

func TestReleaseProcesses(t *testing.T) {
	if os.Getenv("AGW_RELEASE_SMOKE") != "1" {
		t.Skip("set AGW_RELEASE_SMOKE=1 or run make smoke-release")
	}
	repo := repositoryRoot(t)
	temp := t.TempDir()
	for _, binary := range []string{"agw", "agwd", "agwctl"} {
		if _, err := os.Stat(filepath.Join(repo, binary)); err != nil {
			t.Fatalf("%s is not built; run make smoke-release: %v", binary, err)
		}
	}

	exampleBinary := filepath.Join(temp, "a2a-example")
	runCommand(t, repo, "go", "build", "-o", exampleBinary, "./examples/a2a-agent")
	secrets := []string{}
	children := []*childProcess{}
	t.Cleanup(func() {
		for i := len(children) - 1; i >= 0; i-- {
			stopChild(t, children[i])
		}
		if t.Failed() {
			for _, child := range children {
				logChild(t, child, secrets)
			}
		}
	})

	exampleAddressFile := filepath.Join(temp, "a2a-address")
	example := startChild(t, repo, filepath.Join(temp, "a2a.log"), exampleBinary,
		"--listen", "127.0.0.1:0", "--write-address", exampleAddressFile)
	example.name = "a2a-example"
	children = append(children, example)
	exampleURL := pollFile(t, exampleAddressFile)
	pollHTTP(t, exampleURL+"/healthz", http.StatusOK)

	adminPort := freePort(t)
	dataPort := freePort(t)
	caddyAdminPort := freePort(t)
	dbPath := filepath.Join(temp, "caddy", "configstore.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	caddyfile := filepath.Join(temp, "Caddyfile")
	writeFile(t, caddyfile, fmt.Sprintf(`{
	admin 127.0.0.1:%d
	agent_gateway {
		config_store sqlite {
			path %s
		}
	}
}

http://127.0.0.1:%d {
	route /admin/* {
		agent_gateway_admin
	}
}

http://127.0.0.1:%d {
	agent_route_dispatcher {
		agent
	}
}
`, caddyAdminPort, dbPath, adminPort, dataPort))
	agw := startChild(t, repo, filepath.Join(temp, "agw.log"), filepath.Join(repo, "agw"),
		"run", "--config", caddyfile, "--adapter", "caddyfile")
	agw.name = "agw"
	children = append(children, agw)
	adminURL := fmt.Sprintf("http://127.0.0.1:%d", adminPort)
	dataURL := fmt.Sprintf("http://127.0.0.1:%d", dataPort)
	pollHTTP(t, adminURL+"/admin/health", http.StatusOK)

	bundlePath := filepath.Join(temp, "gateway.bundle.yaml")
	writeFile(t, bundlePath, fmt.Sprintf(`apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
agents:
  - id: smoke-agent
    name: Smoke Agent
    runtime:
      type: http
      http:
        card_url: %s/.well-known/agent-card.json
        protocol: a2a
        timeout_seconds: 30
    routes: {}
    resources: {}
    policy: {}
agentRoutes:
  - id: smoke-turn
    protocol: agent
    agent_id: smoke-agent
    match_policy:
      path_prefix: /agents/smoke
    auth_policy:
      require_virtual_key: true
  - id: smoke-a2a
    protocol: a2a
    agent_id: smoke-agent
    match_policy:
      host: 127.0.0.1
      path_prefix: /a2a/smoke
      methods: [GET, POST]
    auth_policy:
      require_virtual_key: true
virtualKeys:
  - id: smoke-key
    allowed_route_ids: [smoke-turn, smoke-a2a]
`, exampleURL))
	agwctl := filepath.Join(repo, "agwctl")
	runCommand(t, repo, agwctl, "--admin-addr", adminURL, "validate", "-f", bundlePath)
	runCommand(t, repo, agwctl, "--admin-addr", adminURL, "apply", "-f", bundlePath)
	keyOutput := runCommand(t, repo, agwctl, "--output", "json", "--admin-addr", adminURL, "virtualkey", "get", "smoke-key")
	var keyView struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(keyOutput, &keyView); err != nil || keyView.Key == "" {
		t.Fatalf("decode generated VirtualKey: %v", err)
	}
	secrets = append(secrets, keyView.Key)

	turnResponse := request(t, http.MethodPost, dataURL+"/agents/smoke/turn", keyView.Key, "",
		`{"input":"release smoke"}`, "application/json")
	if !bytes.Contains(turnResponse, []byte("event: done")) {
		t.Fatalf("common turn did not complete: %s", turnResponse)
	}
	card := request(t, http.MethodGet, dataURL+"/a2a/smoke/.well-known/agent-card.json", "", "", "", "")
	if !bytes.Contains(card, []byte(`"protocolBinding":"JSONRPC"`)) || !bytes.Contains(card, []byte(dataURL+"/a2a/smoke")) {
		t.Fatalf("rewritten Card = %s", card)
	}

	traceID := "11111111111111111111111111111111"
	traceparent := "00-" + traceID + "-2222222222222222-01"
	syncResponse := request(t, http.MethodPost, dataURL+"/a2a/smoke", keyView.Key, traceparent,
		`{"jsonrpc":"2.0","id":"sync","method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"text":"hello"}]}}}`, "application/json")
	if !bytes.Contains(syncResponse, []byte(contextIDForSmoke)) {
		t.Fatalf("native SendMessage response = %s", syncResponse)
	}
	streamResponse := request(t, http.MethodPost, dataURL+"/a2a/smoke", keyView.Key, "",
		`{"jsonrpc":"2.0","id":"stream","method":"SendStreamingMessage","params":{"message":{"messageId":"m2","role":"ROLE_USER","parts":[{"text":"hello"}]}}}`, "application/json")
	if !bytes.Contains(streamResponse, []byte("TASK_STATE_COMPLETED")) {
		t.Fatalf("native SendStreamingMessage response = %s", streamResponse)
	}
	pollInteraction(t, adminURL, traceID)

	standalonePort := freePort(t)
	standaloneAdminPort := freePort(t)
	staticPath := filepath.Join(temp, "standalone.yaml")
	writeFile(t, staticPath, "apiVersion: gateway.agw/v1alpha1\nkind: GatewayBundle\n")
	agwd := startChild(t, repo, filepath.Join(temp, "agwd.log"), filepath.Join(repo, "agwd"),
		"--addr", fmt.Sprintf("127.0.0.1:%d", standalonePort),
		"--admin-addr", fmt.Sprintf("127.0.0.1:%d", standaloneAdminPort),
		"--config-store", filepath.Join(temp, "standalone.db"),
		"--static-config", staticPath)
	agwd.name = "agwd"
	children = append(children, agwd)
	pollHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/admin/health", standaloneAdminPort), http.StatusOK)
}

const contextIDForSmoke = "example-context"

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(dir, "..", ".."))
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runCommand(t *testing.T, dir, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return output
}

func startChild(t *testing.T, dir, logPath, name string, args ...string) *childProcess {
	t.Helper()
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatalf("start %s: %v", name, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		_ = logFile.Close()
	}()
	return &childProcess{name: filepath.Base(name), cmd: cmd, logPath: logPath, done: done}
}

func stopChild(t *testing.T, child *childProcess) {
	t.Helper()
	if child == nil || child.cmd == nil || child.cmd.Process == nil {
		return
	}
	select {
	case <-child.done:
		return
	default:
	}
	_ = child.cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-child.done:
		if err != nil {
			t.Errorf("%s did not exit cleanly: %v", child.name, err)
		}
	case <-time.After(processTimeout):
		_ = child.cmd.Process.Kill()
		<-child.done
		t.Errorf("%s did not stop within %s", child.name, processTimeout)
	}
}

func logChild(t *testing.T, child *childProcess, secrets []string) {
	t.Helper()
	content, err := os.ReadFile(child.logPath)
	if err != nil {
		return
	}
	text := string(content)
	for _, secret := range secrets {
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	t.Logf("%s logs:\n%s", child.name, text)
}

func pollFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(processTimeout)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(content)) != "" {
			return strings.TrimSpace(string(content))
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}

func pollHTTP(t *testing.T, url string, status int) {
	t.Helper()
	deadline := time.Now().Add(processTimeout)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == status {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", url)
}

func request(t *testing.T, method, url, key, traceparent, body, contentType string) []byte {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if traceparent != "" {
		req.Header.Set("traceparent", traceparent)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method == http.MethodPost && strings.Contains(url, "/a2a/") {
		req.Header.Set("A2A-Version", "1.0")
	}
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	response, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s %s returned %d: %s", method, url, response.StatusCode, payload)
	}
	return payload
}

func pollInteraction(t *testing.T, adminURL, traceID string) {
	t.Helper()
	url := adminURL + "/admin/metrics/interactions?trace_id=" + traceID + "&route_id=smoke-a2a"
	deadline := time.Now().Add(processTimeout)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			payload, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK &&
				bytes.Contains(payload, []byte(`"route_protocol":"a2a"`)) && bytes.Contains(payload, []byte(traceID)) {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for persisted A2A interaction for trace %s", traceID)
}
