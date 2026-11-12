// Package runtime defines connector execution contracts independent of Agents,
// management transports and persistence.
package runtime

import (
	"context"

	"github.com/agent-guide/agent-gateway/pkg/mcp"
)

type Resolver interface {
	Acquire(context.Context, string) (Lease, error)
}
type Lease interface {
	Descriptor() Descriptor
	ListTools(context.Context) (ToolCatalog, error)
	CallTool(context.Context, ToolCall) (CallOutcome, error)
	Close() error
}
type Descriptor struct{ ConnectorID, Type, Generation, Incarnation string }
type ToolCatalog struct {
	Generation string
	Tools      []mcp.Tool
}
type ToolCall struct {
	Name      string
	Arguments map[string]any
	Progress  func(Progress)
}
type Progress struct {
	ProgressToken any
	Progress      float64
	Total         *float64
	Message       string
}
type CallOutcome struct {
	Result  *mcp.ToolResult
	Failure *ExecutionFailure
}
type ExecutionFailure struct {
	Category, Message string
	OutcomeUncertain  bool
}
type CallError struct{ Category, Message string }

func (e *CallError) Error() string { return e.Category + ": " + e.Message }
