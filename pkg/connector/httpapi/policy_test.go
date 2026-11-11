package httpapi

import (
	"net/netip"
	"net/url"
	"testing"
)

func TestPolicySelectorsAndSpecialUse(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "224.0.0.1", "240.1.1.1", "::1", "2001:db8::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1"} {
		if publicAddress(netip.MustParseAddr(s)) {
			t.Fatalf("special-use admitted: %s", s)
		}
	}
	p := DefaultPolicy()
	p.DefaultAction = "deny"
	p.Rules = []Rule{{Action: "allow", Schemes: []string{"https"}, Hosts: []string{"*.example.com"}, Ports: []int{443}, CIDRs: []string{"10.0.0.0/8"}}, {Action: "deny", Schemes: []string{"https"}, Hosts: []string{"blocked.example.com"}, Ports: []int{443}, CIDRs: []string{"10.0.0.0/8"}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"a.example.com": true, "deep.a.example.com": true, "example.com": false, "blocked.example.com": false} {
		if got := p.admits(target{"https", host, 443}, netip.MustParseAddr("10.1.2.3")); got != want {
			t.Fatalf("host %s: %v", host, got)
		}
	}
	for raw, want := range map[string]bool{"https://example.org": true, "https://example.org:444": false, "http://example.org": false} {
		u, _ := url.Parse(raw)
		if DefaultPolicy().CanAdmit(u) != want {
			t.Fatalf("default origin: %s", raw)
		}
	}
}
func TestPolicyStrictParsing(t *testing.T) {
	for _, raw := range []string{`{}`, `{"version":1,"version":1}`, `{"version":1,"rules":null}`, `{"version":1,"limits":{"global_active":0}}`, `{"version":1,"unknown":true}`, `{"version":1,"rules":[{"action":"allow","schemes":["https"],"hosts":["*.example.com"],"ports":[443],"cidrs":["10.1.0.1/16"]}]}`} {
		if _, err := ParsePolicy([]byte(raw)); err == nil {
			t.Fatalf("invalid policy accepted: %s", raw)
		}
	}
}
