package credentialmgr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	sched "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
)

type testCredentialLifecycleListener struct {
	registered   []*ManagedCredential
	updated      []*ManagedCredential
	deregistered []*ManagedCredential
	replaced     [][]*ManagedCredential
}

func (l *testCredentialLifecycleListener) OnCredentialRegistered(_ context.Context, cred *ManagedCredential) {
	if l == nil || cred == nil {
		return
	}
	l.registered = append(l.registered, cred.Clone())
}

func (l *testCredentialLifecycleListener) OnCredentialUpdated(_ context.Context, cred *ManagedCredential) {
	if l == nil || cred == nil {
		return
	}
	l.updated = append(l.updated, cred.Clone())
}

func (l *testCredentialLifecycleListener) OnCredentialDeregistered(_ context.Context, cred *ManagedCredential) {
	if l == nil || cred == nil {
		return
	}
	l.deregistered = append(l.deregistered, cred.Clone())
}

func (l *testCredentialLifecycleListener) OnCredentialsReplaced(_ context.Context, creds []*ManagedCredential) {
	if l == nil {
		return
	}
	snapshot := make([]*ManagedCredential, 0, len(creds))
	for _, cred := range creds {
		if cred == nil {
			continue
		}
		snapshot = append(snapshot, cred.Clone())
	}
	l.replaced = append(l.replaced, snapshot)
}

type testCredentialStore struct {
	items []any
}

func (s *testCredentialStore) List(ctx context.Context) ([]any, error) {
	return s.ListByTagPrefix(ctx, "")
}

func (s *testCredentialStore) ListByTag(_ context.Context, _ string) ([]any, error) {
	return s.ListByTagPrefix(context.Background(), "")
}

func (s *testCredentialStore) ListByTagPrefix(_ context.Context, _ string) ([]any, error) {
	out := make([]any, len(s.items))
	copy(out, s.items)
	return out, nil
}

func (s *testCredentialStore) Create(_ context.Context, _ any) error {
	return nil
}

func (s *testCredentialStore) Update(_ context.Context, _ any) error {
	return nil
}

func (s *testCredentialStore) Delete(_ context.Context, _ ...any) error {
	return nil
}

func (s *testCredentialStore) Get(_ context.Context, _ ...any) (any, error) {
	return nil, nil
}

func (s *testCredentialStore) GetByIndex(context.Context, string, any) (any, error) {
	return nil, nil
}

type testManualRefresher struct {
	refreshFn func(context.Context, *Credential) (*Credential, error)
}

func (r *testManualRefresher) Refresh(ctx context.Context, cred *Credential) (*Credential, error) {
	if r == nil || r.refreshFn == nil {
		return nil, nil
	}
	return r.refreshFn(ctx, cred)
}

func newTestScheduler(t *testing.T, mgr *Manager) sched.CredentialScheduler {
	t.Helper()
	scheduler := sched.NewScheduler(nil)
	listener, ok := scheduler.(CredentialLifecycleListener)
	if !ok {
		t.Fatal("scheduler does not implement CredentialLifecycleListener")
	}
	mgr.AddListener(testScopeListener{next: listener})
	scheduler.Rebuild(mgr.ListCredentials(Filter{}))
	return scheduler
}

type testScopeListener struct {
	next CredentialLifecycleListener
}

func (l testScopeListener) OnCredentialRegistered(ctx context.Context, cred *ManagedCredential) {
	l.next.OnCredentialRegistered(ctx, withTestScope(cred))
}

func (l testScopeListener) OnCredentialUpdated(ctx context.Context, cred *ManagedCredential) {
	l.next.OnCredentialUpdated(ctx, withTestScope(cred))
}

func (l testScopeListener) OnCredentialDeregistered(ctx context.Context, cred *ManagedCredential) {
	l.next.OnCredentialDeregistered(ctx, withTestScope(cred))
}

func (l testScopeListener) OnCredentialsReplaced(ctx context.Context, creds []*ManagedCredential) {
	out := make([]*ManagedCredential, 0, len(creds))
	for _, cred := range creds {
		out = append(out, withTestScope(cred))
	}
	l.next.OnCredentialsReplaced(ctx, out)
}

func withTestScope(cred *ManagedCredential) *ManagedCredential {
	if cred == nil {
		return nil
	}
	if cred.ScopeValue() == "" {
		cred.Scope = ProviderIDCredentialScope(cred.ProviderID)
	}
	return cred
}

func TestPickSelectsRequestedSource(t *testing.T) {
	mgr := NewManager(nil)
	scheduler := newTestScheduler(t, mgr)
	for _, cred := range []*Credential{
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Type: TypeAPIKey},
		{ID: "cliauth", ProviderType: "openai", ProviderID: "openai", Type: TypeCLIAuthToken},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	picked, err := scheduler.Pick(context.Background(), sched.Filter{
		Type:            TypeCLIAuthToken,
		CredentialScope: "id:openai",
		Model:           "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if picked.ID != "cliauth" {
		t.Fatalf("picked credential = %q, want cliauth", picked.ID)
	}
}

func TestPickSelectsRequestedProviderID(t *testing.T) {
	mgr := NewManager(nil)
	scheduler := newTestScheduler(t, mgr)
	for _, cred := range []*Credential{
		{ID: "openai-main", ProviderType: "openai", ProviderID: "openai-main", Type: TypeAPIKey},
		{ID: "openai-backup", ProviderType: "openai", ProviderID: "openai-backup", Type: TypeAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	picked, err := scheduler.Pick(context.Background(), sched.Filter{
		Type:            TypeAPIKey,
		CredentialScope: "id:openai-main",
		Model:           "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if picked.ID != "openai-main" {
		t.Fatalf("picked credential = %q, want openai-main", picked.ID)
	}
}

func TestPickRejectsMissingCredentialScope(t *testing.T) {
	mgr := NewManager(nil)
	scheduler := newTestScheduler(t, mgr)

	_, err := scheduler.Pick(context.Background(), sched.Filter{
		Model: "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("Pick returned nil error, want credential_scope requirement")
	}
}

func TestPickReturnsNotFoundWhenFilteredCandidatesAbsent(t *testing.T) {
	mgr := NewManager(nil)
	scheduler := newTestScheduler(t, mgr)
	for _, cred := range []*Credential{
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Type: TypeAPIKey},
		{ID: "api-key-2", ProviderType: "openai", ProviderID: "openai", Type: TypeAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	_, err := scheduler.Pick(context.Background(), sched.Filter{
		Type:            TypeCLIAuthToken,
		CredentialScope: "id:openai",
		Model:           "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("Pick returned nil error, want credential_not_found")
	}

	var credErr *Error
	if !errors.As(err, &credErr) {
		t.Fatalf("Pick error type = %T, want *Error", err)
	}
	if credErr.Code != "credential_not_found" {
		t.Fatalf("error code = %q, want credential_not_found", credErr.Code)
	}
}

func TestPickReturnsCooldownWhenFilteredCandidatesAllCoolingDown(t *testing.T) {
	mgr := NewManager(nil)
	scheduler := newTestScheduler(t, mgr)
	for _, cred := range []*Credential{
		{ID: "cli-1", ProviderType: "openai", ProviderID: "openai", Type: TypeCLIAuthToken},
		{ID: "cli-2", ProviderType: "openai", ProviderID: "openai", Type: TypeCLIAuthToken},
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Type: TypeAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	for _, id := range []string{"cli-1", "cli-2"} {
		scheduler.MarkResult(context.Background(), sched.Result{
			CredentialID: id,
			Model:        "gpt-test",
			Error: &sched.Error{
				Message:    "quota exceeded",
				HTTPStatus: http.StatusTooManyRequests,
				Retryable:  true,
			},
		})
	}

	_, err := scheduler.Pick(context.Background(), sched.Filter{
		Type:            TypeCLIAuthToken,
		CredentialScope: "id:openai",
		Model:           "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("Pick returned nil error, want cooldown error")
	}

	type statusCoder interface {
		StatusCode() int
	}
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("Pick error type = %T, want status code capable error", err)
	}
	if sc.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want %d", sc.StatusCode(), http.StatusTooManyRequests)
	}
	if !strings.Contains(err.Error(), "retry after") {
		t.Fatalf("error message = %q, want retry-after hint", err.Error())
	}
}

func TestPickReturnsUnavailableWhenFilteredCandidateBlockedButNotCoolingDown(t *testing.T) {
	mgr := NewManager(nil)
	scheduler := newTestScheduler(t, mgr)
	for _, cred := range []*Credential{
		{ID: "cli-1", ProviderType: "openai", ProviderID: "openai", Type: TypeCLIAuthToken},
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Type: TypeAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	retryAfter := 5 * time.Second
	scheduler.MarkResult(context.Background(), sched.Result{
		CredentialID: "cli-1",
		Model:        "gpt-test",
		RetryAfter:   &retryAfter,
		Error: &sched.Error{
			Message:    "upstream temporarily unavailable",
			HTTPStatus: http.StatusServiceUnavailable,
			Retryable:  true,
		},
	})

	_, err := scheduler.Pick(context.Background(), sched.Filter{
		Type:            TypeCLIAuthToken,
		CredentialScope: "id:openai",
		Model:           "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("Pick returned nil error, want credential_unavailable")
	}

	var credErr *Error
	if !errors.As(err, &credErr) {
		t.Fatalf("Pick error type = %T, want *Error", err)
	}
	if credErr.Code != "credential_unavailable" {
		t.Fatalf("error code = %q, want credential_unavailable", credErr.Code)
	}
}

func TestMarkResultAppliesQuotaCooldown(t *testing.T) {
	mgr := NewManager(nil)
	scheduler := newTestScheduler(t, mgr)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Type:         TypeAPIKey,
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	scheduler.MarkResult(context.Background(), sched.Result{
		CredentialID: "cred-1",
		Model:        "gpt-test",
		Error: &sched.Error{
			Message:    "quota exceeded",
			HTTPStatus: http.StatusTooManyRequests,
			Retryable:  true,
		},
	})

	cred := mgr.GetCredential("cred-1")
	if cred == nil {
		t.Fatal("credential not found")
	}
	if !cred.Unavailable || !cred.Quota.Exceeded || cred.NextRetryAfter.IsZero() {
		t.Fatalf("credential was not marked cooling down: %+v", cred)
	}
	state := cred.ModelStates["gpt-test"]
	if state == nil || !state.Unavailable || !state.Quota.Exceeded {
		t.Fatalf("model state was not marked cooling down: %+v", state)
	}
}

func TestManagerNotifiesSchedulerAndExternalLifecycleListener(t *testing.T) {
	listener := &testCredentialLifecycleListener{}
	scheduler := sched.NewScheduler(nil)
	mgr := NewManager(nil)
	if schedulerListener, ok := scheduler.(CredentialLifecycleListener); ok {
		mgr.AddListener(testScopeListener{next: schedulerListener})
	}
	mgr.AddListener(listener)

	cred := &Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		ProviderID:   "openai-main",
		Type:         TypeAPIKey,
	}
	if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	picked, err := scheduler.Pick(context.Background(), sched.Filter{
		CredentialScope: "id:openai-main",
		Model:           "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("pick registered credential: %v", err)
	}
	if picked.ID != cred.ID {
		t.Fatalf("picked credential = %q, want %q", picked.ID, cred.ID)
	}

	updated := cred.Clone()
	updated.Label = "updated"
	if err := mgr.UpdateCredential(context.Background(), updated); err != nil {
		t.Fatalf("update credential: %v", err)
	}
	if got := mgr.GetCredential(cred.ID); got == nil || got.Label != "updated" {
		t.Fatalf("stored credential label = %q, want updated", got.Label)
	}

	if err := mgr.DeregisterCredential(context.Background(), cred.ID); err != nil {
		t.Fatalf("deregister credential: %v", err)
	}
	_, err = scheduler.Pick(context.Background(), sched.Filter{
		CredentialScope: "id:openai-main",
		Model:           "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("pick after deregister returned nil error, want not found")
	}

	if len(listener.registered) != 1 || listener.registered[0].ID != cred.ID {
		t.Fatalf("registered events = %#v, want one event for %q", listener.registered, cred.ID)
	}
	if len(listener.updated) != 1 || listener.updated[0].Label != "updated" {
		t.Fatalf("updated events = %#v, want one updated event", listener.updated)
	}
	if len(listener.deregistered) != 1 || listener.deregistered[0].ID != cred.ID {
		t.Fatalf("deregistered events = %#v, want one event for %q", listener.deregistered, cred.ID)
	}
}

func TestReloadFromStoreReplacesManagerStateAndRebuildsScheduler(t *testing.T) {
	store := &testCredentialStore{
		items: []any{
			&Credential{
				ID:           "cred-new",
				ProviderType: "openai",
				ProviderID:   "openai-main",
				Type:         TypeAPIKey,
			},
		},
	}
	listener := &testCredentialLifecycleListener{}
	scheduler := sched.NewScheduler(nil)
	mgr := NewManager(store)
	if schedulerListener, ok := scheduler.(CredentialLifecycleListener); ok {
		mgr.AddListener(testScopeListener{next: schedulerListener})
	}
	mgr.AddListener(listener)

	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cred-old",
		ProviderType: "openai",
		ProviderID:   "openai-main",
		Type:         TypeAPIKey,
	}); err != nil {
		t.Fatalf("register old credential: %v", err)
	}

	if err := mgr.ReloadFromStore(context.Background()); err != nil {
		t.Fatalf("ReloadFromStore returned error: %v", err)
	}

	if got := mgr.GetCredential("cred-old"); got != nil {
		t.Fatalf("old credential still present after reload: %+v", got)
	}
	if got := mgr.GetCredential("cred-new"); got == nil {
		t.Fatal("new credential missing after reload")
	}

	picked, err := scheduler.Pick(context.Background(), sched.Filter{
		CredentialScope: "id:openai-main",
		Model:           "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("pick reloaded credential: %v", err)
	}
	if picked.ID != "cred-new" {
		t.Fatalf("picked credential = %q, want cred-new", picked.ID)
	}

	if len(listener.replaced) != 1 {
		t.Fatalf("replaced events = %d, want 1", len(listener.replaced))
	}
	if len(listener.replaced[0]) != 1 || listener.replaced[0][0].ID != "cred-new" {
		t.Fatalf("replaced snapshot = %#v, want only cred-new", listener.replaced[0])
	}
}

func TestRefreshCredentialIfNeededRefreshesExpiredCLIAuthCredential(t *testing.T) {
	mgr := NewManager(nil)
	mgr.SetManualRefresher("codex", &testManualRefresher{
		refreshFn: func(_ context.Context, cred *Credential) (*Credential, error) {
			updated := cred.Clone()
			updated.Attributes["api_key"] = "fresh-key"
			updated.Metadata["expired"] = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
			return updated, nil
		},
	})

	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cli-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Type:         TypeCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-key",
		},
		Metadata: map[string]any{
			MetadataManualRefreshNameKey: "codex",
			"expired":                    expired,
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	updated, err := mgr.RefreshCredentialIfNeeded(context.Background(), "cli-1")
	if err != nil {
		t.Fatalf("RefreshCredentialIfNeeded returned error: %v", err)
	}
	if updated == nil {
		t.Fatal("RefreshCredentialIfNeeded returned nil credential")
	}
	if got := updated.Attributes["api_key"]; got != "fresh-key" {
		t.Fatalf("api_key = %q, want fresh-key", got)
	}
}

func TestRefreshCredentialIfNeededSkipsWhenNoMatchingManualRefresher(t *testing.T) {
	mgr := NewManager(nil)

	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cli-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Type:         TypeCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-key",
		},
		Metadata: map[string]any{
			MetadataManualRefreshNameKey: "codex",
			"expired":                    expired,
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	updated, err := mgr.RefreshCredentialIfNeeded(context.Background(), "cli-1")
	if err != nil {
		t.Fatalf("RefreshCredentialIfNeeded returned error: %v", err)
	}
	if updated == nil {
		t.Fatal("RefreshCredentialIfNeeded returned nil credential")
	}
	if got := updated.Attributes["api_key"]; got != "stale-key" {
		t.Fatalf("api_key = %q, want stale-key", got)
	}
}

func TestRefreshCredentialIfNeededHonorsCredentialSpecificExpiryDelta(t *testing.T) {
	mgr := NewManager(nil)
	refreshed := false
	mgr.SetManualRefresher("gemini", &testManualRefresher{
		refreshFn: func(_ context.Context, cred *Credential) (*Credential, error) {
			refreshed = true
			updated := cred.Clone()
			updated.Attributes["api_key"] = "fresh-key"
			return updated, nil
		},
	})

	expiry := time.Now().UTC().Add(20 * time.Second).Format(time.RFC3339)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cli-1",
		ProviderType: "gemini",
		ProviderID:   "gemini",
		Type:         TypeCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-key",
		},
		Metadata: map[string]any{
			MetadataManualRefreshNameKey:     "gemini",
			MetadataManualRefreshExpiryDelta: "10s",
			"expired":                        expiry,
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	updated, err := mgr.RefreshCredentialIfNeeded(context.Background(), "cli-1")
	if err != nil {
		t.Fatalf("RefreshCredentialIfNeeded returned error: %v", err)
	}
	if updated == nil {
		t.Fatal("RefreshCredentialIfNeeded returned nil credential")
	}
	if refreshed {
		t.Fatal("expected credential-specific delta to skip refresh")
	}
	if got := updated.Attributes["api_key"]; got != "stale-key" {
		t.Fatalf("api_key = %q, want stale-key", got)
	}
}

func TestCredentialMarshalsProviderTypeAndProviderID(t *testing.T) {
	cred := &Credential{
		ID:           "cred-1",
		ProviderType: "zhipu",
		ProviderID:   "zhipu-test",
	}

	raw, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if got["provider_type"] != "zhipu" {
		t.Fatalf("provider_type = %#v, want zhipu", got["provider_type"])
	}
	if got["provider_id"] != "zhipu-test" {
		t.Fatalf("provider_id = %#v, want zhipu-test", got["provider_id"])
	}
	if _, exists := got["provider"]; exists {
		t.Fatalf("legacy provider field should not be emitted: %#v", got)
	}
}

func TestRegisterCredentialRejectsEmptyProviderID(t *testing.T) {
	mgr := NewManager(nil)

	err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		Type:         TypeAPIKey,
	})
	if err == nil {
		t.Fatal("RegisterCredential returned nil error, want provider_id validation failure")
	}
	if !strings.Contains(err.Error(), "provider_id is required") {
		t.Fatalf("error = %q, want provider_id validation failure", err)
	}
}

func TestUpdateCredentialRejectsEmptyProviderID(t *testing.T) {
	mgr := NewManager(nil)

	err := mgr.UpdateCredential(context.Background(), &Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		Type:         TypeAPIKey,
	})
	if err == nil {
		t.Fatal("UpdateCredential returned nil error, want provider_id validation failure")
	}
	if !strings.Contains(err.Error(), "provider_id is required") {
		t.Fatalf("error = %q, want provider_id validation failure", err)
	}
}
