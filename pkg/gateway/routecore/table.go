package routecore

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
)

type compiledRoute struct {
	config RouteConfig
	match  RouteMatchPolicy
	score  routeMatchScore
}

// routeTable owns immutable copies. Requests retain one generation while
// mutations compile and replace another; only the selected result is copied.
type routeTable struct {
	routes []compiledRoute
	byID   map[string]RouteConfig
}

func compileRouteTable(routes []RouteConfig) *routeTable {
	table := &routeTable{routes: make([]compiledRoute, 0, len(routes)), byID: make(map[string]RouteConfig, len(routes))}
	for _, route := range routes {
		route = cloneRouteConfig(route)
		table.routes = append(table.routes, compiledRoute{config: route, match: EffectiveMatch(route), score: scoreRouteMatch(route.MatchPolicy)})
		table.byID[route.ID] = route
	}
	return table
}

// publishTable is called under writeMu after successful validation/persistence.
func (m *RouteConfigManager) publishTable(loaded bool) {
	table := compileRouteTable(m.base.Snapshot(configmgr.ListQuery{}))
	m.mu.Lock()
	old := m.table
	m.table = table
	m.dynamicLoaded = loaded
	invalidators := append([]func(string){}, m.invalidators...)
	m.mu.Unlock()
	changed := map[string]bool{}
	if old != nil {
		for id, previous := range old.byID {
			next, exists := table.byID[id]
			if !exists || previous.Fingerprint() != next.Fingerprint() {
				changed[id] = true
			}
		}
	}
	for id := range table.byID {
		if old == nil {
			changed[id] = true
		} else if _, exists := old.byID[id]; !exists {
			changed[id] = true
		}
	}
	for id := range changed {
		for _, invalidate := range invalidators {
			invalidate(id)
		}
	}
}

func (m *RouteConfigManager) publishedTable() *routeTable {
	m.mu.RLock()
	table := m.table
	m.mu.RUnlock()
	return table
}

func (t *routeTable) match(r *http.Request) (RouteConfig, bool, error) {
	var best *compiledRoute
	var tied []string
	if t != nil {
		for i := range t.routes {
			route := &t.routes[i]
			if !routeMatchesRequest(route.match, r) {
				continue
			}
			if best == nil || route.score.betterThan(best.score) {
				best = route
				tied = nil
			} else if route.score == best.score {
				if len(tied) == 0 {
					tied = append(tied, best.config.ID)
				}
				tied = append(tied, route.config.ID)
			}
		}
	}
	if len(tied) > 1 {
		sort.Strings(tied)
		return RouteConfig{}, false, fmt.Errorf("%w: equally matching routes %q", ErrAmbiguousRoute, tied)
	}
	if best == nil {
		return RouteConfig{}, false, nil
	}
	return cloneRouteConfig(best.config), true, nil
}

// AddInvalidator attaches a cache owned by a typed resolver. Callbacks perform
// bounded in-memory eviction only, without I/O or reentering manager mutations.
func (m *RouteConfigManager) AddInvalidator(invalidate func(string)) {
	if m == nil || invalidate == nil {
		return
	}
	m.mu.Lock()
	m.invalidators = append(m.invalidators, invalidate)
	m.mu.Unlock()
}
