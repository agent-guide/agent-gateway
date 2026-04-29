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
	defaultRefreshMaxConcurrent = 8
	defaultRefreshLeadTime      = 5 * time.Minute
)

// AutoRefresher manages credential lifecycle: loading, registration, update,
// and automatic background refresh scheduling.
type AutoRefresher struct {
	manager       *Manager // for resolveAuthenticator during refresh cycles
	credentialMgr CredentialManager

	mu    sync.RWMutex
	creds map[string]*CLIAuthCredential

	dispatcher *RefreshJobDispatcher
	jobs       chan string

	refreshCancel context.CancelFunc
	concurrency   int
}

// NewAutoRefresher constructs an AutoRefresher.
// manager may be nil when authenticator-backed refresh is not needed.
func NewAutoRefresher(credMgr CredentialManager, manager *Manager) *AutoRefresher {
	concurrency := defaultRefreshMaxConcurrent
	jobBuffer := concurrency * 4
	if jobBuffer < 64 {
		jobBuffer = 64
	}
	r := &AutoRefresher{
		manager:       manager,
		credentialMgr: credMgr,
		creds:         make(map[string]*CLIAuthCredential),
		jobs:          make(chan string, jobBuffer),
		concurrency:   concurrency,
	}
	r.dispatcher = newRefreshJobDispatcher(r.jobs, r.nextScheduleAt)
	return r
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
	r.rebuildHeap()
	return nil
}

// RegisterLoginCredential adds a new credential obtained from a CLI login flow.
func (r *AutoRefresher) RegisterLoginCredential(ctx context.Context, cred *CLIAuthCredential) error {
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
	r.dispatcher.Enqueue(cred.ID)
	return nil
}

// updateCredential merges new state into an existing credential and optionally persists.
func (r *AutoRefresher) updateCredential(ctx context.Context, cred *CLIAuthCredential) error {
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
	r.dispatcher.Enqueue(cred.ID)
	return nil
}

// Start starts the background goroutines that periodically refresh expiring credentials.
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

	for i := 0; i < r.concurrency; i++ {
		go r.worker(loopCtx)
	}
	go r.dispatcher.DispatchLoop(loopCtx)
}

// Stop stops the background refresh goroutines.
func (r *AutoRefresher) Stop() {
	r.mu.Lock()
	cancel := r.refreshCancel
	r.refreshCancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsRunning reports whether the background refresh loop is currently active.
func (r *AutoRefresher) IsRunning() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.refreshCancel != nil
}

func (r *AutoRefresher) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case credID := <-r.jobs:
			if credID == "" {
				continue
			}
			cred, auth := r.resolveRefreshTarget(credID)
			if cred != nil && auth != nil {
				r.refreshOne(ctx, cred, auth, time.Now())
			}
			r.dispatcher.Enqueue(credID)
		}
	}
}

// rebuildHeap scans all loaded credentials and initialises the scheduler heap.
func (r *AutoRefresher) rebuildHeap() {
	now := time.Now()
	r.mu.RLock()
	ids := make([]string, 0, len(r.creds))
	for id, cred := range r.creds {
		if cred != nil {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()

	r.dispatcher.Reset(ids, now)
}

func (r *AutoRefresher) nextScheduleAt(credID string, now time.Time) (time.Time, bool) {
	r.mu.RLock()
	cred := r.creds[credID]
	r.mu.RUnlock()

	var leadTime *time.Duration
	if r.manager != nil && cred != nil {
		if auth := r.manager.resolveAuthenticator(cred.ProviderType); auth != nil {
			leadTime = auth.RefreshLeadTime()
		}
	}
	return nextScheduleAt(cred, now, leadTime)
}

func (r *AutoRefresher) resolveRefreshTarget(credID string) (*CLIAuthCredential, Authenticator) {
	if strings.TrimSpace(credID) == "" {
		return nil, nil
	}
	r.mu.RLock()
	cred := r.creds[credID]
	r.mu.RUnlock()
	if cred == nil {
		return nil, nil
	}
	cred = cred.Clone()
	if r.manager == nil {
		return cred, nil
	}
	return cred, r.manager.resolveAuthenticator(cred.ProviderType)
}

func (r *AutoRefresher) refreshOne(ctx context.Context, cred *CLIAuthCredential, auth Authenticator, now time.Time) {
	refreshing := cred.Clone()
	refreshing.Status = StatusRefreshing
	refreshing.UpdatedAt = now
	_ = r.updateCredential(ctx, refreshing)

	updatedCommon, err := auth.Refresh(ctx, cred.Credential.Clone())
	if err != nil {
		failed := cred.Clone()
		failed.Status = StatusError
		failed.StatusMessage = err.Error()
		failed.NextRefreshAfter = time.Now().Add(defaultRefreshLeadTime)
		_ = r.updateCredential(ctx, failed)
		return
	}
	if updatedCommon == nil {
		// Authenticator returned nil: leave credential unchanged.
		restored := cred.Clone()
		restored.Status = StatusActive
		_ = r.updateCredential(ctx, restored)
		return
	}

	updated := fromCommonCred(updatedCommon)
	if updated == nil {
		restored := cred.Clone()
		restored.Status = StatusActive
		_ = r.updateCredential(ctx, restored)
		return
	}
	updated.StatusMessage = ""
	updated.NextRefreshAfter = time.Time{}
	updated.LastRefreshedAt = time.Now().UTC()
	if updated.Status == "" || updated.Status == StatusRefreshing {
		updated.Status = StatusActive
	}
	_ = r.updateCredential(ctx, updated)
}

// nextScheduleAt returns when to next wake up and evaluate this credential.
// leadTime controls how early before token expiry to schedule a refresh;
// pass nil to disable background pre-refresh scheduling for this credential.
// Returns (zero, false) if the credential should not be tracked by the scheduler.
func nextScheduleAt(cred *CLIAuthCredential, now time.Time, leadTime *time.Duration) (time.Time, bool) {
	if cred == nil || cred.IsDisabled() {
		return time.Time{}, false
	}
	// While a refresh is in flight the worker will reschedule on completion.
	if cred.Status == StatusRefreshing {
		return time.Time{}, false
	}
	if !cred.NextRefreshAfter.IsZero() && now.Before(cred.NextRefreshAfter) {
		return cred.NextRefreshAfter, true
	}
	if leadTime == nil {
		return time.Time{}, false
	}
	lead := *leadTime
	if lead <= 0 {
		lead = defaultRefreshLeadTime
	}
	if exp, ok := cred.ExpirationTime(); ok {
		dueAt := exp.Add(-lead)
		if !dueAt.After(now) {
			return now, true
		}
		return dueAt, true
	}
	return time.Time{}, false
}

func toCommonCred(c *CLIAuthCredential, source string) *credentialmgr.Credential {
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

func fromCommonCred(c *credentialmgr.Credential) *CLIAuthCredential {
	out := NewCLIAuthCredential(c)
	if out == nil {
		return nil
	}
	if c.Disabled {
		out.Status = StatusDisabled
	}
	return out
}
