package mcproute

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

type RouteKind = routecore.RouteKind

const (
	RouteKindMCP = routecore.RouteKindMCP
)

type RouteProtocol = routecore.RouteProtocol

const (
	RouteProtocolMCP = routecore.RouteProtocolMCP
)

type RouteConfig = routecore.RouteConfig
type RouteMatch = routecore.RouteMatchPolicy
type RouteAuthPolicy = routecore.RouteAuthPolicy
type RouteListOptions = routecore.RouteListOptions

// MCPRouteState holds runtime-only state for a resolved MCP route.
type MCPRouteState struct{}

// MCPRouteConfig is the expanded configuration form of an MCP route.
type MCPRouteConfig struct {
	RouteConfig
	ServiceID string `json:"service_id"`
}

type MCPRoute struct {
	RouteConfig
	ServiceID string        `json:"service_id"`
	State     MCPRouteState `json:"-"`
}

type routeTargetPolicy struct {
	Kind      routecore.RouteTargetPolicyKind `json:"kind,omitempty"`
	ServiceID string                          `json:"service_id"`
}

func normalizeConfigDefaults(cfg RouteConfig) RouteConfig {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Kind = RouteKindMCP
	cfg.Protocol = RouteProtocolMCP
	cfg.Description = strings.TrimSpace(cfg.Description)
	cfg.MatchPolicy.Host = strings.TrimSpace(cfg.MatchPolicy.Host)
	cfg.MatchPolicy.PathPrefix = strings.TrimSpace(cfg.MatchPolicy.PathPrefix)
	for i := range cfg.MatchPolicy.Methods {
		cfg.MatchPolicy.Methods[i] = strings.TrimSpace(cfg.MatchPolicy.Methods[i])
	}
	return cfg
}

func (r *MCPRouteConfig) Normalize() {
	if r == nil {
		return
	}
	r.RouteConfig = normalizeConfigDefaults(r.RouteConfig)
	r.ServiceID = strings.TrimSpace(r.ServiceID)
	if r.ID == "" && r.ServiceID != "" {
		r.ID = routecore.GenerateRouteID("mcp", r.ServiceID, r.MatchPolicy.PathPrefix)
	}
}

func (r *MCPRouteConfig) NormalizeTimestamps(now time.Time) {
	if r == nil {
		return
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = now
	}
}

func (r MCPRouteConfig) MarshalJSON() ([]byte, error) {
	type mcpRouteJSON struct {
		ID          string          `json:"id"`
		Kind        RouteKind       `json:"kind,omitempty"`
		Protocol    RouteProtocol   `json:"protocol,omitempty"`
		Description string          `json:"description,omitempty"`
		Disabled    bool            `json:"disabled"`
		MatchPolicy RouteMatch      `json:"match_policy"`
		AuthPolicy  RouteAuthPolicy `json:"auth_policy"`
		ServiceID   string          `json:"service_id"`
		CreatedAt   time.Time       `json:"created_at"`
		UpdatedAt   time.Time       `json:"updated_at"`
	}
	r.Normalize()
	cfg, err := r.ToConfig()
	if err != nil {
		return nil, err
	}
	return json.Marshal(mcpRouteJSON{
		ID:          cfg.ID,
		Kind:        cfg.Kind,
		Protocol:    cfg.Protocol,
		Description: cfg.Description,
		Disabled:    cfg.Disabled,
		MatchPolicy: cfg.MatchPolicy,
		AuthPolicy:  cfg.AuthPolicy,
		ServiceID:   r.ServiceID,
		CreatedAt:   cfg.CreatedAt,
		UpdatedAt:   cfg.UpdatedAt,
	})
}

func (r *MCPRoute) UnmarshalJSON(data []byte) error {
	var cfg MCPRouteConfig
	if err := cfg.UnmarshalJSON(data); err != nil {
		return err
	}
	*r = NewRuntimeMCPRoute(cfg)
	return nil
}

func (r *MCPRouteConfig) UnmarshalJSON(data []byte) error {
	type mcpRouteJSON struct {
		ID          string          `json:"id"`
		Kind        RouteKind       `json:"kind,omitempty"`
		Protocol    RouteProtocol   `json:"protocol,omitempty"`
		Description string          `json:"description,omitempty"`
		Disabled    bool            `json:"disabled"`
		MatchPolicy RouteMatch      `json:"match_policy"`
		AuthPolicy  RouteAuthPolicy `json:"auth_policy"`
		ServiceID   string          `json:"service_id"`
		CreatedAt   time.Time       `json:"created_at"`
		UpdatedAt   time.Time       `json:"updated_at"`
	}
	var raw mcpRouteJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.RouteConfig = normalizeConfigDefaults(RouteConfig{
		ID:          raw.ID,
		Kind:        raw.Kind,
		Protocol:    raw.Protocol,
		Description: raw.Description,
		Disabled:    raw.Disabled,
		AuthPolicy:  raw.AuthPolicy,
		MatchPolicy: raw.MatchPolicy,
		CreatedAt:   raw.CreatedAt,
		UpdatedAt:   raw.UpdatedAt,
	})
	r.ServiceID = raw.ServiceID
	r.Normalize()
	return nil
}

func DecodeStoredMCPRoute(data []byte) (any, error) {
	var cfg RouteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode mcp route config: %w", err)
	}
	route, err := NewMCPRouteConfigFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	route.Normalize()
	return &route, nil
}

func NewMCPRouteConfigFromConfig(cfg RouteConfig) (MCPRouteConfig, error) {
	cfg = normalizeConfigDefaults(cfg)
	serviceID, err := decodeServiceID(cfg.TargetPolicy)
	if err != nil {
		return MCPRouteConfig{}, fmt.Errorf("route %q decode target policy: %w", cfg.ID, err)
	}
	route := MCPRouteConfig{
		RouteConfig: cfg,
		ServiceID:   serviceID,
	}
	route.Normalize()
	return route, nil
}

func NewMCPRouteFromConfig(cfg RouteConfig) (MCPRoute, error) {
	routeCfg, err := NewMCPRouteConfigFromConfig(cfg)
	if err != nil {
		return MCPRoute{}, err
	}
	return NewRuntimeMCPRoute(routeCfg), nil
}

func NewRuntimeMCPRoute(cfg MCPRouteConfig) MCPRoute {
	return MCPRoute{
		RouteConfig: cfg.RouteConfig,
		ServiceID:   cfg.ServiceID,
		State:       MCPRouteState{},
	}
}

func (r MCPRouteConfig) ToConfig() (RouteConfig, error) {
	r.Normalize()
	targetPolicy, err := json.Marshal(routeTargetPolicy{
		Kind:      routecore.RouteTargetPolicyKindMCPService,
		ServiceID: r.ServiceID,
	})
	if err != nil {
		return RouteConfig{}, fmt.Errorf("route %q encode target policy: %w", r.ID, err)
	}
	cfg := r.RouteConfig
	cfg.ID = r.ID
	cfg.Kind = RouteKindMCP
	cfg.Protocol = RouteProtocolMCP
	cfg.Description = r.Description
	cfg.Disabled = r.Disabled
	cfg.AuthPolicy = r.AuthPolicy
	cfg.MatchPolicy = r.MatchPolicy
	cfg.TargetPolicy = targetPolicy
	cfg.CreatedAt = r.CreatedAt
	cfg.UpdatedAt = r.UpdatedAt
	return cfg, nil
}

func (r MCPRoute) Config() MCPRouteConfig {
	return MCPRouteConfig{
		RouteConfig: r.RouteConfig,
		ServiceID:   r.ServiceID,
	}
}

func (r *MCPRoute) Normalize() {
	if r == nil {
		return
	}
	cfg := r.Config()
	cfg.Normalize()
	r.RouteConfig = cfg.RouteConfig
	r.ServiceID = cfg.ServiceID
}

func (r *MCPRoute) NormalizeTimestamps(now time.Time) {
	if r == nil {
		return
	}
	cfg := r.Config()
	cfg.NormalizeTimestamps(now)
	r.RouteConfig = cfg.RouteConfig
}

func (r MCPRoute) MarshalJSON() ([]byte, error) {
	return r.Config().MarshalJSON()
}

func (r MCPRoute) ToConfig() (RouteConfig, error) {
	return r.Config().ToConfig()
}

func decodeServiceID(targetPolicy json.RawMessage) (string, error) {
	var target routeTargetPolicy
	if len(targetPolicy) == 0 {
		return "", nil
	}
	if err := json.Unmarshal(targetPolicy, &target); err != nil {
		return "", err
	}
	return strings.TrimSpace(target.ServiceID), nil
}
