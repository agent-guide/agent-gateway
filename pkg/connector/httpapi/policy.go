package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
)

type Policy struct {
	Version       int    `json:"version"`
	DefaultAction string `json:"default_action,omitempty"`
	Rules         []Rule `json:"rules,omitempty"`
	Limits        Limits `json:"limits,omitempty"`
}
type Rule struct {
	Action  string   `json:"action"`
	Schemes []string `json:"schemes"`
	Hosts   []string `json:"hosts"`
	Ports   []int    `json:"ports"`
	CIDRs   []string `json:"cidrs"`
}
type Limits struct {
	GlobalActive    int `json:"global_active,omitempty"`
	GlobalQueued    int `json:"global_queued,omitempty"`
	ConnectorActive int `json:"connector_active,omitempty"`
	ConnectorQueued int `json:"connector_queued,omitempty"`
}

func DefaultPolicy() Policy {
	return Policy{Version: 1, DefaultAction: "public_https", Rules: []Rule{}, Limits: Limits{128, 256, 16, 32}}
}
func LoadPolicy(path string) (Policy, error) {
	if path == "" {
		return DefaultPolicy(), nil
	}
	f, e := os.Open(path)
	if e != nil {
		return Policy{}, fmt.Errorf("open HTTP API policy: %w", e)
	}
	defer f.Close()
	b, e := readBounded(f, 1<<20)
	if e != nil {
		return Policy{}, fmt.Errorf("HTTP API policy exceeds limit")
	}
	return ParsePolicy(b)
}
func ParsePolicy(b []byte) (Policy, error) {
	p := DefaultPolicy()
	v, e := strictjson.Parse(b, 1<<20, 64)
	if e != nil {
		return Policy{}, e
	}
	m, ok := v.(map[string]any)
	if !ok {
		return Policy{}, fmt.Errorf("policy must be object")
	}
	if _, ok = m["version"]; !ok {
		return Policy{}, fmt.Errorf("policy version required")
	}
	if e = nonNull(v, ""); e != nil {
		return Policy{}, e
	}
	if e = strictjson.Decode(b, &p, 1<<20, 64); e != nil {
		return Policy{}, e
	}
	return p, p.Validate()
}
func (p Policy) Validate() error {
	if p.Version != 1 || (p.DefaultAction != "public_https" && p.DefaultAction != "deny") {
		return fmt.Errorf("invalid policy version or default")
	}
	l := p.Limits
	for _, v := range []int{l.GlobalActive, l.GlobalQueued, l.ConnectorActive, l.ConnectorQueued} {
		if v < 1 || v > 65536 {
			return fmt.Errorf("invalid admission limit")
		}
	}
	if l.ConnectorActive > l.GlobalActive || l.ConnectorQueued > l.GlobalQueued {
		return fmt.Errorf("connector limits exceed global limits")
	}
	for _, r := range p.Rules {
		if r.Action != "allow" && r.Action != "deny" {
			return fmt.Errorf("invalid policy action")
		}
		if len(r.Schemes) == 0 || len(r.Hosts) == 0 || len(r.Ports) == 0 || len(r.CIDRs) == 0 {
			return fmt.Errorf("policy selectors required")
		}
		seen := map[string]bool{}
		for _, s := range r.Schemes {
			if seen[s] || s != "http" && s != "https" {
				return fmt.Errorf("invalid policy scheme")
			}
			seen[s] = true
		}
		seen = map[string]bool{}
		for _, h := range r.Hosts {
			bare := strings.TrimPrefix(h, "*.")
			if seen[h] || !validHost(bare) || strings.ToLower(h) != h || strings.Contains(bare, "*") {
				return fmt.Errorf("invalid policy host")
			}
			if strings.HasPrefix(h, "*.") {
				if _, e := netip.ParseAddr(bare); e == nil {
					return fmt.Errorf("wildcard IP unsupported")
				}
			}
			seen[h] = true
		}
		seen = map[string]bool{}
		for _, port := range r.Ports {
			k := strconv.Itoa(port)
			if port < 1 || port > 65535 || seen[k] {
				return fmt.Errorf("invalid policy port")
			}
			seen[k] = true
		}
		seen = map[string]bool{}
		for _, s := range r.CIDRs {
			v, e := netip.ParsePrefix(s)
			if e != nil || v.Masked().String() != s || seen[s] || v.Addr().Is4In6() {
				return fmt.Errorf("invalid policy CIDR")
			}
			seen[s] = true
		}
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
func ruleOrigin(r Rule, t target) bool {
	s, h, p := false, false, false
	for _, v := range r.Schemes {
		s = s || v == t.scheme
	}
	for _, v := range r.Hosts {
		h = h || v == t.host || strings.HasPrefix(v, "*.") && strings.HasSuffix(t.host, v[1:]) && t.host != v[2:]
	}
	for _, v := range r.Ports {
		p = p || v == t.port
	}
	return s && h && p
}
func (p Policy) CanAdmit(u *url.URL) bool {
	t, e := destination(u)
	if e != nil {
		return false
	}
	if a, e := netip.ParseAddr(t.host); e == nil {
		return p.admits(t, a.Unmap())
	}
	for _, r := range p.Rules {
		if r.Action == "allow" && ruleOrigin(r, t) {
			return true
		}
	}
	return p.DefaultAction == "public_https" && t.scheme == "https" && t.port == 443
}
func (p Policy) admits(t target, a netip.Addr) bool {
	allow := false
	a = a.Unmap()
	for _, r := range p.Rules {
		if !ruleOrigin(r, t) {
			continue
		}
		match := false
		for _, s := range r.CIDRs {
			prefix, _ := netip.ParsePrefix(s)
			match = match || prefix.Contains(a)
		}
		if match {
			if r.Action == "deny" {
				return false
			}
			allow = true
		}
	}
	return allow || p.DefaultAction == "public_https" && t.scheme == "https" && t.port == 443 && publicAddress(a)
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

func resolve(ctx context.Context, resolver Resolver, p Policy, u *url.URL) (target, []netip.Addr, error) {
	t, e := destination(u)
	if e != nil {
		return t, nil, failure("egress_denied")
	}
	if !p.CanAdmit(u) {
		return t, nil, failure("egress_denied")
	}
	var addresses []netip.Addr
	if a, e := netip.ParseAddr(t.host); e == nil {
		addresses = []netip.Addr{a}
	} else {
		addresses, e = resolver.LookupNetIP(ctx, "ip", t.host)
		if e != nil {
			return t, nil, failure("egress_denied")
		}
	}
	if len(addresses) == 0 {
		return t, nil, failure("egress_denied")
	}
	for i, a := range addresses {
		a = a.Unmap()
		if !p.admits(t, a) {
			return t, nil, failure("egress_denied")
		}
		addresses[i] = a
	}
	return t, addresses, nil
}

// admission coordinates both dimensions; waiting never holds an active slot.
type admission struct {
	mu             sync.Mutex
	limits         Limits
	active, queued int
	byID           map[string]int
	waiting        map[string]int
	changed        chan struct{}
}

func newAdmission(l Limits) *admission {
	return &admission{limits: l, byID: map[string]int{}, waiting: map[string]int{}, changed: make(chan struct{})}
}
func (a *admission) acquire(ctx context.Context, id string) (func(), error) {
	a.mu.Lock()
	queued := false
	for {
		if ctx.Err() != nil {
			if queued {
				a.queued--
				a.waiting[id]--
			}
			a.mu.Unlock()
			return nil, ctx.Err()
		}
		if a.active < a.limits.GlobalActive && a.byID[id] < a.limits.ConnectorActive {
			if queued {
				a.queued--
				a.waiting[id]--
			}
			a.active++
			a.byID[id]++
			a.mu.Unlock()
			return func() {
				a.mu.Lock()
				a.active--
				a.byID[id]--
				if a.byID[id] == 0 {
					delete(a.byID, id)
				}
				close(a.changed)
				a.changed = make(chan struct{})
				a.mu.Unlock()
			}, nil
		}
		if !queued {
			if a.queued >= a.limits.GlobalQueued || a.waiting[id] >= a.limits.ConnectorQueued {
				a.mu.Unlock()
				return nil, failure("capacity_exceeded")
			}
			a.queued++
			a.waiting[id]++
			queued = true
		}
		ch := a.changed
		a.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-ch:
		}
		a.mu.Lock()
	}
}

var _ Resolver = net.DefaultResolver
