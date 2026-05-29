package routecore

import (
	"encoding/json"
	"time"
)

type RouteKind string

const (
	RouteKindLLM RouteKind = "llm"
	RouteKindMCP RouteKind = "mcp"
)

type RouteProtocol string

const (
	RouteProtocolOpenAI    RouteProtocol = "openai"
	RouteProtocolAnthropic RouteProtocol = "anthropic"
	RouteProtocolCC        RouteProtocol = "cc"
	RouteProtocolMCP       RouteProtocol = "mcp"
)

type RouteTargetPolicyKind string

const (
	RouteTargetPolicyKindDirectProvider RouteTargetPolicyKind = "direct-provider"
	RouteTargetPolicyKindLogicalModel   RouteTargetPolicyKind = "logical-model"
	RouteTargetPolicyKindMCPService     RouteTargetPolicyKind = "mcp-service"
)

type AgentRouteConfig struct {
	ID           string           `json:"id"`
	Kind         RouteKind        `json:"kind"`
	Protocol     RouteProtocol    `json:"protocol"`
	Description  string           `json:"description,omitempty"`
	Disabled     bool             `json:"disabled"`
	AuthPolicy   RouteAuthPolicy  `json:"auth_policy"`
	MatchPolicy  RouteMatchPolicy `json:"match_policy"`
	TargetPolicy json.RawMessage  `json:"target_policy"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// RouteMatchPolicy contains transport-facing match fields for binding requests to a route.
type RouteMatchPolicy struct {
	Host       string   `json:"host,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Methods    []string `json:"methods,omitempty"`
}

type RouteAuthPolicy struct {
	RequireVirtualKey bool `json:"require_virtual_key"`
}
