package gateway

import (
	"context"
	"errors"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/caddyserver/caddy/v2"
)

type testProvisionLocalAPIKeyStore struct {
	items map[string]*localapikeypkg.LocalAPIKey
}

type testAppConfigStore struct {
	localAPIKeyStore configstoreintf.LocalAPIKeyStorer
}

func (s testAppConfigStore) GetCredentialStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.CredentialStorer, error) {
	return nil, nil
}

func (s testAppConfigStore) GetProviderConfigStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ProviderConfigStorer, error) {
	return nil, nil
}

func (s testAppConfigStore) GetLocalAPIKeyStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.LocalAPIKeyStorer, error) {
	return s.localAPIKeyStore, nil
}

func (s testAppConfigStore) GetRouteStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.RouteStorer, error) {
	return nil, nil
}

func (s *testProvisionLocalAPIKeyStore) ListByUserID(context.Context, string) ([]any, error) {
	return nil, nil
}

func (s *testProvisionLocalAPIKeyStore) Create(_ context.Context, key string, _ string, obj any) error {
	item, ok := obj.(*localapikeypkg.LocalAPIKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*localapikeypkg.LocalAPIKey{}
	}
	cloned := *item
	s.items[key] = &cloned
	return nil
}

func (s *testProvisionLocalAPIKeyStore) Update(_ context.Context, key string, obj any) error {
	item, ok := obj.(*localapikeypkg.LocalAPIKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*localapikeypkg.LocalAPIKey{}
	}
	if _, exists := s.items[key]; !exists {
		return configstoreintf.ErrNotFound
	}
	cloned := *item
	s.items[key] = &cloned
	return nil
}

func (s *testProvisionLocalAPIKeyStore) Delete(context.Context, string) error { return nil }

func (s *testProvisionLocalAPIKeyStore) Get(_ context.Context, key string) (any, error) {
	item, ok := s.items[key]
	if !ok {
		return nil, configstoreintf.ErrNotFound
	}
	return item, nil
}

func TestProvisionAuthenticatorsWithEmptyConfig(t *testing.T) {
	app := &App{cliauthManager: manager.NewManager(nil, nil, nil)}

	if err := app.provisionAuthenticators(caddy.Context{}); err != nil {
		t.Fatalf("provisionAuthenticators() error = %v", err)
	}

	if _, ok := app.cliauthManager.GetAuthenticator("codex"); ok {
		t.Fatal("expected codex authenticator to remain disabled without configuration")
	}
	if _, ok := app.cliauthManager.GetAuthenticator("claude"); ok {
		t.Fatal("expected claude authenticator to remain disabled without configuration")
	}
}
