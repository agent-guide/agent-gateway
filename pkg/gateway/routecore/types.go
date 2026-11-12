package routecore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type RouteKind string

const (
	RouteKindLLM RouteKind = "llm"
	RouteKindMCP RouteKind = "mcp"
	// RouteKindAgent is the unified Agent ingress route kind. It targets an
	// agent_id and stays runtime-neutral: the resolved Agent's runtime.type
	// selects the execution backend (docs/design/agents-control-plane.md).
	RouteKindAgent RouteKind = "agent"
)

type RouteProtocol string

const (
	RouteProtocolOpenAI    RouteProtocol = "openai"
	RouteProtocolAnthropic RouteProtocol = "anthropic"
	RouteProtocolCC        RouteProtocol = "cc"
	RouteProtocolMCP       RouteProtocol = "mcp"
	RouteProtocolAgent     RouteProtocol = "agent"
	RouteProtocolA2A       RouteProtocol = "a2a"
)

type RouteTargetPolicyKind string

const (
	RouteTargetPolicyKindDirectProvider RouteTargetPolicyKind = "direct-provider"
	RouteTargetPolicyKindLogicalModel   RouteTargetPolicyKind = "logical-model"
	RouteTargetPolicyKindConnector      RouteTargetPolicyKind = "connector"
	RouteTargetPolicyKindAgent          RouteTargetPolicyKind = "agent"
)

type RouteConfig struct {
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

// Fingerprint identifies all persisted fields, including target changes whose
// timestamps collide in custom stores. Hash the whole value because callers may
// mutate a detached config without updating its timestamp.
func (c RouteConfig) Fingerprint() string {
	data, _ := json.Marshal(c)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// RouteMatchPolicy contains transport-facing match fields.
type RouteMatchPolicy struct {
	Host       string   `json:"host,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Methods    []string `json:"methods,omitempty"`
}

type RouteAuthPolicy struct {
	RequireVirtualKey bool `json:"require_virtual_key"`
}
