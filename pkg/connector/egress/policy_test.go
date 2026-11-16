package egress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestDefaultAndRestrictedDestinations(t *testing.T) {
	for raw, restricted := range map[string]bool{
		"https://example.com":     true,
		"https://example.com:444": false,
		"http://example.com":      false,
		"http://127.0.0.1:8080":   false,
		"https://10.0.0.1":        false,
		"https://[::1]":           false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !(Policy{}).CanAdmit(u) {
			t.Fatalf("default rejected %s", raw)
		}
		if got := (Policy{Mode: "public_only"}).CanAdmit(u); got != restricted {
			t.Fatalf("restricted %s: %v", raw, got)
		}
	}
	p := Policy{Mode: "public_only", AllowOrigins: []string{"http://orders.internal:8080", "https://[::1]"}}
	for raw, want := range map[string]bool{
		"http://ORDERS.internal:8080/path": true,
		"http://orders.internal:8081":      false,
		"http://sub.orders.internal:8080":  false,
		"https://[::1]:443/path":           true,
	} {
		u, _ := url.Parse(raw)
		if p.CanAdmit(u) != want {
			t.Fatalf("origin match %s", raw)
		}
	}
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "224.0.0.1", "240.1.1.1", "::1", "2001:db8::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1"} {
		if publicAddress(netip.MustParseAddr(s)) {
			t.Fatalf("special-use admitted: %s", s)
		}
	}
}

func TestInvalidPolicy(t *testing.T) {
	if err := (Policy{Mode: "deny"}).Validate(); err == nil {
		t.Fatal("invalid mode accepted")
	}
	for _, origin := range []string{"", "ftp://example.com", "https://*.example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?", "https://example.com#", "https://example.com:0", "https://example.com:65536"} {
		if err := (Policy{AllowOrigins: []string{origin}}).Validate(); err == nil {
			t.Fatalf("invalid origin accepted: %s", origin)
		}
	}
	if err := (Policy{AllowOrigins: []string{"https://EXAMPLE.com", "https://example.com:443/"}}).Validate(); err == nil {
		t.Fatal("duplicate canonical origin accepted")
	}
}

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

func TestDNSAnswersAndOriginExceptions(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
	})
	u, _ := url.Parse("https://example.com")
	p := Policy{Mode: "public_only"}
	if _, err := p.Resolve(t.Context(), resolver, u); err == nil {
		t.Fatal("mixed DNS accepted")
	}
	p.AllowOrigins = []string{"https://example.com"}
	if addresses, err := p.Resolve(t.Context(), resolver, u); err != nil || len(addresses) != 2 {
		t.Fatalf("explicit origin: %v %v", addresses, err)
	}
}

func TestClientRedirectPolicyAndProxyBypass(t *testing.T) {
	var blockedCalls, proxyCalls atomic.Int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { blockedCalls.Add(1) }))
	defer blocked.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, blocked.URL, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer allowed.Close()
	client := NewClient(Policy{Mode: "public_only", AllowOrigins: []string{allowed.URL}})
	resp, err := client.Get(allowed.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_, err = client.Get(allowed.URL + "/redirect")
	var denied *DeniedError
	if !errors.As(err, &denied) || blockedCalls.Load() != 0 || proxyCalls.Load() != 0 {
		t.Fatalf("redirect/proxy: %v blocked=%d proxy=%d", err, blockedCalls.Load(), proxyCalls.Load())
	}
	resp, err = NewClient(Policy{}).Get(blocked.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if blockedCalls.Load() != 1 {
		t.Fatal("unrestricted local call did not reach server")
	}
}

func TestClientPinsAdmittedResolution(t *testing.T) {
	var expectedHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != expectedHost {
			t.Errorf("unexpected host %s", r.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	u.Host = "api.internal:" + u.Port()
	expectedHost = u.Host
	var lookups atomic.Int32
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		lookups.Add(1)
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	})
	client := &http.Client{Transport: &policyTransport{policy: Policy{Mode: "public_only", AllowOrigins: []string{u.String()}}, resolver: resolver}}
	resp, err := client.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if lookups.Load() != 1 {
		t.Fatalf("DNS resolved %d times", lookups.Load())
	}
}
