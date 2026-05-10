package cliauth

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
)

// Manager orchestrates authenticator lifecycle: registration, enable/disable, and selection.
type Manager struct {
	mu             sync.RWMutex
	authenticators map[string]Authenticator // cli key -> Authenticator
	providerAuths  map[string]string        // providerType key -> cli key
	credentialMgr  *credentialmgr.Manager
}

// NewManager constructs a CLI Authenticators Manager.
func NewManager() *Manager {
	return &Manager{
		authenticators: make(map[string]Authenticator),
		providerAuths:  make(map[string]string),
	}
}

func (m *Manager) SetCredentialManager(credentialMgr *credentialmgr.Manager) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.credentialMgr = credentialMgr
	authenticators := make(map[string]Authenticator, len(m.authenticators))
	for key, auth := range m.authenticators {
		authenticators[key] = auth
	}
	m.mu.Unlock()

	if credentialMgr == nil {
		return
	}
	for key, auth := range authenticators {
		credentialMgr.SetManualRefresher(key, auth)
	}
}

// RegisterAuthenticator registers an Authenticator for a CLI name.
// It also indexes the same Authenticator by its provider type so refresh lookups
// can continue resolving via credential.ProviderType.
func (m *Manager) RegisterAuthenticator(cliname string, auth Authenticator) {
	if auth == nil {
		return
	}
	cliKey := strings.ToLower(strings.TrimSpace(cliname))
	if cliKey == "" {
		return
	}
	providerTypeKey := strings.ToLower(strings.TrimSpace(auth.ProviderType()))
	m.mu.Lock()
	if previous := m.authenticators[cliKey]; previous != nil && previous.ProviderType() != "" {
		previousProviderKey := strings.ToLower(previous.ProviderType())
		if m.providerAuths[previousProviderKey] == cliKey {
			delete(m.providerAuths, previousProviderKey)
		}
	}
	m.authenticators[cliKey] = auth
	if providerTypeKey != "" {
		m.providerAuths[providerTypeKey] = cliKey
	}
	credentialMgr := m.credentialMgr
	m.mu.Unlock()
	if credentialMgr != nil {
		credentialMgr.SetManualRefresher(cliKey, auth)
	}
}

// EnableAuthenticator creates and registers a runtime Authenticator by CLI name.
// The provided config is applied to the runtime authenticator on top of the
// factory defaults before registration.
func (m *Manager) EnableAuthenticator(cliname string, cfg AuthenticatorConfig) (AuthenticatorState, error) {
	cliKey := strings.ToLower(strings.TrimSpace(cliname))
	if cliKey == "" {
		return AuthenticatorState{}, fmt.Errorf("manager: authenticator name is empty")
	}
	auth, err := m.buildAuthenticatorWithConfig(cliKey, cfg)
	if err != nil {
		return AuthenticatorState{}, err
	}
	m.RegisterAuthenticator(cliKey, auth)
	state, _ := m.GetAuthenticatorState(cliKey)
	return state, nil
}

// DisableAuthenticator deregisters an Authenticator by CLI name.
func (m *Manager) DisableAuthenticator(cliname string) error {
	cliKey := strings.ToLower(strings.TrimSpace(cliname))
	if cliKey == "" {
		return fmt.Errorf("manager: authenticator name is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.authenticators[cliKey]
	if entry == nil {
		return nil
	}
	if entry.ProviderType() != "" {
		providerKey := strings.ToLower(entry.ProviderType())
		if m.providerAuths[providerKey] == cliKey {
			delete(m.providerAuths, providerKey)
		}
	}
	delete(m.authenticators, cliKey)
	credentialMgr := m.credentialMgr
	if credentialMgr != nil {
		credentialMgr.RemoveManualRefresher(cliKey)
	}
	return nil
}

// GetAuthenticator returns the Authenticator registered for the given CLI name.
func (m *Manager) GetAuthenticator(cliname string) (Authenticator, bool) {
	key := strings.ToLower(strings.TrimSpace(cliname))
	m.mu.RLock()
	entry, ok := m.authenticators[key]
	m.mu.RUnlock()
	if !ok || entry == nil {
		return nil, false
	}
	return entry, true
}

// GetAuthenticatorState returns lifecycle metadata for the given CLI name.
// If the authenticator is not enabled but its factory is registered, a disabled
// state with the factory defaults is returned.
func (m *Manager) GetAuthenticatorState(cliname string) (AuthenticatorState, bool) {
	key := strings.ToLower(strings.TrimSpace(cliname))
	if key == "" {
		return AuthenticatorState{}, false
	}
	m.mu.RLock()
	entry, ok := m.authenticators[key]
	m.mu.RUnlock()
	if ok && entry != nil {
		return AuthenticatorState{
			Name:         key,
			ProviderType: strings.ToLower(strings.TrimSpace(entry.ProviderType())),
			Enabled:      true,
			Config:       entry.GetConfig(),
		}, true
	}

	auth, err := NewAuthenticator(key)
	if err != nil || auth == nil {
		return AuthenticatorState{}, false
	}
	return AuthenticatorState{
		Name:         key,
		ProviderType: strings.ToLower(strings.TrimSpace(auth.ProviderType())),
		Enabled:      false,
		Config:       auth.GetConfig(),
	}, true
}

// ListAuthenticatorStates returns all supported authenticators, marking the
// ones that are currently enabled with their runtime metadata.
func (m *Manager) ListAuthenticatorStates() []AuthenticatorState {
	cliauthTypes := ListAuthenticatorTypes()
	out := make([]AuthenticatorState, 0, len(cliauthTypes))
	for _, cliauthName := range cliauthTypes {
		state, ok := m.GetAuthenticatorState(cliauthName)
		if ok {
			out = append(out, state)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *Manager) resolveAuthenticator(providerType string) Authenticator {
	key := strings.ToLower(strings.TrimSpace(providerType))
	m.mu.RLock()
	cliKey, ok := m.providerAuths[key]
	entry := m.authenticators[cliKey]
	m.mu.RUnlock()
	if ok && entry != nil {
		return entry
	}
	return nil
}

func (m *Manager) buildAuthenticatorWithConfig(cliname string, cfg AuthenticatorConfig) (Authenticator, error) {
	auth, err := NewAuthenticator(cliname)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, fmt.Errorf("authenticator is nil")
	}
	if err := ApplyAuthenticatorConfigOverrides(auth, cfg); err != nil {
		return nil, err
	}
	return auth, nil
}
