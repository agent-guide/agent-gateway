// Package authenticator provides concrete Authenticator implementations for CLI login flows.
package authenticator

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
	internalhttpclient "github.com/agent-guide/caddy-agent-gateway/internal/httpclient"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/google/uuid"
)

// OAuth constants for OpenAI Codex CLI authentication.
const (
	codexAuthURL     = "https://auth.openai.com/oauth/authorize"
	codexTokenURL    = "https://auth.openai.com/oauth/token"
	codexClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexRedirectURI = "http://localhost:1455/auth/callback"

	// Device flow endpoints.
	codexDeviceUserCodeURL     = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL        = "https://auth.openai.com/api/accounts/deviceauth/token"
	codexDeviceVerificationURL = "https://auth.openai.com/codex/device"
	codexDeviceRedirectURI     = "https://auth.openai.com/deviceauth/callback"

	codexCallbackTimeout     = 5 * time.Minute
	codexDeviceTimeout       = 15 * time.Minute
	codexDefaultCallbackPort = 1455
	codexDefaultPollInterval = 5 * time.Second
	codexRefreshMaxRetries   = 3
	codexManualPromptDelay   = 15 * time.Second
)

// ---- Structured error types ----

// AuthErrorCode identifies the category of a login-flow error.
type AuthErrorCode string

const (
	ErrPortInUse          AuthErrorCode = "port_in_use"
	ErrServerStartFailed  AuthErrorCode = "server_start_failed"
	ErrCallbackTimeout    AuthErrorCode = "callback_timeout"
	ErrInvalidState       AuthErrorCode = "invalid_state"
	ErrCodeExchangeFailed AuthErrorCode = "code_exchange_failed"
)

// AuthenticationError carries a typed error code alongside the underlying cause.
type AuthenticationError struct {
	Code    AuthErrorCode
	Wrapped error
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("codex [%s]: %v", e.Code, e.Wrapped)
}

func (e *AuthenticationError) Unwrap() error { return e.Wrapped }

func newAuthError(code AuthErrorCode, err error) error {
	return &AuthenticationError{Code: code, Wrapped: err}
}

func init() {
	cliauth.RegisterAuthenticatorFactory("codex", NewCodexAuthenticator)
}

// ---- Internal HTTP response types ----

type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type codexDeviceUserCodeReq struct {
	ClientID string `json:"client_id"`
}

type codexDeviceUserCodeResp struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	UserCodeAlt  string          `json:"usercode"`
	Interval     json.RawMessage `json:"interval"`
}

type codexDeviceTokenReq struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
}

type codexDeviceTokenResp struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
	CodeChallenge     string `json:"code_challenge"`
}

// ---- JWT claim types ----

type codexJWTClaims struct {
	Email         string        `json:"email"`
	Exp           int64         `json:"exp"`
	CodexAuthInfo codexAuthInfo `json:"https://api.openai.com/auth"`
}

type codexAuthInfo struct {
	ChatgptAccountID string `json:"chatgpt_account_id"`
	ChatgptPlanType  string `json:"chatgpt_plan_type"`
	ChatgptUserID    string `json:"chatgpt_user_id"`
}

// ---- CodexAuthenticator ----

// CodexAuthenticator implements manager.Authenticator for the OpenAI Codex CLI login flow.
// It supports both browser-based OAuth PKCE and headless device flow authentication.
type CodexAuthenticator struct {
	// CallbackPort is the local port for the OAuth callback server (default: 1455).
	CallbackPort int
	// UseDeviceFlow forces device-code authentication instead of browser-based OAuth.
	UseDeviceFlow bool
	// NoBrowser suppresses automatic browser opening and prints the URL instead.
	NoBrowser bool
	// NetworkConfig controls the HTTP behavior for outbound token requests.
	NetworkConfig internalhttpclient.NetworkConfig

	client *http.Client // lazily initialized from NetworkConfig
}

// NewCodexAuthenticator creates a CodexAuthenticator with default settings.
func NewCodexAuthenticator() (cliauth.Authenticator, error) {
	return &CodexAuthenticator{CallbackPort: codexDefaultCallbackPort}, nil
}

// ProviderType returns the provider type this authenticator handles.
func (a *CodexAuthenticator) ProviderType() string {
	return "openai"
}

// RefreshLeadTime returns how far in advance of token expiry to refresh Codex credentials.
// Five days gives ample runway for CLI tokens whose refresh window is typically 30 days.
func (a *CodexAuthenticator) RefreshLeadTime() time.Duration {
	return 5 * 24 * time.Hour
}

// Login initiates the Codex CLI login flow and returns a new Credential on success.
// It uses browser-based OAuth PKCE by default; set UseDeviceFlow for headless environments.
func (a *CodexAuthenticator) Login(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.UseDeviceFlow {
		return a.loginWithDeviceFlow(ctx, reporter)
	}
	return a.loginWithBrowser(ctx, reporter)
}

// Refresh refreshes the credential's access token before it expires.
// Returns nil if no refresh token is present or the credential has no expiry metadata.
func (a *CodexAuthenticator) Refresh(ctx context.Context, cred *cliauth.Credential) (*cliauth.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("codex: credential is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	refreshToken, _ := cred.Metadata["refresh_token"].(string)
	if strings.TrimSpace(refreshToken) == "" {
		return nil, nil
	}

	tokenResp, err := a.refreshTokensWithRetry(ctx, refreshToken, codexRefreshMaxRetries)
	if err != nil {
		return nil, fmt.Errorf("codex: token refresh failed: %w", err)
	}

	updated := cred.Clone()
	a.applyTokenToMetadata(updated, tokenResp)
	return updated, nil
}

// ---- Browser-based OAuth PKCE flow ----

func (a *CodexAuthenticator) loginWithBrowser(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
	codeVerifier, codeChallenge, err := generatePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("codex: PKCE generation failed: %w", err)
	}

	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("codex: state generation failed: %w", err)
	}

	port := a.CallbackPort
	if port <= 0 {
		port = codexDefaultCallbackPort
	}

	srv := newOAuthCallbackServer(port)
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

	authURL := buildAuthURL(state, codeChallenge)
	reportLoginStatus(reporter, cliauth.LoginStatusUpdate{
		Phase:           "awaiting_browser_auth",
		Message:         "Open the verification URL in a browser and complete the Codex login flow.",
		VerificationURL: authURL,
	})

	if a.NoBrowser || reporter != nil {
		fmt.Printf("Visit the following URL to authenticate with Codex:\n%s\n", authURL)
	} else {
		fmt.Println("Opening browser for Codex authentication...")
		if openErr := openBrowser(authURL); openErr != nil {
			fmt.Printf("Could not open browser automatically. Please visit:\n%s\n", authURL)
		}
	}

	fmt.Println("Waiting for Codex authentication callback...")
	reportLoginStatus(reporter, cliauth.LoginStatusUpdate{
		Phase:           "waiting_for_callback",
		Message:         "Waiting for the Codex OAuth callback after browser verification.",
		VerificationURL: authURL,
	})

	// Run the callback wait in a goroutine so we can also offer a manual URL-paste
	// fallback after codexManualPromptDelay (useful for SSH / tunnelled environments).
	type cbResult struct{ code, state string }
	cbCh := make(chan cbResult, 1)
	cbErrCh := make(chan error, 1)
	go func() {
		code, gotState, waitErr := srv.waitForCallback(codexCallbackTimeout)
		if waitErr != nil {
			cbErrCh <- waitErr
			return
		}
		cbCh <- cbResult{code, gotState}
	}()

	manualTimer := time.NewTimer(codexManualPromptDelay)
	defer manualTimer.Stop()
	var manualLineCh <-chan string
	var manualLineErrCh <-chan error

	var outcome cbResult
waitLoop:
	for {
		select {
		case outcome = <-cbCh:
			break waitLoop
		case waitErr := <-cbErrCh:
			return nil, newAuthError(ErrCallbackTimeout, waitErr)
		case <-manualTimer.C:
			// Drain the callback channel one more time before prompting.
			select {
			case outcome = <-cbCh:
				break waitLoop
			case waitErr := <-cbErrCh:
				return nil, newAuthError(ErrCallbackTimeout, waitErr)
			default:
			}
			manualLineCh, manualLineErrCh = asyncReadLine(
				"Paste the Codex callback URL (or press Enter to keep waiting): ")
		case line := <-manualLineCh:
			manualLineCh = nil
			manualLineErrCh = nil
			if strings.TrimSpace(line) == "" {
				// Re-issue the prompt so the user can try again.
				manualLineCh, manualLineErrCh = asyncReadLine(
					"Paste the Codex callback URL (or press Enter to keep waiting): ")
				continue
			}
			code, gotState, parseErr := parseCallbackURL(line)
			if parseErr != nil {
				return nil, parseErr
			}
			outcome = cbResult{code, gotState}
			break waitLoop
		case readErr := <-manualLineErrCh:
			return nil, readErr
		}
	}

	if outcome.state != state {
		return nil, newAuthError(ErrInvalidState, fmt.Errorf("state mismatch"))
	}

	tokenResp, err := a.exchangeCode(ctx, outcome.code, codexRedirectURI, codeVerifier)
	if err != nil {
		return nil, newAuthError(ErrCodeExchangeFailed, err)
	}

	return a.buildCredential(tokenResp)
}

// ---- Device flow ----

func (a *CodexAuthenticator) loginWithDeviceFlow(ctx context.Context, reporter cliauth.LoginStatusReporter) (*cliauth.Credential, error) {
	client := a.httpClient()

	userCodeResp, err := requestDeviceUserCode(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("codex device: %w", err)
	}

	deviceCode := strings.TrimSpace(userCodeResp.UserCode)
	if deviceCode == "" {
		deviceCode = strings.TrimSpace(userCodeResp.UserCodeAlt)
	}
	deviceAuthID := strings.TrimSpace(userCodeResp.DeviceAuthID)
	if deviceCode == "" || deviceAuthID == "" {
		return nil, fmt.Errorf("codex device: server did not return required device_auth_id and user_code")
	}

	pollInterval := parseDevicePollInterval(userCodeResp.Interval)

	fmt.Printf("Codex device authentication\n")
	fmt.Printf("  Visit:     %s\n", codexDeviceVerificationURL)
	fmt.Printf("  User code: %s\n", deviceCode)
	reportLoginStatus(reporter, cliauth.LoginStatusUpdate{
		Phase:           "awaiting_device_auth",
		Message:         "Open the verification URL and enter the user code to continue Codex device authentication.",
		VerificationURL: codexDeviceVerificationURL,
		UserCode:        deviceCode,
	})

	if !a.NoBrowser && reporter == nil {
		if openErr := openBrowser(codexDeviceVerificationURL); openErr != nil {
			fmt.Printf("Could not open browser automatically. Please visit the URL above.\n")
		}
	}

	reportLoginStatus(reporter, cliauth.LoginStatusUpdate{
		Phase:           "waiting_for_device_confirmation",
		Message:         "Waiting for Codex device authentication to be completed.",
		VerificationURL: codexDeviceVerificationURL,
		UserCode:        deviceCode,
	})
	devTokenResp, err := pollDeviceToken(ctx, client, deviceAuthID, deviceCode, pollInterval)
	if err != nil {
		return nil, fmt.Errorf("codex device: %w", err)
	}

	authCode := strings.TrimSpace(devTokenResp.AuthorizationCode)
	codeVerifier := strings.TrimSpace(devTokenResp.CodeVerifier)
	codeChallenge := strings.TrimSpace(devTokenResp.CodeChallenge)
	if authCode == "" || codeVerifier == "" || codeChallenge == "" {
		return nil, fmt.Errorf("codex device: token response missing required fields (authorization_code, code_verifier, code_challenge)")
	}

	tokenResp, err := a.exchangeCode(ctx, authCode, codexDeviceRedirectURI, codeVerifier)
	if err != nil {
		return nil, fmt.Errorf("codex device: token exchange failed: %w", err)
	}

	return a.buildCredential(tokenResp)
}

// ---- Token exchange & refresh ----

func (a *CodexAuthenticator) exchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*codexTokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token exchange response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp codexTokenResponse
	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token exchange response: %w", err)
	}
	return &tokenResp, nil
}

func (a *CodexAuthenticator) refreshTokens(ctx context.Context, refreshToken string) (*codexTokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {codexClientID},
		"refresh_token": {refreshToken},
		"scope":         {"openid profile email"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp codexTokenResponse
	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}
	return &tokenResp, nil
}

func (a *CodexAuthenticator) refreshTokensWithRetry(ctx context.Context, refreshToken string, maxRetries int) (*codexTokenResponse, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
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
		// Non-retryable: refresh token reuse detected.
		if strings.Contains(strings.ToLower(err.Error()), "refresh_token_reused") {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("token refresh failed after %d attempts: %w", maxRetries, lastErr)
}

// ---- Credential builder ----

func (a *CodexAuthenticator) buildCredential(tokenResp *codexTokenResponse) (*cliauth.Credential, error) {
	cred := &cliauth.Credential{
		Credential: credentialmgr.Credential{
			ID:           uuid.New().String(),
			ProviderType: a.ProviderType(),
			Metadata:     make(map[string]any),
			Attributes:   make(map[string]string),
		},
		Status: cliauth.StatusActive,
	}

	a.applyTokenToMetadata(cred, tokenResp)

	// Extract user info from ID token JWT.
	if tokenResp.IDToken != "" {
		claims, err := parseJWTClaims(tokenResp.IDToken)
		if err == nil && claims != nil {
			email := strings.TrimSpace(claims.Email)
			planType := strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType)
			accountID := strings.TrimSpace(claims.CodexAuthInfo.ChatgptAccountID)

			cred.Label = email
			cred.Attributes["email"] = email
			cred.Attributes["plan_type"] = planType
			cred.Attributes["account_id"] = accountID
			cred.Metadata["email"] = email
		}
	}

	fmt.Println("Codex authentication successful.")
	return cred, nil
}

// applyTokenToMetadata writes token fields into cred.Metadata.
func (a *CodexAuthenticator) applyTokenToMetadata(cred *cliauth.Credential, tokenResp *codexTokenResponse) {
	if cred.Metadata == nil {
		cred.Metadata = make(map[string]any)
	}
	cred.Metadata["access_token"] = tokenResp.AccessToken
	cred.Metadata["refresh_token"] = tokenResp.RefreshToken
	cred.Metadata["id_token"] = tokenResp.IDToken

	if tokenResp.ExpiresIn > 0 {
		expiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
		cred.Metadata["expired"] = expiry
	}
	cred.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
}

func (a *CodexAuthenticator) httpClient() *http.Client {
	if a.client != nil {
		return a.client
	}
	a.client = internalhttpclient.BuildHTTPClient(a.NetworkConfig)
	return a.client
}

// ---- Authorization URL ----

func buildAuthURL(state, codeChallenge string) string {
	params := url.Values{
		"client_id":                  {codexClientID},
		"response_type":              {"code"},
		"redirect_uri":               {codexRedirectURI},
		"scope":                      {"openid email profile offline_access"},
		"state":                      {state},
		"code_challenge":             {codeChallenge},
		"code_challenge_method":      {"S256"},
		"prompt":                     {"login"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	return codexAuthURL + "?" + params.Encode()
}

// ---- PKCE helpers ----

func generatePKCECodes() (verifier, challenge string, err error) {
	raw := make([]byte, 96)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	verifier = base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(sum[:])
	return verifier, challenge, nil
}

func generateState() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate OAuth state: %w", err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw), nil
}

// ---- JWT parsing ----

func parseJWTClaims(idToken string) (*codexJWTClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	// Add padding if needed.
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims codexJWTClaims
	if err = json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JWT claims: %w", err)
	}
	return &claims, nil
}

// ---- OAuth callback server ----

type oauthCallbackResult struct {
	code  string
	state string
	err   string
}

type oauthCallbackServer struct {
	port     int
	srv      *http.Server
	resultCh chan oauthCallbackResult
	mu       sync.Mutex
	running  bool
}

func newOAuthCallbackServer(port int) *oauthCallbackServer {
	return &oauthCallbackServer{
		port:     port,
		resultCh: make(chan oauthCallbackResult, 1),
	}
}

func (s *oauthCallbackServer) start() error {
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
	mux.HandleFunc("/auth/callback", s.handleCallback)
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

func (s *oauthCallbackServer) stop(ctx context.Context) error {
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

func (s *oauthCallbackServer) waitForCallback(timeout time.Duration) (code, state string, err error) {
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

func (s *oauthCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
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

func (s *oauthCallbackServer) handleSuccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(codexSuccessHTML))
}

// ---- Browser opener ----

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	case "linux":
		for _, bin := range []string{"xdg-open", "x-www-browser", "www-browser", "firefox", "chromium", "google-chrome"} {
			if _, err := exec.LookPath(bin); err == nil {
				cmd = exec.Command(bin, rawURL)
				break
			}
		}
		if cmd == nil {
			return fmt.Errorf("no browser found on this Linux system")
		}
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return cmd.Start()
}

// ---- Device flow helpers ----

func requestDeviceUserCode(ctx context.Context, client *http.Client) (*codexDeviceUserCodeResp, error) {
	body, err := json.Marshal(codexDeviceUserCodeReq{ClientID: codexClientID})
	if err != nil {
		return nil, fmt.Errorf("failed to encode device code request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexDeviceUserCodeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read device code response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("device flow endpoint unavailable (status 404)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		trimmed := strings.TrimSpace(string(respBody))
		if trimmed == "" {
			trimmed = "empty response body"
		}
		return nil, fmt.Errorf("device code request returned status %d: %s", resp.StatusCode, trimmed)
	}

	var parsed codexDeviceUserCodeResp
	if err = json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse device code response: %w", err)
	}
	return &parsed, nil
}

func pollDeviceToken(ctx context.Context, client *http.Client, deviceAuthID, userCode string, interval time.Duration) (*codexDeviceTokenResp, error) {
	deadline := time.Now().Add(codexDeviceTimeout)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device authentication timed out after 15 minutes")
		}

		body, err := json.Marshal(codexDeviceTokenReq{
			DeviceAuthID: deviceAuthID,
			UserCode:     userCode,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to encode device poll request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexDeviceTokenURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create device poll request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("device poll request failed: %w", err)
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read device poll response: %w", readErr)
		}

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			var parsed codexDeviceTokenResp
			if err = json.Unmarshal(respBody, &parsed); err != nil {
				return nil, fmt.Errorf("failed to parse device token response: %w", err)
			}
			return &parsed, nil
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
			// Still pending; wait and retry.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
				continue
			}
		default:
			trimmed := strings.TrimSpace(string(respBody))
			if trimmed == "" {
				trimmed = "empty response body"
			}
			return nil, fmt.Errorf("device token polling returned status %d: %s", resp.StatusCode, trimmed)
		}
	}
}

func parseDevicePollInterval(raw json.RawMessage) time.Duration {
	if len(raw) == 0 {
		return codexDefaultPollInterval
	}
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(asStr)); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil && asInt > 0 {
		return time.Duration(asInt) * time.Second
	}
	return codexDefaultPollInterval
}

// ---- Manual callback URL input helpers ----

// asyncReadLine prints prompt and reads one line from stdin in a goroutine.
func asyncReadLine(prompt string) (<-chan string, <-chan error) {
	ch := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		fmt.Print(prompt)
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			ch <- scanner.Text()
		} else if err := scanner.Err(); err != nil {
			errCh <- err
		} else {
			ch <- ""
		}
	}()
	return ch, errCh
}

// parseCallbackURL extracts the OAuth code and state from a pasted callback URL.
func parseCallbackURL(rawURL string) (code, state string, err error) {
	u, parseErr := url.Parse(strings.TrimSpace(rawURL))
	if parseErr != nil {
		return "", "", fmt.Errorf("codex: failed to parse callback URL: %w", parseErr)
	}
	q := u.Query()
	if errParam := q.Get("error"); errParam != "" {
		return "", "", fmt.Errorf("codex: OAuth error from callback URL: %s", errParam)
	}
	code = q.Get("code")
	if code == "" {
		return "", "", fmt.Errorf("codex: callback URL missing 'code' parameter")
	}
	state = q.Get("state")
	return code, state, nil
}

// ---- Success page ----

const codexSuccessHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Authentication Successful</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
               display: flex; justify-content: center; align-items: center;
               min-height: 100vh; margin: 0;
               background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); }
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
