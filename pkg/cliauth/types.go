package cliauth

import (
	"time"

	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
)

// Status represents the lifecycle state of a CLIAuthCredential entry.
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

// CLIAuthCredential encapsulates the runtime state and metadata for a single upstream credential.
type CLIAuthCredential struct {
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

// LoginStatusUpdate describes a user-visible state transition during an interactive login flow.
type LoginStatusUpdate struct {
	Phase           string `json:"phase,omitempty"`
	Message         string `json:"message,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
}

// LoginRequest describes the provider binding metadata that the admin layer
// resolved before invoking a runtime authenticator.
type LoginRequest struct {
	ProviderID string
	Scope      string
}

// AuthenticatorState describes a supported or enabled Authenticator.
type AuthenticatorState struct {
	Name    string              `json:"name"`
	Enabled bool                `json:"enabled"`
	Config  AuthenticatorConfig `json:"config"`
}

// AuthenticatorConfig describes the runtime configuration supported by built-in
// CLI authenticators.
type AuthenticatorConfig struct {
	CallbackPort     int           `json:"callback_port,omitempty"`
	NoBrowser        bool          `json:"no_browser,omitempty"`
	DeviceFlow       bool          `json:"device_flow,omitempty"`
	TransportProfile string        `json:"transport_profile,omitempty"`
	Network          NetworkConfig `json:"network"`
}

// NetworkConfig re-exports the shared HTTP network config type for authenticator configs.
type NetworkConfig = httpclient.NetworkConfig

// Defaults fills in zero values with sensible defaults.
func (c *AuthenticatorConfig) Defaults() {
	c.Network.Defaults()
}

// ApplyOverrides merges non-zero override values into the receiver while
// preserving any existing runtime defaults already present on the config.
func (c *AuthenticatorConfig) ApplyOverrides(overrides AuthenticatorConfig) {
	if c == nil {
		return
	}
	if overrides.CallbackPort > 0 {
		c.CallbackPort = overrides.CallbackPort
	}
	c.NoBrowser = overrides.NoBrowser
	c.DeviceFlow = overrides.DeviceFlow
	if overrides.TransportProfile != "" {
		c.TransportProfile = overrides.TransportProfile
	}
	applyNetworkConfigOverrides(&c.Network, overrides.Network)
}

func applyNetworkConfigOverrides(dst *NetworkConfig, overrides NetworkConfig) {
	if dst == nil {
		return
	}
	if overrides.RequestTimeoutSeconds > 0 {
		dst.RequestTimeoutSeconds = overrides.RequestTimeoutSeconds
	}
	if overrides.MaxRetries > 0 {
		dst.MaxRetries = overrides.MaxRetries
	}
	if overrides.RetryDelaySeconds > 0 {
		dst.RetryDelaySeconds = overrides.RetryDelaySeconds
	}
	if overrides.MaxIdleConnections > 0 {
		dst.MaxIdleConnections = overrides.MaxIdleConnections
	}
	if overrides.MaxIdleConnectionsPerHost > 0 {
		dst.MaxIdleConnectionsPerHost = overrides.MaxIdleConnectionsPerHost
	}
	if overrides.IdleKeepAliveTimeoutSeconds > 0 {
		dst.IdleKeepAliveTimeoutSeconds = overrides.IdleKeepAliveTimeoutSeconds
	}
	if overrides.ProxyURL != "" {
		dst.ProxyURL = overrides.ProxyURL
	}
	if overrides.ExtraHeaders != nil {
		headers := make(map[string]string, len(overrides.ExtraHeaders))
		for k, v := range overrides.ExtraHeaders {
			headers[k] = v
		}
		dst.ExtraHeaders = headers
	}
}

// Clone shallow copies the CLIAuthCredential, duplicating maps to avoid accidental mutation.
func (c *CLIAuthCredential) Clone() *CLIAuthCredential {
	if c == nil {
		return nil
	}
	copy := *c
	copy.Credential = *c.Credential.Clone()
	return &copy
}

// IsDisabled reports whether the credential has been intentionally disabled.
func (c *CLIAuthCredential) IsDisabled() bool {
	return c != nil && c.Status == StatusDisabled
}

// NewCLIAuthCredential wraps a shared credential with CLI-auth runtime state.
func NewCLIAuthCredential(cred *credentialmgr.Credential) *CLIAuthCredential {
	if cred == nil {
		return nil
	}
	return &CLIAuthCredential{
		Credential: *cred.Clone(),
		Status:     StatusActive,
	}
}
