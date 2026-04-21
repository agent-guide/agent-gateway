package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"golang.org/x/crypto/bcrypt"
)

type testAuthenticator struct {
	providerType string
	loginFn      func(context.Context) (*cliauth.Credential, error)
}

func (a *testAuthenticator) Provider() string {
	return a.providerType
}

func (a *testAuthenticator) Login(ctx context.Context) (*cliauth.Credential, error) {
	if a.loginFn != nil {
		return a.loginFn(ctx)
	}
	return &cliauth.Credential{Credential: credentialmgr.Credential{ProviderType: a.providerType}}, nil
}

func (a *testAuthenticator) RefreshLead(context.Context, *cliauth.Credential) (*cliauth.Credential, error) {
	return nil, nil
}

func TestCLIAuthResolvesAuthenticatorAndRegistersCredential(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	credMgr := credentialmgr.NewManager(nil, nil, nil)
	cliauthMgr := cliauth.NewManager(credMgr, nil)
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{
		providerType: "openai",
		loginFn: func(context.Context) (*cliauth.Credential, error) {
			return &cliauth.Credential{
				Credential: credentialmgr.Credential{
					ID:           "cred-openai-1",
					ProviderType: "openai",
					Label:        "test@example.com",
				},
			}, nil
		},
	})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")
	req := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/login", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusAccepted)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cred := cliauthMgr.CredentialManager().GetCredential("cred-openai-1"); cred != nil {
			if cred.ProviderType != "openai" {
				t.Fatalf("unexpected provider type: got %q want %q", cred.ProviderType, "openai")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("credential was not registered")
}

func TestCLIAuthReturnsNotFoundForUnknownCliname(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	handler := NewHandler(newTestAgentGateway(nil, cliauth.NewManager(nil, nil), nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")
	req := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/unknown/login", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCLIAuthStatusReportsCompletion(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	cliauthMgr := cliauth.NewManager(nil, nil)
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{
		providerType: "openai",
		loginFn: func(context.Context) (*cliauth.Credential, error) {
			time.Sleep(20 * time.Millisecond)
			return &cliauth.Credential{
				Credential: credentialmgr.Credential{
					ID:           "cred-openai-2",
					ProviderType: "openai",
				},
			}, nil
		},
	})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	startReq := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/login", nil)
	startReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("unexpected start status: got %d want %d", startRec.Code, http.StatusAccepted)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/admin/cliauth/authenticators/codex/login/status", nil)
		statusReq.Header.Set("Authorization", "Bearer "+token)
		statusRec := httptest.NewRecorder()
		handler.ServeHTTP(statusRec, statusReq)
		if statusRec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d", statusRec.Code, http.StatusOK)
		}

		var status cliAuthStatus
		if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
			t.Fatalf("decode status response: %v", err)
		}
		if status.Status == "succeeded" {
			if status.CredentialID != "cred-openai-2" {
				t.Fatalf("unexpected credential id: got %q want %q", status.CredentialID, "cred-openai-2")
			}
			if status.FinishedAt == nil {
				t.Fatal("expected finished_at to be set")
			}
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("cli auth status did not reach succeeded")
}

func TestCLIAuthEnableAndListAuthenticators(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	const authName = "test-admin-authenticator"
	cliauth.RegisterAuthenticatorFactory(authName, func() (cliauth.Authenticator, error) {
		return &testAuthenticator{providerType: "openai"}, nil
	})

	cliauthMgr := cliauth.NewManager(nil, nil)
	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	enableReq := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/"+authName+"/enable", nil)
	enableReq.Header.Set("Authorization", "Bearer "+token)
	enableRec := httptest.NewRecorder()
	handler.ServeHTTP(enableRec, enableReq)

	if enableRec.Code != http.StatusCreated {
		t.Fatalf("unexpected enable status: got %d want %d", enableRec.Code, http.StatusCreated)
	}
	if _, ok := cliauthMgr.GetAuthenticator(authName); !ok {
		t.Fatal("authenticator was not registered")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/admin/cliauth/authenticators", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: got %d want %d", listRec.Code, http.StatusOK)
	}

	var body struct {
		Items []cliauth.AuthenticatorState `json:"items"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&body); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, item := range body.Items {
		if item.Name == authName {
			if !item.Enabled {
				t.Fatal("expected enabled authenticator to be marked enabled")
			}
			return
		}
	}
	t.Fatalf("authenticator %q not found in list: %#v", authName, body.Items)
}

func TestCLIAuthDisableRuntimeAuthenticator(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	cliauthMgr := cliauth.NewManager(nil, nil)
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{providerType: "openai"})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/disable", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	if _, ok := cliauthMgr.GetAuthenticator("codex"); ok {
		t.Fatal("authenticator was not disabled")
	}
}

func TestCLIAuthDisableCaddyfileAuthenticatorReturnsConflict(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	cliauthMgr := cliauth.NewManager(nil, nil)
	cliauthMgr.RegisterAuthenticatorWithOptions("codex", &testAuthenticator{providerType: "openai"}, cliauth.RegisterAuthenticatorOptions{
		Source:   cliauth.AuthenticatorSourceCaddyfile,
		ReadOnly: true,
	})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	req := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/disable", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusConflict)
	}
	if _, ok := cliauthMgr.GetAuthenticator("codex"); !ok {
		t.Fatal("read-only authenticator was disabled")
	}
}
