package route

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type RouteTargetPolicyKind string

const (
	RouteTargetPolicyKindDirectProvider RouteTargetPolicyKind = "direct-provider"
	RouteTargetPolicyKindLogicalModel   RouteTargetPolicyKind = "logical-model"
)

// RouteSelectionStrategy controls how a route prefers model candidates.
type RouteSelectionStrategy string

const (
	RouteSelectionStrategyAuto     RouteSelectionStrategy = "auto"
	RouteSelectionStrategyWeighted RouteSelectionStrategy = "weighted"
	RouteSelectionStrategyPriority RouteSelectionStrategy = "priority"
)

type RouteCredentialSelectStrategy string

const (
	RouteCredentialSelectRoundRobin RouteCredentialSelectStrategy = "round_robin"
	RouteCredentialSelectFillFirst  RouteCredentialSelectStrategy = "fill_first"
)

type RouteCredentialScope string

const (
	RouteCredentialScopeModelCustom RouteCredentialScope = "model_custom"
	RouteCredentialScopeProviderID  RouteCredentialScope = "provider_id"
)

type RouteCredentialSource string

const (
	RouteCredentialSourceAPIKey       RouteCredentialSource = "api_key"
	RouteCredentialSourceCLIAuthToken RouteCredentialSource = "cliauth_token"
)

type RouteTargetPolicy interface {
	Normalize()
	PolicyKind() RouteTargetPolicyKind
	ValidateDefinition(routeID string) error
	ResolveTarget(ctx context.Context, routeID string, catalog ModelCatalogResolver, providers ProviderConfigResolver, req RequestRequirements) (*ResolvedTarget, error)
	ProviderIDs() []string
	CredentialSelector() RouteCredentialSelectStrategy
	CredentialScopeOrder() []RouteCredentialScope
	CredentialSourceOrder() []RouteCredentialSource
	FallbackPolicy() RouteFallbackPolicy
}

type RouteTargetPolicyCommon struct {
	CredentialSelectorValue    RouteCredentialSelectStrategy `json:"credential_selector,omitempty"`
	CredentialScopeOrderValue  []RouteCredentialScope        `json:"credential_scope_order,omitempty"`
	CredentialSourceOrderValue []RouteCredentialSource       `json:"credential_source_order,omitempty"`
}

type RouteLogicalModelTargetPolicy struct {
	RouteTargetPolicyCommon
	DefaultModel          string                 `json:"default_model,omitempty"`
	ModelSelectorStrategy RouteSelectionStrategy `json:"model_selector_strategy,omitempty"`
	Fallback              RouteFallbackPolicy    `json:"fallback,omitempty"`
	ModelTargets          []RouteModelTarget     `json:"model_targets,omitempty"`
}

type RouteDirectProviderPolicy struct {
	RouteTargetPolicyCommon
	ProviderID     string               `json:"provider_id,omitempty"`
	ProviderTarget DirectProviderTarget `json:"provider_target,omitempty"`
}

// DecodeStoredRoute decodes a persisted route and fills missing runtime defaults.
func DecodeStoredRoute(data []byte) (any, error) {
	var r AgentRoute
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode route: %w", err)
	}
	r.Normalize()
	r.NormalizeTimestamps(time.Now().UTC())
	return &r, nil
}

// AgentRoute is the primary gateway route configuration exposed to agent clients.
type AgentRoute struct {
	ID           string            `json:"id"`
	Description  string            `json:"description,omitempty"`
	Disabled     bool              `json:"disabled"`
	LLMAPI       string            `json:"llm_api"`
	Match        RouteMatch        `json:"match"`
	TargetPolicy RouteTargetPolicy `json:"target_policy"`
	AuthPolicy   RouteAuthPolicy   `json:"auth_policy"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type RouteModelTarget struct {
	Name             string                 `json:"name"`
	Strategy         RouteSelectionStrategy `json:"strategy,omitempty"`
	DefaultCandidate string                 `json:"default_candidate,omitempty"`
	Candidates       []RouteModelCandidate  `json:"candidates,omitempty"`
}

type RouteModelCandidate struct {
	ProviderID    string `json:"provider_id"`
	UpstreamModel string `json:"upstream_model"`
	Weight        int    `json:"weight,omitempty"`
	Priority      int    `json:"priority,omitempty"`
	Default       bool   `json:"default,omitempty"`
}

type DirectProviderTarget struct {
	ProviderID string `json:"provider_id"`
}

type RouteFallbackPolicy struct {
	Enabled bool `json:"enabled,omitempty"`
	MaxNum  int  `json:"max_num,omitempty"`
}

// RouteMatch contains transport-facing match fields for binding requests to a route.
type RouteMatch struct {
	Host       string   `json:"host,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Methods    []string `json:"methods,omitempty"`
}

type RouteAuthPolicy struct {
	RequireVirtualKey bool `json:"require_virtual_key"`
}

func (r *AgentRoute) NormalizeTimestamps(now time.Time) {
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

func (r *AgentRoute) UnmarshalJSON(data []byte) error {
	type agentRouteJSON struct {
		ID           string          `json:"id"`
		Description  string          `json:"description,omitempty"`
		Disabled     bool            `json:"disabled"`
		LLMAPI       string          `json:"llm_api"`
		Match        RouteMatch      `json:"match"`
		TargetPolicy json.RawMessage `json:"target_policy"`
		AuthPolicy   RouteAuthPolicy `json:"auth_policy"`
		CreatedAt    time.Time       `json:"created_at"`
		UpdatedAt    time.Time       `json:"updated_at"`
	}
	var raw agentRouteJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	policy, err := unmarshalRouteTargetPolicy(raw.TargetPolicy)
	if err != nil {
		return err
	}
	r.ID = raw.ID
	r.Description = raw.Description
	r.Disabled = raw.Disabled
	r.LLMAPI = raw.LLMAPI
	r.Match = raw.Match
	r.TargetPolicy = policy
	r.AuthPolicy = raw.AuthPolicy
	r.CreatedAt = raw.CreatedAt
	r.UpdatedAt = raw.UpdatedAt
	return nil
}

func (p RouteLogicalModelTargetPolicy) MarshalJSON() ([]byte, error) {
	p.Normalize()
	type logicalJSON struct {
		Type                  RouteTargetPolicyKind         `json:"type,omitempty"`
		DefaultModel          string                        `json:"default_model,omitempty"`
		ModelSelectorStrategy RouteSelectionStrategy        `json:"model_selector_strategy,omitempty"`
		CredentialSelector    RouteCredentialSelectStrategy `json:"credential_selector,omitempty"`
		CredentialScopeOrder  []RouteCredentialScope        `json:"credential_scope_order,omitempty"`
		CredentialSourceOrder []RouteCredentialSource       `json:"credential_source_order,omitempty"`
		Fallback              RouteFallbackPolicy           `json:"fallback,omitempty"`
		ModelTargets          []RouteModelTarget            `json:"model_targets,omitempty"`
	}
	return json.Marshal(logicalJSON{
		Type:                  p.PolicyKind(),
		DefaultModel:          p.DefaultModel,
		ModelSelectorStrategy: p.ModelSelectorStrategy,
		CredentialSelector:    p.CredentialSelector(),
		CredentialScopeOrder:  p.CredentialScopeOrder(),
		CredentialSourceOrder: p.CredentialSourceOrder(),
		Fallback:              p.Fallback,
		ModelTargets:          p.ModelTargets,
	})
}

func (p RouteDirectProviderPolicy) MarshalJSON() ([]byte, error) {
	p.Normalize()
	type directJSON struct {
		Type                  RouteTargetPolicyKind         `json:"type,omitempty"`
		ProviderID            string                        `json:"provider_id,omitempty"`
		CredentialSelector    RouteCredentialSelectStrategy `json:"credential_selector,omitempty"`
		CredentialScopeOrder  []RouteCredentialScope        `json:"credential_scope_order,omitempty"`
		CredentialSourceOrder []RouteCredentialSource       `json:"credential_source_order,omitempty"`
		ProviderTarget        DirectProviderTarget          `json:"provider_target,omitempty"`
	}
	return json.Marshal(directJSON{
		Type:                  p.PolicyKind(),
		ProviderID:            p.ProviderID,
		CredentialSelector:    p.CredentialSelector(),
		CredentialScopeOrder:  p.CredentialScopeOrder(),
		CredentialSourceOrder: p.CredentialSourceOrder(),
		ProviderTarget:        p.ProviderTarget,
	})
}

func unmarshalRouteTargetPolicy(data []byte) (RouteTargetPolicy, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var probe struct {
		Type           RouteTargetPolicyKind `json:"type,omitempty"`
		ProviderID     string                `json:"provider_id,omitempty"`
		ProviderTarget DirectProviderTarget  `json:"provider_target,omitempty"`
		ModelTargets   []RouteModelTarget    `json:"model_targets,omitempty"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	kind := probe.Type
	if kind == "" {
		switch {
		case strings.TrimSpace(probe.ProviderID) != "" || strings.TrimSpace(probe.ProviderTarget.ProviderID) != "":
			kind = RouteTargetPolicyKindDirectProvider
		case len(probe.ModelTargets) > 0:
			kind = RouteTargetPolicyKindLogicalModel
		}
	}
	switch kind {
	case RouteTargetPolicyKindDirectProvider:
		var policy RouteDirectProviderPolicy
		if err := json.Unmarshal(data, &policy); err != nil {
			return nil, err
		}
		policy.Normalize()
		return &policy, nil
	case RouteTargetPolicyKindLogicalModel:
		var policy RouteLogicalModelTargetPolicy
		if err := json.Unmarshal(data, &policy); err != nil {
			return nil, err
		}
		policy.Normalize()
		return &policy, nil
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown route target policy kind %q", kind)
	}
}

// Normalize fills runtime defaults on a route value before it is used by the gateway.
func (r *AgentRoute) Normalize() {
	if r.TargetPolicy != nil {
		r.TargetPolicy.Normalize()
	}
}

func (r AgentRoute) UsesLogicalModel() bool {
	return r.TargetPolicy != nil && r.TargetPolicy.PolicyKind() == RouteTargetPolicyKindLogicalModel
}

func (c *RouteTargetPolicyCommon) Normalize(defaultScopes []RouteCredentialScope) {
	if c == nil {
		return
	}
	if len(c.CredentialScopeOrderValue) == 0 {
		c.CredentialScopeOrderValue = append([]RouteCredentialScope(nil), defaultScopes...)
	}
	if c.CredentialSelectorValue == "" {
		c.CredentialSelectorValue = RouteCredentialSelectRoundRobin
	}
	if len(c.CredentialSourceOrderValue) == 0 {
		c.CredentialSourceOrderValue = []RouteCredentialSource{RouteCredentialSourceAPIKey, RouteCredentialSourceCLIAuthToken}
	}
}

func (c RouteTargetPolicyCommon) CredentialSelector() RouteCredentialSelectStrategy {
	return c.CredentialSelectorValue
}

func (c RouteTargetPolicyCommon) CredentialScopeOrder() []RouteCredentialScope {
	return c.CredentialScopeOrderValue
}

func (c RouteTargetPolicyCommon) CredentialSourceOrder() []RouteCredentialSource {
	return c.CredentialSourceOrderValue
}

func (p *RouteDirectProviderPolicy) Normalize() {
	if p == nil {
		return
	}
	p.ProviderID = strings.TrimSpace(p.ProviderID)
	p.ProviderTarget.ProviderID = strings.TrimSpace(p.ProviderTarget.ProviderID)
	if p.ProviderID == "" && p.ProviderTarget.ProviderID != "" {
		p.ProviderID = p.ProviderTarget.ProviderID
	}
	if p.ProviderTarget.ProviderID == "" && p.ProviderID != "" {
		p.ProviderTarget.ProviderID = p.ProviderID
	}
	p.RouteTargetPolicyCommon.Normalize([]RouteCredentialScope{RouteCredentialScopeProviderID})
}

func (p *RouteLogicalModelTargetPolicy) Normalize() {
	if p == nil {
		return
	}
	p.DefaultModel = strings.TrimSpace(p.DefaultModel)
	for i := range p.ModelTargets {
		p.ModelTargets[i].Normalize()
		hasDefaultCandidate := false
		for j := range p.ModelTargets[i].Candidates {
			p.ModelTargets[i].Candidates[j].ProviderID = strings.TrimSpace(p.ModelTargets[i].Candidates[j].ProviderID)
			p.ModelTargets[i].Candidates[j].UpstreamModel = strings.TrimSpace(p.ModelTargets[i].Candidates[j].UpstreamModel)
			hasDefaultCandidate = hasDefaultCandidate || p.ModelTargets[i].Candidates[j].Default
		}
		if hasDefaultCandidate && p.DefaultModel == "" {
			p.DefaultModel = p.ModelTargets[i].Name
		}
	}
	if p.ModelSelectorStrategy == "" {
		p.ModelSelectorStrategy = RouteSelectionStrategyAuto
	}
	if !p.Fallback.Enabled && p.Fallback.MaxNum == 0 {
		p.Fallback.Enabled = true
		p.Fallback.MaxNum = 1
	}
	p.RouteTargetPolicyCommon.Normalize([]RouteCredentialScope{RouteCredentialScopeModelCustom, RouteCredentialScopeProviderID})
}

func (p *RouteDirectProviderPolicy) PolicyKind() RouteTargetPolicyKind {
	return RouteTargetPolicyKindDirectProvider
}

func (p *RouteLogicalModelTargetPolicy) PolicyKind() RouteTargetPolicyKind {
	return RouteTargetPolicyKindLogicalModel
}

func (p *RouteDirectProviderPolicy) FallbackPolicy() RouteFallbackPolicy {
	return RouteFallbackPolicy{}
}

func (p *RouteLogicalModelTargetPolicy) FallbackPolicy() RouteFallbackPolicy {
	return p.Fallback
}

func (p *RouteDirectProviderPolicy) ProviderIDs() []string {
	p.Normalize()
	if p.ProviderID == "" {
		return nil
	}
	return []string{p.ProviderID}
}

func (p *RouteLogicalModelTargetPolicy) ProviderIDs() []string {
	p.Normalize()
	ids := map[string]struct{}{}
	for _, target := range p.ModelTargets {
		for _, candidate := range target.Candidates {
			if candidate.ProviderID == "" {
				continue
			}
			ids[candidate.ProviderID] = struct{}{}
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	return out
}

func DirectProviderPolicyOf(policy RouteTargetPolicy) (*RouteDirectProviderPolicy, bool) {
	p, ok := policy.(*RouteDirectProviderPolicy)
	return p, ok
}

func LogicalModelTargetPolicyOf(policy RouteTargetPolicy) (*RouteLogicalModelTargetPolicy, bool) {
	p, ok := policy.(*RouteLogicalModelTargetPolicy)
	return p, ok
}

func (t *RouteModelTarget) Normalize() {
	if t == nil {
		return
	}
	if t.Strategy == "" {
		t.Strategy = RouteSelectionStrategyAuto
	}
}
