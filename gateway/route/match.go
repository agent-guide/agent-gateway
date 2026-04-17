package route

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strings"
)

// Match resolves the most specific route whose RouteMatch accepts the request.
func (m *AgentRouteManager) Match(ctx context.Context, r *http.Request) (AgentRoute, bool, error) {
	routes, err := m.List(ctx, RouteListOptions{})
	if err != nil {
		return AgentRoute{}, false, err
	}

	var (
		best      AgentRoute
		bestScore routeMatchScore
		found     bool
	)
	for _, route := range routes {
		if !routeMatchesRequest(route.Match, r) {
			continue
		}
		score := scoreRouteMatch(route.Match)
		if !found || score.betterThan(bestScore) {
			best = route
			bestScore = score
			found = true
		}
	}
	return best, found, nil
}

type routeMatchScore struct {
	pathLen        int
	hostSpecific   bool
	methodSpecific bool
}

func (s routeMatchScore) betterThan(other routeMatchScore) bool {
	if s.pathLen != other.pathLen {
		return s.pathLen > other.pathLen
	}
	if s.hostSpecific != other.hostSpecific {
		return s.hostSpecific
	}
	if s.methodSpecific != other.methodSpecific {
		return s.methodSpecific
	}
	return false
}

func routeMatchesRequest(match RouteMatch, r *http.Request) bool {
	if r == nil {
		return false
	}
	if match.Host != "" && !strings.EqualFold(match.Host, requestHost(r)) {
		return false
	}
	if match.PathPrefix != "" && !strings.HasPrefix(r.URL.Path, match.PathPrefix) {
		return false
	}
	if len(match.Methods) > 0 && !methodMatches(match.Methods, r.Method) {
		return false
	}
	return true
}

func methodMatches(methods []string, method string) bool {
	return slices.ContainsFunc(methods, func(candidate string) bool {
		return strings.EqualFold(candidate, method)
	})
}

func scoreRouteMatch(match RouteMatch) routeMatchScore {
	return routeMatchScore{
		pathLen:        len(match.PathPrefix),
		hostSpecific:   match.Host != "",
		methodSpecific: len(match.Methods) > 0,
	}
}

func requestHost(r *http.Request) string {
	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}
