package provider

import (
	"context"
	"testing"

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

func newTestCredentialManager() *credentialmgr.Manager {
	return credentialmgr.NewManager(nil, nil, nil)
}

func newProviderIDScopedCredentialManager() *credentialmgr.Manager {
	return credentialmgr.NewManager(nil, nil, nil)
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
	if cfg.AuthStrategy != AuthStrategyManagedAPIKeyFirst {
		t.Fatalf("unexpected default auth strategy: got %q want %q", cfg.AuthStrategy, AuthStrategyManagedAPIKeyFirst)
	}
}

func TestWrapWithCredentialManagerUsesStaticAPIKeyAsFallback(t *testing.T) {
	credMgr := newTestCredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "cred-key",
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyManagedAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred != nil {
		t.Fatalf("expected static api key fallback, got %+v", base.lastCred)
	}
	if base.lastAPIKey != "static-key" {
		t.Fatalf("unexpected api key: got %q want static-key", base.lastAPIKey)
	}
	if got := credMgr.GetCredential(StaticAPIKeyCredentialID(base.cfg)); got != nil {
		t.Fatalf("static api key credential should not be registered, got %+v", got)
	}
}

func TestWrapWithCredentialManagerScopesManagedCredentialToProviderID(t *testing.T) {
	credMgr := newTestCredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "provider-static-api-key:zhipu",
		ProviderType: "zhipu",
		ProviderID:   "zhipu",
		Source:       credentialmgr.SourceAPIKey,
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
			AuthStrategy: AuthStrategyManagedAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred != nil {
		t.Fatalf("expected static api key fallback, got %+v", base.lastCred)
	}
	if base.lastAPIKey != "right-key" {
		t.Fatalf("api key = %q, want right-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerUsesProviderIDScopedManagedCredential(t *testing.T) {
	credMgr := newProviderIDScopedCredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "zhipu-main",
		ProviderType: "zhipu",
		ProviderID:   "zhipu-main",
		Source:       credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"api_key": "scoped-key",
		},
	}); err != nil {
		t.Fatalf("register provider-scoped credential: %v", err)
	}
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "zhipu-other",
		ProviderType: "zhipu",
		ProviderID:   "zhipu-other",
		Source:       credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"api_key": "other-key",
		},
	}); err != nil {
		t.Fatalf("register other provider-scoped credential: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "zhipu-main",
			ProviderType: "zhipu",
			AuthStrategy: AuthStrategyManagedAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected provider-scoped managed credential override")
	}
	if base.lastCred.ID != "zhipu-main" {
		t.Fatalf("credential id = %q, want zhipu-main", base.lastCred.ID)
	}
	if base.lastAPIKey != "scoped-key" {
		t.Fatalf("unexpected api key: got %q want scoped-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerFallsBackToStaticAPIKeyWhenManagedCredentialMissing(t *testing.T) {
	credMgr := newTestCredentialManager()
	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyManagedAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{Model: "gpt-test"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred != nil {
		t.Fatalf("expected no context credential for static API key fallback, got %+v", base.lastCred)
	}
	if base.lastAPIKey != "static-key" {
		t.Fatalf("unexpected api key: got %q want static-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerPrefersManagedAPIKey(t *testing.T) {
	credMgr := newTestCredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "api-key-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"api_key": "managed-api-key",
		},
	}); err != nil {
		t.Fatalf("register managed api key: %v", err)
	}
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cliauth-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "managed-cli-key",
		},
	}); err != nil {
		t.Fatalf("register cli auth token: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyManagedAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected managed credential override")
	}
	if base.lastCred.ID != "api-key-1" {
		t.Fatalf("credential id = %q, want api-key-1", base.lastCred.ID)
	}
	if base.lastAPIKey != "managed-api-key" {
		t.Fatalf("unexpected api key: got %q want managed-api-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerPrefersManagedCLIAuthToken(t *testing.T) {
	credMgr := newTestCredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "api-key-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"api_key": "managed-api-key",
		},
	}); err != nil {
		t.Fatalf("register managed api key: %v", err)
	}
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cliauth-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "managed-cli-key",
		},
	}); err != nil {
		t.Fatalf("register cli auth token: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyManagedCLIAuthTokenFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected managed credential override")
	}
	if base.lastCred.ID != "cliauth-1" {
		t.Fatalf("credential id = %q, want cliauth-1", base.lastCred.ID)
	}
	if base.lastAPIKey != "managed-cli-key" {
		t.Fatalf("unexpected api key: got %q want managed-cli-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerDoesNotFallbackBetweenManagedSources(t *testing.T) {
	credMgr := newTestCredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "api-key-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"api_key": "managed-api-key",
		},
	}); err != nil {
		t.Fatalf("register managed api key: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyManagedCLIAuthTokenFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred != nil {
		t.Fatalf("expected no managed credential override, got %+v", base.lastCred)
	}
	if base.lastAPIKey != "static-key" {
		t.Fatalf("unexpected api key: got %q want static-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerPreservesManagedRoundRobin(t *testing.T) {
	credMgr := newTestCredentialManager()
	for _, id := range []string{"cred-a", "cred-b"} {
		if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
			ID:           id,
			ProviderType: "openai",
			ProviderID:   "openai",
			Source:       credentialmgr.SourceCLIAuthToken,
			Attributes: map[string]string{
				"api_key": id + "-key",
			},
		}); err != nil {
			t.Fatalf("register credential %s: %v", id, err)
		}
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			AuthStrategy: AuthStrategyManagedCLIAuthTokenFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
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
