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
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type testConfigStore struct {
	routeStore       configstoreintf.RouteStorer
	localAPIKeyStore configstoreintf.LocalAPIKeyStorer
}

func (s *testConfigStore) GetCredentialStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.CredentialStorer, error) {
	return nil, nil
}

func (s *testConfigStore) GetProviderConfigStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ProviderConfigStorer, error) {
	return nil, nil
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
	items map[string]*routepkg.LocalAPIKey
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
	item, ok := obj.(*routepkg.LocalAPIKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*routepkg.LocalAPIKey{}
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

	handler := NewHandler(nil, &testConfigStore{
		routeStore: &testRouteStore{items: map[string]*routepkg.Route{}},
	}, nil, "admin", string(passwordHash), nil)
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

	var got routepkg.Route
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode route: %v", err)
	}
	if got.ID != "chat-prod" {
		t.Fatalf("unexpected route id: got %q want %q", got.ID, "chat-prod")
	}
	if len(got.Targets) != 1 || got.Targets[0].ProviderRef != "openai" {
		t.Fatalf("unexpected targets: %#v", got.Targets)
	}
}

func TestLocalAPIKeyCRUD(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(nil, &testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*routepkg.LocalAPIKey{}},
	}, nil, "admin", string(passwordHash), nil)
	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(routepkg.LocalAPIKey{
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

	var got routepkg.LocalAPIKey
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode local api key: %v", err)
	}
	if got.Key != "lk-test" {
		t.Fatalf("unexpected local api key: got %q want %q", got.Key, "lk-test")
	}
	if len(got.AllowedRouteIDs) != 1 || got.AllowedRouteIDs[0] != "chat-prod" {
		t.Fatalf("unexpected allowed routes: %#v", got.AllowedRouteIDs)
	}
}

func TestProtectedRouteRejectsRequestsWhenAdminAuthIsNotConfigured(t *testing.T) {
	handler := NewHandler(nil, &testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*routepkg.LocalAPIKey{}},
	}, nil, "", "", nil)

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

	handler := NewHandler(nil, &testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*routepkg.LocalAPIKey{}},
	}, nil, "admin", string(passwordHash), nil)

	token := loginForTest(t, handler, "admin", "secret-pass")

	body, err := json.Marshal(routepkg.LocalAPIKey{
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

	handler := NewHandler(nil, &testConfigStore{
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*routepkg.LocalAPIKey{
			"lk-test": {Key: "lk-test", UserID: "admin"},
		}},
	}, nil, "admin", string(passwordHash), nil)

	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodGet, "/admin/local_api_keys?user_id=someone-else", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected list status: got %d want %d", rec.Code, http.StatusForbidden)
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
