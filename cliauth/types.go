package cliauth

import (
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

// Status represents the lifecycle state of a Credential entry.
type Status string

const (
	// StatusUnknown means the credential state could not be determined.
	StatusUnknown Status = "unknown"
	// StatusActive indicates the credential is valid and ready for use.
	StatusActive Status = "active"
	// StatusPending indicates the credential is waiting for an external action.
	StatusPending Status = "pending"
	// StatusRefreshing indicates the credential is undergoing a refresh flow.
	StatusRefreshing Status = "refreshing"
	// StatusError indicates the credential is temporarily unavailable due to errors.
	StatusError Status = "error"
	// StatusDisabled marks the credential as intentionally disabled.
	StatusDisabled Status = "disabled"
)

// Error is kept as a cliauth-facing alias for the shared credential error type.
type Error = credentialmgr.Error

// QuotaState is kept as a cliauth-facing alias for the shared quota state.
type QuotaState = credentialmgr.QuotaState

// Credential encapsulates the runtime state and metadata for a single upstream credential.
type Credential struct {
	credentialmgr.Credential
	// Status is the lifecycle status managed by the Manager.
	// Use StatusDisabled to mark a credential as intentionally disabled.
	Status Status `json:"status"`
	// StatusMessage holds a short description for the current status.
	StatusMessage string `json:"status_message,omitempty"`
	// ModelStates tracks per-model runtime availability data.
	ModelStates map[string]*ModelState `json:"model_states,omitempty"`
	// LastRefreshedAt records the last successful source-level refresh.
	LastRefreshedAt time.Time `json:"last_refreshed_at,omitempty"`
	// NextRefreshAfter is the earliest time a source-level refresh should retrigger.
	NextRefreshAfter time.Time `json:"next_refresh_after,omitempty"`
}

// ModelState captures the execution state for a specific model under a credential.
type ModelState struct {
	// Status reflects the lifecycle status for this model.
	Status Status `json:"status"`
	// StatusMessage provides an optional short description of the status.
	StatusMessage string `json:"status_message,omitempty"`
	// Unavailable mirrors whether the model is temporarily blocked for retries.
	Unavailable bool `json:"unavailable"`
	// NextRetryAfter defines the per-model retry time.
	NextRetryAfter time.Time `json:"next_retry_after"`
	// LastError records the latest error observed for this model.
	LastError *Error `json:"last_error,omitempty"`
	// Quota retains quota information if this model hit rate limits.
	Quota QuotaState `json:"quota"`
	// UpdatedAt tracks the last update timestamp for this model state.
	UpdatedAt time.Time `json:"updated_at"`
}

// Clone shallow copies the Credential, duplicating maps to avoid accidental mutation.
func (c *Credential) Clone() *Credential {
	if c == nil {
		return nil
	}
	copy := *c
	copy.Credential = *c.Credential.Clone()
	if len(c.ModelStates) > 0 {
		copy.ModelStates = make(map[string]*ModelState, len(c.ModelStates))
		for k, v := range c.ModelStates {
			copy.ModelStates[k] = v.Clone()
		}
	}
	return &copy
}

// Clone duplicates a ModelState including nested error details.
func (m *ModelState) Clone() *ModelState {
	if m == nil {
		return nil
	}
	copy := *m
	if m.LastError != nil {
		copy.LastError = &Error{
			Code:       m.LastError.Code,
			Message:    m.LastError.Message,
			Retryable:  m.LastError.Retryable,
			HTTPStatus: m.LastError.HTTPStatus,
		}
	}
	return &copy
}

// IsDisabled reports whether the credential has been intentionally disabled.
func (c *Credential) IsDisabled() bool {
	return c != nil && c.Status == StatusDisabled
}
