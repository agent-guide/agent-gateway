// Package authenticator provides concrete Authenticator implementations for CLI login flows.
package authenticator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/google/uuid"
)

// OAuth constants for Anthropic Claude CLI authentication.
const (
	claudeAuthURL     = "https://claude.ai/oauth/authorize"
	claudeTokenURL    = "https://api.anthropic.com/v1/oauth/token"
	claudeClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeRedirectURI = "http://localhost:54545/callback"
	claudeScopes      = "org:create_api_key user:profile user:inference"

	claudeCallbackTimeout     = 5 * time.Minute
	claudeDefaultCallbackPort = 54545
	claudeRefreshMaxRetries   = 3
)

func init() {
	caddy.RegisterModule(ClaudeAuthenticator{})
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
	// CallbackPort is the local port for the OAuth callback server (default: 54545).
	CallbackPort int
	// NoBrowser suppresses automatic browser opening and prints the URL instead.
	NoBrowser bool
	// HTTPClient is the HTTP client used for token requests. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// CaddyModule returns the Caddy module information.
func (ClaudeAuthenticator) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.authenticators.claude",
		New: func() caddy.Module { return new(ClaudeAuthenticator) },
	}
}

// NewClaudeAuthenticator creates a ClaudeAuthenticator with default settings.
func NewClaudeAuthenticator() (cliauth.Authenticator, error) {
	return &ClaudeAuthenticator{CallbackPort: claudeDefaultCallbackPort}, nil
}

// Provision applies default settings after the module is loaded.
func (a *ClaudeAuthenticator) Provision(caddy.Context) error {
	if a.CallbackPort <= 0 {
		a.CallbackPort = claudeDefaultCallbackPort
	}
	return nil
}

// UnmarshalCaddyfile configures the authenticator from Caddyfile tokens.
func (a *ClaudeAuthenticator) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "callback_port":
				if !d.NextArg() {
					return d.ArgErr()
				}
				port, err := strconv.Atoi(d.Val())
				if err != nil {
					return d.Errf("invalid callback_port: %v", err)
				}
				a.CallbackPort = port
			case "no_browser":
				if !d.NextArg() {
					return d.ArgErr()
				}
				val, err := strconv.ParseBool(d.Val())
				if err != nil {
					return d.Errf("invalid no_browser: %v", err)
				}
				a.NoBrowser = val
			default:
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

// Provider returns the provider type this authenticator handles.
func (a *ClaudeAuthenticator) Provider() string {
	return "anthropic"
}

// Login initiates the Claude CLI login flow and returns a new Credential on success.
func (a *ClaudeAuthenticator) Login(ctx context.Context) (*cliauth.Credential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return a.loginWithBrowser(ctx)
}

// RefreshLead refreshes the credential's access token before it expires.
// Returns nil if no refresh token is present.
func (a *ClaudeAuthenticator) RefreshLead(ctx context.Context, cred *cliauth.Credential) (*cliauth.Credential, error) {
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

func (a *ClaudeAuthenticator) loginWithBrowser(ctx context.Context) (*cliauth.Credential, error) {
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

	srv := newClaudeCallbackServer(port)
	if err = srv.start(); err != nil {
		return nil, fmt.Errorf("claude: failed to start callback server on port %d: %w", port, err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.stop(stopCtx)
	}()

	authURL := buildClaudeAuthURL(state, codeChallenge)

	if a.NoBrowser {
		fmt.Printf("Visit the following URL to authenticate with Claude:\n%s\n", authURL)
	} else {
		fmt.Println("Opening browser for Claude authentication...")
		if openErr := openBrowser(authURL); openErr != nil {
			fmt.Printf("Could not open browser automatically. Please visit:\n%s\n", authURL)
		}
	}

	fmt.Println("Waiting for Claude authentication callback...")

	code, gotState, err := srv.waitForCallback(claudeCallbackTimeout)
	if err != nil {
		return nil, fmt.Errorf("claude: callback error: %w", err)
	}
	if gotState != state {
		return nil, fmt.Errorf("claude: OAuth state mismatch (CSRF check failed)")
	}

	tokenResp, err := a.exchangeCode(ctx, code, state, codeVerifier)
	if err != nil {
		return nil, fmt.Errorf("claude: token exchange failed: %w", err)
	}

	return a.buildCredential(tokenResp)
}

// ---- Token exchange & refresh ----

func (a *ClaudeAuthenticator) exchangeCode(ctx context.Context, code, state, codeVerifier string) (*claudeTokenResponse, error) {
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
		"redirect_uri":  claudeRedirectURI,
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

func (a *ClaudeAuthenticator) buildCredential(tokenResp *claudeTokenResponse) (*cliauth.Credential, error) {
	cred := &cliauth.Credential{
		Credential: credentialmgr.Credential{
			ID:         uuid.New().String(),
			Provider:   a.Provider(),
			Metadata:   make(map[string]any),
			Attributes: make(map[string]string),
		},
		Status: cliauth.StatusActive,
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
func (a *ClaudeAuthenticator) applyTokenToMetadata(cred *cliauth.Credential, tokenResp *claudeTokenResponse) {
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
	return http.DefaultClient
}

// ---- Authorization URL ----

func buildClaudeAuthURL(state, codeChallenge string) string {
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {claudeClientID},
		"response_type":         {"code"},
		"redirect_uri":          {claudeRedirectURI},
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

// ---- OAuth callback server for Claude ----

type claudeCallbackServer struct {
	port     int
	srv      *http.Server
	resultCh chan oauthCallbackResult
	mu       sync.Mutex
	running  bool
}

func newClaudeCallbackServer(port int) *claudeCallbackServer {
	return &claudeCallbackServer{
		port:     port,
		resultCh: make(chan oauthCallbackResult, 1),
	}
}

func (s *claudeCallbackServer) start() error {
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
	mux.HandleFunc("/callback", s.handleCallback)
	mux.HandleFunc("/success", s.handleSuccess)

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

func (s *claudeCallbackServer) stop(ctx context.Context) error {
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

func (s *claudeCallbackServer) waitForCallback(timeout time.Duration) (code, state string, err error) {
	select {
	case result := <-s.resultCh:
		if result.err != "" {
			return "", "", fmt.Errorf("OAuth callback error: %s", result.err)
		}
		return result.code, result.state, nil
	case <-time.After(timeout):
		return "", "", fmt.Errorf("timed out waiting for OAuth callback after %s", timeout)
	}
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(claudeSuccessHTML))
}

// ---- Success page ----

const claudeSuccessHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Authentication Successful</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
               display: flex; justify-content: center; align-items: center;
               min-height: 100vh; margin: 0;
               background: linear-gradient(135deg, #d97706 0%, #b45309 100%); }
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
        <div class="icon">✅</div>
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
