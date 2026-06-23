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
	ErrInvalidRouteID      = routecore.ErrInvalidRouteID
)

type MCPRouteResolver struct {
	configManager *routecore.AgentRouteConfigManager
	base          *runtimecore.Resolver[routecore.AgentRouteConfig, *MCPRoute, RouteListOptions]
}

func NewMCPRouteResolver(configManager *routecore.AgentRouteConfigManager) *MCPRouteResolver {
	return &MCPRouteResolver{
		configManager: configManager,
		base: runtimecore.NewResolver(
			runtimecore.FuncSource[routecore.AgentRouteConfig, RouteListOptions]{
				GetFunc: func(ctx context.Context, routeID string) (routecore.AgentRouteConfig, error) {
					if configManager == nil {
						return routecore.AgentRouteConfig{}, fmt.Errorf("route config manager is not configured")
					}
					return configManager.Get(ctx, routeID)
				},
				ListFunc: func(ctx context.Context, opts RouteListOptions) ([]routecore.AgentRouteConfig, error) {
					if configManager == nil {
						return nil, fmt.Errorf("route config manager is not configured")
					}
					return configManager.List(ctx, routecore.RouteListOptions(opts))
				},
			},
			func(cfg routecore.AgentRouteConfig) string {
				return cfg.ID
			},
			func(cfg routecore.AgentRouteConfig) (string, error) {
				return cfg.Fingerprint(), nil
			},
			func(cfg routecore.AgentRouteConfig) (*MCPRoute, error) {
				route, err := NewMCPRouteFromConfig(cfg)
				if err != nil {
					return nil, err
				}
				return &route, nil
			},
		),
	}
}

func (r *MCPRouteResolver) ConfigManager() *routecore.AgentRouteConfigManager {
	if r == nil {
		return nil
	}
	return r.configManager
}

func (r *MCPRouteResolver) GetConfig(ctx context.Context, routeID string) (routecore.AgentRouteConfig, error) {
	manager := r.ConfigManager()
	if manager == nil {
		return routecore.AgentRouteConfig{}, fmt.Errorf("route config manager is not configured")
	}
	return manager.Get(ctx, routeID)
}

func (r *MCPRouteResolver) ListConfigs(ctx context.Context, opts RouteListOptions) ([]routecore.AgentRouteConfig, error) {
	manager := r.ConfigManager()
	if manager == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	return manager.List(ctx, routecore.RouteListOptions(opts))
}

func (r *MCPRouteResolver) CreateConfig(ctx context.Context, route routecore.AgentRouteConfig, tag string) error {
	if route.ID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := routecore.ValidateRouteID(route.ID); err != nil {
		return err
	}
	manager := r.ConfigManager()
	if manager == nil {
		return fmt.Errorf("route config manager is not configured")
	}
	if err := manager.Create(ctx, route, tag); err != nil {
		return err
	}
	r.base.Invalidate(route.ID)
	return nil
}

func (r *MCPRouteResolver) UpdateConfig(ctx context.Context, routeID string, route routecore.AgentRouteConfig) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := routecore.ValidateRouteID(routeID); err != nil {
		return err
	}
	manager := r.ConfigManager()
	if manager == nil {
		return fmt.Errorf("route config manager is not configured")
	}
	if err := manager.Update(ctx, routeID, route); err != nil {
		return err
	}
	r.base.Invalidate(routeID)
	return nil
}

func (r *MCPRouteResolver) DeleteConfig(ctx context.Context, routeID string) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
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

func (r *MCPRouteResolver) Resolve(ctx context.Context, cfg routecore.AgentRouteConfig) (*MCPRoute, error) {
	if r == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	if cfg.ID == "" || cfg.Kind != routecore.RouteKindMCP {
		return nil, nil
	}
	route, err := r.base.ResolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, fmt.Errorf("route %q is nil", cfg.ID)
	}
	return route, nil
}

func (r *MCPRouteResolver) ResolveByID(ctx context.Context, routeID string) (*MCPRoute, error) {
	cfg, err := r.GetConfig(ctx, routeID)
	if err != nil {
		return nil, err
	}
	return r.Resolve(ctx, cfg)
}
