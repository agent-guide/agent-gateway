package routecore

import (
	"fmt"
	"testing"
)

func TestRouteConflicts(t *testing.T) {
	base := RouteConfig{ID: "a", Kind: RouteKindLLM, MatchPolicy: RouteMatchPolicy{PathPrefix: "/api", Methods: []string{"GET", "POST"}}}
	for _, tc := range []struct {
		name   string
		change func(*RouteConfig)
		want   bool
	}{
		{"same", func(*RouteConfig) {}, true},
		{"partial methods", func(r *RouteConfig) { r.MatchPolicy.Methods = []string{"POST"} }, true},
		{"disjoint methods", func(r *RouteConfig) { r.MatchPolicy.Methods = []string{"DELETE"} }, false},
		{"disabled", func(r *RouteConfig) { r.Disabled = true }, true},
		{"cross kind", func(r *RouteConfig) { r.Kind = RouteKindMCP }, true},
		{"nested", func(r *RouteConfig) { r.MatchPolicy.PathPrefix = "/api/chat" }, false},
		{"disjoint path", func(r *RouteConfig) { r.MatchPolicy.PathPrefix = "/xyz" }, false},
		{"host specificity", func(r *RouteConfig) { r.MatchPolicy.Host = "example.org" }, false},
		{"method specificity", func(r *RouteConfig) { r.MatchPolicy.Methods = nil }, false},
		{"a2a ignores methods", func(r *RouteConfig) {
			r.Kind = RouteKindAgent
			r.Protocol = RouteProtocolA2A
			r.MatchPolicy.Methods = []string{"DELETE"}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := base
			other.ID = "b"
			tc.change(&other)
			got := RouteConflicts([]RouteConfig{base, other})
			if (len(got) > 0) != tc.want {
				t.Fatalf("conflicts = %v, want conflict %v", got, tc.want)
			}
			reverse := RouteConflicts([]RouteConfig{other, base})
			if fmt.Sprint(got) != fmt.Sprint(reverse) {
				t.Fatalf("unstable diagnostics: %v vs %v", got, reverse)
			}
		})
	}
}

func TestRouteConflictsHostsAndA2A(t *testing.T) {
	a := RouteConfig{ID: "a", Kind: RouteKindAgent, Protocol: RouteProtocolA2A, MatchPolicy: RouteMatchPolicy{Host: "EXAMPLE.org", PathPrefix: "/a", Methods: []string{"GET", "POST"}}}
	b := a
	b.ID = "b"
	b.MatchPolicy.Host = "example.org"
	b.MatchPolicy.Methods = []string{"PUT"}
	if len(RouteConflicts([]RouteConfig{a, b})) != 1 {
		t.Fatal("A2A routes must intersect despite disjoint declared methods")
	}
	b.MatchPolicy.Host = "other.org"
	if len(RouteConflicts([]RouteConfig{a, b})) != 0 {
		t.Fatal("disjoint hosts must coexist")
	}
}
