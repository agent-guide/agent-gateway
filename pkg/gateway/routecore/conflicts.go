package routecore

import (
	"fmt"
	"sort"
	"strings"
)

// EffectiveMatch preserves native A2A endpoint ownership for every HTTP method.
// Scoring still uses the original declaration, including its method specificity.
func EffectiveMatch(route AgentRouteConfig) RouteMatchPolicy {
	match := route.MatchPolicy
	if route.Kind == RouteKindAgent && route.Protocol == RouteProtocolA2A {
		match.Methods = nil
	}
	return match
}

// RouteConflicts reports every equal-score intersection in stable ID order.
// Disabled routes reserve their match space and therefore participate.
// This function does not choose a winner or modify the supplied generation.
func RouteConflicts(routes []AgentRouteConfig) []error {
	ordered := append([]AgentRouteConfig(nil), routes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var conflicts []error
	for i, a := range ordered {
		for _, b := range ordered[i+1:] {
			if scoreRouteMatch(a.MatchPolicy) != scoreRouteMatch(b.MatchPolicy) {
				continue
			}
			if matchesIntersect(EffectiveMatch(a), EffectiveMatch(b)) {
				conflicts = append(conflicts, fmt.Errorf("ambiguous routes %q and %q: equal-score match conditions intersect; explicitly rewrite or delete a route", a.ID, b.ID))
			}
		}
	}
	return conflicts
}

func matchesIntersect(a, b RouteMatchPolicy) bool {
	if a.Host != "" && b.Host != "" && !strings.EqualFold(a.Host, b.Host) {
		return false
	}
	if !strings.HasPrefix(a.PathPrefix, b.PathPrefix) && !strings.HasPrefix(b.PathPrefix, a.PathPrefix) {
		return false
	}
	if len(a.Methods) == 0 || len(b.Methods) == 0 {
		return true
	}
	for _, method := range a.Methods {
		if methodMatches(b.Methods, method) {
			return true
		}
	}
	return false
}
