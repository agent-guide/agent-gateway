// Package contracttest exercises independently built distribution binaries.
// It is test infrastructure, not an additional protected production SPI.
package contracttest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	extcontract "github.com/agent-guide/agent-gateway/pkg/extension/contracttest"
	"golang.org/x/crypto/bcrypt"
)

// ProcessFixture supplies external assembly inputs. Binaries are named agw,
// agwd and agwctl; callers may give private commands those names in a temp dir.
// EventFileEnv selects an independently owned JSONL sink. ProbePath identifies
// a read-only contributed POST accepting {}. Envelopes must select a backend.
type ProcessFixture struct {
	BinDir       string
	Runtime      distribution.Assembly
	Validation   distribution.Assembly
	Envelopes    []extension.Envelope
	ProbePath    string
	EventFileEnv string
}

// Distribution runs both real server modes, CLI apply/export, authenticated
// Admin/body contracts, bounded admission, protocol/stream/cancellation and
// persisted usage checks. It requires only public packages and loopback I/O.
func Distribution(t *testing.T, f ProcessFixture) {
	t.Helper()
	if f.BinDir == "" || f.ProbePath == "" || f.EventFileEnv == "" || len(f.Envelopes) == 0 {
		t.Fatal("incomplete process fixture")
	}
	policy, err := f.Runtime.AttributionPolicy()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := f.Runtime.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	validation, err := f.Validation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, dir, binary string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(f.BinDir, binary), args...)
		cmd.Dir = dir
		// The process contract must not inherit a developer's dotenv defaults.
		cmd.Env = cleanEnv(f.EventFileEnv)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", binary, err, raw)
		}
		return raw
	}
	for _, binary := range []string{"agw", "agwd", "agwctl"} {
		raw := run(t, t.TempDir(), binary, "manifest")
		var got distribution.Assembly
		if json.Unmarshal(raw, &got) != nil {
			t.Fatalf("invalid %s manifest", binary)
		}
		want := runtime
		if binary == "agwctl" {
			want = validation
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s manifest mismatch", binary)
		}
	}
	for _, binary := range []string{"agw", "agwd"} {
		t.Run(binary, func(t *testing.T) {
			work := t.TempDir()
			events := filepath.Join(work, "events.jsonl")
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := l.Addr().String()
			_ = l.Close()
			hash, err := bcrypt.GenerateFromPassword([]byte("contract-pass"), bcrypt.MinCost)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(work, "config.json")
			db := filepath.Join(work, "config.db")
			args := []string{"--addr", addr, "--admin-addr", addr, "--config-store", db, "--admin-basic-auth-hash", "contract:" + string(hash)}
			if binary == "agw" {
				config := map[string]any{"admin": map[string]any{"disabled": true}, "apps": map[string]any{
					"agent_gateway": map[string]any{"config_store": map[string]any{"sqlite": map[string]string{"sqlite_path": db}}},
					"http": map[string]any{"servers": map[string]any{"proof": map[string]any{"listen": []string{addr}, "routes": []any{
						map[string]any{"match": []any{map[string]any{"path": []string{"/admin/*"}}}, "handle": []any{map[string]any{"handler": "authentication", "providers": map[string]any{"http_basic": map[string]any{"accounts": []any{map[string]string{"username": "contract", "password": string(hash)}}}}}, map[string]string{"handler": "agent_gateway_admin"}}},
						map[string]any{"handle": []any{map[string]any{"handler": "agent_route_dispatcher", "mcp": true, "api_handlers": map[string]any{"openai": map[string]any{}, "anthropic": map[string]any{}, "cc": map[string]any{}}}}},
					}}}},
				}}
				raw, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(configPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				args = []string{"run", "--config", configPath}
			}
			logPath := filepath.Join(work, "server.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd := exec.Command(filepath.Join(f.BinDir, binary), args...)
			cmd.Dir = work
			cmd.Env = append(cleanEnv(f.EventFileEnv), f.EventFileEnv+"="+events)
			cmd.Stdout = log
			cmd.Stderr = log
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			stopped := false
			t.Cleanup(func() {
				if !stopped {
					_ = cmd.Process.Kill()
					<-done
				}
				if t.Failed() {
					raw, _ := os.ReadFile(logPath)
					t.Logf("server log: %s", raw)
				}
			})
			client := &http.Client{Timeout: 5 * time.Second}
			base := "http://" + addr
			send := func(req *http.Request) (*http.Response, error) {
				req.SetBasicAuth("contract", "contract-pass")
				return client.Do(req)
			}
			poll(t, func() bool {
				req, _ := http.NewRequest("GET", base+"/admin/manifest", nil)
				resp, err := send(req)
				if err != nil {
					return false
				}
				defer resp.Body.Close()
				return resp.StatusCode == 200
			})
			call := func(method, path, body string, status int) []byte {
				t.Helper()
				req, err := http.NewRequest(method, base+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := send(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != status {
					t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
				}
				return raw
			}
			var got distribution.Assembly
			if json.Unmarshal(call("GET", "/admin/manifest", "", 200), &got) != nil || !reflect.DeepEqual(got, runtime) {
				t.Fatal("serving manifest mismatch")
			}
			canceled := make(chan struct{}, 1)
			var upstreamCalls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				var req struct {
					Stream   bool `json:"stream"`
					Messages []struct {
						Content any `json:"content"`
					} `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					http.Error(w, "invalid request", 400)
					return
				}
				if !req.Stream {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"proof","object":"chat.completion","model":"proof","choices":[{"index":0,"message":{"role":"assistant","content":"proof-response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"id\":\"proof\",\"object\":\"chat.completion.chunk\",\"model\":\"proof\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"proof-response\"}}]}\n\n")
				w.(http.Flusher).Flush()
				raw, _ := json.Marshal(req.Messages)
				if bytes.Contains(raw, []byte("cancel-proof")) {
					<-r.Context().Done()
					select {
					case canceled <- struct{}{}:
					default:
					}
					return
				}
				io.WriteString(w, "data: {\"id\":\"proof\",\"object\":\"chat.completion.chunk\",\"model\":\"proof\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			mcpUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var message struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&message) != nil {
					http.Error(w, "invalid MCP request", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				var result any
				switch message.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "proof-mcp", "version": "1"}}
					w.Header().Set("MCP-Session-Id", "proof-session")
				case "tools/list":
					result = map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]string{"type": "object"}}}}
				case "tools/call":
					result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "proof-tool-result"}}}
				default:
					w.WriteHeader(202)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
			}))
			defer mcpUpstream.Close()
			routes := []any{}
			ids := []string{"proof-mcp"}
			for _, protocol := range []string{"openai", "anthropic", "cc"} {
				id := "proof-" + protocol
				ids = append(ids, id)
				routes = append(routes, map[string]any{"id": id, "protocol": protocol, "match_policy": map[string]string{"path_prefix": "/" + id}, "auth_policy": map[string]bool{"require_virtual_key": true}, "target_policy": map[string]any{"provider_target": map[string]string{"provider_id": "proof-provider"}}})
			}
			bundle := map[string]any{"apiVersion": "gateway.agw/v1alpha1", "kind": "GatewayBundle", "extensions": f.Envelopes, "providers": []any{map[string]any{"id": "proof-provider", "provider_type": "openai", "base_url": upstream.URL + "/v1", "api_key": "proof-upstream-secret"}}, "llmRoutes": routes, "virtualKeys": []any{map[string]any{"id": "proof-key", "allowed_route_ids": ids, "rate_limits": map[string]any{"llm": map[string]int{"requests_per_minute": 1, "burst": 16}}}}}
			bundle["mcpServices"] = []any{map[string]any{"id": "proof-mcp-service", "name": "Proof MCP service", "transport": "streamable_http", "url": mcpUpstream.URL}}
			bundle["mcpRoutes"] = []any{map[string]any{"id": "proof-mcp", "service_id": "proof-mcp-service", "match_policy": map[string]string{"path_prefix": "/proof-mcp"}, "auth_policy": map[string]bool{"require_virtual_key": true}}}
			raw, err := json.Marshal(bundle)
			if err != nil {
				t.Fatal(err)
			}
			bundlePath := filepath.Join(work, "bundle.json")
			if err = os.WriteFile(bundlePath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			cliArgs := []string{"--admin-addr", base, "--admin-basic-auth", "contract:contract-pass"}
			run(t, work, "agwctl", append(append([]string{}, cliArgs...), "validate", "-f", bundlePath)...)
			run(t, work, "agwctl", append(append([]string{}, cliArgs...), "apply", "-f", bundlePath)...)
			exported := run(t, work, "agwctl", append(append([]string{}, cliArgs...), "export")...)
			var exportedBundle struct {
				Extensions []struct {
					ModuleID      string         `yaml:"module_id"`
					SchemaVersion int            `yaml:"schema_version"`
					Config        map[string]any `yaml:"config"`
				} `yaml:"extensions"`
			}
			if yaml.Unmarshal(exported, &exportedBundle) != nil {
				t.Fatal("invalid exported Bundle")
			}
			registry, err := f.Runtime.Registry()
			if err != nil {
				t.Fatal(err)
			}
			expected, err := registry.Validate(t.Context(), f.Envelopes)
			if err != nil {
				t.Fatal(err)
			}
			if len(exportedBundle.Extensions) != len(expected) {
				t.Fatal("export lost module")
			}
			for _, e := range expected {
				found := false
				for _, actual := range exportedBundle.Extensions {
					if actual.ModuleID == e.ModuleID {
						var config map[string]any
						actualJSON, _ := json.Marshal(actual.Config)
						expectedJSON, _ := json.Marshal(json.RawMessage(e.Config))
						_ = json.Unmarshal(actualJSON, &config)
						var expectedConfig map[string]any
						_ = json.Unmarshal(expectedJSON, &expectedConfig)
						if actual.SchemaVersion != e.SchemaVersion || !reflect.DeepEqual(config, expectedConfig) {
							t.Fatal("export changed reference configuration")
						}
						found = true
					}
				}
				if !found {
					t.Fatal("export lost module")
				}
			}
			resp, err := client.Post(base+f.ProbePath, "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 401 {
				t.Fatal("contributor bypassed authentication")
			}
			probeBefore := call("POST", f.ProbePath, `{}`, 200)
			extcontract.AdminBody(t, extcontract.AdminBodyFixture{URL: base + "/admin/llm/providers", Method: "POST", Body: []byte(`{"id":"body-policy","provider_type":"openai","base_url":"http://127.0.0.1"}`), SuccessStatus: 201, Send: send, CheckUnchanged: func(t *testing.T) { call("GET", "/admin/llm/providers/body-policy", "", 404) }})
			extcontract.AdminBody(t, extcontract.AdminBodyFixture{URL: base + f.ProbePath, Method: "POST", Body: []byte(`{}`), SuccessStatus: 200, Send: send, CheckUnchanged: func(t *testing.T) {
				if !bytes.Equal(probeBefore, call("POST", f.ProbePath, `{}`, 200)) {
					t.Fatal("rejected body changed probe")
				}
			}})
			var key struct {
				Key string `json:"key"`
			}
			if json.Unmarshal(call("GET", "/admin/virtual_keys/proof-key", "", 200), &key) != nil || key.Key == "" {
				t.Fatal("missing VirtualKey")
			}
			request := func(protocol string, stream bool, message string) *http.Request {
				path := "/proof-" + protocol + "/v1/messages"
				body := map[string]any{"model": "proof", "max_tokens": 16, "stream": stream, "messages": []any{map[string]string{"role": "user", "content": message}}}
				if protocol == "openai" {
					path = "/proof-openai/v1/chat/completions"
				}
				raw, _ := json.Marshal(body)
				req, _ := http.NewRequest("POST", base+path, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+key.Key)
				req.Header.Set("X-Tenant-ID", "untrusted-tenant")
				return req
			}
			mcpReq, _ := http.NewRequest("POST", base+"/proof-mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"proof-call","method":"tools/call","params":{"name":"echo","arguments":{}}}`))
			mcpReq.Header.Set("Authorization", "Bearer "+key.Key)
			mcpReq.Header.Set("Content-Type", "application/json")
			mcpResp, err := client.Do(mcpReq)
			if err != nil {
				t.Fatal(err)
			}
			mcpRaw, err := io.ReadAll(mcpResp.Body)
			mcpResp.Body.Close()
			if err != nil || mcpResp.StatusCode != 200 || !json.Valid(mcpRaw) || !bytes.Contains(mcpRaw, []byte("proof-tool-result")) {
				t.Fatalf("MCP tool execution: %d %s %v", mcpResp.StatusCode, mcpRaw, err)
			}
			poll(t, func() bool {
				raw := call("GET", "/admin/metrics/mcp/events?service_id=proof-mcp-service", "", 200)
				return bytes.Contains(raw, []byte(`"tool_name":"echo"`)) && bytes.Contains(raw, []byte(`"success":true`))
			})
			for _, protocol := range []string{"openai", "anthropic", "cc"} {
				for _, stream := range []bool{false, true} {
					resp, err := client.Do(request(protocol, stream, "hello"))
					if err != nil {
						t.Fatal(err)
					}
					raw, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil || resp.StatusCode != 200 || !bytes.Contains(raw, []byte("proof-response")) {
						t.Fatalf("%s stream=%v: %d %s %v", protocol, stream, resp.StatusCode, raw, err)
					}
					if stream {
						terminal := "event: message_stop"
						if protocol == "openai" {
							terminal = "data: [DONE]"
						}
						if !bytes.Contains(raw, []byte(terminal)) {
							t.Fatalf("missing %s terminal", protocol)
						}
					} else if !json.Valid(raw) {
						t.Fatal("invalid protocol response")
					}
				}
			}
			req := request("openai", true, "cancel-proof")
			ctx, cancel := context.WithCancel(t.Context())
			resp, err = client.Do(req.WithContext(ctx))
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			reader := bufio.NewReader(resp.Body)
			if _, err = reader.ReadString('\n'); err != nil {
				cancel()
				resp.Body.Close()
				t.Fatal(err)
			}
			cancel()
			resp.Body.Close()
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream stream was not canceled")
			}
			// Six completed requests account for 30 tokens; cancellation must finalize
			// its interaction without creating an extra completed upstream invocation.
			poll(t, func() bool {
				raw := call("GET", "/admin/metrics/llm/events?limit=100&provider_id=proof-provider", "", 200)
				var view struct {
					Items []map[string]any `json:"items"`
				}
				if json.Unmarshal(raw, &view) != nil {
					t.Fatal("invalid usage response")
				}
				completed := map[string]int{}
				canceledCount := 0
				for _, item := range view.Items {
					success := item["success"] == float64(1) || item["success"] == true
					if success && item["input_tokens"] == float64(3) && item["output_tokens"] == float64(2) {
						protocol, _ := item["llm_api"].(string)
						completed[protocol]++
					}
					if item["usage_finalized"] == false && item["input_tokens"] == float64(0) && item["output_tokens"] == float64(0) {
						canceledCount++
					}
				}
				return len(view.Items) == 7 && completed["openai"] == 2 && completed["anthropic"] == 2 && completed["cc"] == 2 && canceledCount == 1
			})
			// Exhaust the fixed burst; rejection occurs before any upstream execution.
			for i := 0; i < 9; i++ {
				resp, err := client.Do(request("openai", false, "hello"))
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatalf("premature limit: %d", resp.StatusCode)
				}
			}
			callsBeforeDenial := upstreamCalls.Load()
			resp, err = client.Do(request("openai", false, "hello"))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" || upstreamCalls.Load() != callsBeforeDenial {
				t.Fatal("missing bounded admission rejection")
			}
			if err = cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				stopped = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("shutdown deadline")
			}
			raw, err = os.ReadFile(events)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte("proof-upstream-secret")) || bytes.Contains(raw, []byte("proof-response")) || bytes.Contains(raw, []byte(key.Key)) || bytes.Contains(raw, []byte("untrusted-tenant")) {
				t.Fatal("public event leaked private content")
			}
			seenAdmission, seenMutation := false, false
			expectedBackend := ""
			for _, e := range f.Envelopes {
				if e.ModuleID == "online.agentguide.community.limiter" {
					var cfg struct {
						BackendModuleID string `json:"backend_module_id"`
					}
					_ = json.Unmarshal(e.Config, &cfg)
					expectedBackend = cfg.BackendModuleID
				}
			}
			scanner := bufio.NewScanner(bytes.NewReader(raw))
			for scanner.Scan() {
				var e event.Envelope
				if json.Unmarshal(scanner.Bytes(), &e) != nil || event.Validate(e, policy) != nil {
					t.Fatal("invalid sanitized event")
				}
				seenAdmission = seenAdmission || (e.Family == "limiter.admission" && e.Limiter.BackendID == expectedBackend)
				seenMutation = seenMutation || e.Family == "extension.mutation"
			}
			if scanner.Err() != nil || !seenAdmission || !seenMutation {
				t.Fatal("missing flushed event families")
			}
		})
	}
}

func cleanEnv(eventEnv string) []string {
	var result []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "AGW_ADMIN_") || strings.HasPrefix(entry, eventEnv+"=") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
func poll(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("process condition did not pass before %s", deadline.Format(time.RFC3339)))
}
