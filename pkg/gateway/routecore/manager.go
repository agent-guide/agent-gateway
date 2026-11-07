package routecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
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

type RouteConfigManager struct {
	base *configmgr.BaseConfigManager[RouteConfig]
	// Serializes full-generation validation, persistence and publication.
	writeMu sync.Mutex
	guard   configmgr.MutationGuard[RouteConfig]

	mu            sync.RWMutex
	dynamicLoaded bool
}

func NewRouteConfigManager(store configstore.ConfigStore, guards ...configmgr.MutationGuard[RouteConfig]) *RouteConfigManager {
	return &RouteConfigManager{
		guard: configmgr.SelectMutationGuard(guards),
		base: configmgr.NewBaseConfigManager(store, configmgr.Definition[RouteConfig]{
			DisableListCache: true,
			ValidateSnapshot: ValidateRouteConfigs,
			GetID:            routeConfigID,
			Decode:           decodeRouteConfigItem,
			Clone:            cloneRouteConfig,
			PrepareCreate: func(route RouteConfig) (any, RouteConfig, error) {
				if route.ID == "" {
					return nil, RouteConfig{}, fmt.Errorf("route id is required")
				}
				route = normalizeRouteConfigTimestamps(route, time.Now().UTC(), true)
				return storedRouteConfig{route: route}, route, nil
			},
			PrepareUpdate: func(routeID string, current RouteConfig, route RouteConfig) (any, RouteConfig, error) {
				route.ID = routeID
				route = normalizeRouteConfigTimestamps(route, time.Now().UTC(), false)
				route.CreatedAt = current.CreatedAt
				return storedRouteConfig{route: route}, route, nil
			},
			ShouldIncludeStatic: func(query configmgr.ListQuery) bool {
				return query.TagPrefix == "" && query.Tag == ""
			},
			NotConfiguredErr: func(id string) error {
				return fmt.Errorf("%w: %q", ErrRouteNotConfigured, id)
			},
			ReadOnlyErr: func(id string) error {
				return fmt.Errorf("%w: %q", ErrStaticRouteReadOnly, id)
			},
			StoreNilErr: func() error {
				return fmt.Errorf("route store is not configured")
			},
		}),
	}
}

func (m *RouteConfigManager) Reset() {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	m.base.Reset()
	m.mu.Lock()
	m.dynamicLoaded = false
	m.mu.Unlock()
}

func (m *RouteConfigManager) InitStaticRoutes(routes []RouteConfig) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	candidate := append([]RouteConfig(nil), routes...)
	for _, r := range m.base.Snapshot(configmgr.ListQuery{}) {
		if !m.base.IsStatic(r.ID) {
			candidate = append(candidate, r)
		}
	}
	if err := ValidateRouteConfigs(candidate); err != nil {
		return err
	}
	m.base.InitStatic(routes)
	return nil
}

func (m *RouteConfigManager) IsStatic(routeID string) bool {
	return m.base.IsStatic(routeID)
}

func (m *RouteConfigManager) Get(ctx context.Context, routeID string) (RouteConfig, error) {
	if routeID == "" {
		return RouteConfig{}, fmt.Errorf("route_id is required")
	}

	if err := m.ensureDynamicLoaded(ctx); err != nil {
		return RouteConfig{}, err
	}
	if route, ok := m.base.GetCached(routeID); ok {
		return route, nil
	}
	return RouteConfig{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
}

func (m *RouteConfigManager) List(ctx context.Context, opts RouteListOptions) ([]RouteConfig, error) {
	return m.base.List(ctx, configmgr.ListQuery{
		Tag:       opts.Tag,
		TagPrefix: opts.TagPrefix,
	})
}

func (m *RouteConfigManager) Refresh(ctx context.Context) error {
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationRefresh, "", nil, m.refresh)
}

func (m *RouteConfigManager) refresh(ctx context.Context) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return m.refreshLocked(ctx)
}

func (m *RouteConfigManager) refreshLocked(ctx context.Context) error {
	if err := m.base.Refresh(ctx, configmgr.ListQuery{}); err != nil {
		return err
	}
	m.mu.Lock()
	m.dynamicLoaded = true
	m.mu.Unlock()
	return nil
}

// Match resolves the most specific route config whose MatchPolicy accepts the request.
func (m *RouteConfigManager) Match(ctx context.Context, r *http.Request) (RouteConfig, bool, error) {
	if err := m.ensureDynamicLoaded(ctx); err != nil {
		return RouteConfig{}, false, err
	}
	routes := m.base.Snapshot(configmgr.ListQuery{})
	return matchRouteConfigs(routes, r)
}

func (m *RouteConfigManager) ensureDynamicLoaded(ctx context.Context) error {
	m.mu.RLock()
	loaded := m.dynamicLoaded
	m.mu.RUnlock()
	if loaded {
		return nil
	}
	return m.Refresh(ctx)
}

func MatchManagers(ctx context.Context, r *http.Request, managers ...*RouteConfigManager) (RouteConfig, bool, error) {
	routes := make([]RouteConfig, 0)
	for _, manager := range managers {
		if manager == nil {
			continue
		}
		if err := manager.ensureDynamicLoaded(ctx); err != nil {
			return RouteConfig{}, false, err
		}
		routes = append(routes, manager.base.Snapshot(configmgr.ListQuery{})...)
	}

	return matchRouteConfigs(routes, r)
}

func MatchRouteConfigs(routes []RouteConfig, r *http.Request) (RouteConfig, bool, error) {
	return matchRouteConfigs(routes, r)
}

func matchRouteConfigs(routes []RouteConfig, r *http.Request) (RouteConfig, bool, error) {
	var (
		best      RouteConfig
		bestScore routeMatchScore
		found     bool
	)
	var tied []string
	for _, route := range routes {
		// Native A2A owns its exact endpoint's 404/405 behavior. Its validated
		// GET+POST method declaration must not prevent the dispatcher from
		// returning protocol-correct transport errors for other methods.
		match := EffectiveMatch(route)
		if !routeMatchesRequest(match, r) {
			continue
		}
		score := scoreRouteMatch(route.MatchPolicy)
		if !found || score.betterThan(bestScore) {
			best = route
			bestScore = score
			found = true
			tied = nil
		} else if score == bestScore {
			if len(tied) == 0 {
				tied = append(tied, best.ID)
			}
			tied = append(tied, route.ID)
		}
	}
	if len(tied) > 1 {
		sort.Strings(tied)
		return RouteConfig{}, false, fmt.Errorf("%w: equally matching routes %q", ErrAmbiguousRoute, tied)
	}
	return cloneRouteConfig(best), found, nil
}

func (m *RouteConfigManager) Create(ctx context.Context, route RouteConfig, tag string) error {
	route = cloneRouteConfig(route)
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationCreate, route.ID, &route, func(ctx context.Context) error { return m.create(ctx, route, tag) })
}

func (m *RouteConfigManager) create(ctx context.Context, route RouteConfig, tag string) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if err := m.ensureLoadedLocked(ctx); err != nil {
		return err
	}
	if route.ID == "" {
		return fmt.Errorf("route id is required")
	}
	route = normalizeRouteConfigTimestamps(route, time.Now().UTC(), true)
	if err := ValidateRouteConfigs(append(m.base.Snapshot(configmgr.ListQuery{}), route)); err != nil {
		return err
	}
	if err := m.base.CreatePrepared(ctx, route.ID, storedRouteConfig{route: route, tag: tag}, route); err != nil {
		return err
	}
	m.mu.Lock()
	m.dynamicLoaded = true
	m.mu.Unlock()
	return nil
}

func (m *RouteConfigManager) Update(ctx context.Context, routeID string, route RouteConfig) error {
	route = cloneRouteConfig(route)
	route.ID = routeID
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationUpdate, routeID, &route, func(ctx context.Context) error { return m.update(ctx, routeID, route) })
}

func (m *RouteConfigManager) update(ctx context.Context, routeID string, route RouteConfig) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if err := m.ensureLoadedLocked(ctx); err != nil {
		return err
	}
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	route.ID = routeID
	candidate := m.base.Snapshot(configmgr.ListQuery{})
	found := false
	for i, r := range candidate {
		if r.ID == routeID {
			candidate[i] = route
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
	}
	if err := ValidateRouteConfigs(candidate); err != nil {
		return err
	}
	if err := m.base.Update(ctx, routeID, route); err != nil {
		return err
	}
	m.mu.Lock()
	m.dynamicLoaded = true
	m.mu.Unlock()
	return nil
}

func (m *RouteConfigManager) Delete(ctx context.Context, routeID string) error {
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationDelete, routeID, nil, func(ctx context.Context) error { return m.delete(ctx, routeID) })
}

func (m *RouteConfigManager) delete(ctx context.Context, routeID string) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if err := m.ensureLoadedLocked(ctx); err != nil {
		return err
	}
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.base.Delete(ctx, routeID); err != nil {
		return err
	}
	m.mu.Lock()
	m.dynamicLoaded = true
	m.mu.Unlock()
	return nil
}

func (m *RouteConfigManager) ensureLoadedLocked(ctx context.Context) error {
	m.mu.RLock()
	loaded := m.dynamicLoaded
	m.mu.RUnlock()
	if loaded {
		return nil
	}
	return m.refreshLocked(ctx)
}

type storedRouteConfig struct {
	route RouteConfig
	tag   string
}

func (r storedRouteConfig) ConfigStoreObject() any {
	return &r.route
}

func (r storedRouteConfig) ConfigStoreTag() string {
	return r.tag
}

func DecodeStoredRouteConfig(data []byte) (any, error) {
	var cfg RouteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode route config: %w", err)
	}
	if cfg.Kind == RouteKindLLM {
		if err := ValidateLLMTargetFields(cfg.ID, cfg.TargetPolicy); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

func decodeRouteConfigItem(routeID string, item any) (RouteConfig, error) {
	switch route := item.(type) {
	case *RouteConfig:
		if route == nil || route.ID == "" {
			break
		}
		return *route, nil
	case RouteConfig:
		if route.ID == "" {
			break
		}
		return route, nil
	}
	if routeID == "" {
		routeID = "<unknown>"
	}
	return RouteConfig{}, fmt.Errorf("route %q has unexpected type %T", routeID, item)
}

func cloneRouteConfig(route RouteConfig) RouteConfig {
	route.TargetPolicy = append(json.RawMessage(nil), route.TargetPolicy...)
	if len(route.MatchPolicy.Methods) > 0 {
		route.MatchPolicy.Methods = append([]string(nil), route.MatchPolicy.Methods...)
	}
	return route
}

func routeConfigID(route RouteConfig) string {
	return route.ID
}

func normalizeRouteConfigTimestamps(route RouteConfig, now time.Time, create bool) RouteConfig {
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
	if r == nil {
		return ""
	}

	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	if host == "" {
		return ""
	}

	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		return parsedHost
	}
	return host
}
