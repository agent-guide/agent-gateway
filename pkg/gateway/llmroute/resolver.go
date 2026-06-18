package llmroute

import (
	"context"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/runtimecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type ProviderResolver interface {
	ResolveProvider(ctx context.Context, providerID string) (provider.Provider, string, error)
}

type LLMRouteResolver struct {
	configManager *routecore.AgentRouteConfigManager
	base          *runtimecore.Resolver[routecore.AgentRouteConfig, *LLMRoute, routecore.RouteListOptions]
}

func NewLLMRouteResolver(configManager *routecore.AgentRouteConfigManager) *LLMRouteResolver {
	return &LLMRouteResolver{
		configManager: configManager,
		base: runtimecore.NewResolver(
			runtimecore.FuncSource[routecore.AgentRouteConfig, routecore.RouteListOptions]{
				GetFunc: func(ctx context.Context, routeID string) (routecore.AgentRouteConfig, error) {
					if configManager == nil {
						return routecore.AgentRouteConfig{}, fmt.Errorf("route config manager is not configured")
					}
					return configManager.Get(ctx, routeID)
				},
				ListFunc: func(ctx context.Context, opts routecore.RouteListOptions) ([]routecore.AgentRouteConfig, error) {
					if configManager == nil {
						return nil, fmt.Errorf("route config manager is not configured")
					}
					return configManager.List(ctx, opts)
				},
			},
			func(cfg routecore.AgentRouteConfig) string {
				return cfg.ID
			},
			func(cfg routecore.AgentRouteConfig) (string, error) {
				return cfg.Fingerprint(), nil
			},
			func(cfg routecore.AgentRouteConfig) (*LLMRoute, error) {
				routeCfg, err := NewLLMRouteConfigFromConfig(cfg)
				if err != nil {
					return nil, err
				}
				route := NewRuntimeLLMRoute(routeCfg)
				route.Normalize()
				return &route, nil
			},
		),
	}
}

func (r *LLMRouteResolver) ConfigManager() *routecore.AgentRouteConfigManager {
	if r == nil {
		return nil
	}
	return r.configManager
}

func (r *LLMRouteResolver) GetConfig(ctx context.Context, routeID string) (routecore.AgentRouteConfig, error) {
	manager := r.ConfigManager()
	if manager == nil {
		return routecore.AgentRouteConfig{}, fmt.Errorf("route config manager is not configured")
	}
	return manager.Get(ctx, routeID)
}

func (r *LLMRouteResolver) ListConfigs(ctx context.Context, opts routecore.RouteListOptions) ([]routecore.AgentRouteConfig, error) {
	manager := r.ConfigManager()
	if manager == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	return manager.List(ctx, opts)
}

func (r *LLMRouteResolver) CreateConfig(ctx context.Context, route routecore.AgentRouteConfig, tag string) error {
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

func (r *LLMRouteResolver) UpdateConfig(ctx context.Context, routeID string, route routecore.AgentRouteConfig) error {
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

func (r *LLMRouteResolver) DeleteConfig(ctx context.Context, routeID string) error {
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

func (r *LLMRouteResolver) Resolve(ctx context.Context, cfg routecore.AgentRouteConfig) (*LLMRoute, error) {
	if r == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	if cfg.ID == "" || cfg.Kind != routecore.RouteKindLLM {
		return nil, nil
	}
	route, err := r.base.ResolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, fmt.Errorf("route %q is nil", cfg.ID)
	}
	if err := route.ValidateDefinition(); err != nil {
		return nil, err
	}
	if route.Disabled {
		return nil, fmt.Errorf("route %q is disabled", route.ID)
	}
	return route, nil
}

func (r *LLMRouteResolver) ResolveByID(ctx context.Context, routeID string) (*LLMRoute, error) {
	cfg, err := r.GetConfig(ctx, routeID)
	if err != nil {
		return nil, err
	}
	return r.Resolve(ctx, cfg)
}

func (r *LLMRouteResolver) Validate(ctx context.Context, routeID string, resolver ProviderResolver) error {
	route, err := r.ResolveByID(ctx, routeID)
	if err != nil {
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
