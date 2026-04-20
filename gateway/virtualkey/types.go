package virtualkey

import "time"

// VirtualKey represents a gateway consumer identity, not an upstream provider credential.
type VirtualKey struct {
	Key         string `json:"key"`
	Tag         string `json:"tag,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled"`

	AllowedRouteIDs []string `json:"allowed_route_ids,omitempty"`

	StatusMessage string    `json:"status_message,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}
