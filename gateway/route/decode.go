package route

import (
	"encoding/json"
	"fmt"
	"time"
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
