package cliauth

import (
	"context"
	"sort"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

type stubAuthenticator struct {
	providerType string
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

func (a *stubAuthenticator) ProviderType() string {
	return a.providerType
}

func (a *stubAuthenticator) Login(context.Context, LoginStatusReporter) (*Credential, error) {
	return nil, nil
}

func (a *stubAuthenticator) RefreshLead(context.Context, *Credential) (*Credential, error) {
	return nil, nil
}

func TestRegisterAuthenticatorIndexesProviderKey(t *testing.T) {
	mgr := NewManager(nil)
	auth := &stubAuthenticator{providerType: "openai"}

	mgr.RegisterAuthenticator("codex", auth)

	if got, ok := mgr.GetAuthenticator("codex"); !ok || got != auth {
		t.Fatalf("GetAuthenticator(codex) = (%v, %v), want registered authenticator", got, ok)
	}

	resolved := mgr.resolveAuthenticator("openai")
	if resolved == nil {
		t.Fatal("resolveAuthenticator(openai) returned nil")
	}
	if resolved != auth {
		t.Fatal("resolveAuthenticator(openai) did not return the registered authenticator")
	}
}

func TestDisableAuthenticatorRemovesRuntimeAuthenticator(t *testing.T) {
	mgr := NewManager(nil)
	auth := &stubAuthenticator{providerType: "openai"}

	mgr.RegisterAuthenticator("codex", auth)

	if err := mgr.DisableAuthenticator("codex"); err != nil {
		t.Fatalf("DisableAuthenticator returned error: %v", err)
	}
	if _, ok := mgr.GetAuthenticator("codex"); ok {
		t.Fatal("GetAuthenticator(codex) returned disabled authenticator")
	}
	if resolved := mgr.resolveAuthenticator("openai"); resolved != nil {
		t.Fatalf("resolveAuthenticator(openai) = %T, want nil", resolved)
	}
}

func TestDisableAuthenticatorRejectsReadOnlyAuthenticator(t *testing.T) {
	mgr := NewManager(nil)
	auth := &stubAuthenticator{providerType: "openai"}

	mgr.RegisterAuthenticatorWithOptions("codex", auth, RegisterAuthenticatorOptions{
		Source:   AuthenticatorSourceCaddyfile,
		ReadOnly: true,
	})

	if err := mgr.DisableAuthenticator("codex"); err != ErrAuthenticatorReadOnly {
		t.Fatalf("DisableAuthenticator error = %v, want %v", err, ErrAuthenticatorReadOnly)
	}
	if got, ok := mgr.GetAuthenticator("codex"); !ok || got != auth {
		t.Fatalf("GetAuthenticator(codex) = (%v, %v), want read-only authenticator", got, ok)
	}
}

func TestRegisterAuthenticatorFactoryListsNames(t *testing.T) {
	authFactoryMu.Lock()
	originalFactories := authFactories
	authFactories = map[string]AuthenticatorFactory{}
	authFactoryMu.Unlock()
	t.Cleanup(func() {
		authFactoryMu.Lock()
		authFactories = originalFactories
		authFactoryMu.Unlock()
	})

	factory := func() (Authenticator, error) {
		return &stubAuthenticator{providerType: "openai"}, nil
	}

	RegisterAuthenticatorFactory(" Codex ", factory)
	RegisterAuthenticatorFactory("claude", factory)
	RegisterAuthenticatorFactory("", factory)
	RegisterAuthenticatorFactory("gemini", nil)

	names := ListAuthenticatorTypes()
	sort.Strings(names)

	want := []string{"claude", "codex"}
	if len(names) != len(want) {
		t.Fatalf("ListAuthenticatorTypes() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("ListAuthenticatorTypes() = %v, want %v", names, want)
		}
	}
}

func TestListAuthenticatorStatesListsSupportedAuthenticators(t *testing.T) {
	authFactoryMu.Lock()
	originalFactories := authFactories
	authFactories = map[string]AuthenticatorFactory{}
	authFactoryMu.Unlock()
	t.Cleanup(func() {
		authFactoryMu.Lock()
		authFactories = originalFactories
		authFactoryMu.Unlock()
	})

	RegisterAuthenticatorFactory("codex", func() (Authenticator, error) {
		return &stubAuthenticator{providerType: "openai"}, nil
	})
	RegisterAuthenticatorFactory("claude", func() (Authenticator, error) {
		return &stubAuthenticator{providerType: "anthropic"}, nil
	})

	mgr := NewManager(nil)
	mgr.RegisterAuthenticatorWithOptions("codex", &stubAuthenticator{providerType: "openai"}, RegisterAuthenticatorOptions{
		Source:   AuthenticatorSourceCaddyfile,
		ReadOnly: true,
	})

	states := mgr.ListAuthenticatorStates()
	if len(states) != 2 {
		t.Fatalf("ListAuthenticatorStates() = %#v, want 2 states", states)
	}
	if states[0].Name != "claude" || states[0].Enabled || states[0].ProviderType != "" || states[0].Source != "" || states[0].ReadOnly {
		t.Fatalf("first state = %#v, want disabled placeholder claude", states[0])
	}
	if states[1].Name != "codex" || !states[1].Enabled || !states[1].ReadOnly || states[1].Source != AuthenticatorSourceCaddyfile {
		t.Fatalf("second state = %#v, want read-only enabled codex", states[1])
	}
}

func TestRegisterCredentialPersistsWithCreate(t *testing.T) {
	store := &stubCredentialStore{}
	credMgr := credentialmgr.NewManager(store, nil, nil)
	mgr := NewManager(credMgr)

	if err := mgr.RegisterLoginCredential(context.Background(), &Credential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "openai",
			ProviderID:   "openai-main",
		},
	}); err != nil {
		t.Fatalf("RegisterCredential returned error: %v", err)
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
	mgr := NewManager(credMgr)

	if err := mgr.UpdateCredential(context.Background(), &Credential{
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
	mgr := NewManager(commonMgr)
	if err := mgr.RegisterLoginCredential(context.Background(), &Credential{
		Credential: credentialmgr.Credential{
			ID:           "cred-1",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Attributes: map[string]string{
				"api_key": "old-key",
			},
		},
	}); err != nil {
		t.Fatalf("RegisterCredential returned error: %v", err)
	}

	if err := mgr.UpdateCredential(context.Background(), &Credential{
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
