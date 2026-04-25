package cliauth

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Manager orchestrates authenticator lifecycle: registration, enable/disable, and selection.
type Manager struct {
	mu             sync.RWMutex
	authenticators map[string]*authenticatorEntry // cli key -> Authenticator
	providerAuths  map[string]string              // providerType key -> cli key
}

// NewManager constructs a CLI Authenticators Manager.
func NewManager() *Manager {
	return &Manager{
		authenticators: make(map[string]*authenticatorEntry),
		providerAuths:  make(map[string]string),
	}
}

// RegisterAuthenticator registers an Authenticator for a CLI name.
// It also indexes the same Authenticator by its provider type so refresh lookups
// can continue resolving via credential.ProviderType.
func (m *Manager) RegisterAuthenticator(cliname string, auth Authenticator) {
	m.RegisterAuthenticatorWithOptions(cliname, auth, RegisterAuthenticatorOptions{
		Source: AuthenticatorSourceRuntime,
	})
}

// RegisterAuthenticatorWithOptions registers an Authenticator with lifecycle metadata.
func (m *Manager) RegisterAuthenticatorWithOptions(cliname string, auth Authenticator, opts RegisterAuthenticatorOptions) {
	if auth == nil {
		return
	}
	cliKey := strings.ToLower(strings.TrimSpace(cliname))
	if cliKey == "" {
		return
	}
	providerTypeKey := strings.ToLower(strings.TrimSpace(auth.ProviderType()))
	if opts.Source == "" {
		opts.Source = AuthenticatorSourceRuntime
	}
	m.mu.Lock()
	if previous := m.authenticators[cliKey]; previous != nil && previous.state.ProviderType != "" {
		previousProviderKey := strings.ToLower(previous.state.ProviderType)
		if m.providerAuths[previousProviderKey] == cliKey {
			delete(m.providerAuths, previousProviderKey)
		}
	}
	m.authenticators[cliKey] = &authenticatorEntry{
		state: AuthenticatorState{
			Name:         cliKey,
			ProviderType: providerTypeKey,
			Source:       opts.Source,
			ReadOnly:     opts.ReadOnly,
			Enabled:      true,
		},
		auth: auth,
	}
	if providerTypeKey != "" {
		m.providerAuths[providerTypeKey] = cliKey
	}
	m.mu.Unlock()
}

// EnableAuthenticator creates and registers a runtime Authenticator by CLI name.
func (m *Manager) EnableAuthenticator(cliname string) (AuthenticatorState, error) {
	cliKey := strings.ToLower(strings.TrimSpace(cliname))
	if cliKey == "" {
		return AuthenticatorState{}, fmt.Errorf("manager: authenticator name is empty")
	}
	if state, ok := m.AuthenticatorState(cliKey); ok && state.Enabled {
		return state, nil
	}
	auth, err := NewAuthenticator(cliKey)
	if err != nil {
		return AuthenticatorState{}, err
	}
	m.RegisterAuthenticatorWithOptions(cliKey, auth, RegisterAuthenticatorOptions{
		Source: AuthenticatorSourceRuntime,
	})
	state, _ := m.AuthenticatorState(cliKey)
	return state, nil
}

// DisableAuthenticator deregisters a runtime Authenticator by CLI name.
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
	if entry.state.ReadOnly {
		return ErrAuthenticatorReadOnly
	}
	if entry.state.ProviderType != "" {
		providerKey := strings.ToLower(entry.state.ProviderType)
		if m.providerAuths[providerKey] == cliKey {
			delete(m.providerAuths, providerKey)
		}
	}
	delete(m.authenticators, cliKey)
	return nil
}

// GetAuthenticator returns the Authenticator registered for the given CLI name.
func (m *Manager) GetAuthenticator(cliname string) (Authenticator, bool) {
	key := strings.ToLower(strings.TrimSpace(cliname))
	m.mu.RLock()
	entry, ok := m.authenticators[key]
	m.mu.RUnlock()
	if !ok || entry == nil || entry.auth == nil {
		return nil, false
	}
	return entry.auth, true
}

// AuthenticatorState returns lifecycle metadata for the given CLI name.
func (m *Manager) AuthenticatorState(cliname string) (AuthenticatorState, bool) {
	key := strings.ToLower(strings.TrimSpace(cliname))
	m.mu.RLock()
	entry, ok := m.authenticators[key]
	m.mu.RUnlock()
	if !ok || entry == nil {
		return AuthenticatorState{Name: key}, false
	}
	return entry.state, true
}

// ListAuthenticatorStates returns all supported authenticators, marking the
// ones that are currently enabled with their runtime metadata.
func (m *Manager) ListAuthenticatorStates() []AuthenticatorState {
	cliauthTypes := ListAuthenticatorTypes()
	out := make([]AuthenticatorState, 0, len(m.authenticators))
	m.mu.RLock()
	for _, cliauthName := range cliauthTypes {
		if entry := m.authenticators[cliauthName]; entry != nil {
			out = append(out, entry.state)
			continue
		}
		out = append(out, AuthenticatorState{Name: cliauthName})
	}
	m.mu.RUnlock()

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
	if ok && entry != nil && entry.auth != nil {
		return entry.auth
	}
	return nil
}
