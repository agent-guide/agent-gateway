package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	// Standard Caddy modules
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	// LLM Gateway modules
	_ "github.com/agent-guide/caddy-agent-gateway/admin"
	_ "github.com/agent-guide/caddy-agent-gateway/api"
	_ "github.com/agent-guide/caddy-agent-gateway/api/llmapi/anthropic"
	_ "github.com/agent-guide/caddy-agent-gateway/api/llmapi/openai"
	_ "github.com/agent-guide/caddy-agent-gateway/gateway"

	// CLI authenticators (register as factory + Caddy modules via init())
	_ "github.com/agent-guide/caddy-agent-gateway/cliauth/authenticator"

	// LLM Providers (register as factory + Caddy modules via init())
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/anthropic"
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/deepseek"
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/gemini"
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/ollama"
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/openai"
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/openrouter"
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/zhipu"
)

func main() {
	caddycmd.Main()
}
