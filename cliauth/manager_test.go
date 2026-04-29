package cliauth

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

type stubAuthenticator struct {
	providerType string
	config       AuthenticatorConfig
}

func (a *stubAuthenticator) ProviderType() string {
	return a.providerType
}

func (a *stubAuthenticator) GetConfig() AuthenticatorConfig {
	if a == nil {
		return AuthenticatorConfig{}
	}
	return a.config
}

func (a *stubAuthenticator) SetConfig(cfg AuthenticatorConfig) error {
	if a == nil {
		return nil
	}
	a.config = cfg
	return nil
}

func (a *stubAuthenticator) Login(context.Context, LoginStatusReporter) (*credentialmgr.Credential, error) {
	return nil, nil
}

func (a *stubAuthenticator) Refresh(context.Context, *credentialmgr.Credential) (*credentialmgr.Credential, error) {
	return nil, nil
}

func (a *stubAuthenticator) RefreshLeadTime() *time.Duration { return nil }

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

func TestEnableAuthenticatorAppliesConfig(t *testing.T) {
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

	mgr := NewManager()
	state, err := mgr.EnableAuthenticator("codex", AuthenticatorConfig{
		CallbackPort: 1455,
		NoBrowser:    true,
		DeviceFlow:   true,
	})
	if err != nil {
		t.Fatalf("EnableAuthenticator returned error: %v", err)
	}
	if !state.Enabled || state.ProviderType != "openai" {
		t.Fatalf("unexpected state: %#v", state)
	}

	auth, ok := mgr.GetAuthenticator("codex")
	if !ok {
		t.Fatal("expected enabled authenticator")
	}
	cfg := auth.GetConfig()
	if cfg.CallbackPort != 1455 || !cfg.NoBrowser || !cfg.DeviceFlow {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestApplyAuthenticatorConfigOverridesPreservesExistingDefaults(t *testing.T) {
	auth := &stubAuthenticator{
		providerType: "openai",
		config: AuthenticatorConfig{
			CallbackPort: 1455,
			Network: NetworkConfig{
				RequestTimeoutSeconds: 120,
				MaxRetries:            3,
				ExtraHeaders: map[string]string{
					"X-Base": "1",
				},
			},
		},
	}

	err := ApplyAuthenticatorConfigOverrides(auth, AuthenticatorConfig{
		NoBrowser:  true,
		DeviceFlow: true,
		Network: NetworkConfig{
			MaxRetries: 5,
		},
	})
	if err != nil {
		t.Fatalf("ApplyAuthenticatorConfigOverrides returned error: %v", err)
	}

	cfg := auth.GetConfig()
	if cfg.CallbackPort != 1455 {
		t.Fatalf("CallbackPort = %d, want 1455", cfg.CallbackPort)
	}
	if !cfg.NoBrowser || !cfg.DeviceFlow {
		t.Fatalf("unexpected boolean overrides: %#v", cfg)
	}
	if cfg.Network.RequestTimeoutSeconds != 120 {
		t.Fatalf("RequestTimeoutSeconds = %d, want 120", cfg.Network.RequestTimeoutSeconds)
	}
	if cfg.Network.MaxRetries != 5 {
		t.Fatalf("MaxRetries = %d, want 5", cfg.Network.MaxRetries)
	}
	if cfg.Network.ExtraHeaders["X-Base"] != "1" {
		t.Fatalf("ExtraHeaders = %#v, want existing headers preserved", cfg.Network.ExtraHeaders)
	}
}

func TestEnableAuthenticatorPreservesFactoryDefaults(t *testing.T) {
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
		return &stubAuthenticator{
			providerType: "openai",
			config: AuthenticatorConfig{
				CallbackPort: 1455,
			},
		}, nil
	})

	mgr := NewManager()
	state, err := mgr.EnableAuthenticator("codex", AuthenticatorConfig{
		NoBrowser: true,
	})
	if err != nil {
		t.Fatalf("EnableAuthenticator returned error: %v", err)
	}
	if !state.Enabled || state.ProviderType != "openai" {
		t.Fatalf("unexpected state: %#v", state)
	}
	auth, ok := mgr.GetAuthenticator("codex")
	if !ok {
		t.Fatal("expected enabled authenticator")
	}
	cfg := auth.GetConfig()
	if cfg.CallbackPort != 1455 {
		t.Fatalf("CallbackPort = %d, want 1455", cfg.CallbackPort)
	}
	if !cfg.NoBrowser {
		t.Fatalf("NoBrowser = false, want true")
	}
}

func TestEnableAuthenticatorReplacesExistingRuntimeConfig(t *testing.T) {
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
		return &stubAuthenticator{
			providerType: "openai",
			config: AuthenticatorConfig{
				CallbackPort: 1455,
			},
		}, nil
	})

	mgr := NewManager()
	if _, err := mgr.EnableAuthenticator("codex", AuthenticatorConfig{
		NoBrowser:  true,
		DeviceFlow: true,
	}); err != nil {
		t.Fatalf("initial EnableAuthenticator returned error: %v", err)
	}

	_, err := mgr.EnableAuthenticator("codex", AuthenticatorConfig{
		CallbackPort: 9002,
	})
	if err != nil {
		t.Fatalf("second EnableAuthenticator returned error: %v", err)
	}

	auth, ok := mgr.GetAuthenticator("codex")
	if !ok {
		t.Fatal("expected enabled authenticator")
	}
	cfg := auth.GetConfig()
	if cfg.CallbackPort != 9002 {
		t.Fatalf("CallbackPort = %d, want 9002", cfg.CallbackPort)
	}
	if cfg.NoBrowser {
		t.Fatalf("NoBrowser = true, want false")
	}
	if cfg.DeviceFlow {
		t.Fatalf("DeviceFlow = true, want false")
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
	if states[0].Name != "claude" || states[0].Enabled || states[0].ProviderType != "anthropic" {
		t.Fatalf("first state = %#v, want disabled claude with defaults", states[0])
	}
	if states[1].Name != "codex" || !states[1].Enabled || states[1].ProviderType != "openai" {
		t.Fatalf("second state = %#v, want enabled codex", states[1])
	}
}

func TestGetAuthenticatorStateReturnsFactoryDefaultsWhenDisabled(t *testing.T) {
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
		return &stubAuthenticator{
			providerType: "openai",
			config: AuthenticatorConfig{
				CallbackPort: 1455,
				NoBrowser:    true,
				DeviceFlow:   true,
			},
		}, nil
	})

	mgr := NewManager()
	state, ok := mgr.GetAuthenticatorState("codex")
	if !ok {
		t.Fatal("expected supported authenticator state")
	}
	if state.Name != "codex" || state.Enabled || state.ProviderType != "openai" {
		t.Fatalf("unexpected state metadata: %#v", state)
	}
	if state.Config.CallbackPort != 1455 || !state.Config.NoBrowser || !state.Config.DeviceFlow {
		t.Fatalf("unexpected default config: %#v", state.Config)
	}
}
