package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/agent-guide/agent-gateway/pkg/connector/execution"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/google/uuid"
)

type lease struct {
	manager *Manager
	entry   *entry
	closed  atomic.Bool
	mu      sync.Mutex
	catalog *execution.ToolCatalog
}

func (m *Manager) Acquire(ctx context.Context, id string) (execution.Lease, error) {
	v, e := m.lookup(ctx, id)
	if e != nil {
		boundary := &execution.CallError{Category: "connector_unavailable", Message: "connector unavailable"}
		if errors.Is(e, ErrConnectorNotConfigured) {
			return nil, errors.Join(ErrConnectorNotConfigured, boundary)
		}
		return nil, boundary
	}
	if v.config.Disabled {
		return nil, &execution.CallError{Category: "connector_unavailable", Message: "connector disabled"}
	}
	l := &lease{manager: m, entry: v}
	if err := l.check(); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *lease) Descriptor() execution.Descriptor {
	return execution.Descriptor{ConnectorID: l.entry.config.ID, Type: l.entry.config.Type, Generation: l.entry.token, Incarnation: l.entry.incarnation}
}
func (l *lease) Close() error { l.closed.Store(true); return nil }
func (l *lease) check() error {
	if err := l.manager.CheckExecution(l.entry.config.ID); err != nil {
		return &execution.CallError{Category: "mutation_pending", Message: "connector requires recovery"}
	}
	if l.closed.Load() {
		return &execution.CallError{Category: "generation_unavailable", Message: "lease closed"}
	}
	l.manager.snapshotMu.RLock()
	v := l.manager.entries[l.entry.config.ID]
	valid := !l.manager.closed && v != nil && !v.config.Disabled && v.incarnation == l.entry.incarnation && v.token == l.entry.token
	l.manager.snapshotMu.RUnlock()
	if !valid {
		return &execution.CallError{Category: "generation_unavailable", Message: "connector revoked"}
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
func (l *lease) ListTools(ctx context.Context) (execution.ToolCatalog, error) {
	if e := l.check(); e != nil {
		return execution.ToolCatalog{}, e
	}
	_, ctx, done, err := l.manager.admit(ctx, l.entry.config.ID, l.entry.token)
	if err != nil {
		return execution.ToolCatalog{}, err
	}
	defer done()
	return l.listTools(ctx)
}

// listTools runs inside an existing admission; display writes must not interrupt it.
func (l *lease) listTools(ctx context.Context) (execution.ToolCatalog, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.catalog == nil {
		var tools []basemcp.Tool
		var err error
		token := l.entry.token
		if l.entry.catalog != nil {
			tools = l.entry.catalog.Tools()
		} else {
			tools, err = l.manager.mcpEngine.ListTools(ctx, l.entry.mcpTarget)
			token = uuid.NewString()
		}
		if err != nil {
			return execution.ToolCatalog{}, err
		}
		l.catalog = &execution.ToolCatalog{Generation: token, Tools: tools}
	}
	return execution.ToolCatalog{Generation: l.catalog.Generation, Tools: cloneTools(l.catalog.Tools)}, nil
}
func (l *lease) CallTool(ctx context.Context, call execution.ToolCall) (out execution.CallOutcome, err error) {
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
		return out, &execution.CallError{Category: "unknown_tool", Message: "unknown tool"}
	}
	if l.entry.catalog == nil {
		out.Result, err = l.manager.mcpEngine.CallTool(ctx, l.entry.mcpTarget, call)
		return out, err
	}

	return l.manager.httpEngine.CallTool(ctx, l.entry.config.ID, l.entry.catalog, call)
}

var _ execution.Resolver = (*Manager)(nil)
