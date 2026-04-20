package route

import (
	"time"
)

// TargetMode describes how a route target participates in routing.
type TargetMode string

const (
	TargetModeWeighted    TargetMode = "weighted"
	TargetModeFailover    TargetMode = "failover"
	TargetModeConditional TargetMode = "conditional"
)

// RouteSelectionStrategy controls how a route prefers targets under SelectionPolicy.
type RouteSelectionStrategy string

const (
	RouteSelectionStrategyAuto        RouteSelectionStrategy = "auto"
	RouteSelectionStrategyWeighted    RouteSelectionStrategy = "weighted"
	RouteSelectionStrategyFailover    RouteSelectionStrategy = "failover"
	RouteSelectionStrategyConditional RouteSelectionStrategy = "conditional"
)

// RouteResolveRequest captures the request attributes required for route resolution.
type RouteResolveRequest struct {
	Model  string
	Stream bool
}

// AgentRoute is the primary gateway route configuration exposed to agent clients.
// A route config owns request matching, target selection metadata, and default policy.
type AgentRoute struct {
	ID          string        `json:"id"`
	Description string        `json:"description,omitempty"`
	Disabled    bool          `json:"disabled"`
	LLMAPI      string        `json:"llm_api,omitempty"`
	Match       RouteMatch    `json:"match"`
	Targets     []RouteTarget `json:"targets"`
	Policy      RoutePolicy   `json:"policy"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// RouteMatch contains transport-facing match fields for binding requests to a route.
type RouteMatch struct {
	Host       string   `json:"host,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Methods    []string `json:"methods,omitempty"`
}

// RouteTarget references an upstream provider candidate under a route.
type RouteTarget struct {
	ProviderID string            `json:"provider_id"`
	Mode       TargetMode        `json:"mode"`
	Weight     int               `json:"weight,omitempty"`
	Priority   int               `json:"priority,omitempty"`
	ModelMap   map[string]string `json:"model_map,omitempty"`
	Conditions TargetConditions  `json:"conditions,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
}

// TargetConditions express conditional target eligibility.
type TargetConditions struct {
	Models            []string `json:"models,omitempty"`
	RequireStreaming  *bool    `json:"require_streaming,omitempty"`
	RequireTools      *bool    `json:"require_tools,omitempty"`
	RequireVision     *bool    `json:"require_vision,omitempty"`
	RequireEmbeddings *bool    `json:"require_embeddings,omitempty"`
	Regions           []string `json:"regions,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// RoutePolicy contains default route-level auth, quota, rate-limit, and execution controls.
type RoutePolicy struct {
	Auth AuthPolicy `json:"auth"`

	RateLimit RateLimitPolicy `json:"rate_limit,omitempty"`
	Quota     QuotaPolicy     `json:"quota,omitempty"`

	AllowedModels []string `json:"allowed_models,omitempty"`

	AllowStreaming  *bool `json:"allow_streaming,omitempty"`
	AllowTools      *bool `json:"allow_tools,omitempty"`
	AllowVision     *bool `json:"allow_vision,omitempty"`
	AllowEmbeddings *bool `json:"allow_embeddings,omitempty"`

	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
	Selection      SelectionPolicy `json:"selection,omitempty"`
	Retry          RetryPolicy     `json:"retry,omitempty"`
	Fallback       FallbackPolicy  `json:"fallback,omitempty"`
}

// Defaults fills zero values with pragmatic route policy defaults.
func (p *RoutePolicy) Defaults() {
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 120
	}
	p.Selection.Defaults()
	p.Retry.Defaults()
}

type AuthPolicy struct {
	RequireVirtualKey bool `json:"require_virtual_key"`
}

type RateLimitPolicy struct {
	RequestsPerMinute int `json:"requests_per_minute,omitempty"`
	RequestsPerHour   int `json:"requests_per_hour,omitempty"`
	ConcurrentLimit   int `json:"concurrent_limit,omitempty"`
}

type QuotaPolicy struct {
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

type SelectionPolicy struct {
	Strategy RouteSelectionStrategy `json:"strategy,omitempty"`
}

func (p *SelectionPolicy) Defaults() {
	if p.Strategy == "" {
		p.Strategy = RouteSelectionStrategyAuto
	}
}

// Defaults fills zero values with pragmatic retry defaults for provider calls.
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
