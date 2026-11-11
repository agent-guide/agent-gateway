// Package runtime defines connector execution contracts independent of Agents,
// management transports and persistence.
package runtime

import (
	"context"
	"fmt"
	"github.com/agent-guide/agent-gateway/internal/strictjson"
	"github.com/agent-guide/agent-gateway/pkg/mcp"
	"net"
	"strings"
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
	HTTP    *HTTPExecution
	Failure *ExecutionFailure
}
type ExecutionFailure struct {
	Category, Message string
	OutcomeUncertain  bool
}
type CallError struct{ Category, Message string }

func (e *CallError) Error() string { return e.Category + ": " + e.Message }

type HTTPExecution struct {
	Method               string `json:"method"`
	HostPort             string `json:"host_port"`
	PathTemplate         string `json:"path_template"`
	StatusCode           *int   `json:"status_code"`
	DurationMS           int64  `json:"duration_ms"`
	RequestBodyBytes     int64  `json:"request_body_bytes"`
	ResponseBodyBytes    int64  `json:"response_body_bytes"`
	NetworkErrorCategory string `json:"network_error_category,omitempty"`
}

func (h HTTPExecution) Validate() error {
	if h.Method != "GET" && h.Method != "POST" {
		return fmt.Errorf("invalid HTTP method")
	}
	if _, _, err := net.SplitHostPort(h.HostPort); err != nil || strings.ContainsAny(h.HostPort, "/@?#\r\n") {
		return fmt.Errorf("invalid HTTP host")
	}
	if !strings.HasPrefix(h.PathTemplate, "/") || strings.ContainsAny(h.PathTemplate, "?#\r\n") {
		return fmt.Errorf("invalid HTTP path template")
	}
	if h.StatusCode != nil && (*h.StatusCode < 100 || *h.StatusCode > 599) {
		return fmt.Errorf("invalid HTTP status")
	}
	if h.DurationMS < 0 || h.RequestBodyBytes < 0 || h.ResponseBodyBytes < 0 {
		return fmt.Errorf("negative HTTP measure")
	}
	switch h.NetworkErrorCategory {
	case "", "network_error", "timeout", "cancelled":
	default:
		return fmt.Errorf("invalid network error")
	}
	return nil
}
func (h *HTTPExecution) UnmarshalJSON(data []byte) error {
	v, err := strictjson.Parse(data, 4<<20, 8)
	if err != nil {
		return err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid HTTP summary")
	}
	for _, key := range []string{"method", "host_port", "path_template", "status_code", "duration_ms", "request_body_bytes", "response_body_bytes"} {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("missing HTTP summary field")
		}
	}
	type plain HTTPExecution
	var p plain
	if err := strictjson.Decode(data, &p, 4<<20, 8); err != nil {
		return err
	}
	*h = HTTPExecution(p)
	return h.Validate()
}
