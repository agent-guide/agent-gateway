package mcproute

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

var (
	ErrRouteNotConfigured  = errors.New("mcp route is not configured")
	ErrStaticRouteReadOnly = errors.New("mcp route is read-only")
)

type RouteListOptions struct {
	Tag       string
	TagPrefix string
}

type Manager struct {
	mu sync.RWMutex

	staticRoutes map[string]MCPRoute
	dynamicCache map[string]MCPRoute

	routeStore configstore.ConfigStore
}

func NewManager(store configstore.ConfigStore) *Manager {
	return &Manager{
		staticRoutes: map[string]MCPRoute{},
		dynamicCache: map[string]MCPRoute{},
		routeStore:   store,
	}
}

func (m *Manager) InitStaticRoutes(routes []MCPRoute) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticRoutes = make(map[string]MCPRoute, len(routes))
	for _, route := range routes {
		route.Normalize()
		if route.ID == "" {
			continue
		}
		m.staticRoutes[route.ID] = route
	}
}

func (m *Manager) Get(ctx context.Context, routeID string) (MCPRoute, error) {
	if routeID == "" {
		return MCPRoute{}, fmt.Errorf("route_id is required")
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
		return MCPRoute{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
	}
	item, err := store.Get(ctx, routeID)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return MCPRoute{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
		}
		return MCPRoute{}, fmt.Errorf("load mcp route %q: %w", routeID, err)
	}
	route, err := decodeRouteItem(routeID, item)
	if err != nil {
		return MCPRoute{}, err
	}
	m.cacheDynamicRoute(route)
	return route, nil
}

func (m *Manager) List(ctx context.Context, opts RouteListOptions) ([]MCPRoute, error) {
	m.mu.RLock()
	store := m.routeStore
	staticRoutes := make(map[string]MCPRoute, len(m.staticRoutes))
	for id, route := range m.staticRoutes {
		staticRoutes[id] = route
	}
	m.mu.RUnlock()

	out := make(map[string]MCPRoute, len(staticRoutes))
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
	cached := make(map[string]MCPRoute, len(items))
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

func (m *Manager) Create(ctx context.Context, route MCPRoute, tag string) error {
	if route.ID == "" && route.ServiceID == "" {
		return fmt.Errorf("route id or service_id is required")
	}
	route.Normalize()
	if err := m.ensureWritable(route.ID); err != nil {
		return err
	}
	route.NormalizeTimestamps(time.Now().UTC())
	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("mcp route store is not configured")
	}
	if err := store.Create(ctx, storedRoute{route: &route, tag: tag}); err != nil {
		return err
	}
	m.cacheDynamicRoute(route)
	return nil
}

func (m *Manager) Update(ctx context.Context, routeID string, route MCPRoute) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(routeID); err != nil {
		return err
	}
	current, err := m.Get(ctx, routeID)
	if err != nil {
		return err
	}
	route.ID = routeID
	route.Normalize()
	route.CreatedAt = current.CreatedAt
	route.UpdatedAt = time.Now().UTC()

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("mcp route store is not configured")
	}
	if err := store.Update(ctx, &route); err != nil {
		return err
	}
	m.cacheDynamicRoute(route)
	return nil
}

func (m *Manager) Delete(ctx context.Context, routeID string) error {
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
		return fmt.Errorf("mcp route store is not configured")
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

func (m *Manager) ensureWritable(routeID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.staticRoutes[routeID]; ok {
		return fmt.Errorf("%w: %q", ErrStaticRouteReadOnly, routeID)
	}
	return nil
}

func (m *Manager) cacheDynamicRoute(route MCPRoute) {
	if route.ID == "" {
		return
	}
	route.Normalize()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]MCPRoute{}
	}
	m.dynamicCache[route.ID] = route
}

func (m *Manager) cacheDynamicRoutes(routes map[string]MCPRoute) {
	if len(routes) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]MCPRoute{}
	}
	for id, route := range routes {
		route.Normalize()
		m.dynamicCache[id] = route
	}
}

func decodeRouteItem(routeID string, item any) (MCPRoute, error) {
	route, ok := item.(*MCPRoute)
	if !ok || route == nil || route.ID == "" {
		if routeID == "" {
			routeID = "<unknown>"
		}
		return MCPRoute{}, fmt.Errorf("mcp route %q has unexpected type %T", routeID, item)
	}
	cloned := *route
	cloned.Normalize()
	return cloned, nil
}

func shouldIncludeStaticRoutes(opts RouteListOptions) bool {
	return opts.Tag == "" && opts.TagPrefix == ""
}

func mapRoutes(routes map[string]MCPRoute) []MCPRoute {
	out := make([]MCPRoute, 0, len(routes))
	for _, route := range routes {
		out = append(out, route)
	}
	return out
}
