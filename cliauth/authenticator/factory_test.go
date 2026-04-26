package authenticator

import (
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/cliauth"
)

func TestBuiltinAuthenticatorsRegisterFactories(t *testing.T) {
	tests := []struct {
		name         string
		wantProvider string
	}{
		{name: "codex", wantProvider: "openai"},
		{name: "claude", wantProvider: "anthropic"},
		{name: "gemini", wantProvider: "gemini"},
	}

	for _, tt := range tests {
		auth, err := cliauth.NewAuthenticator(tt.name)
		if err != nil {
			t.Fatalf("NewAuthenticator(%q) error = %v", tt.name, err)
		}
		if auth.ProviderType() != tt.wantProvider {
			t.Fatalf("NewAuthenticator(%q) provider = %q, want %q", tt.name, auth.ProviderType(), tt.wantProvider)
		}
	}
}

func TestBuiltinAuthenticatorsUseDefaultSettings(t *testing.T) {
	codexAuth, err := NewCodexAuthenticator()
	if err != nil {
		t.Fatalf("NewCodexAuthenticator() error = %v", err)
	}
	codex, ok := codexAuth.(*CodexAuthenticator)
	if !ok {
		t.Fatalf("NewCodexAuthenticator() type = %T, want *CodexAuthenticator", codexAuth)
	}
	if codex.CallbackPort != codexDefaultCallbackPort {
		t.Fatalf("codex callback port = %d, want %d", codex.CallbackPort, codexDefaultCallbackPort)
	}

	claudeAuth, err := NewClaudeAuthenticator()
	if err != nil {
		t.Fatalf("NewClaudeAuthenticator() error = %v", err)
	}
	claude, ok := claudeAuth.(*ClaudeAuthenticator)
	if !ok {
		t.Fatalf("NewClaudeAuthenticator() type = %T, want *ClaudeAuthenticator", claudeAuth)
	}
	if claude.CallbackPort != claudeDefaultCallbackPort {
		t.Fatalf("claude callback port = %d, want %d", claude.CallbackPort, claudeDefaultCallbackPort)
	}

	geminiAuth, err := NewGeminiAuthenticator()
	if err != nil {
		t.Fatalf("NewGeminiAuthenticator() error = %v", err)
	}
	gemini, ok := geminiAuth.(*GeminiAuthenticator)
	if !ok {
		t.Fatalf("NewGeminiAuthenticator() type = %T, want *GeminiAuthenticator", geminiAuth)
	}
	if gemini.CallbackPort != geminiDefaultCallbackPort {
		t.Fatalf("gemini callback port = %d, want %d", gemini.CallbackPort, geminiDefaultCallbackPort)
	}
}
