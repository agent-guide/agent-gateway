package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

type catalogTransport struct {
	call func(*transport.Message) (*transport.Message, error)
}

func (c catalogTransport) Call(_ context.Context, msg *transport.Message) (*transport.Message, error) {
	return c.call(msg)
}
func (catalogTransport) Close() error { return nil }

func TestCompleteToolCatalogPaginationBounds(t *testing.T) {
	for _, mode := range []string{"complete", "cycle", "page_limit", "tool_limit", "partial_failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			target := NewTarget("tools", Config{Transport: basemcp.TransportStdio, Command: "unused"})
			engine := NewEngine(nil)
			defer func() { <-engine.BeginClose() }()
			calls := 0
			engine.upstreamSessions["tools"] = &Session{configSignature: connectionSignature(target.config), lastUsedAt: time.Now(), transport: catalogTransport{call: func(msg *transport.Message) (*transport.Message, error) {
				calls++
				cursor, _ := msg.Params.(map[string]any)["cursor"].(string)
				if mode == "complete" && ((calls == 1 && cursor != "") || (calls == 2 && cursor != "page-2")) {
					t.Fatalf("wrong cursor: %q", cursor)
				}
				if mode == "partial_failure" && calls == 2 {
					return nil, errors.New("page unavailable")
				}
				next := "page-2"
				tools := []basemcp.Tool{{Name: fmt.Sprintf("tool-%d", calls)}}
				switch mode {
				case "complete":
					if calls == 2 {
						next = ""
					}
				case "page_limit":
					next = fmt.Sprintf("cursor-%d", calls)
				case "tool_limit":
					tools = make([]basemcp.Tool, maxToolCatalogTools+1)
				case "cancel":
					cancel()
				}
				return &transport.Message{Result: map[string]any{"tools": tools, "nextCursor": next}}, nil
			}}}
			tools, err := engine.ListTools(ctx, target)
			if mode == "complete" {
				if err != nil || len(tools) != 2 || tools[1].Name != "tool-2" {
					t.Fatalf("incomplete catalog: %#v %v", tools, err)
				}
			} else if err == nil || tools != nil {
				t.Fatalf("published partial/invalid catalog: %d tools, %v", len(tools), err)
			}
			if calls > maxToolCatalogPages {
				t.Fatalf("unbounded discovery: %d", calls)
			}
		})
	}
}
