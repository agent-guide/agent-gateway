package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/agent-guide/agent-gateway/pkg/connector/httpapi"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/google/uuid"
)

type lease struct {
	manager *Manager
	entry   *entry
	closed  atomic.Bool
	mu      sync.Mutex
	catalog *cr.ToolCatalog
}

func (m *Manager) Acquire(ctx context.Context, id string) (cr.Lease, error) {
	v, e := m.lookup(ctx, id)
	if e != nil {
		return nil, &cr.CallError{Category: "connector_unavailable", Message: "connector unavailable"}
	}
	if v.config.Disabled {
		return nil, &cr.CallError{Category: "connector_unavailable", Message: "connector disabled"}
	}
	l := &lease{manager: m, entry: v}
	if err := l.check(); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *lease) Descriptor() cr.Descriptor {
	return cr.Descriptor{ConnectorID: l.entry.config.ID, Type: l.entry.config.Type, Generation: l.entry.token, Incarnation: l.entry.incarnation}
}
func (l *lease) Close() error { l.closed.Store(true); return nil }
func (l *lease) check() error {
	if err := l.manager.CheckExecution(l.entry.config.ID); err != nil {
		return &cr.CallError{Category: "mutation_pending", Message: "connector requires recovery"}
	}
	if l.closed.Load() {
		return &cr.CallError{Category: "generation_unavailable", Message: "lease closed"}
	}
	l.manager.snapshotMu.RLock()
	v := l.manager.entries[l.entry.config.ID]
	valid := !l.manager.closed && v != nil && !v.config.Disabled && v.incarnation == l.entry.incarnation && v.token == l.entry.token
	l.manager.snapshotMu.RUnlock()
	if !valid {
		return &cr.CallError{Category: "generation_unavailable", Message: "connector revoked"}
	}
	return nil
}
func cloneTools(tools []basemcp.Tool) []basemcp.Tool {
	b, _ := json.Marshal(tools)
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var out []basemcp.Tool
	_ = d.Decode(&out)
	return out
}
func (l *lease) ListTools(ctx context.Context) (cr.ToolCatalog, error) {
	if e := l.check(); e != nil {
		return cr.ToolCatalog{}, e
	}
	_, ctx, done, err := l.manager.admit(ctx, l.entry.config.ID, l.entry.token)
	if err != nil {
		return cr.ToolCatalog{}, err
	}
	defer done()
	return l.listTools(ctx)
}

// listTools runs inside an existing admission; display writes must not interrupt it.
func (l *lease) listTools(ctx context.Context) (cr.ToolCatalog, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.catalog == nil {
		var tools []basemcp.Tool
		var err error
		token := l.entry.token
		if l.entry.catalog != nil {
			tools = l.entry.catalog.Tools()
		} else {
			var result listToolsResult
			err = l.manager.callMethodWithRetryConfig(ctx, l.entry.config, "tools/list", map[string]any{}, &result, nil)
			tools = result.Tools
			token = uuid.NewString()
		}
		if err != nil {
			return cr.ToolCatalog{}, err
		}
		l.catalog = &cr.ToolCatalog{Generation: token, Tools: tools}
	}
	return cr.ToolCatalog{Generation: l.catalog.Generation, Tools: cloneTools(l.catalog.Tools)}, nil
}
func (l *lease) CallTool(ctx context.Context, call cr.ToolCall) (out cr.CallOutcome, err error) {
	if e := l.check(); e != nil {
		return out, e
	}
	_, ctx, release, e := l.manager.admit(ctx, l.entry.config.ID, l.entry.token)
	if e != nil {
		return out, e
	}
	defer release()
	catalog, e := l.listTools(ctx)
	if e != nil {
		return out, e
	}
	found := false
	for _, t := range catalog.Tools {
		found = found || t.Name == call.Name
	}
	if !found {
		return out, &cr.CallError{Category: "unknown_tool", Message: "unknown tool"}
	}
	if l.entry.catalog == nil {

		var progress chan UpstreamProgress
		var done chan struct{}
		if call.Progress != nil {
			progress = make(chan UpstreamProgress, 64)
			done = make(chan struct{})
			go func() {
				defer func() { _ = recover(); close(done) }()
				for p := range progress {
					call.Progress(cr.Progress{ProgressToken: p.ProgressToken, Progress: p.Progress, Total: p.Total, Message: p.Message})
				}
			}()
		}
		var result basemcp.ToolResult
		err = l.manager.callMethodWithRetryConfig(ctx, l.entry.config, "tools/call", map[string]any{"name": call.Name, "arguments": call.Arguments}, &result, progress)
		if err == nil {
			out.Result = &result
		}
		if progress != nil {
			close(progress)
			<-done
		}
		return out, err
	}
	cctx, summary := httpapi.Capture(ctx)
	defer func() {
		if summary.Method != "" {
			out.HTTP = summary
		}
		if recover() != nil {
			out.Result = nil
			err = &cr.CallError{Category: "internal_error", Message: "tool execution failed"}
		}
	}()
	out.Result, err = l.manager.engine.Call(cctx, l.entry.config.ID, l.entry.catalog, call.Name, call.Arguments)
	if err == nil {
		return out, nil
	}
	var safe *httpapi.Error
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

var _ cr.Resolver = (*Manager)(nil)
