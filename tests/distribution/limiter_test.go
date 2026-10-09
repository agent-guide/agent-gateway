package distribution_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
)

func checkLimiterProcess(t *testing.T, ctx context.Context, base string, client *adminclient.Client, send func(*http.Request) (*http.Response, error)) {
	t.Helper()
	adminWrite := func(method, path, body string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
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
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
		return raw
	}
	adminWrite("POST", "/admin/llm/routes", `{"id":"limiter-proof","kind":"llm","protocol":"cc","auth_policy":{"require_virtual_key":true},"match_policy":{"path_prefix":"/limiter-proof"},"target_policy":{"provider_target":{"provider_id":"body-proof"}}}`, 201)
	keyRaw := adminWrite("POST", "/admin/virtual_keys", `{"id":"limiter-proof","allowed_route_ids":["limiter-proof"],"rate_limits":{"llm":{"requests_per_minute":1,"burst":1}}}`, 201)
	var key virtualkey.VirtualKey
	if json.Unmarshal(keyRaw, &key) != nil || key.Key == "" {
		t.Fatal("key not generated")
	}
	put := func(id, raw string) {
		t.Helper()
		if _, err := client.PutExtension(ctx, extension.Envelope{ModuleID: id, SchemaVersion: 1, Config: json.RawMessage(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	put("example.fixture.limiter", `{}`)
	selection := `{"backend_module_id":"example.fixture.limiter","backend_type":"fixture","timeout_ms":10,"failure_mode":"closed"}`
	put(limiter.ModuleID, selection)
	dataClient := &http.Client{Timeout: time.Second}
	admit := func(want int) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/limiter-proof/v1/messages/count_tokens", strings.NewReader(`{"model":"proof","messages":[{"role":"user","content":"hello"}]}`))
		req.Header.Set("Authorization", "Bearer "+key.Key)
		req.Header.Set("X-Tenant-ID", "forged")
		resp, err := dataClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("admission status=%d want=%d body=%s", resp.StatusCode, want, raw)
		}
		if want == 429 {
			n, err := strconv.Atoi(resp.Header.Get("Retry-After"))
			if err != nil || n < 1 {
				t.Fatal("invalid retry-after")
			}
		}
		if want == 503 && (!bytes.Contains(raw, []byte(`"error":"limiter_unavailable"`)) || len(raw) > 1024 || bytes.Contains(raw, []byte("private"))) {
			t.Fatal("unbounded backend diagnostic", string(raw))
		}
	}
	admit(200)
	admit(429)
	put("example.fixture.mutable", `{"message":"unrelated-refresh"}`)
	admit(429)
	// Metadata-only key writes preserve its consumed bucket.
	adminWrite("PUT", "/admin/virtual_keys/limiter-proof", `{"tag":"updated","allowed_route_ids":["limiter-proof"],"rate_limits":{"llm":{"requests_per_minute":1,"burst":1}}}`, 200)
	admit(429)
	if _, err := client.PutExtension(ctx, extension.Envelope{ModuleID: limiter.ModuleID, SchemaVersion: 1, Config: json.RawMessage(`{"backend_module_id":"example.fixture.limiter","backend_type":"unavailable"}`)}); err == nil {
		t.Fatal("unavailable selection published")
	}
	admit(429)
	adminWrite("PUT", "/admin/virtual_keys/limiter-proof", `{"allowed_route_ids":["limiter-proof"],"rate_limits":{"llm":{"requests_per_minute":1,"burst":2}}}`, 200)
	admit(200)
	admit(200)
	admit(429)
	put("example.fixture.limiter", `{"mode":"error"}`)
	admit(503)
	put(limiter.ModuleID, strings.Replace(selection, `"closed"`, `"open"`, 1))
	admit(200)
	put("example.fixture.limiter", `{"mode":"timeout"}`)
	put(limiter.ModuleID, selection)
	started := time.Now()
	admit(503)
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("backend exceeded admission budget")
	}
	put(limiter.ModuleID, strings.Replace(selection, `"closed"`, `"open"`, 1))
	admit(200)
	put("example.fixture.limiter", `{"mode":"ambiguous"}`)
	put(limiter.ModuleID, selection)
	admit(503)
	admit(503)
	admit(429)
	// Switching to the local authority resets state without transferring tokens.
	put(limiter.ModuleID, `{}`)
	admit(200)
	admit(200)
	admit(429)
	metrics := string(adminWrite("GET", "/admin/metrics/prometheus", "", 200))
	if !strings.Contains(metrics, `outcome="degraded",backend_type="fixture"`) || strings.Contains(metrics, `virtual_key_id=`) || strings.Contains(metrics, `tenant=`) {
		t.Fatal("limiter counter labels", metrics)
	}
}
