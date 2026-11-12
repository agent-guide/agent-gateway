// Package einotool adapts gateway-managed Connectors to eino tools without an
// HTTP loopback. Connector Manager owns configuration and admission; the MCP
// and HTTP engines execute calls through retained runtime leases.
//
// The adapter is a pure bridge: it does not record MCP usage events. In-process
// callers own their observability span; the builtin agent runtime (see
// docs/design/agents-control-plane.md §5.7.6) is the layer that attributes
// tool calls.
package einotool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// Tools lists the Connector's tools and wraps each as an eino
// InvokableTool. When names is non-empty it acts as an allowlist and every
// listed name must exist — a missing tool is an error, not a silent skip, so
// an agent definition referencing a renamed tool fails loudly at
// materialization time.
func Tools(ctx context.Context, resolver cr.Resolver, connectorID string, names ...string) ([]tool.BaseTool, error) {
	if resolver == nil {
		return nil, fmt.Errorf("einotool: tool caller is nil")
	}
	connectorID = strings.TrimSpace(connectorID)
	if connectorID == "" {
		return nil, fmt.Errorf("einotool: connector id is empty")
	}
	pinned, err := resolver.Acquire(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = pinned.Close()
		}
	}()
	catalog, err := pinned.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("einotool: list tools of connector %q: %w", connectorID, err)
	}
	listed := catalog.Tools

	byName := make(map[string]basemcp.Tool, len(listed))
	for _, t := range listed {
		byName[t.Name] = t
	}
	selected := make([]basemcp.Tool, 0, len(listed))
	if len(names) == 0 {
		selected = listed
	} else {
		for _, name := range names {
			t, ok := byName[strings.TrimSpace(name)]
			if !ok {
				return nil, fmt.Errorf("einotool: tool %q not found in connector %q", name, connectorID)
			}
			selected = append(selected, t)
		}
	}
	out := make([]tool.BaseTool, 0, len(selected))
	for _, t := range selected {
		info, err := toolInfo(t)
		if err != nil {
			return nil, fmt.Errorf("einotool: tool %q of connector %q: %w", t.Name, connectorID, err)
		}
		out = append(out, &invokableTool{lease: pinned, name: t.Name, info: info})
	}
	complete = true
	return out, nil
}

type invokableTool struct {
	lease cr.Lease
	name  string
	info  *schema.ToolInfo
}

var _ tool.InvokableTool = (*invokableTool)(nil)

func (t *invokableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *invokableTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	text, _, err := t.RunWithOutcome(ctx, arguments, opts...)
	return text, err
}
func (t *invokableTool) ConnectorType() string {
	return t.lease.Descriptor().Type
}
func (t *invokableTool) RunWithOutcome(ctx context.Context, arguments string, _ ...tool.Option) (string, cr.CallOutcome, error) {
	var args map[string]any
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	v, err := strictjson.Parse([]byte(arguments), 1<<20, 64)
	if err == nil {
		var ok bool
		args, ok = v.(map[string]any)
		if !ok {
			err = fmt.Errorf("arguments must be object")
		}
	}
	if err != nil {
		return "", cr.CallOutcome{}, &cr.CallError{Category: "invalid_arguments", Message: "invalid tool arguments"}
	}
	outcome, err := t.lease.CallTool(ctx, cr.ToolCall{Name: t.name, Arguments: args})
	if err != nil {
		return "", outcome, err
	}
	rendered := renderToolResult(outcome.Result)
	if outcome.Result != nil && outcome.Result.IsError {
		return "", outcome, fmt.Errorf("tool %q: %s", t.name, rendered)
	}
	return rendered, outcome, nil
}

func toolInfo(t basemcp.Tool) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{
		Name: t.Name,
		Desc: t.Description,
	}
	if len(t.InputSchema) > 0 {
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("encode input schema: %w", err)
		}
		js := &jsonschema.Schema{}
		if err := json.Unmarshal(raw, js); err != nil {
			return nil, fmt.Errorf("decode input schema: %w", err)
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(js)
	}
	return info, nil
}

// renderToolResult flattens an MCP tool result into the string an eino tool
// returns. Text-only content collapses to plain text; anything else (images,
// structured content, mixed blocks) is returned as compact JSON.
func renderToolResult(result *basemcp.ToolResult) string {
	if result == nil {
		return ""
	}
	if result.StructuredContent != nil {
		if raw, err := json.Marshal(result.StructuredContent); err == nil {
			return string(raw)
		}
	}
	if texts, ok := textBlocks(result.Content); ok {
		return strings.Join(texts, "\n")
	}
	if result.Content == nil {
		return ""
	}
	raw, err := json.Marshal(result.Content)
	if err != nil {
		return fmt.Sprintf("%v", result.Content)
	}
	return string(raw)
}

func textBlocks(content any) ([]string, bool) {
	blocks, ok := content.([]any)
	if !ok {
		return nil, false
	}
	texts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			return nil, false
		}
		if blockType, _ := block["type"].(string); blockType != "text" {
			return nil, false
		}
		text, ok := block["text"].(string)
		if !ok {
			return nil, false
		}
		texts = append(texts, text)
	}
	return texts, true
}
