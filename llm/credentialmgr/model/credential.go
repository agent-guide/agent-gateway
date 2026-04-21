package model

import (
	"strconv"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
)

// Credential holds the routing-relevant state for a single upstream credential.
// It is intentionally decoupled from lifecycle management (refresh scheduling,
// status tracking) so that both cliauth-managed tokens and static API keys can
// share the same CredentialScheduler implementation.
type Credential struct {
	// ID uniquely identifies the credential.
	ID string `json:"id"`
	// Provider is the upstream provider key (e.g. "openai", "anthropic").
	Provider string `json:"provider"`
	// Source identifies where the credential came from (e.g. api_key, cliauth).
	Source string `json:"source,omitempty"`
	// Label is a human-readable label for logging and display.
	Label string `json:"label,omitempty"`
	// Attributes stores provider-specific configuration (e.g. api_key, base_url, priority).
	Attributes map[string]string `json:"attributes,omitempty"`
	// Metadata stores runtime mutable provider state (e.g. tokens, cookies).
	Metadata map[string]any `json:"metadata,omitempty"`
	// Disabled marks the credential as intentionally excluded from scheduling.
	Disabled bool `json:"disabled,omitempty"`
	// Unavailable flags transient provider unavailability (e.g. quota exceeded).
	Unavailable bool `json:"unavailable,omitempty"`
	// NextRetryAfter is the earliest time a retry should be attempted.
	NextRetryAfter time.Time `json:"next_retry_after,omitempty"`
	// Quota captures recent quota information for load-balancing decisions.
	Quota QuotaState `json:"quota"`
	// LastError records the last failure encountered.
	LastError *Error `json:"last_error,omitempty"`
	// ModelStates tracks per-model runtime availability data.
	ModelStates map[string]*ModelState `json:"model_states,omitempty"`
	// CreatedAt is the creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last modification timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// QuotaState captures quota limiter tracking data for a credential.
type QuotaState struct {
	Exceeded      bool      `json:"exceeded"`
	Reason        string    `json:"reason,omitempty"`
	NextRecoverAt time.Time `json:"next_recover_at,omitempty"`
	BackoffLevel  int       `json:"backoff_level,omitempty"`
}

// ModelState captures the execution state for a specific model under a credential.
type ModelState struct {
	// Disabled marks the model as intentionally excluded from scheduling.
	Disabled bool `json:"disabled,omitempty"`
	// Unavailable flags transient unavailability for this model.
	Unavailable bool `json:"unavailable,omitempty"`
	// NextRetryAfter is the earliest time this model may be retried.
	NextRetryAfter time.Time `json:"next_retry_after,omitempty"`
	// LastError records the latest error observed for this model.
	LastError *Error `json:"last_error,omitempty"`
	// Quota retains quota information if this model hit rate limits.
	Quota QuotaState `json:"quota"`
	// UpdatedAt tracks the last update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// Error describes a credential-related failure in a provider-agnostic format.
type Error struct {
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// StatusCode implements optional status accessor for retry decision making.
func (e *Error) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.HTTPStatus
}

// IsDisabled reports whether the credential has been intentionally disabled.
func (c *Credential) IsDisabled() bool {
	return c != nil && c.Disabled
}

// APIKey returns the api_key attribute value, or empty string if not set.
func (c *Credential) APIKey() string {
	if c == nil || c.Attributes == nil {
		return ""
	}
	return c.Attributes["api_key"]
}

// BaseURL returns the base_url attribute value, or empty string if not set.
func (c *Credential) BaseURL() string {
	if c == nil || c.Attributes == nil {
		return ""
	}
	return c.Attributes["base_url"]
}

// Priority returns the scheduling priority for this credential (higher = preferred).
func (c *Credential) Priority() int {
	if c == nil || c.Attributes == nil {
		return 0
	}
	raw := strings.TrimSpace(c.Attributes["priority"])
	if raw == "" {
		return 0
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return parsed
}

// ExpirationTime attempts to extract the credential expiration timestamp from metadata.
func (c *Credential) ExpirationTime() (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	return utils.ExpirationFromMap(c.Metadata)
}

// DisableCoolingOverride returns the per-credential disable_cooling override when present.
func (c *Credential) DisableCoolingOverride() (bool, bool) {
	if c == nil || c.Metadata == nil {
		return false, false
	}
	for _, key := range []string{"disable_cooling", "disable-cooling"} {
		if val, ok := c.Metadata[key]; ok {
			if parsed, okParse := utils.ParseBoolAny(val); okParse {
				return parsed, true
			}
		}
	}
	return false, false
}

// RequestRetryOverride returns the per-credential request_retry override when present.
func (c *Credential) RequestRetryOverride() (int, bool) {
	if c == nil || c.Metadata == nil {
		return 0, false
	}
	for _, key := range []string{"request_retry", "request-retry"} {
		if val, ok := c.Metadata[key]; ok {
			if parsed, okParse := utils.ParseIntAny(val); okParse {
				if parsed < 0 {
					parsed = 0
				}
				return parsed, true
			}
		}
	}
	return 0, false
}

// Clone shallow copies the Credential, duplicating maps to avoid mutation.
func (c *Credential) Clone() *Credential {
	if c == nil {
		return nil
	}
	cp := *c
	if len(c.Attributes) > 0 {
		cp.Attributes = make(map[string]string, len(c.Attributes))
		for k, v := range c.Attributes {
			cp.Attributes[k] = v
		}
	}
	if len(c.Metadata) > 0 {
		cp.Metadata = make(map[string]any, len(c.Metadata))
		for k, v := range c.Metadata {
			cp.Metadata[k] = v
		}
	}
	if len(c.ModelStates) > 0 {
		cp.ModelStates = make(map[string]*ModelState, len(c.ModelStates))
		for k, v := range c.ModelStates {
			cp.ModelStates[k] = v.Clone()
		}
	}
	return &cp
}

// Clone duplicates a ModelState.
func (m *ModelState) Clone() *ModelState {
	if m == nil {
		return nil
	}
	cp := *m
	if m.LastError != nil {
		cp.LastError = &Error{
			Code:       m.LastError.Code,
			Message:    m.LastError.Message,
			Retryable:  m.LastError.Retryable,
			HTTPStatus: m.LastError.HTTPStatus,
		}
	}
	return &cp
}
