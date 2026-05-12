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

	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	dispatcherpkg "github.com/agent-guide/agent-gateway/pkg/dispatcher"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type testConfigStore struct {
	providerStore   configstoreintf.ProviderConfigStorer
	routeStore      configstoreintf.RouteStorer
	virtualKeyStore configstoreintf.VirtualKeyStorer
	modelStore      configstoreintf.ModelStorer
}

func newTestAgentGateway(configStore configstoreintf.ConfigStorer, cliauthMgr *cliauth.Manager, cliauthRefresher *cliauth.AutoRefresher, staticRoutes []routepkg.AgentRoute, staticVirtualKeys []virtualkeypkg.VirtualKey, staticProviders ...map[string]provider.Provider) *gateway.AgentGateway {
	var providers map[string]provider.Provider
	if len(staticProviders) > 0 {
		providers = staticProviders[0]
	}
	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore:       configStore,
		StaticRoutes:      staticRoutes,
		StaticVirtualKeys: staticVirtualKeys,
		StaticProviders:   providers,
		CLIAuthManager:    cliauthMgr,
		CLIAuthRefresher:  cliauthRefresher,
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

func (s *testConfigStore) GetVirtualKeyStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.VirtualKeyStorer, error) {
	return s.virtualKeyStore, nil
}

func (s *testConfigStore) GetRouteStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.RouteStorer, error) {
	return s.routeStore, nil
}

func (s *testConfigStore) GetModelStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ModelStorer, error) {
	return s.modelStore, nil
}

type testModelStore struct {
	items map[string]*modelcatalog.ManagedModel
}

func (s *testModelStore) List(_ context.Context) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testModelStore) Get(_ context.Context, providerID string, upstreamModel string) (any, bool, error) {
	item, ok := s.items[providerID+"\x00"+upstreamModel]
	if !ok {
		return nil, false, nil
	}
	cloned := *item
	return &cloned, true, nil
}

func (s *testModelStore) Create(_ context.Context, obj any) error {
	item, ok := obj.(*modelcatalog.ManagedModel)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*modelcatalog.ManagedModel{}
	}
	key := item.ProviderID + "\x00" + item.UpstreamModel
	if _, exists := s.items[key]; exists {
		return errors.New("managed model already exists")
	}
	cloned := *item
	s.items[key] = &cloned
	return nil
}

func (s *testModelStore) Update(_ context.Context, obj any) error {
	item, ok := obj.(*modelcatalog.ManagedModel)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*modelcatalog.ManagedModel{}
	}
	key := item.ProviderID + "\x00" + item.UpstreamModel
	if _, exists := s.items[key]; !exists {
		return gorm.ErrRecordNotFound
	}
	cloned := *item
	s.items[key] = &cloned
	return nil
}

func (s *testModelStore) Delete(_ context.Context, providerID string, upstreamModel string) error {
	delete(s.items, providerID+"\x00"+upstreamModel)
	return nil
}

type testRouteStore struct {
	items map[string]*routepkg.AgentRoute
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
	r, ok := obj.(*routepkg.AgentRoute)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*routepkg.AgentRoute{}
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

type testVirtualKeyStore struct {
	items map[string]*virtualkeypkg.VirtualKey
}

type testProviderConfigStore struct {
	items map[string]*provider.ProviderConfig
}

func (s *testProviderConfigStore) ListByType(_ context.Context, name string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if name != "" && item.ProviderType != name {
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
	if cloned.ProviderType == "" {
		cloned.ProviderType = name
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
	tag := cloned.ProviderType
	cloned.ProviderType = ""
	return tag, &cloned, nil
}

type stubAdminProvider struct {
	cfg    provider.ProviderConfig
	models []provider.ModelInfo
}

func (p *stubAdminProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}

func (p *stubAdminProvider) StreamChat(context.Context, *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p *stubAdminProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return append([]provider.ModelInfo(nil), p.models...), nil
}

func (p *stubAdminProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (p *stubAdminProvider) Config() provider.ProviderConfig {
	return p.cfg
}

func (s *testVirtualKeyStore) ListByTag(_ context.Context, tag string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if tag != "" && item.Tag != tag {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *testVirtualKeyStore) Create(_ context.Context, id string, _ string, obj any) error {
	item, ok := obj.(*virtualkeypkg.VirtualKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*virtualkeypkg.VirtualKey{}
	}
	cloned := *item
	s.items[id] = &cloned
	return nil
}

func (s *testVirtualKeyStore) Update(ctx context.Context, id string, obj any) error {
	if _, ok := s.items[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	return s.Create(ctx, id, "", obj)
}

func (s *testVirtualKeyStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func (s *testVirtualKeyStore) Get(_ context.Context, id string) (any, error) {
	item, ok := s.items[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

func (s *testVirtualKeyStore) GetByKey(_ context.Context, key string) (any, error) {
	for _, item := range s.items {
		if item.Key == key {
			return item, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func TestRouteCRUD(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		routeStore: &testRouteStore{items: map[string]*routepkg.AgentRoute{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	createBody, err := json.Marshal(routepkg.AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		TargetPolicy: routepkg.RouteTargetPolicy{
			ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai"},
		},
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
	if got.TargetPolicy.ProviderTarget.ProviderID != "openai" {
		t.Fatalf("unexpected provider_target: %#v", got.TargetPolicy.ProviderTarget)
	}
	if got.Source != "store" || got.ReadOnly {
		t.Fatalf("unexpected route metadata: %#v", got)
	}
}

func TestVirtualKeyCRUD(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		virtualKeyStore: &testVirtualKeyStore{items: map[string]*virtualkeypkg.VirtualKey{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(virtualkeypkg.VirtualKey{
		ID:              "vk-test",
		Tag:             "admin",
		AllowedRouteIDs: []string{"chat-prod"},
	})
	if err != nil {
		t.Fatalf("marshal virtual key: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/admin/virtual_keys", bytes.NewReader(body))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: got %d want %d", createRec.Code, http.StatusCreated)
	}

	var created virtualkeypkg.VirtualKey
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created virtual key: %v", err)
	}
	if created.Key == "" {
		t.Fatal("created virtual key is empty")
	}
	if !strings.HasPrefix(created.Key, virtualkeypkg.GeneratedKeyPrefix) {
		t.Fatalf("created virtual key = %q, want prefix %q", created.Key, virtualkeypkg.GeneratedKeyPrefix)
	}
	if created.ID != "vk-test" {
		t.Fatalf("created virtual key id = %q, want vk-test", created.ID)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/virtual_keys/"+created.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", getRec.Code, http.StatusOK)
	}

	var got VirtualKeyView
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode virtual key: %v", err)
	}
	if got.Key != created.Key {
		t.Fatalf("unexpected virtual key: got %q want %q", got.Key, created.Key)
	}
	if got.ID != created.ID {
		t.Fatalf("unexpected virtual key id: got %q want %q", got.ID, created.ID)
	}
	if len(got.AllowedRouteIDs) != 1 || got.AllowedRouteIDs[0] != "chat-prod" {
		t.Fatalf("unexpected allowed routes: %#v", got.AllowedRouteIDs)
	}
	if got.Source != "store" || got.ReadOnly {
		t.Fatalf("unexpected virtual key metadata: %#v", got)
	}
}

func TestVirtualKeyGetMarksStaticKeyAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	virtualKeyManager := virtualkeypkg.NewVirtualKeyManager(&testVirtualKeyStore{
		items: map[string]*virtualkeypkg.VirtualKey{
			"vk-static": {ID: "vk-static", Key: "lk-static", Tag: "dynamic copy"},
		},
	})
	virtualKeyManager.InitStaticKeys([]virtualkeypkg.VirtualKey{
		{ID: "vk-static", Key: "lk-static", Tag: "static key"},
	})

	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore: &testConfigStore{
			virtualKeyStore: &testVirtualKeyStore{items: map[string]*virtualkeypkg.VirtualKey{}},
		},
		StaticVirtualKeys: []virtualkeypkg.VirtualKey{
			{ID: "vk-static", Key: "lk-static", Tag: "static key"},
		},
	}); err != nil {
		t.Fatalf("bootstrap gateway: %v", err)
	}
	handler := NewHandler(agentGateway, nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/virtual_keys/vk-static", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got VirtualKeyView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode virtual key: %v", err)
	}
	if got.ID != "vk-static" {
		t.Fatalf("id = %q, want vk-static", got.ID)
	}
	if got.Tag != "static key" {
		t.Fatalf("tag = %q, want static key", got.Tag)
	}
	if got.Source != "caddyfile" || !got.ReadOnly {
		t.Fatalf("unexpected virtual key metadata: %#v", got)
	}
}

func TestVirtualKeyListMarksStaticKeysAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	store := &testVirtualKeyStore{items: map[string]*virtualkeypkg.VirtualKey{
		"vk-dynamic": {ID: "vk-dynamic", Key: "lk-dynamic", Tag: "dynamic key"},
	}}
	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore: &testConfigStore{virtualKeyStore: store},
		StaticVirtualKeys: []virtualkeypkg.VirtualKey{
			{ID: "vk-static", Key: "lk-static", Tag: "static key"},
		},
	}); err != nil {
		t.Fatalf("bootstrap gateway: %v", err)
	}
	handler := NewHandler(agentGateway, nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/virtual_keys", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got struct {
		Items []VirtualKeyView `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode virtual keys: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("item count = %d, want 2", len(got.Items))
	}

	byID := map[string]VirtualKeyView{}
	for _, item := range got.Items {
		byID[item.ID] = item
	}
	if byID["vk-static"].Source != "caddyfile" || !byID["vk-static"].ReadOnly {
		t.Fatalf("unexpected static virtual key metadata: %#v", byID["vk-static"])
	}
	if byID["vk-dynamic"].Source != "store" || byID["vk-dynamic"].ReadOnly {
		t.Fatalf("unexpected dynamic virtual key metadata: %#v", byID["vk-dynamic"])
	}
}

func TestProviderCRUD(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(provider.ProviderConfig{
		Id:           "openai-main",
		ProviderType: "openai",
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
	if got.ProviderType != "openai" {
		t.Fatalf("unexpected provider_type: got %q want %q", got.ProviderType, "openai")
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
			"openai-main": {Id: "openai-main", ProviderType: "openai"},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
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

func TestProviderTypeListEnableDisable(t *testing.T) {
	const providerType = "test-admin-provider-name"
	provider.RegisterProviderFactory(providerType, func(cfg provider.ProviderConfig) (provider.Provider, error) {
		return &stubAdminProvider{cfg: cfg}, nil
	})
	defer func() {
		if err := provider.EnableProviderType(providerType); err != nil {
			t.Fatalf("restore provider type: %v", err)
		}
	}()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(nil, nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	disableReq := httptest.NewRequest(http.MethodPost, "/admin/provider_types/"+providerType+"/disable", nil)
	disableReq.Header.Set("Authorization", "Bearer "+token)
	disableRec := httptest.NewRecorder()
	handler.ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want %d", disableRec.Code, http.StatusOK)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/admin/provider_types", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRec.Code, http.StatusOK)
	}

	var listed struct {
		Items []ProviderTypeView `json:"items"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode provider types: %v", err)
	}
	found := false
	for _, item := range listed.Items {
		if item.ProviderType != providerType {
			continue
		}
		found = true
		if item.Enabled {
			t.Fatal("provider type enabled = true, want false")
		}
	}
	if !found {
		t.Fatalf("provider type %q not listed", providerType)
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/admin/provider_types/"+providerType+"/enable", nil)
	enableReq.Header.Set("Authorization", "Bearer "+token)
	enableRec := httptest.NewRecorder()
	handler.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want %d", enableRec.Code, http.StatusOK)
	}

	var enabled struct {
		Status       string `json:"status"`
		ProviderType string `json:"provider_type"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.NewDecoder(enableRec.Body).Decode(&enabled); err != nil {
		t.Fatalf("decode enabled provider type: %v", err)
	}
	if enabled.Status != "enabled" || enabled.ProviderType != providerType || !enabled.Enabled {
		t.Fatalf("unexpected enable response: %#v", enabled)
	}
}

func TestLLMApiHandlerTypeListEnableDisable(t *testing.T) {
	const handlerType = "test-admin-llm-api-handler"
	dispatcherpkg.RegisterLLMApiHandlerType(handlerType)
	defer func() {
		if err := dispatcherpkg.EnableLLMApiHandlerType(handlerType); err != nil {
			t.Fatalf("restore llm api handler type: %v", err)
		}
	}()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(nil, nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	disableReq := httptest.NewRequest(http.MethodPost, "/admin/llm_api_handler_types/"+handlerType+"/disable", nil)
	disableReq.Header.Set("Authorization", "Bearer "+token)
	disableRec := httptest.NewRecorder()
	handler.ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want %d", disableRec.Code, http.StatusOK)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/admin/llm_api_handler_types", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRec.Code, http.StatusOK)
	}

	var listed struct {
		Items []LLMApiHandlerTypeView `json:"items"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode llm api handler types: %v", err)
	}
	found := false
	for _, item := range listed.Items {
		if item.LLMApiHandlerType != handlerType {
			continue
		}
		found = true
		if item.Enabled {
			t.Fatal("llm api handler type enabled = true, want false")
		}
	}
	if !found {
		t.Fatalf("llm api handler type %q not listed", handlerType)
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/admin/llm_api_handler_types/"+handlerType+"/enable", nil)
	enableReq.Header.Set("Authorization", "Bearer "+token)
	enableRec := httptest.NewRecorder()
	handler.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want %d", enableRec.Code, http.StatusOK)
	}

	var enabled struct {
		Status            string `json:"status"`
		LLMApiHandlerType string `json:"llm_api_handler_type"`
		Enabled           bool   `json:"enabled"`
	}
	if err := json.NewDecoder(enableRec.Body).Decode(&enabled); err != nil {
		t.Fatalf("decode enabled llm api handler type: %v", err)
	}
	if enabled.Status != "enabled" || enabled.LLMApiHandlerType != handlerType || !enabled.Enabled {
		t.Fatalf("unexpected enable response: %#v", enabled)
	}
}

func TestRouteEnableDisable(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		routeStore: &testRouteStore{items: map[string]*routepkg.AgentRoute{
			"chat-prod": {
				ID: "chat-prod",
				TargetPolicy: routepkg.RouteTargetPolicy{
					ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai"},
				},
			},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
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

func TestVirtualKeyEnableDisable(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		virtualKeyStore: &testVirtualKeyStore{items: map[string]*virtualkeypkg.VirtualKey{
			"vk-test": {ID: "vk-test", Key: "lk-test", Tag: "admin"},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	disableReq := httptest.NewRequest(http.MethodPost, "/admin/virtual_keys/vk-test/disable", nil)
	disableReq.Header.Set("Authorization", "Bearer "+token)
	disableRec := httptest.NewRecorder()
	handler.ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want %d", disableRec.Code, http.StatusOK)
	}

	var disabled VirtualKeyView
	if err := json.NewDecoder(disableRec.Body).Decode(&disabled); err != nil {
		t.Fatalf("decode disabled virtual key: %v", err)
	}
	if !disabled.Disabled {
		t.Fatal("virtual key disabled = false, want true")
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/admin/virtual_keys/vk-test/enable", nil)
	enableReq.Header.Set("Authorization", "Bearer "+token)
	enableRec := httptest.NewRecorder()
	handler.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want %d", enableRec.Code, http.StatusOK)
	}

	var enabled VirtualKeyView
	if err := json.NewDecoder(enableRec.Body).Decode(&enabled); err != nil {
		t.Fatalf("decode enabled virtual key: %v", err)
	}
	if enabled.Disabled {
		t.Fatal("virtual key disabled = true, want false")
	}
}

func TestProviderGetMarksStaticProviderAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{
			"openai-main": {Id: "openai-main", ProviderType: "openai", BaseURL: "https://dynamic.example"},
		}},
	}, nil, nil, nil, nil, map[string]provider.Provider{
		"openai-main": &stubAdminProvider{cfg: provider.ProviderConfig{Id: "openai-main", ProviderType: "openai", BaseURL: "https://static.example"}},
	}), nil, "admin", string(passwordHash))
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
			"openai-dynamic": {Id: "openai-dynamic", ProviderType: "openai"},
		}},
	}, nil, nil, nil, nil, map[string]provider.Provider{
		"anthropic-static": &stubAdminProvider{cfg: provider.ProviderConfig{Id: "anthropic-static", ProviderType: "anthropic"}},
	}), nil, "admin", string(passwordHash))
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

func TestCredentialListIncludesProviderStaticAPIKeysAsReadOnly(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	credMgr := credentialmgr.NewManager(nil)
	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore: &testConfigStore{
			providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
		},
		CredentialManager: credMgr,
		StaticProviders: map[string]provider.Provider{
			"openai-static": &stubAdminProvider{cfg: provider.ProviderConfig{
				Id:           "openai-static",
				ProviderType: "openai",
				APIKey:       "static-key",
				BaseURL:      "https://static.example",
			}},
		},
	}); err != nil {
		t.Fatalf("bootstrap gateway: %v", err)
	}
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		ProviderID:   "openai-static",
		Source:       credentialmgr.SourceAPIKey,
		Attributes:   map[string]string{"api_key": "managed-key"},
	}); err != nil {
		t.Fatalf("register managed credential: %v", err)
	}
	staticCred := provider.ProviderConfigAPIKeyCredential(provider.ProviderConfig{
		Id:           "openai-static",
		ProviderType: "openai",
		APIKey:       "static-key",
		BaseURL:      "https://static.example",
	}, "openai-static")
	if staticCred == nil {
		t.Fatal("expected static credential")
	}
	if err := credMgr.RegisterCredential(context.Background(), staticCred); err != nil {
		t.Fatalf("register static credential in manager: %v", err)
	}

	handler := NewHandler(agentGateway, nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/credentials", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got struct {
		Items []CredentialView `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode credentials: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("item count = %d, want 2", len(got.Items))
	}

	byID := map[string]CredentialView{}
	for _, item := range got.Items {
		byID[item.ID] = item
	}
	staticID := provider.ProviderConfigAPIKeyCredentialID(provider.ProviderConfig{Id: "openai-static"})
	if byID[staticID].Attributes["api_key"] != "static-key" || !byID[staticID].ReadOnly {
		t.Fatalf("unexpected static credential view: %#v", byID[staticID])
	}
	if byID["cred-1"].Attributes["api_key"] != "managed-key" || byID["cred-1"].ReadOnly {
		t.Fatalf("unexpected managed credential view: %#v", byID["cred-1"])
	}
}

func TestCredentialCreateUsesProviderID(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	credMgr := credentialmgr.NewManager(nil)
	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{
			"openai-main": {Id: "openai-main", ProviderType: "openai"},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	handler.credentialManager = credMgr
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(map[string]any{
		"provider_id": "openai-main",
		"label":       "primary",
		"attributes": map[string]string{
			"api_key": "sk-test",
		},
	})
	if err != nil {
		t.Fatalf("marshal create request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/credentials", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: got %d want %d", rec.Code, http.StatusCreated)
	}

	var got credentialmgr.Credential
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode created credential: %v", err)
	}
	if strings.TrimSpace(got.ID) == "" {
		t.Fatal("expected created credential response to include id")
	}
	if got.ProviderID != "openai-main" {
		t.Fatalf("unexpected provider_id: got %q want %q", got.ProviderID, "openai-main")
	}
	if got.ProviderType != "openai" {
		t.Fatalf("unexpected provider_type: got %q want %q", got.ProviderType, "openai")
	}
	if got.Label != "primary" {
		t.Fatalf("unexpected label: got %q want %q", got.Label, "primary")
	}
}

func TestCredentialCreateRejectsUnknownProviderID(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	credMgr := credentialmgr.NewManager(nil)
	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	handler.credentialManager = credMgr
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(map[string]any{
		"provider_id": "missing-provider",
		"attributes": map[string]string{
			"api_key": "sk-test",
		},
	})
	if err != nil {
		t.Fatalf("marshal create request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/credentials", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected create status: got %d want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCredentialUpdateRejectsProviderStaticAPIKeys(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	credMgr := credentialmgr.NewManager(nil)
	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore: &testConfigStore{
			providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
		},
		CredentialManager: credMgr,
		StaticProviders: map[string]provider.Provider{
			"openai-static": &stubAdminProvider{cfg: provider.ProviderConfig{
				Id:           "openai-static",
				ProviderType: "openai",
				APIKey:       "static-key",
			}},
		},
	}); err != nil {
		t.Fatalf("bootstrap gateway: %v", err)
	}
	staticCred := provider.ProviderConfigAPIKeyCredential(provider.ProviderConfig{
		Id:           "openai-static",
		ProviderType: "openai",
		APIKey:       "static-key",
	}, "openai-static")
	if staticCred == nil {
		t.Fatal("expected static credential")
	}
	if err := credMgr.RegisterCredential(context.Background(), staticCred); err != nil {
		t.Fatalf("register static credential in manager: %v", err)
	}
	handler := NewHandler(agentGateway, nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(map[string]any{
		"label": "updated",
	})
	if err != nil {
		t.Fatalf("marshal update request: %v", err)
	}

	staticID := provider.ProviderConfigAPIKeyCredentialID(provider.ProviderConfig{Id: "openai-static"})
	req := httptest.NewRequest(http.MethodPut, "/admin/credentials/"+staticID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected update status: got %d want %d", rec.Code, http.StatusForbidden)
	}
}

func TestCredentialGetPrefersReadOnlyViewForProviderStaticAPIKeys(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	credMgr := credentialmgr.NewManager(nil)
	agentGateway := gateway.NewAgentGateway()
	if err := agentGateway.Bootstrap(context.Background(), gateway.BootstrapOptions{
		ConfigStore: &testConfigStore{
			providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
		},
		CredentialManager: credMgr,
		StaticProviders: map[string]provider.Provider{
			"openai-static": &stubAdminProvider{cfg: provider.ProviderConfig{
				Id:           "openai-static",
				ProviderType: "openai",
				APIKey:       "static-key",
			}},
		},
	}); err != nil {
		t.Fatalf("bootstrap gateway: %v", err)
	}
	staticCred := provider.ProviderConfigAPIKeyCredential(provider.ProviderConfig{
		Id:           "openai-static",
		ProviderType: "openai",
		APIKey:       "static-key",
	}, "openai-static")
	if staticCred == nil {
		t.Fatal("expected static credential")
	}
	if err := credMgr.RegisterCredential(context.Background(), staticCred); err != nil {
		t.Fatalf("register static credential in manager: %v", err)
	}

	handler := NewHandler(agentGateway, nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	staticID := provider.ProviderConfigAPIKeyCredentialID(provider.ProviderConfig{Id: "openai-static"})
	req := httptest.NewRequest(http.MethodGet, "/admin/credentials/"+staticID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got CredentialView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode credential: %v", err)
	}
	if !got.ReadOnly {
		t.Fatalf("static credential should be read-only: %#v", got)
	}
}

func TestProviderDeleteRejectsStaticProvider(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}},
	}, nil, nil, nil, nil, map[string]provider.Provider{
		"openai-main": &stubAdminProvider{cfg: provider.ProviderConfig{Id: "openai-main", ProviderType: "openai"}},
	}), nil, "admin", string(passwordHash))
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
		virtualKeyStore: &testVirtualKeyStore{items: map[string]*virtualkeypkg.VirtualKey{}},
	}, nil, nil, nil, nil), nil, "", "")

	req := httptest.NewRequest(http.MethodGet, "/admin/virtual_keys", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestCreateVirtualKeyDoesNotBindTagToSessionUser(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		virtualKeyStore: &testVirtualKeyStore{items: map[string]*virtualkeypkg.VirtualKey{}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))

	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(virtualkeypkg.VirtualKey{
		ID:  "vk-tag-test",
		Tag: "someone-else",
	})
	if err != nil {
		t.Fatalf("marshal virtual key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/virtual_keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: got %d want %d", rec.Code, http.StatusCreated)
	}

	var created virtualkeypkg.VirtualKey
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created virtual key: %v", err)
	}
	if created.Tag != "someone-else" {
		t.Fatalf("created tag = %q, want someone-else", created.Tag)
	}
}

func TestListVirtualKeysFiltersByTag(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		virtualKeyStore: &testVirtualKeyStore{items: map[string]*virtualkeypkg.VirtualKey{
			"vk-admin": {ID: "vk-admin", Key: "lk-admin", Tag: "admin"},
			"vk-other": {ID: "vk-other", Key: "lk-other", Tag: "someone-else"},
		}},
	}, nil, nil, nil, nil), nil, "admin", string(passwordHash))

	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/virtual_keys?tag=someone-else", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got struct {
		Items []VirtualKeyView `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode virtual keys: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "vk-other" || got.Items[0].Key != "lk-other" {
		t.Fatalf("unexpected filtered virtual keys: %#v", got.Items)
	}
}

func TestRouteGetPrefersStaticAgentRouteManager(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	store := &testRouteStore{
		items: map[string]*routepkg.AgentRoute{
			"chat-prod": {
				ID: "chat-prod",
				TargetPolicy: routepkg.RouteTargetPolicy{
					ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai"},
				},
			},
		},
	}
	handler := NewHandler(newTestAgentGateway(&testConfigStore{routeStore: store}, nil, nil, []routepkg.AgentRoute{{
		ID: "chat-prod",
		TargetPolicy: routepkg.RouteTargetPolicy{
			ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "anthropic"},
		},
	}}, nil), nil, "admin", string(passwordHash))
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
	if got.ID != "chat-prod" {
		t.Fatalf("route id = %q, want chat-prod", got.ID)
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
		items: map[string]*routepkg.AgentRoute{
			"chat-dynamic": {
				ID: "chat-dynamic",
				TargetPolicy: routepkg.RouteTargetPolicy{
					ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai"},
				},
			},
		},
	}
	handler := NewHandler(newTestAgentGateway(&testConfigStore{routeStore: store}, nil, nil, []routepkg.AgentRoute{{
		ID: "chat-static",
		TargetPolicy: routepkg.RouteTargetPolicy{
			ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "anthropic"},
		},
	}}, nil), nil, "admin", string(passwordHash))
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

func TestManagedModelViewIncludesResolvedAndSnapshotFields(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{
		modelStore: &testModelStore{
			items: map[string]*modelcatalog.ManagedModel{
				"openai-main\x00gpt-4.1-mini": {
					ProviderID:    "openai-main",
					UpstreamModel: "gpt-4.1-mini",
					Enabled:       true,
				},
			},
		},
	}, nil, nil, nil, nil, map[string]provider.Provider{
		"openai-main": &stubAdminProvider{
			cfg: provider.ProviderConfig{Id: "openai-main", ProviderType: "openai"},
			models: []provider.ModelInfo{{
				ID:          "gpt-4.1-mini",
				DisplayName: "GPT 4.1 Mini",
				Description: "fast chat",
				Capabilities: provider.ModelCapabilities{
					Streaming: true,
					Tools:     true,
				},
			}},
		},
	}), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/models/managed/openai-main/gpt-4.1-mini", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: got %d want %d", rec.Code, http.StatusOK)
	}

	var got ManagedConcreteModelView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode managed model view: %v", err)
	}
	if got.ProviderType != "openai" {
		t.Fatalf("ProviderType = %q, want openai", got.ProviderType)
	}
	if got.DisplayName != "GPT 4.1 Mini" {
		t.Fatalf("DisplayName = %q, want %q", got.DisplayName, "GPT 4.1 Mini")
	}
	if !got.Capabilities.Streaming || !got.Capabilities.Tools {
		t.Fatalf("Capabilities = %#v, want streaming+tools", got.Capabilities)
	}
	if got.SnapshotState != modelcatalog.SnapshotStatusOK {
		t.Fatalf("SnapshotState = %q, want %q", got.SnapshotState, modelcatalog.SnapshotStatusOK)
	}
}

func TestCreateManagedModelUsesStoreCreate(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	store := &testModelStore{}
	handler := NewHandler(newTestAgentGateway(&testConfigStore{modelStore: store}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	body := bytes.NewBufferString(`{"provider_id":"openai-main","upstream_model":"gpt-4.1","enabled":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/models/managed", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected create status: got %d want %d body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if _, ok := store.items["openai-main\x00gpt-4.1"]; !ok {
		t.Fatalf("managed model was not created")
	}
}

func TestUpdateManagedModelUsesStoreUpdate(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	store := &testModelStore{
		items: map[string]*modelcatalog.ManagedModel{
			"openai-main\x00gpt-4.1": {
				ProviderID:    "openai-main",
				UpstreamModel: "gpt-4.1",
				Enabled:       false,
			},
		},
	}
	handler := NewHandler(newTestAgentGateway(&testConfigStore{modelStore: store}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	body := bytes.NewBufferString(`{"enabled":true}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/models/managed/openai-main/gpt-4.1", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected update status: got %d want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !store.items["openai-main\x00gpt-4.1"].Enabled {
		t.Fatalf("managed model was not updated")
	}
}

func TestUpdateManagedModelReturnsNotFoundWhenMissing(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{modelStore: &testModelStore{}}, nil, nil, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	body := bytes.NewBufferString(`{"enabled":true}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/models/managed/openai-main/gpt-4.1", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected update status: got %d want %d body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
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
