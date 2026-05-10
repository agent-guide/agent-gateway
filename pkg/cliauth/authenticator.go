package cliauth

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
)

// Authenticator handles the CLI login flow for a specific provider.
// Each concrete implementation covers one CLI tool (e.g. Codex, Claude CLI).
type Authenticator interface {
	// ProviderType returns the unique provider type this authenticator handles (e.g. "openai", "anthropic").
	ProviderType() string
	// GetConfig returns the runtime configuration currently applied to this authenticator instance.
	GetConfig() AuthenticatorConfig
	// SetConfig applies runtime configuration to this authenticator instance.
	SetConfig(cfg AuthenticatorConfig) error
	// Login initiates the interactive CLI login flow and returns a new credential on success.
	Login(ctx context.Context, reporter LoginStatusReporter) (*credentialmgr.Credential, error)
	// Refresh attempts to refresh the given credential before it expires.
	// Returns nil to indicate no refresh is needed; returns an updated credential on success.
	Refresh(ctx context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error)
	// RefreshLeadTime returns how far in advance of token expiry to attempt a refresh.
	// Returning nil disables provider-level background pre-refresh scheduling.
	RefreshLeadTime() *time.Duration
}

// LoginStatusReporter receives login progress updates suitable for surfacing via the admin API.
type LoginStatusReporter interface {
	UpdateLoginStatus(update LoginStatusUpdate)
}

// AuthenticatorFactory creates an Authenticator instance.
type AuthenticatorFactory func() (Authenticator, error)

var (
	authFactoryMu sync.RWMutex
	authFactories = map[string]AuthenticatorFactory{}
)

// RegisterAuthenticatorFactory registers an Authenticator factory by CLI Authenticator Type.
func RegisterAuthenticatorFactory(cliauthType string, factory AuthenticatorFactory) {
	if factory == nil {
		return
	}
	cliKey := strings.ToLower(strings.TrimSpace(cliauthType))
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

// ListAuthenticatorTypes returns the types of all registered Authenticator factories.
func ListAuthenticatorTypes() []string {
	authFactoryMu.RLock()
	defer authFactoryMu.RUnlock()
	cliauthTypes := make([]string, 0, len(authFactories))
	for cliauthType := range authFactories {
		cliauthTypes = append(cliauthTypes, cliauthType)
	}
	sort.Strings(cliauthTypes)
	return cliauthTypes
}
