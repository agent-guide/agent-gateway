package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	// Standard Caddy modules
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	// LLM Gateway modules
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/admin"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/configstore/sqlite"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/dispatcher"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/dispatcher/llmapi/anthropic"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/dispatcher/llmapi/openai"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/gateway"

	// CLI authenticators (register as factory + Caddy modules via init())
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/cliauth/authenticator"

	// LLM Providers (register as factory + Caddy modules via init())
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/provider/anthropic"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/provider/deepseek"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/provider/gemini"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/provider/ollama"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/provider/openai"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/provider/openrouter"
	_ "github.com/agent-guide/caddy-agent-gateway/caddy/provider/zhipu"
)

func main() {
	caddycmd.Main()
}
