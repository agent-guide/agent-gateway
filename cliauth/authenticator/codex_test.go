package authenticator

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBuildAuthURLIncludesStateAndChallenge(t *testing.T) {
	authURL := buildAuthURL("state-123", "challenge-abc")

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	query := parsed.Query()
	if got := query.Get("state"); got != "state-123" {
		t.Fatalf("state = %q, want %q", got, "state-123")
	}
	if got := query.Get("code_challenge"); got != "challenge-abc" {
		t.Fatalf("code_challenge = %q, want %q", got, "challenge-abc")
	}
	if got := query.Get("redirect_uri"); got != codexRedirectURI {
		t.Fatalf("redirect_uri = %q, want %q", got, codexRedirectURI)
	}
}

func TestParseCallbackURL(t *testing.T) {
	code, state, err := parseCallbackURL("http://localhost:1455/auth/callback?code=abc123&state=xyz789")
	if err != nil {
		t.Fatalf("parseCallbackURL() error = %v", err)
	}
	if code != "abc123" || state != "xyz789" {
		t.Fatalf("parseCallbackURL() = (%q, %q), want (%q, %q)", code, state, "abc123", "xyz789")
	}
}

func TestParseCallbackURLAllowsMissingState(t *testing.T) {
	code, state, err := parseCallbackURL("http://localhost:1455/auth/callback?code=abc123")
	if err != nil {
		t.Fatalf("parseCallbackURL() error = %v", err)
	}
	if code != "abc123" || state != "" {
		t.Fatalf("parseCallbackURL() = (%q, %q), want (%q, %q)", code, state, "abc123", "")
	}
}

func TestParseCallbackURLErrors(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr string
	}{
		{name: "oauth error", rawURL: "http://localhost:1455/auth/callback?error=access_denied", wantErr: "OAuth error"},
		{name: "missing code", rawURL: "http://localhost:1455/auth/callback?state=xyz", wantErr: "missing 'code'"},
	}

	for _, tt := range tests {
		_, _, err := parseCallbackURL(tt.rawURL)
		if err == nil {
			t.Fatalf("%s: parseCallbackURL() error = nil, want substring %q", tt.name, tt.wantErr)
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("%s: parseCallbackURL() error = %q, want substring %q", tt.name, err.Error(), tt.wantErr)
		}
	}
}

func TestOAuthCallbackServerHandleCallbackRequiresState(t *testing.T) {
	srv := newOAuthCallbackServer(1455)
	resultCh := make(chan oauthCallbackResult, 1)
	srv.resultCh = resultCh

	req := httptest.NewRequest("GET", "/auth/callback?code=abc123", nil)
	rec := httptest.NewRecorder()
	srv.handleCallback(rec, req)

	result := <-resultCh
	if result.err != "no_state" {
		t.Fatalf("callback result err = %q, want %q", result.err, "no_state")
	}
	if rec.Code != 400 {
		t.Fatalf("response status = %d, want 400", rec.Code)
	}
}
