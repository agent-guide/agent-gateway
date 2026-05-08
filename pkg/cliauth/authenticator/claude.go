// Package authenticator provides concrete Authenticator implementations for CLI login flows.
package authenticator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/pkg/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/pkg/httpclient"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr"
	"github.com/google/uuid"
)

// OAuth constants for Anthropic Claude CLI authentication.
const (
	claudeAuthURL  = "https://claude.ai/oauth/authorize"
	claudeTokenURL = "https://api.anthropic.com/v1/oauth/token"
	claudeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeScopes   = "org:create_api_key user:profile user:inference"

	claudeCallbackTimeout     = 5 * time.Minute
	claudeDefaultCallbackPort = 54545
	claudeRefreshMaxRetries   = 3
	claudeManualPromptDelay   = 15 * time.Second
)

func init() {
	cliauth.RegisterAuthenticatorFactory("claude", NewClaudeAuthenticator)
}

// claudeTokenResponse represents the token endpoint response from Anthropic.
type claudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Organization struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"organization"`
	Account struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

// ---- ClaudeAuthenticator ----

// ClaudeAuthenticator implements manager.Authenticator for the Anthropic Claude CLI login flow.
// It uses browser-based OAuth PKCE authentication against the Anthropic OAuth endpoints.
type ClaudeAuthenticator struct {
	cliauth.AuthenticatorConfig
	// HTTPClient is the HTTP client used for token requests. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
	client     *http.Client
}

// NewClaudeAuthenticator creates a ClaudeAuthenticator with default settings.
func NewClaudeAuthenticator() (cliauth.Authenticator, error) {
	return &ClaudeAuthenticator{
		AuthenticatorConfig: cliauth.AuthenticatorConfig{
			CallbackPort: claudeDefaultCallbackPort,
		},
	}, nil
}

// ProviderType returns the provider type this authenticator handles.
func (a *ClaudeAuthenticator) ProviderType() string {
	return "anthropic"
}

// RefreshLeadTime returns how far in advance of token expiry to refresh Claude credentials.
func (a *ClaudeAuthenticator) RefreshLeadTime() *time.Duration {
	lead := 4 * time.Hour
	return &lead
}

// GetConfig returns the current runtime configuration for the authenticator.
func (a *ClaudeAuthenticator) GetConfig() cliauth.AuthenticatorConfig {
	if a == nil {
		return cliauth.AuthenticatorConfig{}
	}
	return a.AuthenticatorConfig
}

// SetConfig applies runtime configuration to the authenticator.
func (a *ClaudeAuthenticator) SetConfig(cfg cliauth.AuthenticatorConfig) error {
	if a == nil {
		return fmt.Errorf("claude: authenticator is nil")
	}
	if cfg.DeviceFlow {
		return fmt.Errorf("device flow is not supported by claude authenticator")
	}
	cfg.Defaults()
	a.AuthenticatorConfig = cfg
	a.client = nil
	return nil
}

// Login initiates the Claude CLI login flow and returns a new credential on success.
func (a *ClaudeAuthenticator) Login(ctx context.Context, reporter cliauth.LoginStatusReporter) (*credentialmgr.Credential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return a.loginWithBrowser(ctx, reporter)
}

// Refresh refreshes the credential's access token before it expires.
// Returns nil if no refresh token is present.
func (a *ClaudeAuthenticator) Refresh(ctx context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("claude: credential is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	refreshToken, _ := cred.Metadata["refresh_token"].(string)
	if strings.TrimSpace(refreshToken) == "" {
		return nil, nil
	}

	tokenResp, err := a.refreshTokensWithRetry(ctx, refreshToken, claudeRefreshMaxRetries)
	if err != nil {
		return nil, fmt.Errorf("claude: token refresh failed: %w", err)
	}

	updated := cred.Clone()
	a.applyTokenToMetadata(updated, tokenResp)
	return updated, nil
}

// ---- Browser-based OAuth PKCE flow ----

func (a *ClaudeAuthenticator) loginWithBrowser(ctx context.Context, reporter cliauth.LoginStatusReporter) (*credentialmgr.Credential, error) {
	codeVerifier, codeChallenge, err := generatePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("claude: PKCE generation failed: %w", err)
	}

	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("claude: state generation failed: %w", err)
	}

	port := a.CallbackPort
	if port <= 0 {
		port = claudeDefaultCallbackPort
	}
	redirectURI := buildClaudeRedirectURI(port)

	srv := newClaudeCallbackServer(port)
	if err = srv.start(); err != nil {
		if strings.Contains(err.Error(), "already in use") {
			return nil, newAuthError(ErrPortInUse, err)
		}
		return nil, newAuthError(ErrServerStartFailed, err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.stop(stopCtx)
	}()

	authURL := buildClaudeAuthURL(state, codeChallenge, redirectURI)
	outcome, err := runOAuthBrowserFlow(oauthBrowserFlowOptions{
		ProviderName:              "Claude",
		AuthURL:                   authURL,
		NoBrowser:                 a.NoBrowser,
		Reporter:                  reporter,
		AwaitingBrowserMessage:    "Open the verification URL in a browser and complete the Claude login flow.",
		WaitingForCallbackMessage: "Waiting for the Claude OAuth callback after browser verification.",
		ManualCallbackMessage:     "If the browser cannot reach localhost, paste the full Claude callback URL to continue.",
		ManualCallbackPrompt:      "Paste the Claude callback URL (or press Enter to keep waiting): ",
		CallbackTimeout:           claudeCallbackTimeout,
		ManualPromptDelay:         claudeManualPromptDelay,
		WaitForCallback:           srv.waitForCallback,
		ParseCallbackURL:          parseClaudeCallbackURL,
	})
	if err != nil {
		return nil, err
	}

	if outcome.State != state {
		return nil, newAuthError(ErrInvalidState, fmt.Errorf("state mismatch"))
	}

	tokenResp, err := a.exchangeCode(ctx, outcome.Code, state, redirectURI, codeVerifier)
	if err != nil {
		return nil, newAuthError(ErrCodeExchangeFailed, err)
	}

	return a.buildCredential(tokenResp)
}

// ---- Token exchange & refresh ----

func (a *ClaudeAuthenticator) exchangeCode(ctx context.Context, code, state, redirectURI, codeVerifier string) (*claudeTokenResponse, error) {
	// The code parameter may contain an embedded state fragment (e.g. "code#state").
	parsedCode, parsedState := parseClaudeCodeParam(code)
	if parsedState != "" {
		state = parsedState
	}

	reqBody := map[string]any{
		"code":          parsedCode,
		"state":         state,
		"grant_type":    "authorization_code",
		"client_id":     claudeClientID,
		"redirect_uri":  redirectURI,
		"code_verifier": codeVerifier,
	}

	return a.postTokenRequest(ctx, reqBody)
}

func (a *ClaudeAuthenticator) refreshTokens(ctx context.Context, refreshToken string) (*claudeTokenResponse, error) {
	reqBody := map[string]any{
		"grant_type":    "refresh_token",
		"client_id":     claudeClientID,
		"refresh_token": refreshToken,
	}

	return a.postTokenRequest(ctx, reqBody)
}

func (a *ClaudeAuthenticator) postTokenRequest(ctx context.Context, body map[string]any) (*claudeTokenResponse, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to encode token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeTokenURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var tokenResp claudeTokenResponse
	if err = json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	return &tokenResp, nil
}

func (a *ClaudeAuthenticator) refreshTokensWithRetry(ctx context.Context, refreshToken string, maxRetries int) (*claudeTokenResponse, error) {
	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		resp, err := a.refreshTokens(ctx, refreshToken)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("token refresh failed after %d attempts: %w", maxRetries, lastErr)
}

// ---- Credential builder ----

func (a *ClaudeAuthenticator) buildCredential(tokenResp *claudeTokenResponse) (*credentialmgr.Credential, error) {
	cred := &credentialmgr.Credential{
		ID:           uuid.New().String(),
		ProviderType: a.ProviderType(),
		Metadata:     make(map[string]any),
		Attributes:   make(map[string]string),
	}

	a.applyTokenToMetadata(cred, tokenResp)

	email := strings.TrimSpace(tokenResp.Account.EmailAddress)
	if email != "" {
		cred.Label = email
		cred.Attributes["email"] = email
		cred.Metadata["email"] = email
	}
	if orgName := strings.TrimSpace(tokenResp.Organization.Name); orgName != "" {
		cred.Attributes["org_name"] = orgName
	}
	if orgUUID := strings.TrimSpace(tokenResp.Organization.UUID); orgUUID != "" {
		cred.Attributes["org_id"] = orgUUID
	}

	fmt.Println("Claude authentication successful.")
	return cred, nil
}

// applyTokenToMetadata writes token fields into cred.Metadata.
func (a *ClaudeAuthenticator) applyTokenToMetadata(cred *credentialmgr.Credential, tokenResp *claudeTokenResponse) {
	if cred.Metadata == nil {
		cred.Metadata = make(map[string]any)
	}
	cred.Metadata["access_token"] = tokenResp.AccessToken
	cred.Metadata["refresh_token"] = tokenResp.RefreshToken

	if tokenResp.ExpiresIn > 0 {
		expiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
		cred.Metadata["expired"] = expiry
	}
	cred.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
}

func (a *ClaudeAuthenticator) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	if a.client != nil {
		return a.client
	}
	a.client = httpclient.BuildHTTPClient(a.Network)
	return a.client
}

// ---- Authorization URL ----

func buildClaudeRedirectURI(port int) string {
	if port <= 0 {
		port = claudeDefaultCallbackPort
	}
	return fmt.Sprintf("http://localhost:%d/callback", port)
}

func buildClaudeAuthURL(state, codeChallenge, redirectURI string) string {
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {claudeClientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {claudeScopes},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return claudeAuthURL + "?" + params.Encode()
}

// parseClaudeCodeParam splits a code parameter that may contain an embedded state fragment
// in the form "code#state".
func parseClaudeCodeParam(code string) (parsedCode, parsedState string) {
	parts := strings.SplitN(code, "#", 2)
	parsedCode = parts[0]
	if len(parts) > 1 {
		parsedState = parts[1]
	}
	return
}

func parseClaudeCallbackURL(rawURL string) (code, state string, err error) {
	return parseOAuthCallbackURL("claude", rawURL, true)
}

// ---- OAuth callback server for Claude ----

type claudeCallbackServer struct {
	*callbackHTTPServer
}

func newClaudeCallbackServer(port int) *claudeCallbackServer {
	return &claudeCallbackServer{
		callbackHTTPServer: newCallbackHTTPServer(port),
	}
}

func (s *claudeCallbackServer) start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)
	mux.HandleFunc("/success", s.handleSuccess)
	return s.callbackHTTPServer.start(mux)
}

func (s *claudeCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	errParam := q.Get("error")
	code := q.Get("code")
	state := q.Get("state")

	var result oauthCallbackResult
	switch {
	case errParam != "":
		result.err = errParam
	case code == "":
		result.err = "no_code"
	case state == "":
		result.err = "no_state"
	default:
		result.code = code
		result.state = state
	}

	select {
	case s.resultCh <- result:
	default:
	}

	if result.err != "" {
		http.Error(w, "Authentication failed: "+result.err, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/success", http.StatusFound)
}

func (s *claudeCallbackServer) handleSuccess(w http.ResponseWriter, r *http.Request) {
	writeOAuthSuccessHTML(w, claudeSuccessHTML)
}

// ---- Success page ----

var claudeSuccessHTML = buildOAuthSuccessHTML("✅", "#d97706", "#b45309")
