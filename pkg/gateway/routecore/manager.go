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

type AgentRouteConfigManager struct {
	base *configmgr.BaseConfigManager[AgentRouteConfig]
}

func NewAgentRouteConfigManager(store configstore.ConfigStore) *AgentRouteConfigManager {
	return &AgentRouteConfigManager{
		base: configmgr.NewBaseConfigManager(store, configmgr.Definition[AgentRouteConfig]{
			GetID:  routeConfigID,
			Decode: decodeRouteConfigItem,
			Clone:  cloneRouteConfig,
			PrepareCreate: func(route AgentRouteConfig) (any, AgentRouteConfig, error) {
				if route.ID == "" {
					return nil, AgentRouteConfig{}, fmt.Errorf("route id is required")
				}
				route = normalizeRouteConfigTimestamps(route, time.Now().UTC(), true)
				return storedRouteConfig{route: route}, route, nil
			},
			PrepareUpdate: func(routeID string, current AgentRouteConfig, route AgentRouteConfig) (any, AgentRouteConfig, error) {
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

func (m *AgentRouteConfigManager) Reset() {
	m.base.Reset()
}

func (m *AgentRouteConfigManager) InitStaticRoutes(routes []AgentRouteConfig) {
	m.base.InitStatic(routes)
}

func (m *AgentRouteConfigManager) IsStatic(routeID string) bool {
	return m.base.IsStatic(routeID)
}

func (m *AgentRouteConfigManager) Get(ctx context.Context, routeID string) (AgentRouteConfig, error) {
	if routeID == "" {
		return AgentRouteConfig{}, fmt.Errorf("route_id is required")
	}

	route, err := m.base.Get(ctx, routeID)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return AgentRouteConfig{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
		}
		if errors.Is(err, ErrRouteNotConfigured) {
			return AgentRouteConfig{}, err
		}
		return AgentRouteConfig{}, fmt.Errorf("load route %q: %w", routeID, err)
	}
	return route, nil
}

func (m *AgentRouteConfigManager) List(ctx context.Context, opts RouteListOptions) ([]AgentRouteConfig, error) {
	return m.base.List(ctx, configmgr.ListQuery{
		Tag:       opts.Tag,
		TagPrefix: opts.TagPrefix,
	})
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
	route = normalizeRouteConfigTimestamps(route, time.Now().UTC(), true)
	return m.base.CreatePrepared(ctx, route.ID, storedRouteConfig{route: route, tag: tag}, route)
}

func (m *AgentRouteConfigManager) Update(ctx context.Context, routeID string, route AgentRouteConfig) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	return m.base.Update(ctx, routeID, route)
}

func (m *AgentRouteConfigManager) Delete(ctx context.Context, routeID string) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	return m.base.Delete(ctx, routeID)
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

func cloneRouteConfig(route AgentRouteConfig) AgentRouteConfig {
	if len(route.MatchPolicy.Methods) > 0 {
		route.MatchPolicy.Methods = append([]string(nil), route.MatchPolicy.Methods...)
	}
	return route
}

func routeConfigID(route AgentRouteConfig) string {
	return route.ID
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
