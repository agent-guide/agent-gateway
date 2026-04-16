package route

import (
	"context"
	"errors"
	"fmt"
	"sync"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

var (
	ErrRouteNotConfigured  = errors.New("route is not configured")
	ErrStaticRouteReadOnly = errors.New("static route is read-only")
)

type ProviderResolver interface {
	ResolveProvider(ctx context.Context, ref string) (provider.Provider, string, error)
}

type RouteListOptions struct {
	Tag       string
	TagPrefix string
}

type RouteManager struct {
	mu sync.RWMutex

	staticRoutes map[string]Route
	dynamicCache map[string]Route

	routeStore configstoreintf.RouteStorer
}

func NewRouteManager(store configstoreintf.RouteStorer) *RouteManager {
	return &RouteManager{
		staticRoutes: map[string]Route{},
		dynamicCache: map[string]Route{},
		routeStore:   store,
	}
}

func (m *RouteManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticRoutes = map[string]Route{}
	m.dynamicCache = map[string]Route{}
}

func (m *RouteManager) InitStaticRoutes(routes []Route) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticRoutes = make(map[string]Route, len(routes))
	for _, r := range routes {
		if r.ID == "" {
			continue
		}
		r.Normalize()
		m.staticRoutes[r.ID] = r
	}
}

func (m *RouteManager) IsStatic(routeID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticRoutes[routeID]
	return ok
}

func (m *RouteManager) Get(ctx context.Context, routeID string) (Route, error) {
	if routeID == "" {
		return Route{}, fmt.Errorf("route_id is required")
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
		return Route{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
	}

	item, err := store.Get(ctx, routeID)
	if err != nil {
		if errors.Is(err, configstoreintf.ErrNotFound) {
			return Route{}, fmt.Errorf("%w: %q", ErrRouteNotConfigured, routeID)
		}
		return Route{}, fmt.Errorf("load route %q: %w", routeID, err)
	}

	route, err := decodeRouteItem(routeID, item)
	if err != nil {
		return Route{}, err
	}
	m.cacheDynamicRoute(route)
	return route, nil
}

func (m *RouteManager) List(ctx context.Context, opts RouteListOptions) ([]Route, error) {
	m.mu.RLock()
	store := m.routeStore
	staticRoutes := make(map[string]Route, len(m.staticRoutes))
	for id, route := range m.staticRoutes {
		staticRoutes[id] = route
	}
	m.mu.RUnlock()

	out := make(map[string]Route, len(staticRoutes))
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

	cached := make(map[string]Route, len(items))
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

func (m *RouteManager) Create(ctx context.Context, route Route, tag string) error {
	if route.ID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(route.ID); err != nil {
		return err
	}

	route.Normalize()

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("route store is not configured")
	}
	if err := store.Create(ctx, route.ID, tag, &route); err != nil {
		return err
	}

	m.cacheDynamicRoute(route)
	return nil
}

func (m *RouteManager) Update(ctx context.Context, routeID string, route Route) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureWritable(routeID); err != nil {
		return err
	}

	route.ID = routeID
	route.Normalize()

	m.mu.RLock()
	store := m.routeStore
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("route store is not configured")
	}
	if err := store.Update(ctx, routeID, &route); err != nil {
		return err
	}

	m.cacheDynamicRoute(route)
	return nil
}

func (m *RouteManager) Delete(ctx context.Context, routeID string) error {
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

func (m *RouteManager) Validate(ctx context.Context, routeID string, resolver ProviderResolver) error {
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

	for _, ref := range route.ProviderRefs() {
		if _, _, err := resolver.ResolveProvider(ctx, ref); err != nil {
			return fmt.Errorf("provider %q is not configured", ref)
		}
	}
	return nil
}

func (m *RouteManager) ensureWritable(routeID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticRoutes[routeID]; ok {
		return fmt.Errorf("%w: %q", ErrStaticRouteReadOnly, routeID)
	}
	return nil
}

func (m *RouteManager) cacheDynamicRoute(route Route) {
	if route.ID == "" {
		return
	}
	route.Normalize()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]Route{}
	}
	m.dynamicCache[route.ID] = route
}

func (m *RouteManager) cacheDynamicRoutes(routes map[string]Route) {
	if len(routes) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]Route{}
	}
	for id, route := range routes {
		route.Normalize()
		m.dynamicCache[id] = route
	}
}

func decodeRouteItem(routeID string, item any) (Route, error) {
	route, ok := item.(*Route)
	if !ok || route == nil || route.ID == "" {
		if routeID == "" {
			routeID = "<unknown>"
		}
		return Route{}, fmt.Errorf("route %q has unexpected type %T", routeID, item)
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

func mapRoutes(routes map[string]Route) []Route {
	out := make([]Route, 0, len(routes))
	for _, route := range routes {
		out = append(out, route)
	}
	return out
}
