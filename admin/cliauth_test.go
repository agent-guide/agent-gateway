package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"golang.org/x/crypto/bcrypt"
)

type testAuthenticator struct {
	providerType string
	loginFn      func(context.Context, cliauth.LoginStatusReporter) (*cliauth.Credential, error)
}

type cliAuthLoginStartResponse struct {
	LoginID           string `json:"login_id"`
	Status            string `json:"status"`
	AuthenticatorName string `json:"authenticator_name"`
	Message           string `json:"message"`
}

func (a *testAuthenticator) ProviderType() string {
	return a.providerType
}

func (a *testAuthenticator) Login(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
	if a.loginFn != nil {
		return a.loginFn(ctx, reporter)
	}
	return &cliauth.Credential{Credential: credentialmgr.Credential{ProviderType: a.providerType}}, nil
}

func (a *testAuthenticator) Refresh(context.Context, *cliauth.Credential) (*cliauth.Credential, error) {
	return nil, nil
}

func (a *testAuthenticator) RefreshLeadTime() time.Duration { return 0 }

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestCLIAuthResolvesAuthenticatorAndRegistersCredential(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	credMgr := credentialmgr.NewManager(nil, nil, nil)
	cliauthMgr := cliauth.NewManager()
	cliauthRefresher := cliauth.NewAutoRefresher(cliauth.WrapSharedCredentialManager(credMgr), cliauthMgr)
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{
		providerType: "openai",
		loginFn: func(context.Context, cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
			return &cliauth.Credential{
				Credential: credentialmgr.Credential{
					ID:           "cred-openai-1",
					ProviderType: "openai",
					ProviderID:   "openai-main",
					Label:        "test@example.com",
				},
			}, nil
		},
	})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, cliauthRefresher, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")
	req := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/login", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusAccepted)
	}

	var startResp cliAuthLoginStartResponse
	if err := json.NewDecoder(rec.Body).Decode(&startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResp.LoginID == "" {
		t.Fatal("expected login_id in start response")
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cred := credMgr.GetCredential("cred-openai-1"); cred != nil {
			if cred.ProviderType != "openai" {
				t.Fatalf("unexpected provider type: got %q want %q", cred.ProviderType, "openai")
			}
			if cred.ProviderID != "openai-main" {
				t.Fatalf("unexpected provider id: got %q want %q", cred.ProviderID, "openai-main")
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

	cliauthMgr := cliauth.NewManager()
	cliauthRefresher := cliauth.NewAutoRefresher(nil, cliauthMgr)
	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, cliauthRefresher, nil, nil), nil, "admin", string(passwordHash))
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

	cliauthMgr := cliauth.NewManager()
	cliauthRefresher := cliauth.NewAutoRefresher(nil, cliauthMgr)
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{
		providerType: "openai",
		loginFn: func(context.Context, cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
			time.Sleep(20 * time.Millisecond)
			return &cliauth.Credential{
				Credential: credentialmgr.Credential{
					ID:           "cred-openai-2",
					ProviderType: "openai",
					ProviderID:   "openai-main",
				},
			}, nil
		},
	})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, cliauthRefresher, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	startReq := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/login", nil)
	startReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("unexpected start status: got %d want %d", startRec.Code, http.StatusAccepted)
	}

	var startResp cliAuthLoginStartResponse
	if err := json.NewDecoder(startRec.Body).Decode(&startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResp.LoginID == "" {
		t.Fatal("expected login_id in start response")
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/admin/cliauth/logins/"+startResp.LoginID, nil)
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
			if status.LoginID != startResp.LoginID {
				t.Fatalf("unexpected login id: got %q want %q", status.LoginID, startResp.LoginID)
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

func TestCLIAuthStatusIncludesInteractiveInstructions(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	release := make(chan struct{})
	cliauthMgr := cliauth.NewManager()
	cliauthRefresher := cliauth.NewAutoRefresher(nil, cliauthMgr)
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{
		providerType: "openai",
		loginFn: func(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
			reporter.UpdateLoginStatus(cliauth.LoginStatusUpdate{
				Phase:           "awaiting_browser_auth",
				Message:         "Open the verification URL in a browser.",
				VerificationURL: "https://example.com/login",
			})
			<-release
			return &cliauth.Credential{
				Credential: credentialmgr.Credential{
					ID:           "cred-openai-3",
					ProviderType: "openai",
					ProviderID:   "openai-main",
				},
			}, nil
		},
	})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, cliauthRefresher, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	startReq := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/login", nil)
	startReq.Header.Set("Authorization", "Bearer "+token)
	startRec := httptest.NewRecorder()
	handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("unexpected start status: got %d want %d", startRec.Code, http.StatusAccepted)
	}

	var startResp cliAuthLoginStartResponse
	if err := json.NewDecoder(startRec.Body).Decode(&startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResp.LoginID == "" {
		t.Fatal("expected login_id in start response")
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/admin/cliauth/logins/"+startResp.LoginID, nil)
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
		if status.VerificationURL == "https://example.com/login" {
			if status.Phase != "awaiting_browser_auth" {
				t.Fatalf("unexpected phase: got %q want %q", status.Phase, "awaiting_browser_auth")
			}
			if status.LoginID != startResp.LoginID {
				t.Fatalf("unexpected login id: got %q want %q", status.LoginID, startResp.LoginID)
			}
			close(release)
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	close(release)
	t.Fatal("cli auth status did not expose verification url")
}

func TestCLIAuthRejectsConcurrentLoginForSameAuthenticator(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	release := make(chan struct{})
	cliauthMgr := cliauth.NewManager()
	cliauthRefresher := cliauth.NewAutoRefresher(nil, cliauthMgr)
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{
		providerType: "openai",
		loginFn: func(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
			reporter.UpdateLoginStatus(cliauth.LoginStatusUpdate{
				Phase:           "awaiting_browser_auth",
				Message:         "Open the verification URL in a browser.",
				VerificationURL: "https://example.com/login",
			})
			<-release
			return &cliauth.Credential{
				Credential: credentialmgr.Credential{
					ID:           "cred-openai-4",
					ProviderType: "openai",
				},
			}, nil
		},
	})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, cliauthRefresher, nil, nil), nil, "admin", string(passwordHash))
	token := loginForTest(t, handler, "admin", "secret-pass")

	firstReq := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/login", nil)
	firstReq.Header.Set("Authorization", "Bearer "+token)
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("unexpected first start status: got %d want %d", firstRec.Code, http.StatusAccepted)
	}

	var firstResp cliAuthLoginStartResponse
	if err := json.NewDecoder(firstRec.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first start response: %v", err)
	}
	if firstResp.LoginID == "" {
		t.Fatal("expected login_id in first start response")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/admin/cliauth/authenticators/codex/login", nil)
	secondReq.Header.Set("Authorization", "Bearer "+token)
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	close(release)

	if secondRec.Code != http.StatusConflict {
		t.Fatalf("unexpected second start status: got %d want %d", secondRec.Code, http.StatusConflict)
	}
	if body := secondRec.Body.String(); body == "" || !containsAll(body, "login already running", firstResp.LoginID) {
		t.Fatalf("unexpected conflict body: %q", body)
	}
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

	cliauthMgr := cliauth.NewManager()
	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, nil, nil, nil), nil, "admin", string(passwordHash))
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

	cliauthMgr := cliauth.NewManager()
	cliauthMgr.RegisterAuthenticator("codex", &testAuthenticator{providerType: "openai"})

	handler := NewHandler(newTestAgentGateway(nil, cliauthMgr, nil, nil, nil), nil, "admin", string(passwordHash))
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
