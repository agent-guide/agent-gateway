package main

import (
	fixture "example.com/extensionconsumer"
	_ "github.com/agent-guide/agent-gateway/pkg/acp/agent/codex"
	_ "github.com/agent-guide/agent-gateway/pkg/acp/agent/opencode"
	"github.com/agent-guide/agent-gateway/pkg/distribution/daemoncmd"
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
	"os"
)

func main() { os.Exit(daemoncmd.Run(daemoncmd.Options{Assembly: fixture.Runtime()})) }
