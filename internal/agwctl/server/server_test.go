package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/gatewayadmin"
	"golang.org/x/crypto/bcrypt"
)

func TestGatewayProxyRetriesWithOriginalRequestBody(t *testing.T) {
	var loginCount int
	var forwardedBodies []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			loginCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"token": fmt.Sprintf("token-%d", loginCount)})
		case "/admin/routes":
			body, _ := io.ReadAll(r.Body)
			forwardedBodies = append(forwardedBodies, string(body))
			if r.Header.Get("Authorization") == "Bearer token-1" {
				http.Error(w, "stale token", http.StatusUnauthorized)
				return
			}
			if got, want := r.Header.Get("Authorization"), "Bearer token-2"; got != want {
				t.Errorf("gateway Authorization = %q, want %q", got, want)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			t.Errorf("unexpected gateway path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()

	s := newAuthedTestServer("", gatewayadmin.NewProxy(gateway.URL, "gateway-admin", "gateway-pass"))
	token := loginForTest(t, s)

	req := httptest.NewRequest(http.MethodPost, "/admin/routes", bytes.NewBufferString(`{"id":"route-1"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want %d, body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if loginCount != 2 {
		t.Fatalf("gateway loginCount = %d, want 2", loginCount)
	}
	if len(forwardedBodies) != 2 {
		t.Fatalf("forwardedBodies len = %d, want 2", len(forwardedBodies))
	}
	for i, body := range forwardedBodies {
		if body != `{"id":"route-1"}` {
			t.Fatalf("forwarded body %d = %q, want original body", i, body)
		}
	}
}

func TestGatewayProxyReturnsErrorAfterReauthUnauthorized(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "token"})
		case "/admin/providers":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			t.Errorf("unexpected gateway path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()

	s := newAuthedTestServer("", gatewayadmin.NewProxy(gateway.URL, "gateway-admin", "gateway-pass"))
	token := loginForTest(t, s)

	req := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("proxy status = %d, want %d, body %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestListRoutesHandlerReturnsNotFoundForMissingServer(t *testing.T) {
	caddyAdmin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer caddyAdmin.Close()

	s := newAuthedTestServer(caddyAdmin.URL, nil)
	token := loginForTest(t, s)

	req := httptest.NewRequest(http.MethodGet, "/admin/caddy/servers/missing/routes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func newAuthedTestServer(caddyAdminAddr string, gw *gatewayadmin.Proxy) *Server {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return New(caddyAdminAddr, "admin", string(hash), nil, gw)
}

func loginForTest(t *testing.T, s *Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret-pass"}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if body.Token == "" {
		t.Fatal("login token is empty")
	}
	return body.Token
}
