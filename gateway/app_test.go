package gateway

import (
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/caddyserver/caddy/v2"
)

func TestProvisionAuthenticatorsWithEmptyConfig(t *testing.T) {
	app := &App{cliauthManager: manager.NewManager(nil, nil, nil)}

	if err := app.provisionAuthenticators(caddy.Context{}); err != nil {
		t.Fatalf("provisionAuthenticators() error = %v", err)
	}

	if _, ok := app.cliauthManager.GetAuthenticator("codex"); ok {
		t.Fatal("expected codex authenticator to remain disabled without configuration")
	}
	if _, ok := app.cliauthManager.GetAuthenticator("claude"); ok {
		t.Fatal("expected claude authenticator to remain disabled without configuration")
	}
}
