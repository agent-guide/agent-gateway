package authenticator

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildClaudeRedirectURI(t *testing.T) {
	tests := []struct {
		name string
		port int
		want string
	}{
		{name: "default port when zero", port: 0, want: "http://localhost:54545/callback"},
		{name: "custom port", port: 18443, want: "http://localhost:18443/callback"},
	}

	for _, tt := range tests {
		if got := buildClaudeRedirectURI(tt.port); got != tt.want {
			t.Fatalf("%s: buildClaudeRedirectURI(%d) = %q, want %q", tt.name, tt.port, got, tt.want)
		}
	}
}

func TestBuildClaudeAuthURLUsesRedirectURI(t *testing.T) {
	redirectURI := buildClaudeRedirectURI(18443)
	authURL := buildClaudeAuthURL("state-123", "challenge-abc", redirectURI)

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	query := parsed.Query()
	if got := query.Get("redirect_uri"); got != redirectURI {
		t.Fatalf("redirect_uri = %q, want %q", got, redirectURI)
	}
	if got := query.Get("state"); got != "state-123" {
		t.Fatalf("state = %q, want %q", got, "state-123")
	}
}

func TestParseClaudeCallbackURL(t *testing.T) {
	code, state, err := parseClaudeCallbackURL("http://localhost:54545/callback?code=abc123&state=xyz789")
	if err != nil {
		t.Fatalf("parseClaudeCallbackURL() error = %v", err)
	}
	if code != "abc123" || state != "xyz789" {
		t.Fatalf("parseClaudeCallbackURL() = (%q, %q), want (%q, %q)", code, state, "abc123", "xyz789")
	}
}

func TestParseClaudeCallbackURLErrors(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr string
	}{
		{name: "oauth error", rawURL: "http://localhost:54545/callback?error=access_denied", wantErr: "OAuth error"},
		{name: "missing code", rawURL: "http://localhost:54545/callback?state=xyz", wantErr: "missing 'code'"},
		{name: "missing state", rawURL: "http://localhost:54545/callback?code=abc", wantErr: "missing 'state'"},
	}

	for _, tt := range tests {
		_, _, err := parseClaudeCallbackURL(tt.rawURL)
		if err == nil {
			t.Fatalf("%s: parseClaudeCallbackURL() error = nil, want substring %q", tt.name, tt.wantErr)
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("%s: parseClaudeCallbackURL() error = %q, want substring %q", tt.name, err.Error(), tt.wantErr)
		}
	}
}
