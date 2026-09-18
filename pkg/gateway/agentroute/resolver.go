package agentroute

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/runtimecore"
)

var (
	ErrRouteNotConfigured  = routecore.ErrRouteNotConfigured
	ErrStaticRouteReadOnly = routecore.ErrStaticRouteReadOnly
	ErrInvalidRouteID      = routecore.ErrInvalidRouteID
)

// AgentLookup reports whether a target Agent exists. Existence is a management
// validity check only: disabled Agents and Agents whose backend is not
// currently executable remain valid route targets (plan §6.2).
type AgentLookup interface {
	HasAgent(id string) bool
}

// A2AProxyLookup validates the shared HTTP runtime snapshot without exposing
// gateway implementation types to the route-model package.
type A2AProxyLookup interface {
	A2AProxyReady(agentID string) error
}

type AgentRouteResolver struct {
	configManager *routecore.AgentRouteConfigManager
	base          *runtimecore.Resolver[routecore.AgentRouteConfig, *AgentRoute, RouteListOptions]

	mu          sync.RWMutex
	agentLookup AgentLookup
	proxyLookup A2AProxyLookup
}

func (r *AgentRouteResolver) SetA2AProxyLookup(lookup A2AProxyLookup) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.proxyLookup = lookup
	r.mu.Unlock()
}

func NewAgentRouteResolver(configManager *routecore.AgentRouteConfigManager) *AgentRouteResolver {
	return &AgentRouteResolver{
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
			func(cfg routecore.AgentRouteConfig) (*AgentRoute, error) {
				route, err := NewAgentRouteFromConfig(cfg)
				if err != nil {
					return nil, err
				}
				return &route, nil
			},
		),
	}
}

// SetAgentLookup wires the optional target-existence check used by
// CreateConfig/UpdateConfig. When nil, target validation is skipped.
func (r *AgentRouteResolver) SetAgentLookup(lookup AgentLookup) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.agentLookup = lookup
	r.mu.Unlock()
}

func (r *AgentRouteResolver) lookups() (AgentLookup, A2AProxyLookup) {
	if r == nil {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agentLookup, r.proxyLookup
}

// validateTarget enforces that a persisted AgentRoute names an existing Agent.
// Protocol-agent routes may target disabled or currently non-executable Agents
// and report their normalized runtime error at dispatch. Protocol-a2a routes
// instead require a ready Path A proxy target at create/update time.
func (r *AgentRouteResolver) validateTarget(route routecore.AgentRouteConfig) error {
	if route.Kind != routecore.RouteKindAgent {
		return fmt.Errorf("route %q kind must be %q", route.ID, routecore.RouteKindAgent)
	}
	if route.Protocol != routecore.RouteProtocolAgent && route.Protocol != routecore.RouteProtocolA2A {
		return fmt.Errorf("route %q protocol must be %q or %q", route.ID, routecore.RouteProtocolAgent, routecore.RouteProtocolA2A)
	}
	agentID, err := DecodeTargetAgentID(route.TargetPolicy)
	if err != nil {
		return fmt.Errorf("route %q decode target policy: %w", route.ID, err)
	}
	if agentID == "" {
		return fmt.Errorf("route %q requires target_policy.agent_id", route.ID)
	}
	agentLookup, proxyLookup := r.lookups()
	if agentLookup != nil && !agentLookup.HasAgent(agentID) {
		return fmt.Errorf("route %q targets unknown agent %q", route.ID, agentID)
	}
	if route.Protocol == routecore.RouteProtocolA2A {
		if strings.TrimSpace(route.MatchPolicy.Host) == "" {
			return fmt.Errorf("route %q protocol %q requires match_policy.host", route.ID, route.Protocol)
		}
		if len(route.MatchPolicy.Methods) > 0 && (!containsMethod(route.MatchPolicy.Methods, http.MethodGet) || !containsMethod(route.MatchPolicy.Methods, http.MethodPost)) {
			return fmt.Errorf("route %q protocol %q methods must be empty or contain both GET and POST", route.ID, route.Protocol)
		}
		if proxyLookup == nil {
			return fmt.Errorf("route %q A2A proxy runtime lookup is not configured", route.ID)
		}
		if err := proxyLookup.A2AProxyReady(agentID); err != nil {
			return fmt.Errorf("route %q target agent %q is not A2A proxy ready: %w", route.ID, agentID, err)
		}
	}
	return nil
}

func containsMethod(methods []string, method string) bool {
	return slices.ContainsFunc(methods, func(candidate string) bool { return strings.EqualFold(strings.TrimSpace(candidate), method) })
}

func (r *AgentRouteResolver) ConfigManager() *routecore.AgentRouteConfigManager {
	if r == nil {
		return nil
	}
	return r.configManager
}

func (r *AgentRouteResolver) GetConfig(ctx context.Context, routeID string) (routecore.AgentRouteConfig, error) {
	manager := r.ConfigManager()
	if manager == nil {
		return routecore.AgentRouteConfig{}, fmt.Errorf("route config manager is not configured")
	}
	return manager.Get(ctx, routeID)
}

func (r *AgentRouteResolver) ListConfigs(ctx context.Context, opts RouteListOptions) ([]routecore.AgentRouteConfig, error) {
	manager := r.ConfigManager()
	if manager == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	return manager.List(ctx, routecore.RouteListOptions(opts))
}

func (r *AgentRouteResolver) CreateConfig(ctx context.Context, route routecore.AgentRouteConfig, tag string) error {
	if route.ID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := routecore.ValidateRouteID(route.ID); err != nil {
		return err
	}
	if err := r.validateTarget(route); err != nil {
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

func (r *AgentRouteResolver) UpdateConfig(ctx context.Context, routeID string, route routecore.AgentRouteConfig) error {
	if routeID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := routecore.ValidateRouteID(routeID); err != nil {
		return err
	}
	if err := r.validateTarget(route); err != nil {
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

func (r *AgentRouteResolver) DeleteConfig(ctx context.Context, routeID string) error {
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

func (r *AgentRouteResolver) Resolve(ctx context.Context, cfg routecore.AgentRouteConfig) (*AgentRoute, error) {
	if r == nil {
		return nil, fmt.Errorf("route config manager is not configured")
	}
	if cfg.ID == "" || cfg.Kind != routecore.RouteKindAgent {
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

func (r *AgentRouteResolver) ResolveByID(ctx context.Context, routeID string) (*AgentRoute, error) {
	cfg, err := r.GetConfig(ctx, routeID)
	if err != nil {
		return nil, err
	}
	return r.Resolve(ctx, cfg)
}
