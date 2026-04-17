package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthProtectedRouteRequiresFreshBearerToken(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(&testConfigStore{}, nil, nil, nil), nil, "admin", string(passwordHash))

	listReq := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected unauthenticated status: got %d want %d", listRec.Code, http.StatusUnauthorized)
	}

	loginBody, err := json.Marshal(map[string]string{
		"username": "admin",
		"password": "secret-pass",
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

	authedReq := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
	authedReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	authedRec := httptest.NewRecorder()
	handler.ServeHTTP(authedRec, authedReq)
	if authedRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected authenticated status: got %d want %d", authedRec.Code, http.StatusServiceUnavailable)
	}

	staleReq := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
	staleReq.Header.Set("Authorization", "Bearer stale-token")
	staleRec := httptest.NewRecorder()
	handler.ServeHTTP(staleRec, staleReq)
	if staleRec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected stale-token status: got %d want %d", staleRec.Code, http.StatusUnauthorized)
	}
}

func TestAuthProtectedRouteRejectsRequestsWhenAdminCredentialsAreNotConfigured(t *testing.T) {
	handler := NewHandler(newTestAgentGateway(&testConfigStore{}, nil, nil, nil), nil, "", "")

	req := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected unauthenticated status: got %d want %d", rec.Code, http.StatusUnauthorized)
	}
}
