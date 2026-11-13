package llmroute

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

type RouteKind = routecore.RouteKind

const (
	RouteKindLLM = routecore.RouteKindLLM
	RouteKindMCP = routecore.RouteKindMCP
)

type RouteProtocol = routecore.RouteProtocol

const (
	RouteProtocolOpenAI    = routecore.RouteProtocolOpenAI
	RouteProtocolAnthropic = routecore.RouteProtocolAnthropic
	RouteProtocolCC        = routecore.RouteProtocolCC
	RouteProtocolMCP       = routecore.RouteProtocolMCP
)

type RouteConfig = routecore.RouteConfig
type RouteMetadata = routecore.RouteMetadata
type RouteMatchPolicy = routecore.RouteMatchPolicy
type RouteAuthPolicy = routecore.RouteAuthPolicy

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

type RouteCredentialType string

const (
	RouteCredentialTypeAPIKey     RouteCredentialType = "api_key"
	RouteCredentialTypeOAuthToken RouteCredentialType = "oauth_token"
)

type RouteTargetPolicyCommon struct {
	CredentialSelectorValue   RouteCredentialSelectStrategy `json:"credential_selector,omitempty"`
	CredentialScopeOrderValue []RouteCredentialScope        `json:"credential_scope_order,omitempty"`
	CredentialTypeOrderValue  []RouteCredentialType         `json:"credential_type_order,omitempty"`
}

type RouteModelPolicy struct {
	RouteTargetPolicyCommon
	DefaultModel string             `json:"default_model,omitempty"`
	ModelTargets []RouteModelTarget `json:"model_targets"`
}

// DecodeStoredRoute decodes a persisted route config and converts it to a runtime route.
func DecodeStoredRoute(data []byte) (any, error) {
	var cfg RouteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode route config: %w", err)
	}
	route, err := NewLLMRouteFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	route.Normalize()
	route.NormalizeTimestamps(time.Now().UTC())
	return &route, nil
}

// LLMRoute is the expanded configuration form of an LLM route.
type LLMRoute struct {
	RouteMetadata
	TargetPolicy *RouteModelPolicy `json:"target_policy"`
}

// RouteModelTarget publishes an explicit model reference. Concrete names are
// derived from UpstreamModel when omitted; logical names are explicit aliases.
type RouteModelTarget struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	ProviderID     string `json:"provider_id,omitempty"`
	UpstreamModel  string `json:"upstream_model,omitempty"`
	LogicalModelID string `json:"logical_model_id,omitempty"`
}

func (r LLMRoute) MarshalJSON() ([]byte, error) {
	r.Normalize()
	r.Kind = RouteKindLLM
	type value LLMRoute
	return json.Marshal(struct {
		value
		Protocol RouteProtocol `json:"protocol,omitempty"`
	}{value: value(r), Protocol: r.Protocol})
}

func (r *LLMRoute) UnmarshalJSON(data []byte) error {
	type value LLMRoute
	var v value
	if err := strictjson.Decode(data, &v, 4<<20, 64); err != nil {
		return err
	}
	*r = LLMRoute(v)
	return nil
}

func NewLLMRouteFromConfig(cfg RouteConfig) (LLMRoute, error) {
	cfg = normalizeConfigDefaults(cfg.Clone())
	var targetPolicy *RouteModelPolicy
	if len(cfg.TargetPolicy) > 0 {
		if err := json.Unmarshal(cfg.TargetPolicy, &targetPolicy); err != nil {
			return LLMRoute{}, fmt.Errorf("route %q decode target policy: %w", cfg.ID, err)
		}
	}
	return LLMRoute{
		RouteMetadata: cfg.Metadata(),
		TargetPolicy:  targetPolicy,
	}, nil
}

func (r LLMRoute) ToConfig() (RouteConfig, error) {
	r.Normalize()
	targetPolicy, err := json.Marshal(r.TargetPolicy)
	if err != nil {
		return RouteConfig{}, fmt.Errorf("route %q encode target policy: %w", r.ID, err)
	}
	cfg := r.RouteMetadata.Config(targetPolicy)
	cfg.Kind = RouteKindLLM
	return cfg, nil
}

func normalizeConfigDefaults(cfg RouteConfig) RouteConfig {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Protocol = normalizeRouteProtocol(cfg.Protocol)
	cfg.Description = strings.TrimSpace(cfg.Description)
	if cfg.Kind == "" {
		cfg.Kind = RouteKindLLM
	}
	return cfg
}

func normalizeRouteProtocol(protocol RouteProtocol) RouteProtocol {
	return RouteProtocol(strings.TrimSpace(string(protocol)))
}

func (p RouteModelPolicy) MarshalJSON() ([]byte, error) {
	p.Normalize()
	type policyJSON RouteModelPolicy
	return json.Marshal(struct {
		Type string `json:"type"`
		policyJSON
	}{"models", policyJSON(p)})
}

// Normalize fills defaults on a route config value before it is used.
func (r *LLMRoute) Normalize() {
	if r == nil {
		return
	}
	r.RouteMetadata = normalizeConfigDefaults(r.RouteMetadata.Config(nil)).Metadata()
	if r.TargetPolicy != nil {
		r.TargetPolicy.Normalize()
	}
}

func (r LLMRoute) UsesModels() bool {
	return r.TargetPolicy != nil
}

func (p *RouteModelPolicy) UnmarshalJSON(data []byte) error {
	if err := routecore.ValidateLLMTargetFields("", data); err != nil {
		return err
	}
	type value RouteModelPolicy
	var v struct {
		Type string `json:"type"`
		value
	}
	if err := strictjson.Decode(data, &v, 4<<20, 64); err != nil {
		return err
	}
	if v.Type != "" && v.Type != "models" {
		return fmt.Errorf("unknown route target policy kind %q", v.Type)
	}
	*p = RouteModelPolicy(v.value)
	p.Normalize()
	return nil
}
