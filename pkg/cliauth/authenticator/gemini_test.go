package authenticator

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"golang.org/x/oauth2"
)

func TestParseGeminiCallbackURL(t *testing.T) {
	code, state, err := parseGeminiCallbackURL("http://localhost:8085/oauth2callback?code=abc123&state=xyz789")
	if err != nil {
		t.Fatalf("parseGeminiCallbackURL() error = %v", err)
	}
	if code != "abc123" || state != "xyz789" {
		t.Fatalf("parseGeminiCallbackURL() = (%q, %q), want (%q, %q)", code, state, "abc123", "xyz789")
	}
}

func TestParseGeminiCallbackURLErrors(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr string
	}{
		{name: "oauth error", rawURL: "http://localhost:8085/oauth2callback?error=access_denied", wantErr: "OAuth error"},
		{name: "missing code", rawURL: "http://localhost:8085/oauth2callback?state=xyz", wantErr: "missing 'code'"},
		{name: "missing state", rawURL: "http://localhost:8085/oauth2callback?code=abc", wantErr: "missing 'state'"},
	}

	for _, tt := range tests {
		_, _, err := parseGeminiCallbackURL(tt.rawURL)
		if err == nil {
			t.Fatalf("%s: parseGeminiCallbackURL() error = nil, want substring %q", tt.name, tt.wantErr)
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("%s: parseGeminiCallbackURL() error = %q, want substring %q", tt.name, err.Error(), tt.wantErr)
		}
	}
}

func TestGeminiCallbackServerHandleCallbackRequiresState(t *testing.T) {
	srv := newGeminiCallbackServer(8085)
	resultCh := make(chan oauthCallbackResult, 1)
	srv.resultCh = resultCh

	req := httptest.NewRequest("GET", "/oauth2callback?code=abc123", nil)
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

func TestGeminiBuildCredentialSetsManualRefreshMetadata(t *testing.T) {
	auth := &GeminiAuthenticator{}
	token := &oauth2.Token{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().UTC().Add(time.Hour),
	}

	cred, err := auth.buildCredential(context.Background(), nil, token)
	if err != nil {
		t.Fatalf("buildCredential() error = %v", err)
	}
	if cred == nil {
		t.Fatal("buildCredential() returned nil credential")
	}
	if got := cred.Metadata[credentialmgr.MetadataManualRefreshNameKey]; got != "gemini" {
		t.Fatalf("manual_refresh_name = %#v, want gemini", got)
	}
	if got := cred.Metadata[credentialmgr.MetadataManualRefreshExpiryDelta]; got != 10*time.Second {
		t.Fatalf("manual_refresh_expiry_delta = %#v, want %v", got, 10*time.Second)
	}
}
