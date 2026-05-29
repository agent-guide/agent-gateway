// Package cc provides the Claude Code CLI LLM API profile.
package cc

import (
	"github.com/agent-guide/agent-gateway/pkg/dispatcher"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/anthropic"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func init() {
	dispatcher.RegisterLLMApiHandlerType("cc")
}

// Handler handles Claude Code CLI requests. The wire format is Anthropic
// Messages API compatible, with the token-counting shim Claude Code expects.
type Handler struct {
	*anthropic.Handler
}

// NewHandler creates a Claude Code CLI handler.
func NewHandler(_ provider.Provider) *Handler {
	return &Handler{Handler: anthropic.NewHandlerWithOptions(anthropic.HandlerOptions{
		Name:                "cc",
		EstimateCountTokens: true,
	})}
}
