package cliauth

import (
	"context"
	"sort"
	"testing"
	"time"
)

type stubAuthenticator struct {
	providerType string
}

func (a *stubAuthenticator) ProviderType() string {
	return a.providerType
}

func (a *stubAuthenticator) Login(context.Context, LoginStatusReporter) (*Credential, error) {
	return nil, nil
}

func (a *stubAuthenticator) Refresh(context.Context, *Credential) (*Credential, error) {
	return nil, nil
}

func (a *stubAuthenticator) RefreshLeadTime() time.Duration { return 0 }

func TestRegisterAuthenticatorIndexesProviderKey(t *testing.T) {
	mgr := NewManager()
	auth := &stubAuthenticator{providerType: "openai"}

	mgr.RegisterAuthenticator("codex", auth)

	if got, ok := mgr.GetAuthenticator("codex"); !ok || got.ProviderType() != auth.ProviderType() {
		t.Fatalf("GetAuthenticator(codex) = (%v, %v), want registered authenticator", got, ok)
	}

	resolved := mgr.resolveAuthenticator("openai")
	if resolved == nil {
		t.Fatal("resolveAuthenticator(openai) returned nil")
	}
	if resolved.ProviderType() != auth.ProviderType() {
		t.Fatal("resolveAuthenticator(openai) did not return the registered authenticator")
	}
}

func TestDisableAuthenticatorRemovesRuntimeAuthenticator(t *testing.T) {
	mgr := NewManager()
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

	mgr := NewManager()
	mgr.RegisterAuthenticator("codex", &stubAuthenticator{providerType: "openai"})

	states := mgr.ListAuthenticatorStates()
	if len(states) != 2 {
		t.Fatalf("ListAuthenticatorStates() = %#v, want 2 states", states)
	}
	if states[0].Name != "claude" || states[0].Enabled || states[0].ProviderType != "" {
		t.Fatalf("first state = %#v, want disabled placeholder claude", states[0])
	}
	if states[1].Name != "codex" || !states[1].Enabled || states[1].ProviderType != "openai" {
		t.Fatalf("second state = %#v, want enabled codex", states[1])
	}
}
