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
		{name: "zero port", port: 0, want: "http://localhost:0/callback"},
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
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != claudeAuthURL {
		t.Fatalf("authorize endpoint = %q, want %q", got, claudeAuthURL)
	}
	if got := query.Get("scope"); got != claudeScopes {
		t.Fatalf("scope = %q, want %q", got, claudeScopes)
	}
	if got := query.Get("code"); got != "true" {
		t.Fatalf("code = %q, want %q", got, "true")
	}
	if query.Get("org:create_api_key") != "" {
		t.Fatalf("unexpected literal query key %q present", "org:create_api_key")
	}
	if !strings.Contains(query.Get("scope"), "user:file_upload") {
		t.Fatalf("scope missing Claude Code file upload scope: %q", query.Get("scope"))
	}
}

func TestGenerateClaudeStateMatchesClaudeCodeShape(t *testing.T) {
	state, err := generateClaudeState()
	if err != nil {
		t.Fatalf("generateClaudeState() error = %v", err)
	}
	if len(state) != 43 {
		t.Fatalf("state length = %d, want 43", len(state))
	}
	if strings.ContainsAny(state, "+/=") {
		t.Fatalf("state = %q, want unpadded base64url", state)
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

func TestParseClaudeManualInputCode(t *testing.T) {
	outcome, err := parseClaudeManualInput("abc123", "state-123")
	if err != nil {
		t.Fatalf("parseClaudeManualInput() error = %v", err)
	}
	if outcome.Code != "abc123" || outcome.State != "state-123" || !outcome.Manual {
		t.Fatalf("parseClaudeManualInput() = %#v, want manual code outcome", outcome)
	}
}

func TestParseClaudeManualInputCallbackURL(t *testing.T) {
	outcome, err := parseClaudeManualInput("http://localhost:54545/callback?code=abc123&state=xyz789", "state-123")
	if err != nil {
		t.Fatalf("parseClaudeManualInput() error = %v", err)
	}
	if outcome.Code != "abc123" || outcome.State != "xyz789" || !outcome.Manual {
		t.Fatalf("parseClaudeManualInput() = %#v, want parsed callback URL outcome", outcome)
	}
}

func TestRedirectURIForOutcome(t *testing.T) {
	if got := redirectURIForOutcome("http://localhost:54545/callback", false); got != "http://localhost:54545/callback" {
		t.Fatalf("redirectURIForOutcome(false) = %q", got)
	}
	if got := redirectURIForOutcome("http://localhost:54545/callback", true); got != claudeManualRedirectURL {
		t.Fatalf("redirectURIForOutcome(true) = %q, want %q", got, claudeManualRedirectURL)
	}
}
