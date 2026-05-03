package route

import (
	"encoding/json"
	"fmt"
	"time"
)

// RouteSelectionStrategy controls how a route prefers candidates during runtime selection.
type RouteSelectionStrategy string

const (
	RouteSelectionStrategyAuto        RouteSelectionStrategy = "auto"
	RouteSelectionStrategyWeighted    RouteSelectionStrategy = "weighted"
	RouteSelectionStrategyFailover    RouteSelectionStrategy = "failover"
	RouteSelectionStrategyConditional RouteSelectionStrategy = "conditional"
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
	ModelTargets   []RouteModelTarget   `json:"model_targets,omitempty"`
	DefaultModel   string               `json:"default_model,omitempty"`
	ProviderTarget DirectProviderTarget `json:"provider_target,omitempty"`
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
}

type DirectProviderTarget struct {
	ProviderID string `json:"provider_id"`
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

func (r AgentRoute) usesDirectProvider() bool {
	return r.TargetPolicy.ProviderTarget.ProviderID != ""
}

func (p *RouteTargetPolicy) Normalize() {
	if p == nil {
		return
	}
	for i := range p.ModelTargets {
		p.ModelTargets[i].Normalize()
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
