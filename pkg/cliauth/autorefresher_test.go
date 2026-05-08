package cliauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr"
)

type testRefreshAuthenticator struct {
	providerType string
	refreshFn    func(context.Context, *credentialmgr.Credential) (*credentialmgr.Credential, error)
	config       AuthenticatorConfig
}

func (a *testRefreshAuthenticator) ProviderType() string { return a.providerType }

func (a *testRefreshAuthenticator) GetConfig() AuthenticatorConfig {
	if a == nil {
		return AuthenticatorConfig{}
	}
	return a.config
}

func (a *testRefreshAuthenticator) SetConfig(cfg AuthenticatorConfig) error {
	if a == nil {
		return nil
	}
	a.config = cfg
	return nil
}

func (a *testRefreshAuthenticator) Login(context.Context, LoginStatusReporter) (*credentialmgr.Credential, error) {
	return nil, errors.New("not implemented")
}

func (a *testRefreshAuthenticator) Refresh(ctx context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error) {
	if a.refreshFn == nil {
		return cred, nil
	}
	return a.refreshFn(ctx, cred)
}

func (a *testRefreshAuthenticator) RefreshLeadTime() *time.Duration { return nil }

type stubCredentialManager struct {
	getFn      func(string) *credentialmgr.Credential
	listFn     func(credentialmgr.Filter) []*credentialmgr.Credential
	registerFn func(context.Context, *credentialmgr.Credential) error
	updateFn   func(context.Context, *credentialmgr.Credential) error
	deleteFn   func(context.Context, string) error
}

func (s *stubCredentialManager) GetCredential(id string) *credentialmgr.Credential {
	if s.getFn == nil {
		return nil
	}
	return s.getFn(id)
}

func (s *stubCredentialManager) ListCredentials(filter credentialmgr.Filter) []*credentialmgr.Credential {
	if s.listFn == nil {
		return nil
	}
	return s.listFn(filter)
}

func (s *stubCredentialManager) RegisterCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if s.registerFn == nil {
		return nil
	}
	return s.registerFn(ctx, cred)
}

func (s *stubCredentialManager) UpdateCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if s.updateFn == nil {
		return nil
	}
	return s.updateFn(ctx, cred)
}

func (s *stubCredentialManager) DeregisterCredential(ctx context.Context, id string) error {
	if s.deleteFn == nil {
		return nil
	}
	return s.deleteFn(ctx, id)
}

type stubCredentialStore struct {
	createCalls int
	updateCalls int
	lastCreated *credentialmgr.Credential
	lastUpdated *credentialmgr.Credential
}

func (s *stubCredentialStore) ListByProviderType(context.Context, string) ([]any, error) {
	return nil, nil
}

func (s *stubCredentialStore) Create(_ context.Context, _ string, _ string, obj any) (string, error) {
	s.createCalls++
	cred, _ := obj.(*credentialmgr.Credential)
	s.lastCreated = cred
	if cred == nil {
		return "", nil
	}
	return cred.ID, nil
}

func (s *stubCredentialStore) Update(_ context.Context, _ string, obj any) error {
	s.updateCalls++
	cred, _ := obj.(*credentialmgr.Credential)
	s.lastUpdated = cred
	return nil
}

func (s *stubCredentialStore) Delete(context.Context, string) error {
	return nil
}

func (s *stubCredentialStore) Get(context.Context, string) (string, any, error) {
	return "", nil, nil
}

func TestRegisterCredentialPersistsWithCreate(t *testing.T) {
	store := &stubCredentialStore{}
	credMgr := credentialmgr.NewManager(store)
	refresher := NewAutoRefresher(WrapSharedCredentialManager(credMgr), nil)

	if err := refresher.RegisterLoginCredential(context.Background(), &CLIAuthCredential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "openai",
			ProviderID:   "openai-main",
		},
	}); err != nil {
		t.Fatalf("RegisterLoginCredential returned error: %v", err)
	}

	if store.createCalls != 1 {
		t.Fatalf("Create called %d times, want 1", store.createCalls)
	}
	if store.updateCalls != 0 {
		t.Fatalf("Update called %d times, want 0", store.updateCalls)
	}
}

func TestUpdateCredentialPersistsWithUpdate(t *testing.T) {
	store := &stubCredentialStore{}
	credMgr := credentialmgr.NewManager(store)
	refresher := NewAutoRefresher(WrapSharedCredentialManager(credMgr), nil)

	if err := refresher.updateCredential(context.Background(), &CLIAuthCredential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "openai",
			ProviderID:   "openai-main",
		},
	}); err != nil {
		t.Fatalf("UpdateCredential returned error: %v", err)
	}

	if store.updateCalls != 1 {
		t.Fatalf("Update called %d times, want 1", store.updateCalls)
	}
	if store.createCalls != 0 {
		t.Fatalf("Create called %d times, want 0", store.createCalls)
	}
}

func TestCredentialManagerReturnsUpdatedCredentialSnapshot(t *testing.T) {
	commonMgr := credentialmgr.NewManager(nil)
	refresher := NewAutoRefresher(WrapSharedCredentialManager(commonMgr), nil)

	if err := refresher.RegisterLoginCredential(context.Background(), &CLIAuthCredential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Attributes: map[string]string{
				"api_key": "old-key",
			},
		},
	}); err != nil {
		t.Fatalf("RegisterLoginCredential returned error: %v", err)
	}

	if err := refresher.updateCredential(context.Background(), &CLIAuthCredential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Attributes: map[string]string{
				"api_key": "new-key",
			},
		},
	}); err != nil {
		t.Fatalf("UpdateCredential returned error: %v", err)
	}

	picked := commonMgr.GetCredential("cred-1")
	if picked == nil {
		t.Fatal("GetCredential returned nil")
	}
	if got := picked.Attributes["api_key"]; got != "new-key" {
		t.Fatalf("GetCredential returned stale credential api key: got %q want new-key", got)
	}
}

func TestAutoRefresherAcceptsCredentialManagerInterface(t *testing.T) {
	refresher := NewAutoRefresher(&stubCredentialManager{
		listFn: func(filter credentialmgr.Filter) []*credentialmgr.Credential {
			if filter.Source != credentialmgr.SourceCLIAuthToken {
				t.Fatalf("Load filter source = %q, want %q", filter.Source, credentialmgr.SourceCLIAuthToken)
			}
			return []*credentialmgr.Credential{{
				ID:           "cred-1",
				ProviderType: "openai",
				ProviderID:   "openai-main",
				Source:       credentialmgr.SourceCLIAuthToken,
			}}
		},
	}, nil)

	if err := refresher.Load(context.Background()); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if n := len(refresher.dispatcher.heapIndex); n != 0 {
		t.Fatalf("heap should have no scheduled credentials (no expiry set), got %d", n)
	}
	if _, ok := refresher.creds["cred-1"]; !ok {
		t.Fatal("Load did not store credential from interface-backed manager")
	}
}

func TestNextScheduleAtEncodesRefreshReadiness(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	future := now.Add(30 * time.Minute)

	tests := []struct {
		name          string
		cred          *CLIAuthCredential
		wantNext      time.Time
		wantScheduled bool
		wantRefresh   bool
	}{
		{
			name:          "disabled credential is not scheduled",
			cred:          &CLIAuthCredential{Status: StatusDisabled},
			wantScheduled: false,
			wantRefresh:   false,
		},
		{
			name: "next refresh after in future delays refresh",
			cred: &CLIAuthCredential{
				NextRefreshAfter: future,
			},
			wantNext:      future,
			wantScheduled: true,
			wantRefresh:   false,
		},
		{
			name: "expiration inside lead time refreshes now",
			cred: &CLIAuthCredential{
				Credential: credentialmgr.Credential{
					Metadata: map[string]any{
						"expires_at": now.Add(2 * time.Minute).Format(time.RFC3339),
					},
				},
			},
			wantNext:      now,
			wantScheduled: true,
			wantRefresh:   true,
		},
		{
			name: "expiration outside lead time waits until due",
			cred: &CLIAuthCredential{
				Credential: credentialmgr.Credential{
					Metadata: map[string]any{
						"expires_at": future.Format(time.RFC3339),
					},
				},
			},
			wantNext:      future.Add(-defaultRefreshLeadTime),
			wantScheduled: true,
			wantRefresh:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lead := defaultRefreshLeadTime
			gotNext, gotScheduled := nextScheduleAt(tt.cred, now, &lead)
			if gotScheduled != tt.wantScheduled {
				t.Fatalf("nextScheduleAt scheduled = %v, want %v", gotScheduled, tt.wantScheduled)
			}
			if !gotNext.Equal(tt.wantNext) {
				t.Fatalf("nextScheduleAt next = %v, want %v", gotNext, tt.wantNext)
			}
			if got := gotScheduled && !gotNext.After(now); got != tt.wantRefresh {
				t.Fatalf("refresh readiness = %v, want %v", got, tt.wantRefresh)
			}
		})
	}
}

func TestNextScheduleAtDisabledWhenLeadTimeNil(t *testing.T) {
	now := time.Now().UTC()
	cred := &CLIAuthCredential{
		Credential: credentialmgr.Credential{
			Metadata: map[string]any{
				"expires_at": now.Add(time.Hour).Format(time.RFC3339),
			},
		},
	}

	gotNext, gotScheduled := nextScheduleAt(cred, now, nil)
	if gotScheduled {
		t.Fatalf("nextScheduleAt scheduled = %v, want false", gotScheduled)
	}
	if !gotNext.IsZero() {
		t.Fatalf("nextScheduleAt next = %v, want zero", gotNext)
	}
}

func TestAutoRefresherWorkerUsesConsistentCredentialSnapshot(t *testing.T) {
	refresher := NewAutoRefresher(nil, NewManager())
	now := time.Now().UTC()

	usedCred := make(chan *credentialmgr.Credential, 1)
	refresher.manager.RegisterAuthenticator("codex", &testRefreshAuthenticator{
		providerType: "openai",
		refreshFn: func(ctx context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error) {
			usedCred <- cred.Clone()
			return cred.Clone(), nil
		},
	})
	refresher.manager.RegisterAuthenticator("claude", &testRefreshAuthenticator{
		providerType: "anthropic",
		refreshFn: func(context.Context, *credentialmgr.Credential) (*credentialmgr.Credential, error) {
			t.Fatal("worker resolved authenticator from a newer provider type instead of the credential snapshot")
			return nil, nil
		},
	})

	refresher.mu.Lock()
	refresher.creds["cred-1"] = &CLIAuthCredential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "openai",
			Metadata: map[string]any{
				"expires_at": now.Add(2 * time.Minute).Format(time.RFC3339),
			},
		},
		Status: StatusActive,
	}
	refresher.mu.Unlock()

	targetCred, auth := refresher.resolveRefreshTarget("cred-1")
	if targetCred == nil || auth == nil {
		t.Fatal("resolveRefreshTarget returned nil target")
	}

	refresher.mu.Lock()
	refresher.creds["cred-1"] = &CLIAuthCredential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "anthropic",
			Metadata: map[string]any{
				"expires_at": now.Add(2 * time.Minute).Format(time.RFC3339),
			},
		},
		Status: StatusActive,
	}
	refresher.mu.Unlock()

	refresher.refreshOne(context.Background(), targetCred, auth, now)

	select {
	case got := <-usedCred:
		if got.ProviderType != "openai" {
			t.Fatalf("refresh credential provider type = %q, want openai", got.ProviderType)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh authenticator was not invoked")
	}
}
