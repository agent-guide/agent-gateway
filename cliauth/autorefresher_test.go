package cliauth

import (
	"context"
	"testing"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

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
	credMgr := credentialmgr.NewManager(store, nil, nil)
	refresher := NewAutoRefresher(WrapSharedCredentialManager(credMgr), nil)

	if err := refresher.RegisterLoginCredential(context.Background(), &Credential{
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
	credMgr := credentialmgr.NewManager(store, nil, nil)
	refresher := NewAutoRefresher(WrapSharedCredentialManager(credMgr), nil)

	if err := refresher.updateCredential(context.Background(), &Credential{
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
	commonMgr := credentialmgr.NewManager(nil, nil, nil)
	refresher := NewAutoRefresher(WrapSharedCredentialManager(commonMgr), nil)

	if err := refresher.RegisterLoginCredential(context.Background(), &Credential{
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

	if err := refresher.updateCredential(context.Background(), &Credential{
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
	if got := refresher.snapshotForRefresh(time.Time{}); len(got) != 0 {
		t.Fatalf("snapshotForRefresh() loaded unexpected refresh candidate count: got %d want 0", len(got))
	}
	if _, ok := refresher.creds["cred-1"]; !ok {
		t.Fatal("Load did not store credential from interface-backed manager")
	}
}
