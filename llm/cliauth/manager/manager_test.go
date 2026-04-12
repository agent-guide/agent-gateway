package manager

import (
	"context"
	"sort"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/credential"
)

type stubAuthenticator struct {
	provider string
}

type stubCredentialStore struct {
	createCalls int
	updateCalls int
	lastCreated *credential.Credential
	lastUpdated *credential.Credential
}

func (s *stubCredentialStore) ListByProviderName(context.Context, string) ([]any, error) {
	return nil, nil
}

func (s *stubCredentialStore) Create(_ context.Context, _ string, _ string, obj any) (string, error) {
	s.createCalls++
	cred, _ := obj.(*credential.Credential)
	s.lastCreated = cred
	if cred == nil {
		return "", nil
	}
	return cred.ID, nil
}

func (s *stubCredentialStore) Update(_ context.Context, _ string, obj any) error {
	s.updateCalls++
	cred, _ := obj.(*credential.Credential)
	s.lastUpdated = cred
	return nil
}

func (s *stubCredentialStore) Delete(context.Context, string) error {
	return nil
}

func (s *stubCredentialStore) Get(context.Context, string) (string, any, error) {
	return "", nil, nil
}

func (a *stubAuthenticator) Provider() string {
	return a.provider
}

func (a *stubAuthenticator) Login(context.Context) (*credential.Credential, error) {
	return nil, nil
}

func (a *stubAuthenticator) RefreshLead(context.Context, *credential.Credential) (*credential.Credential, error) {
	return nil, nil
}

func TestRegisterAuthenticatorIndexesProviderKey(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	auth := &stubAuthenticator{provider: "openai"}

	mgr.RegisterAuthenticator("codex", auth)

	if got, ok := mgr.GetAuthenticator("codex"); !ok || got != auth {
		t.Fatalf("GetAuthenticator(codex) = (%v, %v), want registered authenticator", got, ok)
	}

	refresher := mgr.resolveRefresher("openai")
	if refresher == nil {
		t.Fatal("resolveRefresher(openai) returned nil")
	}

	wrapped, ok := refresher.(*authenticatorRefresher)
	if !ok {
		t.Fatalf("resolveRefresher(openai) returned %T, want *authenticatorRefresher", refresher)
	}
	if wrapped.auth != auth {
		t.Fatal("resolveRefresher(openai) did not return the registered authenticator")
	}
}

func TestDisableAuthenticatorRemovesRuntimeAuthenticator(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	auth := &stubAuthenticator{provider: "openai"}

	mgr.RegisterAuthenticator("codex", auth)

	if err := mgr.DisableAuthenticator("codex"); err != nil {
		t.Fatalf("DisableAuthenticator returned error: %v", err)
	}
	if _, ok := mgr.GetAuthenticator("codex"); ok {
		t.Fatal("GetAuthenticator(codex) returned disabled authenticator")
	}
	if refresher := mgr.resolveRefresher("openai"); refresher != nil {
		t.Fatalf("resolveRefresher(openai) = %T, want nil", refresher)
	}
}

func TestDisableAuthenticatorRejectsReadOnlyAuthenticator(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	auth := &stubAuthenticator{provider: "openai"}

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
		return &stubAuthenticator{provider: "openai"}, nil
	}

	RegisterAuthenticatorFactory(" Codex ", factory)
	RegisterAuthenticatorFactory("claude", factory)
	RegisterAuthenticatorFactory("", factory)
	RegisterAuthenticatorFactory("gemini", nil)

	names := ListAuthenticatorNames()
	sort.Strings(names)

	want := []string{"claude", "codex"}
	if len(names) != len(want) {
		t.Fatalf("ListAuthenticatorNames() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("ListAuthenticatorNames() = %v, want %v", names, want)
		}
	}
}

func TestListAuthenticatorStatesMergesFactoriesAndEnabledState(t *testing.T) {
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
		return &stubAuthenticator{provider: "openai"}, nil
	})
	RegisterAuthenticatorFactory("claude", func() (Authenticator, error) {
		return &stubAuthenticator{provider: "anthropic"}, nil
	})

	mgr := NewManager(nil, nil, nil)
	mgr.RegisterAuthenticatorWithOptions("codex", &stubAuthenticator{provider: "openai"}, RegisterAuthenticatorOptions{
		Source:   AuthenticatorSourceCaddyfile,
		ReadOnly: true,
	})

	states := mgr.ListAuthenticatorStates()
	if len(states) != 2 {
		t.Fatalf("ListAuthenticatorStates() = %#v, want 2 states", states)
	}
	if states[0].Name != "claude" || states[0].Enabled {
		t.Fatalf("first state = %#v, want disabled claude", states[0])
	}
	if states[1].Name != "codex" || !states[1].Enabled || !states[1].ReadOnly || states[1].Source != AuthenticatorSourceCaddyfile {
		t.Fatalf("second state = %#v, want read-only enabled codex", states[1])
	}
}

func TestRegisterCredentialPersistsWithCreate(t *testing.T) {
	store := &stubCredentialStore{}
	mgr := NewManager(store, nil, nil)

	if err := mgr.RegisterCredential(context.Background(), &credential.Credential{
		ID:       "cred-1",
		Provider: "openai",
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
	mgr := NewManager(store, nil, nil)

	if err := mgr.UpdateCredential(context.Background(), &credential.Credential{
		ID:       "cred-1",
		Provider: "openai",
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
