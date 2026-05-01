package provider

import (
	"context"
	"testing"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/cloudwego/eino/schema"
)

type testConfigurableProvider struct {
	cfg                  ProviderConfig
	errs                 []error
	calls                int
	lastAPIKey           string
	lastBaseURL          string
	lastCred             *credentialmgr.Credential
	lastEmbeddingAPIKey  string
	lastEmbeddingBaseURL string
	lastEmbeddingCred    *credentialmgr.Credential
	lastResponseAPIKey   string
	lastResponseBaseURL  string
	lastResponseCred     *credentialmgr.Credential
}

func newTestCredentialManager() *credentialmgr.Manager {
	return credentialmgr.NewManager(nil, nil, nil)
}

func newProviderIDScopedCredentialManager() *credentialmgr.Manager {
	return credentialmgr.NewManager(nil, nil, nil)
}

func (p *testConfigurableProvider) Chat(ctx context.Context, _ *ChatRequest) (*ChatResponse, error) {
	apiKey, baseURL := ResolveCredential(ctx, p.cfg)
	cred, _ := CredentialFromContext(ctx)
	p.lastAPIKey = apiKey
	p.lastBaseURL = baseURL
	p.lastCred = cred
	if p.calls < len(p.errs) {
		err := p.errs[p.calls]
		p.calls++
		return nil, err
	}
	p.calls++
	return &ChatResponse{}, nil
}

func (p *testConfigurableProvider) StreamChat(context.Context, *ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p *testConfigurableProvider) Embedding(ctx context.Context, _ *EmbeddingRequest) (*EmbeddingResponse, error) {
	apiKey, baseURL := ResolveCredential(ctx, p.cfg)
	cred, _ := CredentialFromContext(ctx)
	p.lastEmbeddingAPIKey = apiKey
	p.lastEmbeddingBaseURL = baseURL
	p.lastEmbeddingCred = cred
	return &EmbeddingResponse{}, nil
}

func (p *testConfigurableProvider) CreateResponses(ctx context.Context, _ *ResponsesRequest) (*ResponsesResponse, error) {
	apiKey, baseURL := ResolveCredential(ctx, p.cfg)
	cred, _ := CredentialFromContext(ctx)
	p.lastResponseAPIKey = apiKey
	p.lastResponseBaseURL = baseURL
	p.lastResponseCred = cred
	return &ResponsesResponse{}, nil
}

func (p *testConfigurableProvider) StreamResponses(context.Context, *ResponsesRequest) (*schema.StreamReader[*ResponsesStreamEvent], error) {
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{Model: "gpt-test"}); err != nil {
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
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

func TestWrapWithCredentialManagerForwardsResponsesProvider(t *testing.T) {
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
			AuthStrategy: AuthStrategyManagedAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	responsesProv, ok := wrapped.(ResponsesProvider)
	if !ok {
		t.Fatal("expected wrapped provider to implement ResponsesProvider")
	}
	if _, err := responsesProv.CreateResponses(context.Background(), &ResponsesRequest{Model: "gpt-test"}); err != nil {
		t.Fatalf("create response: %v", err)
	}
	if base.lastResponseCred == nil || base.lastResponseCred.ID != "api-key-1" {
		t.Fatalf("unexpected response credential: %+v", base.lastResponseCred)
	}
	if base.lastResponseAPIKey != "managed-api-key" {
		t.Fatalf("unexpected response api key: got %q want managed-api-key", base.lastResponseAPIKey)
	}
}

func TestWrapWithCredentialManagerForwardsEmbeddingProvider(t *testing.T) {
	credMgr := newTestCredentialManager()
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "api-key-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"api_key":  "managed-api-key",
			"base_url": "https://managed.example",
		},
	}); err != nil {
		t.Fatalf("register managed api key: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			APIKey:       "static-key",
			BaseURL:      "https://static.example",
			AuthStrategy: AuthStrategyManagedAPIKeyFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	embeddingProv, ok := wrapped.(EmbeddingProvider)
	if !ok {
		t.Fatal("expected wrapped provider to implement EmbeddingProvider")
	}
	if _, err := embeddingProv.Embedding(context.Background(), &EmbeddingRequest{Model: "text-embedding-3-large"}); err != nil {
		t.Fatalf("embedding: %v", err)
	}
	if base.lastEmbeddingCred == nil || base.lastEmbeddingCred.ID != "api-key-1" {
		t.Fatalf("unexpected embedding credential: %+v", base.lastEmbeddingCred)
	}
	if base.lastEmbeddingAPIKey != "managed-api-key" {
		t.Fatalf("unexpected embedding api key: got %q want managed-api-key", base.lastEmbeddingAPIKey)
	}
	if base.lastEmbeddingBaseURL != "https://managed.example" {
		t.Fatalf("unexpected embedding base url: got %q want https://managed.example", base.lastEmbeddingBaseURL)
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
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
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	first := base.lastCred.ID
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	second := base.lastCred.ID
	if first != "cred-a" || second != "cred-b" {
		t.Fatalf("round robin credentials = %q, %q; want cred-a, cred-b", first, second)
	}
}

func TestWrapWithCredentialManagerRefreshesExpiredCLIAuthCredentialBeforeUse(t *testing.T) {
	credMgr := newTestCredentialManager()
	credMgr.SetManualRefresher("codex", &testProviderManualRefresher{
		refreshFn: func(_ context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error) {
			updated := cred.Clone()
			updated.Attributes["api_key"] = "fresh-cli-key"
			updated.Metadata["expired"] = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
			return updated, nil
		},
	})
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cliauth-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-cli-key",
		},
		Metadata: map[string]any{
			credentialmgr.MetadataManualRefreshNameKey: "codex",
			"expired": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("register cli auth token: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
			AuthStrategy: AuthStrategyManagedCLIAuthTokenFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected managed credential override")
	}
	if base.lastAPIKey != "fresh-cli-key" {
		t.Fatalf("api key = %q, want fresh-cli-key", base.lastAPIKey)
	}
}

func TestWrapWithCredentialManagerRefreshesGeminiCredentialUsingCustomExpiryDelta(t *testing.T) {
	credMgr := newTestCredentialManager()
	credMgr.SetManualRefresher("gemini", &testProviderManualRefresher{
		refreshFn: func(_ context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error) {
			updated := cred.Clone()
			updated.Attributes["api_key"] = "fresh-gemini-key"
			updated.Metadata["expired"] = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
			return updated, nil
		},
	})
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "gemini-1",
		ProviderType: "gemini",
		ProviderID:   "gemini",
		Source:       credentialmgr.SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-gemini-key",
		},
		Metadata: map[string]any{
			credentialmgr.MetadataManualRefreshNameKey:     "gemini",
			credentialmgr.MetadataManualRefreshExpiryDelta: 10 * time.Second,
			"expired": time.Now().UTC().Add(5 * time.Second).Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("register cli auth token: %v", err)
	}

	base := &testConfigurableProvider{
		cfg: ProviderConfig{
			Id:           "gemini",
			ProviderType: "gemini",
			AuthStrategy: AuthStrategyManagedCLIAuthTokenFirst,
		},
	}
	wrapped := WrapWithCredentialManager(base, credMgr)
	if _, err := wrapped.Chat(context.Background(), &ChatRequest{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if base.lastCred == nil {
		t.Fatal("expected managed credential override")
	}
	if base.lastAPIKey != "fresh-gemini-key" {
		t.Fatalf("api key = %q, want fresh-gemini-key", base.lastAPIKey)
	}
}

type testProviderManualRefresher struct {
	refreshFn func(context.Context, *credentialmgr.Credential) (*credentialmgr.Credential, error)
}

func (r *testProviderManualRefresher) Refresh(ctx context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error) {
	if r == nil || r.refreshFn == nil {
		return nil, nil
	}
	return r.refreshFn(ctx, cred)
}
