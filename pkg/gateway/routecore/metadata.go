package routecore

import "time"

// RouteMetadata is the target-free common portion of a typed route. Raw target
// JSON belongs only to RouteConfig, never alongside a writable typed target.
type RouteMetadata struct {
	ID          string           `json:"id"`
	Kind        RouteKind        `json:"kind"`
	Protocol    RouteProtocol    `json:"protocol"`
	Description string           `json:"description,omitempty"`
	Disabled    bool             `json:"disabled"`
	AuthPolicy  RouteAuthPolicy  `json:"auth_policy"`
	MatchPolicy RouteMatchPolicy `json:"match_policy"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

func (c RouteConfig) Metadata() RouteMetadata {
	match := c.MatchPolicy
	match.Methods = append([]string(nil), match.Methods...)
	return RouteMetadata{ID: c.ID, Kind: c.Kind, Protocol: c.Protocol, Description: c.Description, Disabled: c.Disabled, AuthPolicy: c.AuthPolicy, MatchPolicy: match, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func (m RouteMetadata) Config(target []byte) RouteConfig {
	match := m.MatchPolicy
	match.Methods = append([]string(nil), match.Methods...)
	return RouteConfig{ID: m.ID, Kind: m.Kind, Protocol: m.Protocol, Description: m.Description, Disabled: m.Disabled, AuthPolicy: m.AuthPolicy, MatchPolicy: match, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, TargetPolicy: append([]byte(nil), target...)}
}

func (m *RouteMetadata) NormalizeTimestamps(now time.Time) {
	if m == nil {
		return
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}
}

func (m RouteMetadata) Clone() RouteMetadata {
	m.MatchPolicy.Methods = append([]string(nil), m.MatchPolicy.Methods...)
	return m
}

func (c RouteConfig) Clone() RouteConfig { return cloneRouteConfig(c) }
