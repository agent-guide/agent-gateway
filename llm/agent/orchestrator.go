package agent

import (
	"context"
	"fmt"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/memory"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

// Orchestrator processes agent-mode requests, automatically calling
// MCP tools and Memory as configured.
type Orchestrator struct {
	provider provider.Provider
	memory   memory.MemoryStore
}

// NewOrchestrator creates a new Agent Orchestrator.
func NewOrchestrator(p provider.Provider, mem memory.MemoryStore) *Orchestrator {
	return &Orchestrator{provider: p, memory: mem}
}

// Request is an agent-mode request.
type Request struct {
	SessionID    string
	AgentID      string
	Messages     []*schema.Message
	Tools        []*schema.ToolInfo
	Config       *Config
	EnableMCP    bool
	EnableMemory bool
	AutoToolCall bool
}

// Config configures agent behavior.
type Config struct {
	MaxIterations int
	Timeout       time.Duration
}

// Response is the final agent-mode response.
type Response struct {
	SessionID string
	Messages  []*schema.Message
	Usage     provider.Usage
}

// Process handles an agent-mode request end-to-end.
func (o *Orchestrator) Process(ctx context.Context, req *Request) (*Response, error) {
	cfg := req.Config
	if cfg == nil {
		cfg = &Config{MaxIterations: 10, Timeout: 5 * time.Minute}
	}
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 10
	}

	messages := req.Messages

	// Retrieve relevant memories
	if req.EnableMemory && o.memory != nil && req.AgentID != "" {
		// TODO: search memory and prepend to system message
	}

	var totalUsage provider.Usage
	for i := 0; i < cfg.MaxIterations; i++ {
		var opts []einomodel.Option
		if len(req.Tools) > 0 {
			opts = append(opts, einomodel.WithTools(req.Tools))
		}
		chatReq := &provider.ChatRequest{
			Messages: messages,
			Options:  opts,
		}

		resp, err := o.provider.Chat(ctx, chatReq)
		if err != nil {
			return nil, fmt.Errorf("agent: generate: %w", err)
		}
		usage := provider.UsageFromMessage(resp.Message)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens

		// Check if model wants to use tools
		hasToolUse := resp.Message != nil && len(resp.Message.ToolCalls) > 0
		if !hasToolUse || !req.AutoToolCall {
			// Final response
			return &Response{
				SessionID: req.SessionID,
				Messages:  messages,
				Usage:     totalUsage,
			}, nil
		}

		// TODO: execute tool calls via MCP and append results
	}

	return nil, fmt.Errorf("agent: exceeded max iterations (%d)", cfg.MaxIterations)
}
