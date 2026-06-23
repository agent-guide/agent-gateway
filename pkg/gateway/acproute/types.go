package acproute

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

type RouteKind = routecore.RouteKind

const (
	RouteKindACP = routecore.RouteKindACP
)

type RouteProtocol = routecore.RouteProtocol

const (
	RouteProtocolACP = routecore.RouteProtocolACP
)

type AgentRouteConfig = routecore.AgentRouteConfig
type RouteMatch = routecore.RouteMatchPolicy
type RouteAuthPolicy = routecore.RouteAuthPolicy
type RouteListOptions = routecore.RouteListOptions

type ACPRouteState struct{}

type ACPRouteConfig struct {
	AgentRouteConfig
	ServiceID string `json:"service_id"`
}

type ACPRoute struct {
	AgentRouteConfig
	ServiceID string        `json:"service_id"`
	State     ACPRouteState `json:"-"`
}

type routeTargetPolicy struct {
	Kind      routecore.RouteTargetPolicyKind `json:"kind,omitempty"`
	ServiceID string                          `json:"service_id"`
}

func normalizeConfigDefaults(cfg AgentRouteConfig) AgentRouteConfig {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Kind = RouteKindACP
	cfg.Protocol = RouteProtocolACP
	cfg.Description = strings.TrimSpace(cfg.Description)
	cfg.MatchPolicy.Host = strings.TrimSpace(cfg.MatchPolicy.Host)
	cfg.MatchPolicy.PathPrefix = strings.TrimSpace(cfg.MatchPolicy.PathPrefix)
	for i := range cfg.MatchPolicy.Methods {
		cfg.MatchPolicy.Methods[i] = strings.TrimSpace(cfg.MatchPolicy.Methods[i])
	}
	return cfg
}

func (r *ACPRouteConfig) Normalize() {
	if r == nil {
		return
	}
	r.AgentRouteConfig = normalizeConfigDefaults(r.AgentRouteConfig)
	r.ServiceID = strings.TrimSpace(r.ServiceID)
	if r.ID == "" && r.ServiceID != "" {
		r.ID = routecore.GenerateRouteID("acp", r.ServiceID, r.MatchPolicy.PathPrefix)
	}
}

func (r *ACPRouteConfig) NormalizeTimestamps(now time.Time) {
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

func (r ACPRouteConfig) MarshalJSON() ([]byte, error) {
	type acpRouteJSON struct {
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
	return json.Marshal(acpRouteJSON{
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

func (r *ACPRoute) UnmarshalJSON(data []byte) error {
	var cfg ACPRouteConfig
	if err := cfg.UnmarshalJSON(data); err != nil {
		return err
	}
	*r = NewRuntimeACPRoute(cfg)
	return nil
}

func (r *ACPRouteConfig) UnmarshalJSON(data []byte) error {
	type acpRouteJSON struct {
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
	var raw acpRouteJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.AgentRouteConfig = normalizeConfigDefaults(AgentRouteConfig{
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

func DecodeStoredACPRoute(data []byte) (any, error) {
	var cfg AgentRouteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode acp route config: %w", err)
	}
	route, err := NewACPRouteConfigFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	route.Normalize()
	return &route, nil
}

func NewACPRouteConfigFromConfig(cfg AgentRouteConfig) (ACPRouteConfig, error) {
	cfg = normalizeConfigDefaults(cfg)
	serviceID, err := decodeServiceID(cfg.TargetPolicy)
	if err != nil {
		return ACPRouteConfig{}, fmt.Errorf("route %q decode target policy: %w", cfg.ID, err)
	}
	route := ACPRouteConfig{
		AgentRouteConfig: cfg,
		ServiceID:        serviceID,
	}
	route.Normalize()
	return route, nil
}

func NewACPRouteFromConfig(cfg AgentRouteConfig) (ACPRoute, error) {
	routeCfg, err := NewACPRouteConfigFromConfig(cfg)
	if err != nil {
		return ACPRoute{}, err
	}
	return NewRuntimeACPRoute(routeCfg), nil
}

func NewRuntimeACPRoute(cfg ACPRouteConfig) ACPRoute {
	return ACPRoute{
		AgentRouteConfig: cfg.AgentRouteConfig,
		ServiceID:        cfg.ServiceID,
		State:            ACPRouteState{},
	}
}

func (r ACPRouteConfig) ToConfig() (AgentRouteConfig, error) {
	r.Normalize()
	targetPolicy, err := json.Marshal(routeTargetPolicy{
		Kind:      routecore.RouteTargetPolicyKindACPService,
		ServiceID: r.ServiceID,
	})
	if err != nil {
		return AgentRouteConfig{}, fmt.Errorf("route %q encode target policy: %w", r.ID, err)
	}
	cfg := r.AgentRouteConfig
	cfg.ID = r.ID
	cfg.Kind = RouteKindACP
	cfg.Protocol = RouteProtocolACP
	cfg.Description = r.Description
	cfg.Disabled = r.Disabled
	cfg.AuthPolicy = r.AuthPolicy
	cfg.MatchPolicy = r.MatchPolicy
	cfg.TargetPolicy = targetPolicy
	cfg.CreatedAt = r.CreatedAt
	cfg.UpdatedAt = r.UpdatedAt
	return cfg, nil
}

func (r ACPRoute) Config() ACPRouteConfig {
	return ACPRouteConfig{
		AgentRouteConfig: r.AgentRouteConfig,
		ServiceID:        r.ServiceID,
	}
}

func (r *ACPRoute) Normalize() {
	if r == nil {
		return
	}
	cfg := r.Config()
	cfg.Normalize()
	r.AgentRouteConfig = cfg.AgentRouteConfig
	r.ServiceID = cfg.ServiceID
}

func (r *ACPRoute) NormalizeTimestamps(now time.Time) {
	if r == nil {
		return
	}
	cfg := r.Config()
	cfg.NormalizeTimestamps(now)
	r.AgentRouteConfig = cfg.AgentRouteConfig
}

func (r ACPRoute) MarshalJSON() ([]byte, error) {
	return r.Config().MarshalJSON()
}

func (r ACPRoute) ToConfig() (AgentRouteConfig, error) {
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
