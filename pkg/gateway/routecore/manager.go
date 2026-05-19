package routecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

var (
	ErrRouteNotConfigured  = errors.New("route is not configured")
	ErrStaticRouteReadOnly = errors.New("static route is read-only")
)

type RouteListOptions struct {
	Tag       string
	TagPrefix string
}

type AgentRouteConfigManager struct {
	mu sync.RWMutex

	staticRoutes map[string]AgentRouteConfig
	dynamicCache map[string]AgentRouteConfig

	routeStore configstore.ConfigStore
}

func NewAgentRouteConfigManager(store configstore.ConfigStore) *AgentRouteConfigManager {
	return &AgentRouteConfigManager{
		staticRoutes: map[string]AgentRouteConfig{},
		dynamicCache: map[string]AgentRouteConfig{},
		routeStore:   store,
	}
}

func (m *AgentRouteConfigManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticRoutes = map[string]AgentRouteConfig{}
	m.dynamicCache = map[string]AgentRouteConfig{}
}

func (m *AgentRouteConfigManager) InitStaticRoutes(routes []AgentRouteConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticRoutes = make(map[string]AgentRouteConfig, len(routes))
	for _, route := range routes {
		if route.ID == "" {
			continue
		}
		m.staticRoutes[route.ID] = route
	}
}

func (m *AgentRouteConfigManager) UpsertStaticRoute(route AgentRouteConfig) {
	if route.ID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.staticRoutes == nil {
		m.staticRoutes = map[string]AgentRouteConfig{}
	}
	m.staticRoutes[route.ID] = route
}

func (m *AgentRouteConfigManager) IsStatic(routeID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticRoutes[routeID]
	return ok
}

func (m *AgentRouteConfigManager) Get(ctx context.Context, routeID string) (AgentRouteConfig, error) {
	if routeID == "" {
		return AgentRouteConfig{}, fmt.Errorf("route_id is required")
	}

	m.mu.RLock()
	staticRoute, ok := m.staticRoutes[routeID]
	m.mu.RUnlock()
	if ok {
		return staticRoute, nil
	}

	m.mu.RLock()
	cachedRoute, ok := m.dynamicCache[routeID]
	store := m.routeStore
	m.mu.RUnlock()
	if ok {
		return cachedRoute, nil
	}

	if store == nil {
		return AgentRouteConfig{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
	}

	item, err := store.Get(ctx, routeID)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return AgentRouteConfig{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
		}
		return AgentRouteConfig{}, fmt.Errorf("load route %q: %w", routeID, err)
	}

	route, err := decodeRouteConfigItem(routeID, item)
	if err != nil {
		return AgentRouteConfig{}, err
	}
	m.cacheDynamicRoute(route)
	return route, nil
}

func (m *AgentRouteConfigManager) List(ctx context.Context, opts RouteListOptions) ([]AgentRouteConfig, error) {
	m.mu.RLock()
	store := m.routeStore
	staticRoutes := make(map[string]AgentRouteConfig, len(m.staticRoutes))
	for id, route := range m.staticRoutes {
		staticRoutes[id] = route
	}
	m.mu.RUnlock()

	out := make(map[string]AgentRouteConfig, len(staticRoutes))
	if shouldIncludeStaticRoutes(opts) {
		for id, route := range staticRoutes {
			out[id] = route
		}
	}

	if store == nil {
		return mapRouteConfigs(out), nil
	}

	var (
		items []any
		err   error
	)
	if opts.TagPrefix != "" {
		items, err = store.ListByTagPrefix(ctx, opts.TagPrefix)
	} else {
		items, err = store.ListByTag(ctx, opts.Tag)
	}
	if err != nil {
		return nil, err
	}

	cached := make(map[string]AgentRouteConfig, len(items))
	for _, item := range items {
		route, err := decodeRouteConfigItem("", item)
		if err != nil {
			return nil, err
		}
		cached[route.ID] = route
		if _, ok := out[route.ID]; !ok {
			out[route.ID] = route
		}
	}
	m.cacheDynamicRoutes(cached)
	return mapRouteConfigs(out), nil
}

// Match resolves the most specific route config whose MatchPolicy accepts the request.
func (m *AgentRouteConfigManager) Match(ctx context.Context, r *http.Request) (AgentRouteConfig, bool, error) {
	routes, err := m.List(ctx, RouteListOptions{})
	if err != nil {
		return AgentRouteConfig{}, false, err
	}
	matched, ok := matchRouteConfigs(routes, r)
	return matched, ok, nil
}

func MatchManagers(ctx context.Context, r *http.Request, managers ...*AgentRouteConfigManager) (AgentRouteConfig, bool, error) {
	routes := make([]AgentRouteConfig, 0)
	for _, manager := range managers {
		if manager == nil {
			continue
		}
		items, err := manager.List(ctx, RouteListOptions{})
		if err != nil {
			return AgentRouteConfig{}, false, err
		}
		routes = append(routes, items...)
	}

	matched, ok := matchRouteConfigs(routes, r)
	return matched, ok, nil
}

func MatchRouteConfigs(routes []AgentRouteConfig, r *http.Request) (AgentRouteConfig, bool) {
	return matchRouteConfigs(routes, r)
}

func matchRouteConfigs(routes []AgentRouteConfig, r *http.Request) (AgentRouteConfig, bool) {
	var (
		best      AgentRouteConfig
		bestScore routeMatchScore
		found     bool
	)
	for _, route := range routes {
		if !routeMatchesRequest(route.MatchPolicy, r) {
			continue
		}
		score := scoreRouteMatch(route.MatchPolicy)
		if !found || score.betterThan(bestScore) {
			best = route
			bestScore = score
			found = true
		}
	}
	return best, found
}

func (m *AgentRouteConfigManager) Create(ctx context.Context, route AgentRouteConfig, tag string) error {
	if route.ID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(route.ID); err != nil {
		return err
	}
	route = normalizeRouteConfigTimestamps(route, time.Now().UTC(), true)

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("route store is not configured")
	}
	if err := store.Create(ctx, storedRouteConfig{route: route, tag: tag}); err != nil {
		return err
	}

	m.cacheDynamicRoute(route)
	return nil
}

func (m *AgentRouteConfigManager) Update(ctx context.Context, routeID string, route AgentRouteConfig) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(routeID); err != nil {
		return err
	}

	route.ID = routeID
	current, err := m.Get(ctx, routeID)
	if err != nil {
		return err
	}
	route = normalizeRouteConfigTimestamps(route, time.Now().UTC(), false)
	route.CreatedAt = current.CreatedAt

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("route store is not configured")
	}
	if err := store.Update(ctx, storedRouteConfig{route: route}); err != nil {
		return err
	}

	m.cacheDynamicRoute(route)
	return nil
}

func (m *AgentRouteConfigManager) Delete(ctx context.Context, routeID string) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(routeID); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("route store is not configured")
	}
	if err := store.Delete(ctx, routeID); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dynamicCache, routeID)
	return nil
}

type storedRouteConfig struct {
	route AgentRouteConfig
	tag   string
}

func (r storedRouteConfig) ConfigStoreObject() any {
	return &r.route
}

func (r storedRouteConfig) ConfigStoreTag() string {
	return r.tag
}

func DecodeStoredAgentRouteConfig(data []byte) (any, error) {
	var cfg AgentRouteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode route config: %w", err)
	}
	return &cfg, nil
}

func (m *AgentRouteConfigManager) ensureWritable(routeID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticRoutes[routeID]; ok {
		return fmt.Errorf("%w: %q", ErrStaticRouteReadOnly, routeID)
	}
	return nil
}

func (m *AgentRouteConfigManager) cacheDynamicRoute(route AgentRouteConfig) {
	if route.ID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]AgentRouteConfig{}
	}
	m.dynamicCache[route.ID] = route
}

func (m *AgentRouteConfigManager) cacheDynamicRoutes(routes map[string]AgentRouteConfig) {
	if len(routes) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]AgentRouteConfig{}
	}
	for id, route := range routes {
		m.dynamicCache[id] = route
	}
}

func decodeRouteConfigItem(routeID string, item any) (AgentRouteConfig, error) {
	switch route := item.(type) {
	case *AgentRouteConfig:
		if route == nil || route.ID == "" {
			break
		}
		return *route, nil
	case AgentRouteConfig:
		if route.ID == "" {
			break
		}
		return route, nil
	}
	if routeID == "" {
		routeID = "<unknown>"
	}
	return AgentRouteConfig{}, fmt.Errorf("route %q has unexpected type %T", routeID, item)
}

func normalizeRouteConfigTimestamps(route AgentRouteConfig, now time.Time, create bool) AgentRouteConfig {
	if create {
		if route.CreatedAt.IsZero() {
			route.CreatedAt = now
		}
		if route.UpdatedAt.IsZero() {
			route.UpdatedAt = now
		}
		return route
	}
	route.UpdatedAt = now
	return route
}

func shouldIncludeStaticRoutes(opts RouteListOptions) bool {
	if opts.TagPrefix != "" {
		return false
	}
	return opts.Tag == ""
}

func mapRouteConfigs(routes map[string]AgentRouteConfig) []AgentRouteConfig {
	out := make([]AgentRouteConfig, 0, len(routes))
	for _, route := range routes {
		out = append(out, route)
	}
	return out
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

func routeMatchesRequest(match RouteMatchPolicy, r *http.Request) bool {
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

func scoreRouteMatch(match RouteMatchPolicy) routeMatchScore {
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
