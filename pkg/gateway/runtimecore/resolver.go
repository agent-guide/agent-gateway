package runtimecore

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

type RouteDecoder[T any] func(routecore.AgentRouteConfig) (T, error)

type RouteConfigAccessor[T any] func(T) routecore.AgentRouteConfig

type Resolver[T any] struct {
	mu sync.RWMutex

	configManager *routecore.AgentRouteConfigManager
	cache         map[string]T
	decode        RouteDecoder[T]
	configOf      RouteConfigAccessor[T]
}

func NewResolver[T any](
	configManager *routecore.AgentRouteConfigManager,
	decode RouteDecoder[T],
	configOf RouteConfigAccessor[T],
) *Resolver[T] {
	return &Resolver[T]{
		configManager: configManager,
		cache:         map[string]T{},
		decode:        decode,
		configOf:      configOf,
	}
}

func (r *Resolver[T]) ConfigManager() *routecore.AgentRouteConfigManager {
	if r == nil {
		return nil
	}
	return r.configManager
}

func (r *Resolver[T]) Get(ctx context.Context, routeID string) (T, error) {
	var zero T
	if r == nil || r.configManager == nil {
		return zero, fmt.Errorf("route config manager is not configured")
	}
	cfg, err := r.configManager.Get(ctx, routeID)
	if err != nil {
		return zero, err
	}
	return r.resolveConfig(cfg)
}

func (r *Resolver[T]) List(ctx context.Context, opts routecore.RouteListOptions) ([]T, error) {
	if r == nil || r.configManager == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	configs, err := r.configManager.List(ctx, opts)
	if err != nil {
		return nil, err
	}

	out := make([]T, 0, len(configs))
	for _, cfg := range configs {
		route, err := r.resolveConfig(cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, route)
	}
	return out, nil
}

func (r *Resolver[T]) Invalidate(routeID string) {
	if r == nil || routeID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, routeID)
}

func (r *Resolver[T]) resolveConfig(cfg routecore.AgentRouteConfig) (T, error) {
	var zero T
	if r == nil || r.decode == nil || r.configOf == nil {
		return zero, fmt.Errorf("route resolver is not configured")
	}

	r.mu.RLock()
	cached, ok := r.cache[cfg.ID]
	r.mu.RUnlock()
	if ok && reflect.DeepEqual(r.configOf(cached), cfg) {
		return cached, nil
	}

	route, err := r.decode(cfg)
	if err != nil {
		return zero, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]T{}
	}
	r.cache[cfg.ID] = route
	return route, nil
}
