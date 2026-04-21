package credentialmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr/model"
	sched "github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr/scheduler"
	"github.com/google/uuid"
)

const (
	SourceAPIKey  = "api_key"
	SourceCLIAuth = "cliauth"

	defaultQuotaBackoffBase = time.Second
	defaultQuotaBackoffMax  = 30 * time.Minute
)

type Credential = model.Credential
type QuotaState = model.QuotaState
type ModelState = model.ModelState
type Error = model.Error

type Result struct {
	CredentialID string
	ProviderType string
	Model        string
	Success      bool
	RetryAfter   *time.Duration
	Error        *Error
}

type Filter struct {
	ProviderType string
	ProviderID   string
	Source       string
}

type Hook interface {
	OnCredentialRegistered(ctx context.Context, cred *Credential)
	OnCredentialUpdated(ctx context.Context, cred *Credential)
	OnResult(ctx context.Context, result Result)
}

type NoopHook struct{}

func (NoopHook) OnCredentialRegistered(context.Context, *Credential) {}
func (NoopHook) OnCredentialUpdated(context.Context, *Credential)    {}
func (NoopHook) OnResult(context.Context, Result)                    {}

type Manager struct {
	store     intf.CredentialStorer
	scheduler sched.CredentialScheduler
	strategy  sched.CredentialSelector
	hook      Hook

	mu    sync.RWMutex
	creds map[string]*Credential

	quotaCooldownDisabled bool
}

func NewManager(store intf.CredentialStorer, strategy sched.CredentialSelector, hook Hook) *Manager {
	if hook == nil {
		hook = NoopHook{}
	}
	return &Manager{
		store:     store,
		strategy:  strategy,
		hook:      hook,
		scheduler: sched.NewScheduler(strategy),
		creds:     make(map[string]*Credential),
	}
}

func DecodeCredential(data []byte) (any, error) {
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode credential object: %w", err)
	}
	return &c, nil
}

func (m *Manager) SetStrategy(strategy sched.CredentialSelector) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.strategy = strategy
	m.mu.Unlock()
	m.scheduler.SetStrategy(strategy)
}

func (m *Manager) SetQuotaCooldownDisabled(disable bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.quotaCooldownDisabled = disable
	m.mu.Unlock()
}

func (m *Manager) Load(ctx context.Context) error {
	if m == nil || m.store == nil {
		return nil
	}
	items, err := m.store.ListByProviderType(ctx, "")
	if err != nil {
		return fmt.Errorf("credential manager: load from store: %w", err)
	}
	for _, item := range items {
		cred, ok := item.(*Credential)
		if !ok || cred == nil {
			return fmt.Errorf("credential manager: unexpected credential type %T", item)
		}
		if err := m.RegisterCredential(WithSkipPersist(ctx), cred); err != nil {
			return fmt.Errorf("credential manager: register credential %s: %w", cred.ID, err)
		}
	}
	return nil
}

func (m *Manager) RegisterCredential(ctx context.Context, cred *Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}
	if strings.TrimSpace(cred.ProviderType) == "" {
		return fmt.Errorf("credential manager: credential has no provider type")
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

	if !shouldSkipPersist(ctx) {
		if err := m.createOrUpdate(ctx, cred); err != nil {
			return err
		}
	}

	m.mu.Lock()
	m.creds[cred.ID] = cred
	m.mu.Unlock()

	m.scheduler.RegisterCredential(cred.Clone())
	m.hook.OnCredentialRegistered(ctx, cred.Clone())
	return nil
}

func (m *Manager) UpdateCredential(ctx context.Context, cred *Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}

	cred = cred.Clone()
	cred.UpdatedAt = time.Now().UTC()
	if !shouldSkipPersist(ctx) {
		if err := m.update(ctx, cred); err != nil {
			return err
		}
	}

	m.mu.Lock()
	m.creds[cred.ID] = cred
	m.mu.Unlock()

	m.scheduler.UpdateCredential(cred.Clone())
	m.hook.OnCredentialUpdated(ctx, cred.Clone())
	return nil
}

func (m *Manager) DeregisterCredential(ctx context.Context, id string) error {
	if m == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("credential manager: id is empty")
	}

	m.mu.Lock()
	_, ok := m.creds[id]
	delete(m.creds, id)
	m.mu.Unlock()
	if !ok {
		return nil
	}

	m.scheduler.DeregisterCredential(id)
	if !shouldSkipPersist(ctx) && m.store != nil {
		if err := m.store.Delete(ctx, id); err != nil {
			return fmt.Errorf("credential manager: delete from store: %w", err)
		}
	}
	return nil
}

func (m *Manager) GetCredential(id string) *Credential {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cred := m.creds[id]; cred != nil {
		return cred.Clone()
	}
	return nil
}

func (m *Manager) ListCredentials(filter Filter) []*Credential {
	if m == nil {
		return nil
	}
	providerType := strings.ToLower(strings.TrimSpace(filter.ProviderType))
	providerID := strings.ToLower(strings.TrimSpace(filter.ProviderID))
	source := strings.ToLower(strings.TrimSpace(filter.Source))

	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Credential, 0, len(m.creds))
	for _, cred := range m.creds {
		if cred == nil {
			continue
		}
		if providerType != "" && strings.ToLower(cred.ProviderType) != providerType {
			continue
		}
		if providerID != "" && strings.ToLower(cred.ProviderID) != providerID {
			continue
		}
		if source != "" && strings.ToLower(cred.Source) != source {
			continue
		}
		out = append(out, cred.Clone())
	}
	return out
}

func (m *Manager) Pick(ctx context.Context, providerType, model string, tried map[string]struct{}) (*Credential, error) {
	return m.PickWithFilter(ctx, providerType, model, tried, Filter{})
}

func (m *Manager) PickWithFilter(ctx context.Context, providerType, model string, tried map[string]struct{}, filter Filter) (*Credential, error) {
	if m == nil {
		return nil, &Error{Code: "manager_nil", Message: "credential manager not initialized"}
	}
	localTried := tried
	for {
		cred, err := m.scheduler.Pick(ctx, providerType, model, localTried)
		if err != nil || cred == nil {
			return nil, err
		}
		m.mu.RLock()
		stored := m.creds[cred.ID]
		m.mu.RUnlock()
		if stored == nil {
			if localTried == nil {
				localTried = make(map[string]struct{})
			}
			localTried[cred.ID] = struct{}{}
			continue
		}
		if matchFilter(stored, filter) {
			return stored.Clone(), nil
		}
		if localTried == nil {
			localTried = make(map[string]struct{})
		}
		localTried[cred.ID] = struct{}{}
	}
}

func matchFilter(cred *Credential, filter Filter) bool {
	if cred == nil {
		return false
	}
	if providerType := strings.ToLower(strings.TrimSpace(filter.ProviderType)); providerType != "" && strings.ToLower(cred.ProviderType) != providerType {
		return false
	}
	if providerID := strings.ToLower(strings.TrimSpace(filter.ProviderID)); providerID != "" && strings.ToLower(cred.ProviderID) != providerID {
		return false
	}
	if source := strings.ToLower(strings.TrimSpace(filter.Source)); source != "" && strings.ToLower(cred.Source) != source {
		return false
	}
	return true
}

func (m *Manager) MarkResult(ctx context.Context, result Result) {
	if m == nil {
		return
	}
	credID := strings.TrimSpace(result.CredentialID)
	if credID == "" {
		m.hook.OnResult(ctx, result)
		return
	}

	m.mu.Lock()
	cred := m.creds[credID]
	if cred != nil {
		cred = cred.Clone()
	}
	disableCooldown := m.quotaCooldownDisabled
	m.mu.Unlock()
	if cred == nil {
		m.hook.OnResult(ctx, result)
		return
	}

	now := time.Now().UTC()
	changed := false
	if result.Success {
		if cred.Unavailable || cred.LastError != nil || cred.Quota.Exceeded {
			cred.Unavailable = false
			cred.LastError = nil
			cred.NextRetryAfter = time.Time{}
			cred.Quota = QuotaState{}
			cred.Disabled = false
			changed = true
		}
		if result.Model != "" {
			if state := cred.ModelStates[result.Model]; state != nil {
				if state.Unavailable || state.LastError != nil || state.Quota.Exceeded || state.Disabled {
					state.Unavailable = false
					state.LastError = nil
					state.NextRetryAfter = time.Time{}
					state.Quota = QuotaState{}
					state.Disabled = false
					state.UpdatedAt = now
					changed = true
				}
			}
		}
	} else if result.Error != nil {
		rerr := result.Error
		changed = true
		isQuota := rerr.HTTPStatus == http.StatusTooManyRequests
		if isQuota && !disableCooldown {
			backoffLevel := cred.Quota.BackoffLevel
			backoff := computeBackoff(backoffLevel, defaultQuotaBackoffBase, defaultQuotaBackoffMax)
			if result.RetryAfter != nil && *result.RetryAfter > backoff {
				backoff = *result.RetryAfter
			}
			cred.Quota = QuotaState{
				Exceeded:      true,
				Reason:        rerr.Message,
				NextRecoverAt: now.Add(backoff),
				BackoffLevel:  backoffLevel + 1,
			}
			cred.Unavailable = true
			cred.NextRetryAfter = now.Add(backoff)
		} else if rerr.HTTPStatus == http.StatusUnauthorized || rerr.HTTPStatus == http.StatusForbidden {
			cred.Disabled = true
		} else {
			cred.LastError = rerr
			if result.RetryAfter != nil {
				cred.Unavailable = true
				cred.NextRetryAfter = now.Add(*result.RetryAfter)
			}
		}

		if result.Model != "" {
			if cred.ModelStates == nil {
				cred.ModelStates = make(map[string]*ModelState)
			}
			state := cred.ModelStates[result.Model]
			if state == nil {
				state = &ModelState{}
				cred.ModelStates[result.Model] = state
			}
			state.UpdatedAt = now
			if isQuota && !disableCooldown {
				state.Quota = cred.Quota
				state.Unavailable = true
				state.NextRetryAfter = cred.NextRetryAfter
			} else if rerr.HTTPStatus == http.StatusUnauthorized || rerr.HTTPStatus == http.StatusForbidden {
				state.Disabled = true
			} else {
				state.LastError = rerr
				if result.RetryAfter != nil {
					state.Unavailable = true
					state.NextRetryAfter = now.Add(*result.RetryAfter)
				}
			}
		}
	}

	if changed {
		cred.UpdatedAt = now
		_ = m.UpdateCredential(ctx, cred)
	}
	m.hook.OnResult(ctx, result)
}

func (m *Manager) create(ctx context.Context, cred *Credential) error {
	if m.store == nil {
		return nil
	}
	if _, err := m.store.Create(ctx, cred.ID, cred.ProviderType, cred); err != nil {
		return fmt.Errorf("credential manager: create credential %s: %w", cred.ID, err)
	}
	return nil
}

func (m *Manager) createOrUpdate(ctx context.Context, cred *Credential) error {
	if m.store == nil {
		return nil
	}
	if err := m.create(ctx, cred); err == nil {
		return nil
	}
	return m.update(ctx, cred)
}

func (m *Manager) update(ctx context.Context, cred *Credential) error {
	if m.store == nil {
		return nil
	}
	if err := m.store.Update(ctx, cred.ID, cred); err != nil {
		return fmt.Errorf("credential manager: update credential %s: %w", cred.ID, err)
	}
	return nil
}

func computeBackoff(level int, base, maxBackoff time.Duration) time.Duration {
	if level <= 0 {
		return base
	}
	d := base
	for i := 0; i < level && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}
