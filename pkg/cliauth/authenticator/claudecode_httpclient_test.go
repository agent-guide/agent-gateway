package authenticator

import (
	"net/http"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/cliauth"
)

func TestBuildClaudeCodeHTTPClientUsesStandardTransportByDefault(t *testing.T) {
	client := buildClaudeCodeHTTPClient(cliauth.AuthenticatorConfig{})
	if client == nil {
		t.Fatal("client is nil")
	}
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
}

func TestBuildClaudeCodeHTTPClientUsesBrowserLikeProfileWhenRequested(t *testing.T) {
	client := buildClaudeCodeHTTPClient(cliauth.AuthenticatorConfig{
		TransportProfile: claudecodeBrowserLikeTLSProfile,
	})
	if client == nil {
		t.Fatal("client is nil")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.DialTLSContext == nil {
		t.Fatal("DialTLSContext is nil, want browser-like TLS dialer")
	}
}
