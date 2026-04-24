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

// Credential encapsulates the runtime state and metadata for a single upstream credential.
type Credential struct {
	credentialmgr.Credential
	// Status is the lifecycle status managed by the Manager.
	// Use StatusDisabled to mark a credential as intentionally disabled.
	Status Status `json:"status"`
	// StatusMessage holds a short description for the current status.
	StatusMessage string `json:"status_message,omitempty"`
	// LastRefreshedAt records the last successful source-level refresh.
	LastRefreshedAt time.Time `json:"last_refreshed_at,omitempty"`
	// NextRefreshAfter is the earliest time a source-level refresh should retrigger.
	NextRefreshAfter time.Time `json:"next_refresh_after,omitempty"`
}

// Clone shallow copies the Credential, duplicating maps to avoid accidental mutation.
func (c *Credential) Clone() *Credential {
	if c == nil {
		return nil
	}
	copy := *c
	copy.Credential = *c.Credential.Clone()
	return &copy
}

// IsDisabled reports whether the credential has been intentionally disabled.
func (c *Credential) IsDisabled() bool {
	return c != nil && c.Status == StatusDisabled
}
