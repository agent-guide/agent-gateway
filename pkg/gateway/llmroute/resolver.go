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
	base *runtimecore.Resolver[*LLMRoute]
}

func NewLLMRouteResolver(configManager *routecore.AgentRouteConfigManager) *LLMRouteResolver {
	return &LLMRouteResolver{
		base: runtimecore.NewResolver(
			configManager,
			func(cfg routecore.AgentRouteConfig) (*LLMRoute, error) {
				route, err := NewLLMRouteFromConfig(cfg)
				if err != nil {
					return nil, err
				}
				route.Normalize()
				return &route, nil
			},
			func(route *LLMRoute) routecore.AgentRouteConfig {
				if route == nil {
					return routecore.AgentRouteConfig{}
				}
				return route.AgentRouteConfig
			},
		),
	}
}

func (r *LLMRouteResolver) ConfigManager() *routecore.AgentRouteConfigManager {
	if r == nil {
		return nil
	}
	return r.base.ConfigManager()
}

func (r *LLMRouteResolver) Get(ctx context.Context, routeID string) (*LLMRoute, error) {
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

func (r *LLMRouteResolver) List(ctx context.Context, opts routecore.RouteListOptions) ([]*LLMRoute, error) {
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

func (r *LLMRouteResolver) Validate(ctx context.Context, routeID string, resolver ProviderResolver) error {
	route, err := r.Get(ctx, routeID)
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
