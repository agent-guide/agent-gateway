package ctlcmd

import (
	_ "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/anthropic"
	_ "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/cc"
	_ "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
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
)
