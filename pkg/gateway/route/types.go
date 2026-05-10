package route

import (
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

// DecodeStoredRoute decodes a persisted route and fills missing runtime defaults.
func DecodeStoredRoute(data []byte) (any, error) {
	var r AgentRoute
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode route: %w", err)
	}
	r.Normalize()
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = now
	}
	return &r, nil
}

// AgentRoute is the primary gateway route configuration exposed to agent clients.
type AgentRoute struct {
	ID              string               `json:"id"`
	Description     string               `json:"description,omitempty"`
	Disabled        bool                 `json:"disabled"`
	LLMAPI          string               `json:"llm_api,omitempty"`
	Match           RouteMatch           `json:"match"`
	TargetPolicy    RouteTargetPolicy    `json:"target_policy,omitempty"`
	AuthPolicy      RouteAuthPolicy      `json:"auth_policy"`
	RateLimitPolicy RouteRateLimitPolicy `json:"rate_limit_policy,omitempty"`
	QuotaPolicy     RouteQuotaPolicy     `json:"quota_policy,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

type RouteTargetPolicy struct {
	Type                  RouteTargetPolicyKind         `json:"type,omitempty"`
	ProviderID            string                        `json:"provider_id,omitempty"`
	DefaultModel          string                        `json:"default_model,omitempty"`
	ModelSelectorStrategy RouteSelectionStrategy        `json:"model_selector_strategy,omitempty"`
	CredentialSelector    RouteCredentialSelectStrategy `json:"credential_selector,omitempty"`
	CredentialScopeOrder  []RouteCredentialScope        `json:"credential_scope_order,omitempty"`
	CredentialSourceOrder []RouteCredentialSource       `json:"credential_source_order,omitempty"`
	Fallback              RouteFallbackPolicy           `json:"fallback,omitempty"`
	ModelTargets          []RouteModelTarget            `json:"model_targets,omitempty"`
	ProviderTarget        DirectProviderTarget          `json:"provider_target,omitempty"`
}

func (p RouteTargetPolicy) MarshalJSON() ([]byte, error) {
	p.Normalize()
	type routeTargetPolicyJSON struct {
		Type                  RouteTargetPolicyKind         `json:"type,omitempty"`
		ProviderID            string                        `json:"provider_id,omitempty"`
		DefaultModel          string                        `json:"default_model,omitempty"`
		ModelSelectorStrategy RouteSelectionStrategy        `json:"model_selector_strategy,omitempty"`
		CredentialSelector    RouteCredentialSelectStrategy `json:"credential_selector,omitempty"`
		CredentialScopeOrder  []RouteCredentialScope        `json:"credential_scope_order,omitempty"`
		CredentialSourceOrder []RouteCredentialSource       `json:"credential_source_order,omitempty"`
		Fallback              RouteFallbackPolicy           `json:"fallback,omitempty"`
		ModelTargets          []RouteModelTarget            `json:"model_targets,omitempty"`
		ProviderTarget        DirectProviderTarget          `json:"provider_target,omitempty"`
	}
	return json.Marshal(routeTargetPolicyJSON{
		Type:                  p.Type,
		ProviderID:            p.ProviderID,
		DefaultModel:          p.DefaultModel,
		ModelSelectorStrategy: p.ModelSelectorStrategy,
		CredentialSelector:    p.CredentialSelector,
		CredentialScopeOrder:  p.CredentialScopeOrder,
		CredentialSourceOrder: p.CredentialSourceOrder,
		Fallback:              p.Fallback,
		ModelTargets:          p.ModelTargets,
		ProviderTarget:        p.ProviderTarget,
	})
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

type RouteRateLimitPolicy struct {
	RequestsPerMinute int `json:"requests_per_minute,omitempty"`
	RequestsPerHour   int `json:"requests_per_hour,omitempty"`
	ConcurrentLimit   int `json:"concurrent_limit,omitempty"`
}

type RouteQuotaPolicy struct {
	DailyRequests   int `json:"daily_requests,omitempty"`
	MonthlyRequests int `json:"monthly_requests,omitempty"`
	DailyTokens     int `json:"daily_tokens,omitempty"`
	MonthlyTokens   int `json:"monthly_tokens,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts          int   `json:"max_attempts,omitempty"`
	BackoffMS            int   `json:"backoff_ms,omitempty"`
	RetryableStatusCodes []int `json:"retryable_status_codes,omitempty"`
}

func (p *RetryPolicy) Defaults() {
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 1
	}
	if p.BackoffMS == 0 {
		p.BackoffMS = 250
	}
	if len(p.RetryableStatusCodes) == 0 {
		p.RetryableStatusCodes = []int{429, 500, 502, 503, 504}
	}
}

type FallbackPolicy struct {
	Enabled       bool  `json:"enabled,omitempty"`
	OnStatusCodes []int `json:"on_status_codes,omitempty"`
}

// Normalize fills runtime defaults on a route value before it is used by the gateway.
func (r *AgentRoute) Normalize() {
	r.TargetPolicy.Normalize()
}

func (r AgentRoute) UsesDirectProvider() bool {
	return r.TargetPolicy.PolicyKind() == RouteTargetPolicyKindDirectProvider
}

func (r AgentRoute) UsesLogicalModel() bool {
	return r.TargetPolicy.PolicyKind() == RouteTargetPolicyKindLogicalModel
}

func (r AgentRoute) usesDirectProvider() bool {
	return r.UsesDirectProvider()
}

func (p RouteTargetPolicy) PolicyKind() RouteTargetPolicyKind {
	if p.Type != "" {
		return p.Type
	}
	if strings.TrimSpace(p.ProviderID) != "" || strings.TrimSpace(p.ProviderTarget.ProviderID) != "" {
		return RouteTargetPolicyKindDirectProvider
	}
	if len(p.ModelTargets) > 0 {
		return RouteTargetPolicyKindLogicalModel
	}
	return ""
}

func (p *RouteTargetPolicy) Normalize() {
	if p == nil {
		return
	}
	p.ProviderID = strings.TrimSpace(p.ProviderID)
	p.DefaultModel = strings.TrimSpace(p.DefaultModel)
	p.ProviderTarget.ProviderID = strings.TrimSpace(p.ProviderTarget.ProviderID)
	if p.ProviderID == "" && p.ProviderTarget.ProviderID != "" {
		p.ProviderID = p.ProviderTarget.ProviderID
	}
	if p.ProviderTarget.ProviderID == "" && p.ProviderID != "" {
		p.ProviderTarget.ProviderID = p.ProviderID
	}
	if p.Type == "" {
		p.Type = p.PolicyKind()
	}

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

	switch p.Type {
	case RouteTargetPolicyKindDirectProvider:
		if len(p.CredentialScopeOrder) == 0 {
			p.CredentialScopeOrder = []RouteCredentialScope{RouteCredentialScopeProviderID}
		}
	case RouteTargetPolicyKindLogicalModel:
		if p.ModelSelectorStrategy == "" {
			p.ModelSelectorStrategy = RouteSelectionStrategyAuto
		}
		if len(p.CredentialScopeOrder) == 0 {
			p.CredentialScopeOrder = []RouteCredentialScope{RouteCredentialScopeModelCustom, RouteCredentialScopeProviderID}
		}
		if !p.Fallback.Enabled && p.Fallback.MaxNum == 0 {
			p.Fallback.Enabled = true
			p.Fallback.MaxNum = 1
		}
	}
	if p.CredentialSelector == "" {
		p.CredentialSelector = RouteCredentialSelectRoundRobin
	}
	if len(p.CredentialSourceOrder) == 0 {
		p.CredentialSourceOrder = []RouteCredentialSource{RouteCredentialSourceAPIKey, RouteCredentialSourceCLIAuthToken}
	}
}

func (t *RouteModelTarget) Normalize() {
	if t == nil {
		return
	}
	if t.Strategy == "" {
		t.Strategy = RouteSelectionStrategyAuto
	}
}
