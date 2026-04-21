package provider

import (
	"context"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/cloudwego/eino/schema"
)

type testConfigurableProvider struct {
	cfg        ProviderConfig
	errs       []error
	calls      int
	lastAPIKey string
	lastCred   *credentialmgr.Credential
}

func newTestCLIAuthManager() *cliauth.Manager {
	return cliauth.NewManager(credentialmgr.NewManager(nil, nil, nil), nil)
}

func (p *testConfigurableProvider) Generate(ctx context.Context, _ *GenerateRequest) (*GenerateResponse, error) {
	apiKey, _ := ResolveCredential(ctx, p.cfg)
	cred, _ := CredentialFromContext(ctx)
	p.lastAPIKey = apiKey
	p.lastCred = cred
	if p.calls < len(p.errs) {
		err := p.errs[p.calls]
		p.calls++
		return nil, err
	}
	p.calls++
	return &GenerateResponse{}, nil
}

func (p *testConfigurableProvider) Stream(context.Context, *GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p *testConfigurableProvider) ListModels(context.Context) ([]ModelInfo, error) {
	return nil, nil
}

func (p *testConfigurableProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{}
}

func (p *testConfigurableProvider) Config() ProviderConfig {
	return p.cfg
}

func TestProviderConfigDefaults(t *testing.T) {
	var cfg ProviderConfig
	cfg.Defaults()
	if cfg.AuthStrategy != AuthStrategyAPIKeyFirst {
		t.Fatalf("unexpected default auth strategy: got %q want %q", cfg.AuthStrategy, AuthStrategyAPIKeyFirst)
	}
}

func TestWrapWithCredentialManagerHonorsAPIKeyFirst(t *testing.T) {
	cliauthMgr := newTestCLIAuthManager()
	credMgr := cliauthMgr.CredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:       "cred-1",
		Provider: "openai",
		Source:   credentialmgr.SourceCLIAuth,
		Attributes: map[string]string{
			"api_key": "cred-key",
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			ProviderType: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, "openai", credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected static API key credential")
	}
	if base.lastCred.ID != "provider-static-api-key:openai" {
		t.Fatalf("unexpected credential override: got %q", base.lastCred.ID)
	}
	if base.lastAPIKey != "static-key" {
		t.Fatalf("unexpected api key: got %q want static-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerScopesStaticCredentialToProviderID(t *testing.T) {
	cliauthMgr := newTestCLIAuthManager()
	credMgr := cliauthMgr.CredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:       "provider-static-api-key:zhipu",
		Provider: "zhipu",
		Source:   credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"api_key":  "wrong-key",
			"base_url": "https://wrong.example",
			"priority": "1",
		},
	}); err != nil {
		t.Fatalf("register stale type-scoped credential: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "zhipu-test",
			ProviderType: "zhipu",
			APIKey:       "right-key",
			BaseURL:      "https://right.example",
			AuthStrategy: AuthStrategyAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, "zhipu-test", credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected provider ID scoped static API key credential")
	}
	if base.lastCred.ID != "provider-static-api-key:zhipu-test" {
		t.Fatalf("credential ID = %q, want provider-static-api-key:zhipu-test", base.lastCred.ID)
	}
	if base.lastCred.Provider != "zhipu-test" {
		t.Fatalf("credential provider = %q, want zhipu-test", base.lastCred.Provider)
	}
	if base.lastAPIKey != "right-key" {
		t.Fatalf("api key = %q, want right-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerFallsBackAfterStaticAPIKeyQuota(t *testing.T) {
	cliauthMgr := newTestCLIAuthManager()
	credMgr := cliauthMgr.CredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:       "cred-1",
		Provider: "openai",
		Source:   credentialmgr.SourceCLIAuth,
		Attributes: map[string]string{
			"api_key": "cred-key",
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			ProviderType: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyAPIKeyFirst,
		},
		errs: []error{NewStatusError(429, "quota exceeded")},
	}
	wrapped := WrapWithCredentialManager(base, "openai", credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{Model: "gpt-test"}); err == nil {
		t.Fatal("expected first generate to fail")
	}
	if base.lastCred == nil || base.lastCred.ID != "provider-static-api-key:openai" {
		t.Fatalf("expected first call to use static API key, got %+v", base.lastCred)
	}

	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{Model: "gpt-test"}); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if base.lastCred == nil || base.lastCred.ID != "cred-1" {
		t.Fatalf("expected second call to use managed credential, got %+v", base.lastCred)
	}
	if base.lastAPIKey != "cred-key" {
		t.Fatalf("unexpected api key: got %q want cred-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerUsesProviderCredentials(t *testing.T) {
	cliauthMgr := newTestCLIAuthManager()
	credMgr := cliauthMgr.CredentialManager()
	for _, id := range []string{"cred-a", "cred-b"} {
		if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
			ID:       id,
			Provider: "openai",
			Source:   credentialmgr.SourceCLIAuth,
			Attributes: map[string]string{
				"api_key": id + "-key",
			},
		}); err != nil {
			t.Fatalf("register credential %s: %v", id, err)
		}
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			ProviderType: "openai",
			AuthStrategy: AuthStrategyCredentialFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, "openai", credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected credential override")
	}
	if base.lastCred.Provider != "openai" {
		t.Fatalf("unexpected credential provider: got %q want %q", base.lastCred.Provider, "openai")
	}
}

func TestWrapWithCredentialManagerPreservesManagedRoundRobin(t *testing.T) {
	cliauthMgr := newTestCLIAuthManager()
	credMgr := cliauthMgr.CredentialManager()
	for _, id := range []string{"cred-a", "cred-b"} {
		if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
			ID:       id,
			Provider: "openai",
			Source:   credentialmgr.SourceCLIAuth,
			Attributes: map[string]string{
				"api_key": id + "-key",
			},
		}); err != nil {
			t.Fatalf("register credential %s: %v", id, err)
		}
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			ProviderType: "openai",
			AuthStrategy: AuthStrategyCredentialFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, "openai", credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	first := base.lastCred.ID
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	second := base.lastCred.ID
	if first != "cred-a" || second != "cred-b" {
		t.Fatalf("round robin credentials = %q, %q; want cred-a, cred-b", first, second)
	}
}
