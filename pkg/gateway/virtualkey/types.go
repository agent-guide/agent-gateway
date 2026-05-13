package virtualkey

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// VirtualKey represents a gateway consumer identity, not an upstream provider credential.
type VirtualKey struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Tag         string `json:"tag,omitempty"`
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled"`

	AllowedRouteIDs []string `json:"allowed_route_ids,omitempty"`

	StatusMessage string    `json:"status_message,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

const GeneratedKeyPrefix = "vk-"

func GenerateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate virtual key: %w", err)
	}
	return GeneratedKeyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeStoredVirtualKey decodes virtual key records.
func DecodeStoredVirtualKey(data []byte) (any, error) {
	var key VirtualKey
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, err
	}
	if key.ID == "" {
		return nil, &json.UnmarshalTypeError{Field: "id"}
	}
	if key.Key == "" {
		return nil, &json.UnmarshalTypeError{Field: "key"}
	}
	return &key, nil
}

func (key *VirtualKey) NormalizeTimestamps(now time.Time) {
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = now
	}
}
