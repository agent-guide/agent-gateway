package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/gateway"
	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type testConfigStore struct {
	providerStore    configstoreintf.ProviderConfigStorer
	routeStore       configstoreintf.RouteStorer
	localAPIKeyStore configstoreintf.LocalAPIKeyStorer
}

func newTestAgentGateway(configStore configstoreintf.ConfigStorer, cliauthMgr *manager.Manager, staticRoutes []routepkg.Route, staticLocalAPIKeys []localapikeypkg.LocalAPIKey, staticProviders ...map[string]provider.Provider) *gateway.AgentGateway {
	var providers map[string]provider.Provider
	if len(staticProviders) > 0 {
		providers = staticProviders[0]
	}
	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore:        configStore,
		StaticRoutes:       staticRoutes,
		StaticLocalAPIKeys: staticLocalAPIKeys,
		StaticProviders:    providers,
		CLIAuthManager:     cliauthMgr,
	}); err != nil {
		panic(err)
	}
	return agentGateway
}

func (s *testConfigStore) GetCredentialStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.CredentialStorer, error) {
	return nil, nil
}

func (s *testConfigStore) GetProviderConfigStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ProviderConfigStorer, error) {
	return s.providerStore, nil
}

func (s *testConfigStore) GetLocalAPIKeyStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.LocalAPIKeyStorer, error) {
	return s.localAPIKeyStore, nil
}

func (s *testConfigStore) GetRouteStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.RouteStorer, error) {
	return s.routeStore, nil
}

type testRouteStore struct {
	items map[string]*routepkg.Route
	tags  map[string]string
}

func (s *testRouteStore) ListByTag(_ context.Context, tag string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for id, item := range s.items {
		if tag != "" && s.tags[id] != tag {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *testRouteStore) ListByTagPrefix(_ context.Context, tagPrefix string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for id, item := range s.items {
		if tagPrefix != "" && !strings.HasPrefix(s.tags[id], tagPrefix) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *testRouteStore) Create(_ context.Context, id string, tag string, obj any) error {
	r, ok := obj.(*routepkg.Route)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*routepkg.Route{}
	}
	if s.tags == nil {
		s.tags = map[string]string{}
	}
	cloned := *r
	s.items[id] = &cloned
	s.tags[id] = tag
	return nil
}

func (s *testRouteStore) Update(ctx context.Context, id string, obj any) error {
	if _, ok := s.items[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	return s.Create(ctx, id, s.tags[id], obj)
}

func (s *testRouteStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	delete(s.tags, id)
	return nil
}

func (s *testRouteStore) Get(_ context.Context, id string) (any, error) {
	item, ok := s.items[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

type testLocalAPIKeyStore struct {
	items map[string]*localapikeypkg.LocalAPIKey
}

type testProviderConfigStore struct {
	items map[string]*provider.ProviderConfig
}

func (s *testProviderConfigStore) ListByName(_ context.Context, name string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if name != "" && item.ProviderName != name {
			continue
		}
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testProviderConfigStore) Create(_ context.Context, id string, name string, obj any) (string, error) {
	cfg, ok := obj.(*provider.ProviderConfig)
	if !ok {
		return "", errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*provider.ProviderConfig{}
	}
	cloned := *cfg
	cloned.Id = id
	if cloned.ProviderName == "" {
		cloned.ProviderName = name
	}
	s.items[id] = &cloned
	return id, nil
}

func (s *testProviderConfigStore) Update(_ context.Context, id string, obj any) error {
	if _, ok := s.items[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	_, err := s.Create(context.Background(), id, "", obj)
	return err
}

func (s *testProviderConfigStore) Delete(_ context.Context, id string) error {
	if _, ok := s.items[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(s.items, id)
	return nil
}

func (s *testProviderConfigStore) Get(_ context.Context, id string) (string, any, error) {
	item, ok := s.items[id]
	if !ok {
		return "", nil, gorm.ErrRecordNotFound
	}
	cloned := *item
	tag := cloned.ProviderName
	cloned.ProviderName = ""
	return tag, &cloned, nil
}

type stubAdminProvider struct {
	cfg provider.ProviderConfig
}

func (p *stubAdminProvider) Generate(context.Context, *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	return nil, nil
}

func (p *stubAdminProvider) Stream(context.Context, *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p *stubAdminProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (p *stubAdminProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (p *stubAdminProvider) Config() provider.ProviderConfig {
	return p.cfg
}

func (s *testLocalAPIKeyStore) ListByUserID(_ context.Context, userID string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if userID != "" && item.UserID != userID {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *testLocalAPIKeyStore) Create(_ context.Context, key string, _ string, obj any) error {
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

func (s *testLocalAPIKeyStore) Update(ctx context.Context, key string, obj any) error {
	if _, ok := s.items[key]; !ok {
		return gorm.ErrRecordNotFound
	}
	return s.Create(ctx, key, "", obj)
}

func (s *testLocalAPIKeyStore) Delete(_ context.Context, key string) error {
	delete(s.items, key)
	return nil
}

func (s *testLocalAPIKeyStore) Get(_ context.Context, key string) (any, error) {
	item, ok := s.items[key]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

func TestRouteCRUD(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		routeStore: &testRouteStore{items: map[string]*routepkg.Route{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	createBody, err := json.Marshal(routepkg.Route{
		ID:   "chat-prod",
		Name: "chat-prod",
		Targets: []routepkg.RouteTarget{{
			ProviderRef: "openai",
			Mode:        routepkg.TargetModeWeighted,
			Weight:      1,
		}},
	})
	if err != nil {
		t.Fatalf("marshal route: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/admin/routes", bytes.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: got %d want %d", createRec.Code, http.StatusCreated)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/routes/chat-prod", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", getRec.Code, http.StatusOK)
	}

	var got RouteView
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode route: %v", err)
	}
	if got.ID != "chat-prod" {
		t.Fatalf("unexpected route id: got %q want %q", got.ID, "chat-prod")
	}
	if len(got.Targets) != 1 || got.Targets[0].ProviderRef != "openai" {
		t.Fatalf("unexpected targets: %#v", got.Targets)
	}
	if got.Source != "store" || got.ReadOnly {
		t.Fatalf("unexpected route metadata: %#v", got)
	}
}

func TestLocalAPIKeyCRUD(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(localapikeypkg.LocalAPIKey{
		Key:             "lk-test",
		UserID:          "admin",
		Name:            "test key",
		AllowedRouteIDs: []string{"chat-prod"},
	})
	if err != nil {
		t.Fatalf("marshal local api key: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/admin/local_api_keys", bytes.NewReader(body))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: got %d want %d", createRec.Code, http.StatusCreated)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/local_api_keys/lk-test", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", getRec.Code, http.StatusOK)
	}

	var got LocalAPIKeyView
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode local api key: %v", err)
	}
	if got.Key != "lk-test" {
		t.Fatalf("unexpected local api key: got %q want %q", got.Key, "lk-test")
	}
	if len(got.AllowedRouteIDs) != 1 || got.AllowedRouteIDs[0] != "chat-prod" {
		t.Fatalf("unexpected allowed routes: %#v", got.AllowedRouteIDs)
	}
	if got.Source != "store" || got.ReadOnly {
		t.Fatalf("unexpected local api key metadata: %#v", got)
	}
}

func TestLocalAPIKeyGetMarksStaticKeyAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	localAPIKeyManager := gateway.NewLocalAPIKeyManager(&testLocalAPIKeyStore{
		items: map[string]*localapikeypkg.LocalAPIKey{
			"lk-static": {Key: "lk-static", UserID: "admin", Name: "dynamic copy"},
		},
	})
	localAPIKeyManager.InitStaticKeys([]localapikeypkg.LocalAPIKey{
		{Key: "lk-static", UserID: "admin", Name: "static key"},
	})

	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore: &testConfigStore{
			localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{}},
		},
		StaticLocalAPIKeys: []localapikeypkg.LocalAPIKey{
			{Key: "lk-static", UserID: "admin", Name: "static key"},
		},
	}); err != nil {
		t.Fatalf("bootstrap gateway: %v", err)
	}
	handler := NewHandler(agentGateway, nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/local_api_keys/lk-static", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got LocalAPIKeyView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode local api key: %v", err)
	}
	if got.Name != "static key" {
		t.Fatalf("name = %q, want static key", got.Name)
	}
	if got.Source != "caddyfile" || !got.ReadOnly {
		t.Fatalf("unexpected local api key metadata: %#v", got)
	}
}

func TestLocalAPIKeyListMarksStaticKeysAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	store := &testLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{
		"lk-dynamic": {Key: "lk-dynamic", UserID: "admin", Name: "dynamic key"},
	}}
	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore: &testConfigStore{localAPIKeyStore: store},
		StaticLocalAPIKeys: []localapikeypkg.LocalAPIKey{
			{Key: "lk-static", UserID: "admin", Name: "static key"},
		},
	}); err != nil {
		t.Fatalf("bootstrap gateway: %v", err)
	}
	handler := NewHandler(agentGateway, nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/local_api_keys", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got struct {
		Items []LocalAPIKeyView `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode local api keys: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("item count = %d, want 2", len(got.Items))
	}

	byKey := map[string]LocalAPIKeyView{}
	for _, item := range got.Items {
		byKey[item.Key] = item
	}
	if byKey["lk-static"].Source != "caddyfile" || !byKey["lk-static"].ReadOnly {
		t.Fatalf("unexpected static local api key metadata: %#v", byKey["lk-static"])
	}
	if byKey["lk-dynamic"].Source != "store" || byKey["lk-dynamic"].ReadOnly {
		t.Fatalf("unexpected dynamic local api key metadata: %#v", byKey["lk-dynamic"])
	}
}

func TestProviderCRUD(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(provider.ProviderConfig{
		Id:           "openai-main",
		ProviderName: "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("marshal provider config: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/admin/providers", bytes.NewReader(body))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: got %d want %d", createRec.Code, http.StatusCreated)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/providers/openai-main", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", getRec.Code, http.StatusOK)
	}

	var got ProviderView
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode provider: %v", err)
	}
	if got.Id != "openai-main" {
		t.Fatalf("unexpected provider id: got %q want %q", got.Id, "openai-main")
	}
	if got.ProviderName != "openai" {
		t.Fatalf("unexpected provider_name: got %q want %q", got.ProviderName, "openai")
	}
	if got.Source != "store" || got.ReadOnly {
		t.Fatalf("unexpected provider metadata: %#v", got)
	}
}

func TestProviderEnableDisable(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{
			"openai-main": {Id: "openai-main", ProviderName: "openai"},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	disableReq := httptest.NewRequest(http.MethodPost, "/admin/providers/openai-main/disable", nil)
	disableReq.Header.Set("Authorization", "Bearer "+token)
	disableRec := httptest.NewRecorder()
	handler.ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want %d", disableRec.Code, http.StatusOK)
	}

	var disabled ProviderView
	if err := json.NewDecoder(disableRec.Body).Decode(&disabled); err != nil {
		t.Fatalf("decode disabled provider: %v", err)
	}
	if !disabled.Disabled {
		t.Fatal("provider disabled = false, want true")
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/admin/providers/openai-main/enable", nil)
	enableReq.Header.Set("Authorization", "Bearer "+token)
	enableRec := httptest.NewRecorder()
	handler.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want %d", enableRec.Code, http.StatusOK)
	}

	var enabled ProviderView
	if err := json.NewDecoder(enableRec.Body).Decode(&enabled); err != nil {
		t.Fatalf("decode enabled provider: %v", err)
	}
	if enabled.Disabled {
		t.Fatal("provider disabled = true, want false")
	}
}

func TestRouteEnableDisable(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		routeStore: &testRouteStore{items: map[string]*routepkg.Route{
			"chat-prod": {
				ID: "chat-prod",
				Targets: []routepkg.RouteTarget{{
					ProviderRef: "openai",
				}},
			},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	disableReq := httptest.NewRequest(http.MethodPost, "/admin/routes/chat-prod/disable", nil)
	disableReq.Header.Set("Authorization", "Bearer "+token)
	disableRec := httptest.NewRecorder()
	handler.ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want %d", disableRec.Code, http.StatusOK)
	}

	var disabled RouteView
	if err := json.NewDecoder(disableRec.Body).Decode(&disabled); err != nil {
		t.Fatalf("decode disabled route: %v", err)
	}
	if !disabled.Disabled {
		t.Fatal("route disabled = false, want true")
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/admin/routes/chat-prod/enable", nil)
	enableReq.Header.Set("Authorization", "Bearer "+token)
	enableRec := httptest.NewRecorder()
	handler.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want %d", enableRec.Code, http.StatusOK)
	}

	var enabled RouteView
	if err := json.NewDecoder(enableRec.Body).Decode(&enabled); err != nil {
		t.Fatalf("decode enabled route: %v", err)
	}
	if enabled.Disabled {
		t.Fatal("route disabled = true, want false")
	}
}

func TestLocalAPIKeyEnableDisable(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{
			"lk-test": {Key: "lk-test", UserID: "admin"},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	disableReq := httptest.NewRequest(http.MethodPost, "/admin/local_api_keys/lk-test/disable", nil)
	disableReq.Header.Set("Authorization", "Bearer "+token)
	disableRec := httptest.NewRecorder()
	handler.ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want %d", disableRec.Code, http.StatusOK)
	}

	var disabled LocalAPIKeyView
	if err := json.NewDecoder(disableRec.Body).Decode(&disabled); err != nil {
		t.Fatalf("decode disabled local api key: %v", err)
	}
	if !disabled.Disabled {
		t.Fatal("local api key disabled = false, want true")
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/admin/local_api_keys/lk-test/enable", nil)
	enableReq.Header.Set("Authorization", "Bearer "+token)
	enableRec := httptest.NewRecorder()
	handler.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want %d", enableRec.Code, http.StatusOK)
	}

	var enabled LocalAPIKeyView
	if err := json.NewDecoder(enableRec.Body).Decode(&enabled); err != nil {
		t.Fatalf("decode enabled local api key: %v", err)
	}
	if enabled.Disabled {
		t.Fatal("local api key disabled = true, want false")
	}
}

func TestProviderGetMarksStaticProviderAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{
			"openai-main": {Id: "openai-main", ProviderName: "openai", BaseURL: "https://dynamic.example"},
		}},
	}, nil, nil, nil, map[string]provider.Provider{
		"openai-main": &stubAdminProvider{cfg: provider.ProviderConfig{Id: "openai-main", ProviderName: "openai", BaseURL: "https://static.example"}},
	}), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/providers/openai-main", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got ProviderView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode provider: %v", err)
	}
	if got.BaseURL != "https://static.example" {
		t.Fatalf("base_url = %q, want https://static.example", got.BaseURL)
	}
	if got.Source != "caddyfile" || !got.ReadOnly {
		t.Fatalf("unexpected provider metadata: %#v", got)
	}
}

func TestProviderListMarksStaticProvidersAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{
			"openai-dynamic": {Id: "openai-dynamic", ProviderName: "openai"},
		}},
	}, nil, nil, nil, map[string]provider.Provider{
		"anthropic-static": &stubAdminProvider{cfg: provider.ProviderConfig{Id: "anthropic-static", ProviderName: "anthropic"}},
	}), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got struct {
		Items []ProviderView `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode providers: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("item count = %d, want 2", len(got.Items))
	}

	byID := map[string]ProviderView{}
	for _, item := range got.Items {
		byID[item.Id] = item
	}
	if byID["anthropic-static"].Source != "caddyfile" || !byID["anthropic-static"].ReadOnly {
		t.Fatalf("unexpected static provider metadata: %#v", byID["anthropic-static"])
	}
	if byID["openai-dynamic"].Source != "store" || byID["openai-dynamic"].ReadOnly {
		t.Fatalf("unexpected dynamic provider metadata: %#v", byID["openai-dynamic"])
	}
}

func TestProviderDeleteRejectsStaticProvider(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
	}, nil, nil, nil, map[string]provider.Provider{
		"openai-main": &stubAdminProvider{cfg: provider.ProviderConfig{Id: "openai-main", ProviderName: "openai"}},
	}), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodDelete, "/admin/providers/openai-main", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("unexpected delete status: got %d want %d", rec.Code, http.StatusConflict)
	}
}

func TestProtectedRouteRejectsRequestsWhenAdminAuthIsNotConfigured(t *testing.T) {
	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{}},
	}, nil, nil, nil, nil), nil, "", "", nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/local_api_keys", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestCreateLocalAPIKeyRejectsMismatchedSessionUserID(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)

	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(localapikeypkg.LocalAPIKey{
		Key:    "lk-test",
		UserID: "someone-else",
	})
	if err != nil {
		t.Fatalf("marshal local api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/local_api_keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected create status: got %d want %d", rec.Code, http.StatusForbidden)
	}
}

func TestListLocalAPIKeysRejectsMismatchedSessionUserID(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*localapikeypkg.LocalAPIKey{
			"lk-test": {Key: "lk-test", UserID: "admin"},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash), nil)

	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/local_api_keys?user_id=someone-else", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRouteGetPrefersStaticRouteManager(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	store := &testRouteStore{
		items: map[string]*routepkg.Route{
			"chat-prod": {
				ID:      "chat-prod",
				Name:    "dynamic",
				Targets: []routepkg.RouteTarget{{ProviderRef: "openai"}},
			},
		},
	}
	handler := NewHandler(newTestAgentGateway(&testConfigStore{routeStore: store}, nil, []routepkg.Route{{
		ID:      "chat-prod",
		Name:    "static",
		Targets: []routepkg.RouteTarget{{ProviderRef: "anthropic"}},
	}}, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/routes/chat-prod", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got RouteView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode route: %v", err)
	}
	if got.Name != "static" {
		t.Fatalf("route name = %q, want static", got.Name)
	}
	if got.Source != "caddyfile" || !got.ReadOnly {
		t.Fatalf("unexpected route metadata: %#v", got)
	}
}

func TestRouteListMarksStaticRoutesAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	store := &testRouteStore{
		items: map[string]*routepkg.Route{
			"chat-dynamic": {
				ID:      "chat-dynamic",
				Name:    "dynamic",
				Targets: []routepkg.RouteTarget{{ProviderRef: "openai"}},
			},
		},
	}
	handler := NewHandler(newTestAgentGateway(&testConfigStore{routeStore: store}, nil, []routepkg.Route{{
		ID:      "chat-static",
		Name:    "static",
		Targets: []routepkg.RouteTarget{{ProviderRef: "anthropic"}},
	}}, nil, nil), nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/routes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got struct {
		Items []RouteView `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode routes: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("item count = %d, want 2", len(got.Items))
	}

	byID := map[string]RouteView{}
	for _, item := range got.Items {
		byID[item.ID] = item
	}
	if byID["chat-static"].Source != "caddyfile" || !byID["chat-static"].ReadOnly {
		t.Fatalf("unexpected static route metadata: %#v", byID["chat-static"])
	}
	if byID["chat-dynamic"].Source != "store" || byID["chat-dynamic"].ReadOnly {
		t.Fatalf("unexpected dynamic route metadata: %#v", byID["chat-dynamic"])
	}
}

func loginForTest(t *testing.T, handler *Handler, username string, password string) string {
	t.Helper()

	loginBody, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/admin/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("unexpected login status: got %d want %d", loginRec.Code, http.StatusOK)
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginRec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatal("login token is empty")
	}
	return loginResp.Token
}
