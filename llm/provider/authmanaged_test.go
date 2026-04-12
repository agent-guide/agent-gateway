package provider

import (
	"context"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/credential"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/cloudwego/eino/schema"
)

type testConfigurableProvider struct {
	cfg      ProviderConfig
	lastCred *credential.Credential
}

func (p *testConfigurableProvider) Generate(ctx context.Context, _ *GenerateRequest) (*GenerateResponse, error) {
	cred, _ := CredentialFromContext(ctx)
	p.lastCred = cred
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

func TestWrapWithAuthManagerHonorsAPIKeyFirst(t *testing.T) {
	cliauthMgr := manager.NewManager(nil, nil, nil)
	if err := cliauthMgr.RegisterCredential(context.Background(), &credential.Credential{
		ID:       "cred-1",
		Provider: "openai",
		Attributes: map[string]string{
			"api_key": "cred-key",
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			ProviderName: "openai",
			APIKey:       "static-key",
			AuthStrategy: AuthStrategyAPIKeyFirst,
		},
	}
	wrapped := WrapWithAuthManager(base, "openai", cliauthMgr)
	if _, err := wrapped.Generate(context.Background(), &GenerateRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred != nil {
		t.Fatalf("expected no credential override, got %q", base.lastCred.ID)
	}
}

func TestWrapWithAuthManagerUsesProviderCredentials(t *testing.T) {
	cliauthMgr := manager.NewManager(nil, nil, nil)
	for _, id := range []string{"cred-a", "cred-b"} {
		if err := cliauthMgr.RegisterCredential(context.Background(), &credential.Credential{
			ID:       id,
			Provider: "openai",
			Attributes: map[string]string{
				"api_key": id + "-key",
			},
		}); err != nil {
			t.Fatalf("register credential %s: %v", id, err)
		}
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			ProviderName: "openai",
			AuthStrategy: AuthStrategyCredentialFirst,
		},
	}
	wrapped := WrapWithAuthManager(base, "openai", cliauthMgr)
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
