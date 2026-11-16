// Package egress implements host-owned network policy shared by HTTP API and MCP connectors.
package egress

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Policy's zero value trusts destinations configured by administrators.
// AllowOrigins grants exact scheme/host/effective-port exceptions, including private addresses.
type Policy struct {
	Mode         string   `json:"mode,omitempty"`
	AllowOrigins []string `json:"allow_origins,omitempty"`
}

func (p Policy) Clone() Policy {
	p.AllowOrigins = append([]string(nil), p.AllowOrigins...)
	return p
}

func (p Policy) Validate() error {
	if p.Mode != "" && p.Mode != "unrestricted" && p.Mode != "public_only" {
		return fmt.Errorf("connector_egress mode must be unrestricted or public_only")
	}
	seen := map[target]bool{}
	for i, raw := range p.AllowOrigins {
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
			return fmt.Errorf("invalid connector_egress allow_origin at index %d: expected an HTTP(S) origin", i)
		}
		t, err := destination(u)
		if err != nil {
			return fmt.Errorf("invalid connector_egress allow_origin at index %d", i)
		}
		if seen[t] {
			return fmt.Errorf("duplicate connector_egress allow_origin at index %d", i)
		}
		seen[t] = true
	}
	return nil
}

func validHost(h string) bool {
	if h == "" || len(h) > 253 || strings.HasSuffix(h, ".") {
		return false
	}
	if a, e := netip.ParseAddr(h); e == nil {
		return a.Zone() == "" && a.Unmap().String() == h
	}
	for _, label := range strings.Split(h, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

type target struct {
	scheme, host string
	port         int
}

func destination(u *url.URL) (target, error) {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return target{}, fmt.Errorf("invalid HTTP(S) destination")
	}
	h := strings.ToLower(u.Hostname())
	if a, e := netip.ParseAddr(h); e == nil {
		if a.Zone() != "" {
			return target{}, fmt.Errorf("IPv6 zone unsupported")
		}
		h = a.Unmap().String()
	}
	if !validHost(h) {
		return target{}, fmt.Errorf("invalid destination host")
	}
	port := 443
	if u.Scheme == "http" {
		port = 80
	}
	if u.Port() != "" {
		var e error
		port, e = strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return target{}, fmt.Errorf("invalid destination port")
		}
	}
	return target{u.Scheme, h, port}, nil
}

func (p Policy) allowsOrigin(t target) bool {
	if p.Mode == "" || p.Mode == "unrestricted" {
		return true
	}
	for _, raw := range p.AllowOrigins {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		origin, err := destination(u)
		if err == nil && origin == t {
			return true
		}
	}
	return false
}

// CanAdmit checks configuration without DNS or network I/O.
func (p Policy) CanAdmit(u *url.URL) bool {
	t, err := destination(u)
	if err != nil {
		return false
	}
	if a, err := netip.ParseAddr(t.host); err == nil {
		return p.admits(t, a)
	}
	return p.allowsOrigin(t) || t.scheme == "https" && t.port == 443
}

func (p Policy) admits(t target, a netip.Addr) bool {
	return p.allowsOrigin(t) || t.scheme == "https" && t.port == 443 && publicAddress(a)
}

// AdmitsPeer verifies the connected address against the original destination.
func (p Policy) AdmitsPeer(u *url.URL, a netip.Addr) bool {
	t, err := destination(u)
	return err == nil && p.admits(t, a)
}

// DeniedError identifies policy rejection without exposing credentials or URL paths.
type DeniedError struct{}

func (*DeniedError) Error() string {
	return "destination blocked by connector_egress policy; ask the administrator to add allow_origin"
}

// Explicit special-use table: global-unicast alone includes documentation,
// benchmarking, CGNAT and reserved addresses.
var specialUse = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range strings.Fields(`0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.88.99.0/24 192.168.0.0/16 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4 ::/96 ::ffff:0:0/96 64:ff9b::/96 64:ff9b:1::/48 100::/64 2001::/23 2001:db8::/32 2002::/16 3fff::/20 5f00::/16 fc00::/7 fe80::/10 fec0::/10 ff00::/8`) {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func publicAddress(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() {
		return false
	}
	if a.Is6() && !netip.MustParsePrefix("2000::/3").Contains(a) {
		return false
	}
	for _, p := range specialUse {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// Resolve checks every DNS answer and returns only addresses safe to dial directly.
func (p Policy) Resolve(ctx context.Context, resolver Resolver, u *url.URL) ([]netip.Addr, error) {
	t, err := destination(u)
	if err != nil || !p.CanAdmit(u) {
		return nil, &DeniedError{}
	}
	var addresses []netip.Addr
	if a, err := netip.ParseAddr(t.host); err == nil {
		addresses = []netip.Addr{a}
	} else {
		addresses, err = resolver.LookupNetIP(ctx, "ip", t.host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, &DeniedError{}
	}
	for i, a := range addresses {
		a = a.Unmap()
		if !p.admits(t, a) {
			return nil, &DeniedError{}
		}
		addresses[i] = a
	}
	return addresses, nil
}

var _ Resolver = net.DefaultResolver
