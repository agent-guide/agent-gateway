package authenticator

import (
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestBuiltinAuthenticatorsRegisterAsCaddyModules(t *testing.T) {
	for _, id := range []string{
		"llm.authenticators.codex",
		"llm.authenticators.claude",
		"llm.authenticators.gemini",
	} {
		info, err := caddy.GetModule(id)
		if err != nil {
			t.Fatalf("GetModule(%q) error = %v", id, err)
		}

		mod := info.New()
		if _, ok := mod.(cliauth.Authenticator); !ok {
			t.Fatalf("%q does not implement cliauth.Authenticator", id)
		}
	}
}

func TestCodexAuthenticatorUnmarshalCaddyfile(t *testing.T) {
	var auth CodexAuthenticator
	d := caddyfile.NewTestDispenser(`
	codex {
		callback_port 2455
		use_device_flow true
		no_browser true
	}
	`)

	if err := auth.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile() error = %v", err)
	}
	if auth.CallbackPort != 2455 || !auth.UseDeviceFlow || !auth.NoBrowser {
		t.Fatalf("unexpected codex config: %+v", auth)
	}
}

func TestClaudeAuthenticatorUnmarshalCaddyfile(t *testing.T) {
	var auth ClaudeAuthenticator
	d := caddyfile.NewTestDispenser(`
	claude {
		callback_port 60000
		no_browser true
	}
	`)

	if err := auth.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile() error = %v", err)
	}
	if auth.CallbackPort != 60000 || !auth.NoBrowser {
		t.Fatalf("unexpected claude config: %+v", auth)
	}
}

func TestGeminiAuthenticatorUnmarshalCaddyfile(t *testing.T) {
	var auth GeminiAuthenticator
	d := caddyfile.NewTestDispenser(`
	gemini {
		callback_port 9085
		no_browser true
	}
	`)

	if err := auth.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("UnmarshalCaddyfile() error = %v", err)
	}
	if auth.CallbackPort != 9085 || !auth.NoBrowser {
		t.Fatalf("unexpected gemini config: %+v", auth)
	}
}
