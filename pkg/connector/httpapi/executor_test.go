package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
	cred "github.com/agent-guide/agent-gateway/pkg/credential/model"
)

func testCatalog(t *testing.T, base string, extra string) *Catalog {
	t.Helper()
	var c Config
	raw := `{"base_url":"` + base + `","tools":[{"name":"lookup","description":"Look up","input_schema":{"type":"object","properties":{"id":{"type":"string","minLength":1},"n":{"type":"integer"}},"required":["id"],"additionalProperties":false},"request":{"method":"GET","path":"/orders/{id}","path_params":{"id":{"argument":"id"}},"query":{"n":{"argument":"n"}}}` + extra + `}]}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	cat, err := Compile(c)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}
func localEngine(t *testing.T, server *httptest.Server, credentials CredentialReader) *Engine {
	t.Helper()
	p := DefaultPolicy()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	p.Rules = []Rule{{Action: "allow", Schemes: []string{"http"}, Hosts: []string{"127.0.0.1"}, Ports: []int{port}, CIDRs: []string{"127.0.0.1/32"}}}
	e, err := NewEngine(p, credentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}
func TestMappingPrecisionAndPreflight(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/orders/hello world" || r.URL.Query().Get("n") != "9007199254740993" {
			t.Errorf("mapping: %s", r.URL)
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("compression enabled")
		}
		w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
		io.WriteString(w, `{"data":{"n":9007199254740993}}`)
	}))
	defer s.Close()
	cat := testCatalog(t, s.URL+"/api/v1/", `,"response":{"select":"/data"}`)
	e := localEngine(t, s, nil)
	args := map[string]any{"id": "hello world", "n": json.Number("9007199254740993")}
	ctx, summary := capture(context.Background())
	result, err := e.call(ctx, "orders", cat, "lookup", args)
	if err != nil {
		t.Fatal(err)
	}
	if result.StructuredContent.(map[string]any)["n"] != json.Number("9007199254740993") {
		t.Fatal("numeric precision lost")
	}
	if summary.StatusCode == nil || *summary.StatusCode != 200 || summary.PathTemplate != "/api/v1/orders/{id}" {
		t.Fatalf("summary: %#v", summary)
	}
	for _, id := range []string{"..", "a/b", "%2f", "%252f"} {
		_, err = e.call(context.Background(), "orders", cat, "lookup", map[string]any{"id": id})
		if err == nil {
			t.Fatal("unsafe path allowed")
		}
	}
	_, err = e.call(context.Background(), "orders", cat, "missing", args)
	if err == nil {
		t.Fatal("unknown tool allowed")
	}
	if calls.Load() != 1 {
		t.Fatalf("preflight sent requests: %d", calls.Load())
	}
}
func TestResponseContractsAndNoReplay(t *testing.T) {
	for _, tc := range []struct {
		name, body, ct, encoding, selectField string
		status                                int
		category                              string
	}{{"duplicate", `{"a":1,"a":2}`, "application/json", "", "", 200, "response_invalid"}, {"surrogate", `"\ud800"`, "application/json", "", "", 200, "response_invalid"}, {"trailing", `{} {}`, "application/json", "", "", 200, "response_invalid"}, {"media", `{}`, "text/plain", "", "", 200, "response_invalid"}, {"encoding", `{}`, "application/json", "gzip", "", 200, "response_invalid"}, {"missing", `{}`, "application/json", "", `,"response":{"select":"/data"}`, 200, "response_mapping"}, {"empty204", "", "", "", "", 204, ""}, {"select204", "", "", "", `,"response":{"select":"/data"}`, 204, "response_mapping"}, {"error", "secret upstream details", "text/plain", "", "", 500, "http_error"}} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.Header().Set("Content-Type", tc.ct)
				w.Header().Set("Content-Encoding", tc.encoding)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer s.Close()
			e := localEngine(t, s, nil)
			cat := testCatalog(t, s.URL, tc.selectField)
			_, err := e.call(context.Background(), "x", cat, "lookup", map[string]any{"id": "one"})
			if tc.category == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.category) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error: %v", err)
			}
			if count.Load() != 1 {
				t.Fatal("request replayed")
			}
		})
	}
}
func TestOverflowProbe(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, strings.Repeat(" ", 100))
	}))
	defer s.Close()
	cat := testCatalog(t, s.URL, "")
	cat.config.MaxResponseBytes = 10
	e := localEngine(t, s, nil)
	ctx, summary := capture(context.Background())
	_, err := e.call(ctx, "x", cat, "lookup", map[string]any{"id": "1"})
	if err == nil || summary.ResponseBodyBytes != 11 {
		t.Fatalf("overflow: %v %#v", err, summary)
	}
}

type credentials struct{ current *cred.ManagedCredential }

func (c *credentials) GetCredential(string) *cred.ManagedCredential { return c.current }
func TestCredentialRotationAndRevocation(t *testing.T) {
	var seen []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer s.Close()
	cat := testCatalog(t, s.URL, "")
	cat.config.Auth = &Auth{Type: "bearer", CredentialRef: "token"}
	creds := &credentials{current: &cred.ManagedCredential{Credential: cred.Credential{ID: "token", Type: "api_key", Scope: "connector:Orders", Attributes: map[string]string{"api_key": "one"}}}}
	e := localEngine(t, s, creds)
	args := map[string]any{"id": "1"}
	for _, secret := range []string{"one", "two"} {
		creds.current.Attributes["api_key"] = secret
		if _, err := e.call(context.Background(), "Orders", cat, "lookup", args); err != nil {
			t.Fatal(err)
		}
	}
	creds.current.Disabled = true
	ctx, summary := capture(context.Background())
	if _, err := e.call(ctx, "Orders", cat, "lookup", args); err == nil {
		t.Fatal("revocation ignored")
	}
	if summary.Method != "" || len(seen) != 2 || seen[0] != "Bearer one" || seen[1] != "Bearer two" {
		t.Fatalf("auth: %#v %#v", seen, summary)
	}
	creds.current.Disabled = false
	if _, err := e.call(context.Background(), "orders", cat, "lookup", args); err == nil {
		t.Fatal("owner ID case ignored")
	}
}

type mixedResolver struct{}

func (mixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
}
func TestMixedDNSDeniedBeforeHTTP(t *testing.T) {
	cat := testCatalog(t, "https://example.com", "")
	e, _ := NewEngine(DefaultPolicy(), nil)
	defer e.Close()
	e.resolver = mixedResolver{}
	ctx, summary := capture(context.Background())
	_, err := e.call(ctx, "x", cat, "lookup", map[string]any{"id": "1"})
	if err == nil || summary.Method != "" {
		t.Fatalf("mixed DNS admitted: %v %#v", err, summary)
	}
}
func TestSchemaStrictnessAndNumericSemantics(t *testing.T) {
	for _, raw := range []string{`{"type":"object","$ref":"https://example.com"}`, `{"type":"object","properties":{"a":{"type":"array"}}}`, `{"type":"object","required":["missing"]}`, `{"type":"object","properties":{"a":{"type":"integer","enum":[1,1.0]}}}`, `{"type":"object","properties":{"a":{"type":"string","minimum":1}}}`} {
		v, err := strictjson.Parse([]byte(raw), 1<<20, 64)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = CompileSchema(v.(map[string]any)); err == nil {
			t.Fatalf("invalid schema accepted: %s", raw)
		}
	}
	v, _ := strictjson.Parse([]byte(`{"type":"object","properties":{"n":{"type":"integer","minimum":9007199254740993}},"required":["n"]}`), 1<<20, 64)
	s, err := CompileSchema(v.(map[string]any))
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]bool{`{"n":9007199254740992}`: false, `{"n":9007199254740993}`: true, `{"n":9007199254740993.0}`: true, `{"n":1e999999999999999999999999}`: true} {
		v, _ := strictjson.Parse([]byte(raw), 1<<20, 64)
		if (s.Validate(v) == nil) != want {
			t.Fatalf("numeric validation: %s", raw)
		}
	}
}
