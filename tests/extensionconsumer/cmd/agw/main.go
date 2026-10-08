package main

import (
	fixture "example.com/extensionconsumer"
	_ "github.com/agent-guide/agent-gateway/caddy/admin"
	_ "github.com/agent-guide/agent-gateway/caddy/configstore/sqlite"
	_ "github.com/agent-guide/agent-gateway/caddy/dispatcher"
	_ "github.com/agent-guide/agent-gateway/caddy/dispatcher/llmapi/anthropic"
	_ "github.com/agent-guide/agent-gateway/caddy/dispatcher/llmapi/cc"
	_ "github.com/agent-guide/agent-gateway/caddy/dispatcher/llmapi/openai"
	_ "github.com/agent-guide/agent-gateway/caddy/gateway"
	_ "github.com/agent-guide/agent-gateway/pkg/acp/agent/codex"
	_ "github.com/agent-guide/agent-gateway/pkg/acp/agent/opencode"
	"github.com/agent-guide/agent-gateway/pkg/distribution/caddycmd"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/anthropic"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/anthropicbase"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/claudecode"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/codex"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/deepseek"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/gemini"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/grok"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/ollama"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openrouter"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/qwen"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/zhipu"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

func main() { caddycmd.New(fixture.Runtime()).Run() }
