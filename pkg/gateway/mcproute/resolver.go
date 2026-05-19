package mcproute

import (
	"context"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/runtimecore"
)

var (
	ErrRouteNotConfigured  = routecore.ErrRouteNotConfigured
	ErrStaticRouteReadOnly = routecore.ErrStaticRouteReadOnly
)

type MCPRouteResolver struct {
	base *runtimecore.Resolver[*MCPRoute]
}

func NewMCPRouteResolver(configManager *routecore.AgentRouteConfigManager) *MCPRouteResolver {
	return &MCPRouteResolver{
		base: runtimecore.NewResolver(
			configManager,
			func(cfg routecore.AgentRouteConfig) (*MCPRoute, error) {
				route, err := NewMCPRouteFromConfig(cfg)
				if err != nil {
					return nil, err
				}
				return &route, nil
			},
			func(route *MCPRoute) routecore.AgentRouteConfig {
				if route == nil {
					return routecore.AgentRouteConfig{}
				}
				return route.AgentRouteConfig
			},
		),
	}
}

func (r *MCPRouteResolver) ConfigManager() *routecore.AgentRouteConfigManager {
	if r == nil {
		return nil
	}
	return r.base.ConfigManager()
}

func (r *MCPRouteResolver) Get(ctx context.Context, routeID string) (*MCPRoute, error) {
	if r == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	route, err := r.base.Get(ctx, routeID)
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, fmt.Errorf("route %q is nil", routeID)
	}
	return route, nil
}

func (r *MCPRouteResolver) List(ctx context.Context, opts RouteListOptions) ([]*MCPRoute, error) {
	if r == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	routes, err := r.base.List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, route := range routes {
		if route == nil {
			return nil, fmt.Errorf("route list contains nil route")
		}
	}
	return routes, nil
}

func (r *MCPRouteResolver) Create(ctx context.Context, route *MCPRoute, tag string) error {
	if route == nil {
		return fmt.Errorf("route is required")
	}
	if route.ID == "" && route.ServiceID == "" {
		return fmt.Errorf("route id or service_id is required")
	}
	manager := r.ConfigManager()
	if manager == nil {
		return fmt.Errorf("route config manager is not configured")
	}
	cfg, err := route.ToConfig()
	if err != nil {
		return err
	}
	if err := manager.Create(ctx, cfg, tag); err != nil {
		return err
	}
	r.base.Invalidate(cfg.ID)
	return nil
}

func (r *MCPRouteResolver) Update(ctx context.Context, routeID string, route *MCPRoute) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if route == nil {
		return fmt.Errorf("route is required")
	}
	manager := r.ConfigManager()
	if manager == nil {
		return fmt.Errorf("route config manager is not configured")
	}
	route.ID = routeID
	cfg, err := route.ToConfig()
	if err != nil {
		return err
	}
	if err := manager.Update(ctx, routeID, cfg); err != nil {
		return err
	}
	r.base.Invalidate(routeID)
	return nil
}

func (r *MCPRouteResolver) Delete(ctx context.Context, routeID string) error {
	manager := r.ConfigManager()
	if manager == nil {
		return fmt.Errorf("route config manager is not configured")
	}
	if err := manager.Delete(ctx, routeID); err != nil {
		return err
	}
	r.base.Invalidate(routeID)
	return nil
}
