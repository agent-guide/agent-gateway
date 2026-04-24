package cliauth

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/google/uuid"
)

const (
	defaultRefreshCheckInterval = 5 * time.Second
	defaultRefreshMaxConcurrent = 8
)

// CredentialManager captures the credential store/snapshot operations cliauth
// needs from the shared credential manager.
type CredentialManager interface {
	GetCredential(id string) *credentialmgr.Credential
	ListCredentials(filter credentialmgr.Filter) []*credentialmgr.Credential
	RegisterCredential(ctx context.Context, cred *credentialmgr.Credential) error
	UpdateCredential(ctx context.Context, cred *credentialmgr.Credential) error
	DeregisterCredential(ctx context.Context, id string) error
}

type sharedCredentialManager struct {
	manager *credentialmgr.Manager
}

// WrapSharedCredentialManager adapts the shared credential manager to the
// cliauth-focused CredentialManager interface.
func WrapSharedCredentialManager(manager *credentialmgr.Manager) CredentialManager {
	if manager == nil {
		return nil
	}
	return &sharedCredentialManager{manager: manager}
}

func (m *sharedCredentialManager) GetCredential(id string) *credentialmgr.Credential {
	if m == nil || m.manager == nil {
		return nil
	}
	cred := m.manager.GetCredential(id)
	if cred == nil {
		return nil
	}
	return cred.Credential.Clone()
}

func (m *sharedCredentialManager) ListCredentials(filter credentialmgr.Filter) []*credentialmgr.Credential {
	if m == nil || m.manager == nil {
		return nil
	}
	items := m.manager.ListCredentials(filter)
	out := make([]*credentialmgr.Credential, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, item.Credential.Clone())
	}
	return out
}

func (m *sharedCredentialManager) RegisterCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if m == nil || m.manager == nil {
		return nil
	}
	return m.manager.RegisterCredential(ctx, cred)
}

func (m *sharedCredentialManager) UpdateCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if m == nil || m.manager == nil {
		return nil
	}
	return m.manager.UpdateCredential(ctx, cred)
}

func (m *sharedCredentialManager) DeregisterCredential(ctx context.Context, id string) error {
	if m == nil || m.manager == nil {
		return nil
	}
	return m.manager.DeregisterCredential(ctx, id)
}

// Manager orchestrates credential lifecycle: registration, selection, result
// feedback, quota tracking, and optional persistence.
type Manager struct {
	credentialMgr CredentialManager

	mu             sync.RWMutex
	creds          map[string]*Credential         // credID -> Credential
	authenticators map[string]*authenticatorEntry // cli key -> Authenticator
	providerAuths  map[string]string              // providerType key -> cli key

	// Auto-refresh state.
	refreshCancel    context.CancelFunc
	refreshSemaphore chan struct{}
}

// NewManager constructs a CLI Authenticators Manager.
func NewManager(credMgr CredentialManager) *Manager {
	m := &Manager{
		credentialMgr:    credMgr,
		creds:            make(map[string]*Credential),
		authenticators:   make(map[string]*authenticatorEntry),
		providerAuths:    make(map[string]string),
		refreshSemaphore: make(chan struct{}, defaultRefreshMaxConcurrent),
	}
	return m
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

// Load reads all credentials from the store and registers them in memory.
// This should be called once during startup.
func (m *Manager) Load(ctx context.Context) error {
	if m.credentialMgr == nil {
		return nil
	}
	for _, common := range m.credentialMgr.ListCredentials(credentialmgr.Filter{Source: credentialmgr.SourceCLIAuthToken}) {
		cred := fromCommonCred(common)
		// StatusRefreshing is a transient in-process state. If it survived a
		// restart the refresh never completed; reset to Active so the refresh
		// loop re-evaluates it on the next cycle.
		if cred.Status == StatusRefreshing {
			cred.Status = StatusActive
		}
		m.mu.Lock()
		m.creds[cred.ID] = cred
		m.mu.Unlock()
	}
	return nil
}

// RegisterLoginCredential adds a new credential obtained from a CLI login flow to the manager
// and credential store. If the credential has no ID, one is generated. If a credential with
// the same ID already exists, it is replaced.
func (m *Manager) RegisterLoginCredential(ctx context.Context, cred *Credential) error {
	if cred == nil {
		return fmt.Errorf("manager: credential is nil")
	}
	if strings.TrimSpace(cred.ProviderType) == "" {
		return fmt.Errorf("manager: credential has no provider type")
	}

	cred = cred.Clone()
	if strings.TrimSpace(cred.ID) == "" {
		cred.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if cred.CreatedAt.IsZero() {
		cred.CreatedAt = now
	}
	cred.UpdatedAt = now
	if cred.Status == "" {
		cred.Status = StatusActive
	}
	if m.credentialMgr != nil {
		if err := m.credentialMgr.RegisterCredential(ctx, toCommonCred(cred, credentialmgr.SourceCLIAuthToken)); err != nil {
			return err
		}
	}

	m.mu.Lock()
	m.creds[cred.ID] = cred
	m.mu.Unlock()

	return nil
}

// UpdateCredential merges new state into an existing credential and optionally persists.
func (m *Manager) UpdateCredential(ctx context.Context, cred *Credential) error {
	if cred == nil {
		return fmt.Errorf("manager: credential is nil")
	}

	cred = cred.Clone()
	cred.UpdatedAt = time.Now().UTC()

	if m.credentialMgr != nil {
		if err := m.credentialMgr.UpdateCredential(ctx, toCommonCred(cred, credentialmgr.SourceCLIAuthToken)); err != nil {
			return err
		}
	}

	m.mu.Lock()
	m.creds[cred.ID] = cred
	m.mu.Unlock()

	return nil
}

// StartRefreshLoop starts a background goroutine that periodically checks for
// expiring credentials and calls the matching Authenticator's RefreshLead.
// Call StopRefreshLoop to shut it down.
func (m *Manager) StartRefreshLoop(ctx context.Context) {
	m.mu.Lock()
	if m.refreshCancel != nil {
		m.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	m.refreshCancel = cancel
	m.mu.Unlock()

	go m.refreshLoop(loopCtx)
}

// StopRefreshLoop stops the background refresh goroutine.
func (m *Manager) StopRefreshLoop() {
	m.mu.Lock()
	cancel := m.refreshCancel
	m.refreshCancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultRefreshCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runRefreshCycle(ctx)
		}
	}
}

func (m *Manager) runRefreshCycle(ctx context.Context) {
	now := time.Now()
	candidates := m.snapshotForRefresh(now)
	for _, cred := range candidates {
		auth := m.resolveAuthenticator(cred.ProviderType)
		if auth == nil {
			continue
		}

		select {
		case m.refreshSemaphore <- struct{}{}:
		default:
			// Semaphore full; skip this cycle.
			return
		}
		go func(c *Credential, a Authenticator) {
			defer func() { <-m.refreshSemaphore }()
			m.refreshOne(ctx, c, a, now)
		}(cred, auth)
	}
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

func (m *Manager) snapshotForRefresh(now time.Time) []*Credential {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var candidates []*Credential
	for _, cred := range m.creds {
		if cred.IsDisabled() {
			continue
		}
		if !needsRefresh(cred, now) {
			continue
		}
		candidates = append(candidates, cred.Clone())
	}
	return candidates
}

func needsRefresh(cred *Credential, now time.Time) bool {
	if cred.Status == StatusRefreshing {
		return false
	}
	if !cred.NextRefreshAfter.IsZero() && now.Before(cred.NextRefreshAfter) {
		return false
	}
	if exp, ok := cred.ExpirationTime(); ok {
		// Refresh 5 minutes before expiration.
		return now.After(exp.Add(-5 * time.Minute))
	}
	return false
}

func (m *Manager) refreshOne(ctx context.Context, cred *Credential, auth Authenticator, now time.Time) {
	// Mark as refreshing.
	refreshing := cred.Clone()
	refreshing.Status = StatusRefreshing
	refreshing.UpdatedAt = now
	_ = m.UpdateCredential(ctx, refreshing)

	updated, err := auth.RefreshLead(ctx, cred)
	if err != nil {
		// Refresh failed: mark error and schedule retry.
		failed := cred.Clone()
		failed.Status = StatusError
		failed.StatusMessage = err.Error()
		failed.NextRefreshAfter = time.Now().Add(5 * time.Minute)
		_ = m.UpdateCredential(ctx, failed)
		return
	}
	if updated == nil {
		// Authenticator returned nil: leave credential unchanged.
		restored := cred.Clone()
		restored.Status = StatusActive
		_ = m.UpdateCredential(ctx, restored)
		return
	}

	updated.LastRefreshedAt = time.Now().UTC()
	if updated.Status == "" || updated.Status == StatusRefreshing {
		updated.Status = StatusActive
	}
	_ = m.UpdateCredential(ctx, updated)
}

func toCommonCred(c *Credential, source string) *credentialmgr.Credential {
	if c == nil {
		return nil
	}
	if source == "" {
		source = credentialmgr.SourceCLIAuthToken
	}
	sc := c.Credential.Clone()
	sc.Source = source
	sc.Disabled = c.IsDisabled()
	return sc
}

func fromCommonCred(c *credentialmgr.Credential) *Credential {
	if c == nil {
		return nil
	}
	status := StatusActive
	if c.Disabled {
		status = StatusDisabled
	}
	out := &Credential{
		Credential: *c.Clone(),
		Status:     status,
	}
	if c.Disabled {
		out.Status = StatusDisabled
	}
	return out
}
