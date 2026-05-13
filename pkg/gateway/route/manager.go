package route

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

var (
	ErrRouteNotConfigured  = errors.New("route is not configured")
	ErrStaticRouteReadOnly = errors.New("static route is read-only")
)

type ProviderResolver interface {
	ResolveProvider(ctx context.Context, providerID string) (provider.Provider, string, error)
}

type RouteListOptions struct {
	Tag       string
	TagPrefix string
}

type AgentRouteManager struct {
	mu sync.RWMutex

	staticRoutes map[string]AgentRoute
	dynamicCache map[string]AgentRoute

	routeStore configstore.ConfigStore
}

func NewAgentRouteManager(store configstore.ConfigStore) *AgentRouteManager {
	return &AgentRouteManager{
		staticRoutes: map[string]AgentRoute{},
		dynamicCache: map[string]AgentRoute{},
		routeStore:   store,
	}
}

func (m *AgentRouteManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticRoutes = map[string]AgentRoute{}
	m.dynamicCache = map[string]AgentRoute{}
}

func (m *AgentRouteManager) InitStaticRoutes(routes []AgentRoute) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticRoutes = make(map[string]AgentRoute, len(routes))
	for _, r := range routes {
		if r.ID == "" {
			continue
		}
		r.Normalize()
		m.staticRoutes[r.ID] = r
	}
}

func (m *AgentRouteManager) IsStatic(routeID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticRoutes[routeID]
	return ok
}

func (m *AgentRouteManager) Get(ctx context.Context, routeID string) (AgentRoute, error) {
	if routeID == "" {
		return AgentRoute{}, fmt.Errorf("route_id is required")
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
		return AgentRoute{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
	}

	item, err := store.Get(ctx, routeID)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return AgentRoute{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
		}
		return AgentRoute{}, fmt.Errorf("load route %q: %w", routeID, err)
	}

	route, err := decodeRouteItem(routeID, item)
	if err != nil {
		return AgentRoute{}, err
	}
	m.cacheDynamicRoute(route)
	return route, nil
}

func (m *AgentRouteManager) List(ctx context.Context, opts RouteListOptions) ([]AgentRoute, error) {
	m.mu.RLock()
	store := m.routeStore
	staticRoutes := make(map[string]AgentRoute, len(m.staticRoutes))
	for id, route := range m.staticRoutes {
		staticRoutes[id] = route
	}
	m.mu.RUnlock()

	out := make(map[string]AgentRoute, len(staticRoutes))
	if shouldIncludeStaticRoutes(opts) {
		for id, route := range staticRoutes {
			out[id] = route
		}
	}

	if store == nil {
		return mapRoutes(out), nil
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

	cached := make(map[string]AgentRoute, len(items))
	for _, item := range items {
		route, err := decodeRouteItem("", item)
		if err != nil {
			return nil, err
		}
		cached[route.ID] = route
		if _, ok := out[route.ID]; !ok {
			out[route.ID] = route
		}
	}
	m.cacheDynamicRoutes(cached)
	return mapRoutes(out), nil
}

func (m *AgentRouteManager) Create(ctx context.Context, route AgentRoute, tag string) error {
	if route.ID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(route.ID); err != nil {
		return err
	}

	route.Normalize()
	route.NormalizeTimestamps(time.Now().UTC())

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("route store is not configured")
	}
	if err := store.Create(ctx, storedRoute{route: &route, tag: tag}); err != nil {
		return err
	}

	m.cacheDynamicRoute(route)
	return nil
}

func (m *AgentRouteManager) Update(ctx context.Context, routeID string, route AgentRoute) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(routeID); err != nil {
		return err
	}

	route.ID = routeID
	route.Normalize()
	current, err := m.Get(ctx, routeID)
	if err != nil {
		return err
	}
	route.CreatedAt = current.CreatedAt
	route.UpdatedAt = time.Now().UTC()

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("route store is not configured")
	}
	if err := store.Update(ctx, &route); err != nil {
		return err
	}

	m.cacheDynamicRoute(route)
	return nil
}

func (m *AgentRouteManager) Delete(ctx context.Context, routeID string) error {
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

type storedRoute struct {
	route any
	tag   string
}

func (r storedRoute) ConfigStoreObject() any {
	return r.route
}

func (r storedRoute) ConfigStoreTag() string {
	return r.tag
}

func (m *AgentRouteManager) Validate(ctx context.Context, routeID string, resolver ProviderResolver) error {
	route, err := m.Get(ctx, routeID)
	if err != nil {
		return err
	}
	if err := route.ValidateDefinition(); err != nil {
		return err
	}
	if resolver == nil {
		return fmt.Errorf("provider resolver is not configured")
	}
	for _, providerID := range route.ProviderIDs() {
		if _, _, err := resolver.ResolveProvider(ctx, providerID); err != nil {
			return fmt.Errorf("provider %q is not configured", providerID)
		}
	}
	return nil
}

func (m *AgentRouteManager) ensureWritable(routeID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticRoutes[routeID]; ok {
		return fmt.Errorf("%w: %q", ErrStaticRouteReadOnly, routeID)
	}
	return nil
}

func (m *AgentRouteManager) cacheDynamicRoute(route AgentRoute) {
	if route.ID == "" {
		return
	}
	route.Normalize()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]AgentRoute{}
	}
	m.dynamicCache[route.ID] = route
}

func (m *AgentRouteManager) cacheDynamicRoutes(routes map[string]AgentRoute) {
	if len(routes) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]AgentRoute{}
	}
	for id, route := range routes {
		route.Normalize()
		m.dynamicCache[id] = route
	}
}

func decodeRouteItem(routeID string, item any) (AgentRoute, error) {
	route, ok := item.(*AgentRoute)
	if !ok || route == nil || route.ID == "" {
		if routeID == "" {
			routeID = "<unknown>"
		}
		return AgentRoute{}, fmt.Errorf("route %q has unexpected type %T", routeID, item)
	}

	cloned := *route
	cloned.Normalize()
	return cloned, nil
}

func shouldIncludeStaticRoutes(opts RouteListOptions) bool {
	if opts.TagPrefix != "" {
		return false
	}
	return opts.Tag == ""
}

func mapRoutes(routes map[string]AgentRoute) []AgentRoute {
	out := make([]AgentRoute, 0, len(routes))
	for _, route := range routes {
		out = append(out, route)
	}
	return out
}
