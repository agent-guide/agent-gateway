package cliauth

import (
	"context"
	"fmt"
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

// AutoRefresher manages credential lifecycle: loading, registration, update,
// and automatic background refresh scheduling.
type AutoRefresher struct {
	manager       *Manager // for resolveAuthenticator during refresh cycles
	credentialMgr CredentialManager

	mu    sync.RWMutex
	creds map[string]*Credential

	refreshCancel    context.CancelFunc
	refreshSemaphore chan struct{}
}

// NewAutoRefresher constructs an AutoRefresher.
// manager may be nil when authenticator-backed refresh is not needed.
func NewAutoRefresher(credMgr CredentialManager, manager *Manager) *AutoRefresher {
	return &AutoRefresher{
		manager:          manager,
		credentialMgr:    credMgr,
		creds:            make(map[string]*Credential),
		refreshSemaphore: make(chan struct{}, defaultRefreshMaxConcurrent),
	}
}

// Load reads all CLI-auth credentials from the store and registers them in memory.
// Call once during startup.
func (r *AutoRefresher) Load(ctx context.Context) error {
	if r.credentialMgr == nil {
		return nil
	}
	for _, common := range r.credentialMgr.ListCredentials(credentialmgr.Filter{Source: credentialmgr.SourceCLIAuthToken}) {
		cred := fromCommonCred(common)
		// StatusRefreshing is transient; reset to Active so the refresh loop
		// re-evaluates it on the next cycle after a restart.
		if cred.Status == StatusRefreshing {
			cred.Status = StatusActive
		}
		r.mu.Lock()
		r.creds[cred.ID] = cred
		r.mu.Unlock()
	}
	return nil
}

// RegisterLoginCredential adds a new credential obtained from a CLI login flow.
func (r *AutoRefresher) RegisterLoginCredential(ctx context.Context, cred *Credential) error {
	if cred == nil {
		return fmt.Errorf("cliauth: credential is nil")
	}
	if strings.TrimSpace(cred.ProviderType) == "" {
		return fmt.Errorf("cliauth: credential has no provider type")
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
	if r.credentialMgr != nil {
		if err := r.credentialMgr.RegisterCredential(ctx, toCommonCred(cred, credentialmgr.SourceCLIAuthToken)); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.creds[cred.ID] = cred
	r.mu.Unlock()
	return nil
}

// updateCredential merges new state into an existing credential and optionally persists.
func (r *AutoRefresher) updateCredential(ctx context.Context, cred *Credential) error {
	if cred == nil {
		return fmt.Errorf("cliauth: credential is nil")
	}

	cred = cred.Clone()
	cred.UpdatedAt = time.Now().UTC()

	if r.credentialMgr != nil {
		if err := r.credentialMgr.UpdateCredential(ctx, toCommonCred(cred, credentialmgr.SourceCLIAuthToken)); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.creds[cred.ID] = cred
	r.mu.Unlock()
	return nil
}

// Start starts the background goroutine that periodically refreshes expiring credentials.
// Call Stop to shut it down.
func (r *AutoRefresher) Start(ctx context.Context) {
	r.mu.Lock()
	if r.refreshCancel != nil {
		r.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	r.refreshCancel = cancel
	r.mu.Unlock()

	go r.refreshLoop(loopCtx)
}

// Stop stops the background refresh goroutine.
func (r *AutoRefresher) Stop() {
	r.mu.Lock()
	cancel := r.refreshCancel
	r.refreshCancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *AutoRefresher) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultRefreshCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runRefreshCycle(ctx)
		}
	}
}

func (r *AutoRefresher) runRefreshCycle(ctx context.Context) {
	now := time.Now()
	candidates := r.snapshotForRefresh(now)
	for _, cred := range candidates {
		if r.manager == nil {
			continue
		}
		auth := r.manager.resolveAuthenticator(cred.ProviderType)
		if auth == nil {
			continue
		}

		select {
		case r.refreshSemaphore <- struct{}{}:
		default:
			// Semaphore full; skip this cycle.
			return
		}
		go func(c *Credential, a Authenticator) {
			defer func() { <-r.refreshSemaphore }()
			r.refreshOne(ctx, c, a, now)
		}(cred, auth)
	}
}

func (r *AutoRefresher) snapshotForRefresh(now time.Time) []*Credential {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var candidates []*Credential
	for _, cred := range r.creds {
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

func (r *AutoRefresher) refreshOne(ctx context.Context, cred *Credential, auth Authenticator, now time.Time) {
	refreshing := cred.Clone()
	refreshing.Status = StatusRefreshing
	refreshing.UpdatedAt = now
	_ = r.updateCredential(ctx, refreshing)

	updated, err := auth.RefreshLead(ctx, cred)
	if err != nil {
		failed := cred.Clone()
		failed.Status = StatusError
		failed.StatusMessage = err.Error()
		failed.NextRefreshAfter = time.Now().Add(5 * time.Minute)
		_ = r.updateCredential(ctx, failed)
		return
	}
	if updated == nil {
		// Authenticator returned nil: leave credential unchanged.
		restored := cred.Clone()
		restored.Status = StatusActive
		_ = r.updateCredential(ctx, restored)
		return
	}

	updated.LastRefreshedAt = time.Now().UTC()
	if updated.Status == "" || updated.Status == StatusRefreshing {
		updated.Status = StatusActive
	}
	_ = r.updateCredential(ctx, updated)
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
	out := &Credential{
		Credential: *c.Clone(),
		Status:     StatusActive,
	}
	if c.Disabled {
		out.Status = StatusDisabled
	}
	return out
}
