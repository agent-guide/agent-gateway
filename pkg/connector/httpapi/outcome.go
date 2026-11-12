package httpapi

import (
	"context"
	"errors"
	"fmt"

	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

// CallTool executes one admitted HTTP tool call and returns the common outcome.
// Summary capture, safe failures and uncertain-write classification belong here,
// independently of the control-plane Manager or builtin/ingress adapters.
func (e *Engine) CallTool(ctx context.Context, id string, catalog *Catalog, call cr.ToolCall) (out cr.CallOutcome, err error) {
	cctx, summary := capture(ctx)
	defer func() {
		if summary.Method != "" {
			out.HTTP = summary
		}
		if recover() != nil {
			out.Result = nil
			err = &cr.CallError{Category: "internal_error", Message: "tool execution failed"}
		}
	}()
	out.Result, err = e.call(cctx, id, catalog, call.Name, call.Arguments)
	if err == nil {
		return out, nil
	}
	var safe *Error
	if !errors.As(err, &safe) {
		return out, &cr.CallError{Category: "internal_error", Message: "tool execution failed"}
	}
	category := safe.Category
	if category == "input_validation" {
		return out, &cr.CallError{Category: "invalid_arguments", Message: "invalid tool arguments"}
	}
	if category == "unknown_tool" {
		return out, &cr.CallError{Category: "unknown_tool", Message: "unknown tool"}
	}
	uncertain := summary.Method == "POST" && (summary.RequestBodyBytes > 0 || summary.StatusCode != nil)
	message := fmt.Sprintf("HTTP tool failed (%s)", category)
	if uncertain {
		message += "; write outcome is unconfirmed; do not automatically retry"
	}
	out.Failure = &cr.ExecutionFailure{Category: category, Message: message, OutcomeUncertain: uncertain}
	out.Result = &basemcp.ToolResult{IsError: true, Content: []any{map[string]any{"type": "text", "text": message}}}
	return out, nil
}
