// Package authenticator provides concrete Authenticator implementations for CLI login flows.
package authenticator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
	internalhttpclient "github.com/agent-guide/caddy-agent-gateway/internal/httpclient"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// OAuth constants for Google Gemini CLI authentication.
// These are the public OAuth credentials used by the Gemini CLI application.
// See: https://github.com/google-gemini/gemini-cli
const (
	geminiClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	geminiClientSecret = "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" //nolint:gosec // public OAuth client credentials from Gemini CLI

	geminiCallbackTimeout     = 5 * time.Minute
	geminiDefaultCallbackPort = 8085
	geminiRefreshMaxRetries   = 3
	geminiManualPromptDelay   = 15 * time.Second
)

func init() {
	cliauth.RegisterAuthenticatorFactory("gemini", NewGeminiAuthenticator)
}

// geminiScopes are the OAuth2 scopes requested for Gemini CLI authentication.
var geminiScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// geminiUserInfoURL is the Google API endpoint to retrieve user profile information.
const geminiUserInfoURL = "https://www.googleapis.com/oauth2/v1/userinfo?alt=json"

// ---- GeminiAuthenticator ----

// GeminiAuthenticator implements manager.Authenticator for the Google Gemini CLI login flow.
// It uses browser-based OAuth2 authentication against Google's OAuth endpoints.
type GeminiAuthenticator struct {
	cliauth.AuthenticatorConfig
	// HTTPClient is the HTTP client used for token requests. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
	client     *http.Client
}

// NewGeminiAuthenticator creates a GeminiAuthenticator with default settings.
func NewGeminiAuthenticator() (cliauth.Authenticator, error) {
	return &GeminiAuthenticator{
		AuthenticatorConfig: cliauth.AuthenticatorConfig{
			CallbackPort: geminiDefaultCallbackPort,
		},
	}, nil
}

// ProviderType returns the provider type this authenticator handles.
func (a *GeminiAuthenticator) ProviderType() string {
	return "gemini"
}

// RefreshLeadTime returns nil to disable provider-level background pre-refresh for Gemini.
func (a *GeminiAuthenticator) RefreshLeadTime() *time.Duration {
	return nil
}

// GetConfig returns the current runtime configuration for the authenticator.
func (a *GeminiAuthenticator) GetConfig() cliauth.AuthenticatorConfig {
	if a == nil {
		return cliauth.AuthenticatorConfig{}
	}
	return a.AuthenticatorConfig
}

// SetConfig applies runtime configuration to the authenticator.
func (a *GeminiAuthenticator) SetConfig(cfg cliauth.AuthenticatorConfig) error {
	if a == nil {
		return fmt.Errorf("gemini: authenticator is nil")
	}
	if cfg.DeviceFlow {
		return fmt.Errorf("device flow is not supported by gemini authenticator")
	}
	cfg.Defaults()
	a.AuthenticatorConfig = cfg
	a.client = nil
	return nil
}

// Login initiates the Gemini CLI login flow and returns a new credential on success.
func (a *GeminiAuthenticator) Login(ctx context.Context, reporter cliauth.LoginStatusReporter) (*credentialmgr.Credential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return a.loginWithBrowser(ctx, reporter)
}

// Refresh refreshes the credential's access token before it expires.
// Returns nil if no refresh token is present.
func (a *GeminiAuthenticator) Refresh(ctx context.Context, cred *credentialmgr.Credential) (*credentialmgr.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("gemini: credential is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	refreshToken, _ := cred.Metadata["refresh_token"].(string)
	if strings.TrimSpace(refreshToken) == "" {
		return nil, nil
	}

	token, err := a.refreshTokensWithRetry(ctx, refreshToken, geminiRefreshMaxRetries)
	if err != nil {
		return nil, fmt.Errorf("gemini: token refresh failed: %w", err)
	}

	updated := cred.Clone()
	applyGeminiTokenToMetadata(updated, token)
	return updated, nil
}

// ---- Browser-based OAuth2 flow ----

func (a *GeminiAuthenticator) loginWithBrowser(ctx context.Context, reporter cliauth.LoginStatusReporter) (*credentialmgr.Credential, error) {
	port := a.CallbackPort
	if port <= 0 {
		port = geminiDefaultCallbackPort
	}
	callbackURL := fmt.Sprintf("http://localhost:%d/oauth2callback", port)
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("gemini: state generation failed: %w", err)
	}

	conf := &oauth2.Config{
		ClientID:     geminiClientID,
		ClientSecret: geminiClientSecret,
		RedirectURL:  callbackURL,
		Scopes:       geminiScopes,
		Endpoint:     google.Endpoint,
	}

	srv := newGeminiCallbackServer(port)
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

	authURL := conf.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	outcome, err := runOAuthBrowserFlow(oauthBrowserFlowOptions{
		ProviderName:              "Gemini",
		AuthURL:                   authURL,
		NoBrowser:                 a.NoBrowser,
		Reporter:                  reporter,
		AwaitingBrowserMessage:    "Open the verification URL in a browser and complete the Gemini login flow.",
		WaitingForCallbackMessage: "Waiting for the Gemini OAuth callback after browser verification.",
		ManualCallbackMessage:     "If the browser cannot reach localhost, paste the full Gemini callback URL to continue.",
		ManualCallbackPrompt:      "Paste the Gemini callback URL (or press Enter to keep waiting): ",
		CallbackTimeout:           geminiCallbackTimeout,
		ManualPromptDelay:         geminiManualPromptDelay,
		WaitForCallback:           srv.waitForCallback,
		ParseCallbackURL:          parseGeminiCallbackURL,
	})
	if err != nil {
		return nil, err
	}

	if outcome.State != state {
		return nil, newAuthError(ErrInvalidState, fmt.Errorf("state mismatch"))
	}

	token, err := conf.Exchange(ctx, outcome.Code)
	if err != nil {
		return nil, newAuthError(ErrCodeExchangeFailed, err)
	}

	return a.buildCredential(ctx, conf, token)
}

// ---- Token refresh ----

func (a *GeminiAuthenticator) refreshTokens(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	conf := &oauth2.Config{
		ClientID:     geminiClientID,
		ClientSecret: geminiClientSecret,
		Endpoint:     google.Endpoint,
	}

	// Build an expired token with only the refresh token set so the TokenSource triggers a refresh.
	expired := &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(-time.Hour),
	}

	httpCtx := ctx
	if client := a.httpClient(); client != nil {
		httpCtx = context.WithValue(ctx, oauth2.HTTPClient, client)
	}

	ts := conf.TokenSource(httpCtx, expired)
	token, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	return token, nil
}

func (a *GeminiAuthenticator) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	if a.client != nil {
		return a.client
	}
	a.client = internalhttpclient.BuildHTTPClient(a.Network)
	return a.client
}

func (a *GeminiAuthenticator) refreshTokensWithRetry(ctx context.Context, refreshToken string, maxRetries int) (*oauth2.Token, error) {
	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		token, err := a.refreshTokens(ctx, refreshToken)
		if err == nil {
			return token, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("token refresh failed after %d attempts: %w", maxRetries, lastErr)
}

// ---- Credential builder ----

func (a *GeminiAuthenticator) buildCredential(ctx context.Context, conf *oauth2.Config, token *oauth2.Token) (*credentialmgr.Credential, error) {
	cred := &credentialmgr.Credential{
		ID:           uuid.New().String(),
		ProviderType: a.ProviderType(),
		Metadata:     make(map[string]any),
		Attributes:   make(map[string]string),
	}
	cred.Metadata[credentialmgr.MetadataManualRefreshNameKey] = "gemini"
	cred.Metadata[credentialmgr.MetadataManualRefreshExpiryDelta] = 10 * time.Second

	applyGeminiTokenToMetadata(cred, token)

	// Fetch user email from Google userinfo API.
	httpClient := conf.Client(ctx, token)
	email, err := fetchGeminiUserEmail(ctx, httpClient)
	if err == nil && email != "" {
		cred.Label = email
		cred.Attributes["email"] = email
		cred.Metadata["email"] = email
	}

	fmt.Println("Gemini authentication successful.")
	return cred, nil
}

// applyGeminiTokenToMetadata writes OAuth2 token fields into cred.Metadata.
func applyGeminiTokenToMetadata(cred *credentialmgr.Credential, token *oauth2.Token) {
	if cred.Metadata == nil {
		cred.Metadata = make(map[string]any)
	}
	cred.Metadata["access_token"] = token.AccessToken
	cred.Metadata["token_type"] = token.TokenType

	if token.RefreshToken != "" {
		cred.Metadata["refresh_token"] = token.RefreshToken
	}
	if !token.Expiry.IsZero() {
		cred.Metadata["expired"] = token.Expiry.UTC().Format(time.RFC3339)
	}
	cred.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)

	// Store token_uri, client_id, scopes for reconstructing the token source later.
	cred.Metadata["token_uri"] = "https://oauth2.googleapis.com/token"
	cred.Metadata["client_id"] = geminiClientID
}

// fetchGeminiUserEmail fetches the authenticated user's email from Google userinfo endpoint.
func fetchGeminiUserEmail(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geminiUserInfoURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create userinfo request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("userinfo request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read userinfo response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("userinfo request returned status %d", resp.StatusCode)
	}

	var info struct {
		Email string `json:"email"`
	}
	if err = json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("failed to parse userinfo response: %w", err)
	}
	return strings.TrimSpace(info.Email), nil
}

// ---- OAuth2 callback server for Gemini ----

type geminiCallbackServer struct {
	*callbackHTTPServer
}

func newGeminiCallbackServer(port int) *geminiCallbackServer {
	return &geminiCallbackServer{
		callbackHTTPServer: newCallbackHTTPServer(port),
	}
}

func (s *geminiCallbackServer) start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2callback", s.handleCallback)
	return s.callbackHTTPServer.start(mux)
}

func (s *geminiCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
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

	writeOAuthSuccessHTML(w, geminiSuccessHTML)
}

func parseGeminiCallbackURL(rawURL string) (code, state string, err error) {
	return parseOAuthCallbackURL("gemini", rawURL, true)
}

// ---- Success page ----

var geminiSuccessHTML = buildOAuthSuccessHTML("&#10003;", "#4285F4", "#0F9D58")
