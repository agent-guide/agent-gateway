package cliauth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Authenticator handles the CLI login flow for a specific provider.
// Each concrete implementation covers one CLI tool (e.g. Codex, Claude CLI).
type Authenticator interface {
	// Provider returns the unique provider type this authenticator handles (e.g. "openai", "anthropic").
	Provider() string
	// Login initiates the interactive CLI login flow and returns a new Credential on success.
	Login(ctx context.Context) (*Credential, error)
	// RefreshLead attempts to refresh the given credential before it expires.
	// Returns nil to indicate no refresh is needed; returns an updated Credential on success.
	RefreshLead(ctx context.Context, cred *Credential) (*Credential, error)
}

// AuthenticatorFactory creates an Authenticator instance.
type AuthenticatorFactory func() (Authenticator, error)

// AuthenticatorSource identifies where an enabled Authenticator came from.
type AuthenticatorSource string

const (
	AuthenticatorSourceCaddyfile AuthenticatorSource = "caddyfile"
	AuthenticatorSourceRuntime   AuthenticatorSource = "runtime"
)

// ErrAuthenticatorReadOnly is returned when a read-only Authenticator is modified.
var ErrAuthenticatorReadOnly = errors.New("authenticator is read-only")

// RegisterAuthenticatorOptions controls how an Authenticator is registered.
type RegisterAuthenticatorOptions struct {
	Source   AuthenticatorSource
	ReadOnly bool
}

// AuthenticatorState describes a supported or enabled Authenticator.
type AuthenticatorState struct {
	Name     string              `json:"name"`
	Provider string              `json:"provider,omitempty"`
	Source   AuthenticatorSource `json:"source,omitempty"`
	ReadOnly bool                `json:"read_only"`
	Enabled  bool                `json:"enabled"`
}

type authenticatorEntry struct {
	state AuthenticatorState
	auth  Authenticator
}

var (
	authFactoryMu sync.RWMutex
	authFactories = map[string]AuthenticatorFactory{}
)

// RegisterAuthenticatorFactory registers an Authenticator factory by CLI name.
func RegisterAuthenticatorFactory(cliname string, factory AuthenticatorFactory) {
	if factory == nil {
		return
	}
	cliKey := strings.ToLower(strings.TrimSpace(cliname))
	if cliKey == "" {
		return
	}
	authFactoryMu.Lock()
	authFactories[cliKey] = factory
	authFactoryMu.Unlock()
}

// NewAuthenticator creates an Authenticator by CLI name using registered factories.
func NewAuthenticator(cliname string) (Authenticator, error) {
	cliKey := strings.ToLower(strings.TrimSpace(cliname))
	authFactoryMu.RLock()
	factory, ok := authFactories[cliKey]
	authFactoryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown authenticator: %s", cliname)
	}
	return factory()
}

// ListAuthenticatorNames returns the names of all registered Authenticator factories.
func ListAuthenticatorNames() []string {
	authFactoryMu.RLock()
	defer authFactoryMu.RUnlock()
	names := make([]string, 0, len(authFactories))
	for name := range authFactories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
