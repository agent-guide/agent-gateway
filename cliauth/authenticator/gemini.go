// Package authenticator provides concrete Authenticator implementations for CLI login flows.
package authenticator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
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
	// CallbackPort is the local port for the OAuth callback server (default: 8085).
	CallbackPort int
	// NoBrowser suppresses automatic browser opening and prints the URL instead.
	NoBrowser bool
	// HTTPClient is the HTTP client used for token requests. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// NewGeminiAuthenticator creates a GeminiAuthenticator with default settings.
func NewGeminiAuthenticator() (cliauth.Authenticator, error) {
	return &GeminiAuthenticator{CallbackPort: geminiDefaultCallbackPort}, nil
}

// ProviderType returns the provider type this authenticator handles.
func (a *GeminiAuthenticator) ProviderType() string {
	return "gemini"
}

// RefreshLeadTime returns how far in advance of token expiry to refresh Gemini credentials.
func (a *GeminiAuthenticator) RefreshLeadTime() time.Duration {
	return 5 * time.Minute
}

// Login initiates the Gemini CLI login flow and returns a new Credential on success.
func (a *GeminiAuthenticator) Login(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return a.loginWithBrowser(ctx, reporter)
}

// Refresh refreshes the credential's access token before it expires.
// Returns nil if no refresh token is present.
func (a *GeminiAuthenticator) Refresh(ctx context.Context, cred *cliauth.Credential) (*cliauth.Credential, error) {
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

func (a *GeminiAuthenticator) loginWithBrowser(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
	port := a.CallbackPort
	if port <= 0 {
		port = geminiDefaultCallbackPort
	}
	callbackURL := fmt.Sprintf("http://localhost:%d/oauth2callback", port)

	conf := &oauth2.Config{
		ClientID:     geminiClientID,
		ClientSecret: geminiClientSecret,
		RedirectURL:  callbackURL,
		Scopes:       geminiScopes,
		Endpoint:     google.Endpoint,
	}

	srv := newGeminiCallbackServer(port)
	if err := srv.start(); err != nil {
		return nil, fmt.Errorf("gemini: failed to start callback server on port %d: %w", port, err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.stop(stopCtx)
	}()

	authURL := conf.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	reportLoginStatus(reporter, cliauth.LoginStatusUpdate{
		Phase:           "awaiting_browser_auth",
		Message:         "Open the verification URL in a browser and complete the Gemini login flow.",
		VerificationURL: authURL,
	})

	if a.NoBrowser || reporter != nil {
		fmt.Printf("Visit the following URL to authenticate with Gemini:\n%s\n", authURL)
	} else {
		fmt.Println("Opening browser for Gemini authentication...")
		if openErr := openBrowser(authURL); openErr != nil {
			fmt.Printf("Could not open browser automatically. Please visit:\n%s\n", authURL)
		}
	}

	fmt.Println("Waiting for Gemini authentication callback...")
	reportLoginStatus(reporter, cliauth.LoginStatusUpdate{
		Phase:           "waiting_for_callback",
		Message:         "Waiting for the Gemini OAuth callback after browser verification.",
		VerificationURL: authURL,
	})

	code, err := srv.waitForCallback(geminiCallbackTimeout)
	if err != nil {
		return nil, fmt.Errorf("gemini: callback error: %w", err)
	}

	token, err := conf.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("gemini: token exchange failed: %w", err)
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
	if a.HTTPClient != nil {
		httpCtx = context.WithValue(ctx, oauth2.HTTPClient, a.HTTPClient)
	}

	ts := conf.TokenSource(httpCtx, expired)
	token, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	return token, nil
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

func (a *GeminiAuthenticator) buildCredential(ctx context.Context, conf *oauth2.Config, token *oauth2.Token) (*cliauth.Credential, error) {
	cred := &cliauth.Credential{
		Credential: credentialmgr.Credential{
			ID:           uuid.New().String(),
			ProviderType: a.ProviderType(),
			Metadata:     make(map[string]any),
			Attributes:   make(map[string]string),
		},
		Status: cliauth.StatusActive,
	}

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
func applyGeminiTokenToMetadata(cred *cliauth.Credential, token *oauth2.Token) {
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
	port     int
	srv      *http.Server
	resultCh chan geminiCallbackResult
	mu       sync.Mutex
	running  bool
}

type geminiCallbackResult struct {
	code string
	err  string
}

func newGeminiCallbackServer(port int) *geminiCallbackServer {
	return &geminiCallbackServer{
		port:     port,
		resultCh: make(chan geminiCallbackResult, 1),
	}
}

func (s *geminiCallbackServer) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("callback server already running")
	}

	addr := fmt.Sprintf(":%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d already in use: %w", s.port, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2callback", s.handleCallback)

	s.srv = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	s.running = true

	go func() {
		_ = s.srv.Serve(ln)
	}()
	return nil
}

func (s *geminiCallbackServer) stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.srv == nil {
		return nil
	}
	err := s.srv.Shutdown(ctx)
	s.running = false
	s.srv = nil
	return err
}

func (s *geminiCallbackServer) waitForCallback(timeout time.Duration) (code string, err error) {
	select {
	case result := <-s.resultCh:
		if result.err != "" {
			return "", fmt.Errorf("OAuth callback error: %s", result.err)
		}
		return result.code, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("timed out waiting for OAuth callback after %s", timeout)
	}
}

func (s *geminiCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	errParam := q.Get("error")
	code := q.Get("code")

	var result geminiCallbackResult
	switch {
	case errParam != "":
		result.err = errParam
	case code == "":
		result.err = "no_code"
	default:
		result.code = code
	}

	select {
	case s.resultCh <- result:
	default:
	}

	if result.err != "" {
		http.Error(w, "Authentication failed: "+result.err, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(geminiSuccessHTML))
}

// ---- Success page ----

const geminiSuccessHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Authentication Successful</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
               display: flex; justify-content: center; align-items: center;
               min-height: 100vh; margin: 0;
               background: linear-gradient(135deg, #4285F4 0%, #0F9D58 100%); }
        .card { background: white; padding: 2.5rem; border-radius: 12px;
                box-shadow: 0 10px 25px rgba(0,0,0,0.1); max-width: 420px; text-align: center; }
        .icon { font-size: 3rem; }
        h1 { color: #1f2937; margin: 1rem 0 0.5rem; }
        p { color: #6b7280; }
        .countdown { margin-top: 1.5rem; color: #9ca3af; font-size: 0.85rem; }
    </style>
</head>
<body>
    <div class="card">
        <div class="icon">&#10003;</div>
        <h1>Authentication Successful</h1>
        <p>You can close this window and return to your terminal.</p>
        <div class="countdown">Closing in <span id="t">10</span>s</div>
    </div>
    <script>
        let n = 10;
        const el = document.getElementById('t');
        const iv = setInterval(() => { el.textContent = --n; if (n <= 0) { clearInterval(iv); window.close(); } }, 1000);
    </script>
</body>
</html>`
