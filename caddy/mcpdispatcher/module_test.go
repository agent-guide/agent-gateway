package mcpdispatcher

import (
	"context"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	mcpruntime "github.com/agent-guide/agent-gateway/pkg/mcp/runtime"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

func TestIsNotification(t *testing.T) {
	t.Parallel()

	if !isNotification(transport.Message{Method: "notifications/progress"}) {
		t.Fatal("expected notifications/progress without id to be treated as notification")
	}
	if isNotification(transport.Message{ID: 1, Method: "notifications/progress"}) {
		t.Fatal("expected notification with id to be treated as request")
	}
	if isNotification(transport.Message{Method: "tools/list"}) {
		t.Fatal("expected request method to not be treated as notification")
	}
}

func TestDecodeCompletionParamsPromptRef(t *testing.T) {
	t.Parallel()

	ref, argument, args, err := decodeCompletionParams(map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "summarize",
		},
		"argument": map[string]any{
			"name":  "language",
			"value": "zh",
		},
		"arguments": map[string]any{
			"tone": "formal",
		},
	})
	if err != nil {
		t.Fatalf("decodeCompletionParams returned error: %v", err)
	}
	if ref != (basemcp.CompletionReference{Type: "ref/prompt", Name: "summarize"}) {
		t.Fatalf("unexpected ref: %#v", ref)
	}
	if argument != (basemcp.CompletionArgument{Name: "language", Value: "zh"}) {
		t.Fatalf("unexpected argument: %#v", argument)
	}
	if args["tone"] != "formal" {
		t.Fatalf("unexpected arguments: %#v", args)
	}
}

func TestDecodeCompletionParamsRejectsInvalidArgumentsMap(t *testing.T) {
	t.Parallel()

	_, _, _, err := decodeCompletionParams(map[string]any{
		"ref": map[string]any{
			"type": "ref/resource",
			"uri":  "file:///tmp/{name}",
		},
		"argument": map[string]any{
			"name": "name",
		},
		"arguments": map[string]any{
			"limit": 10,
		},
	})
	if err == nil {
		t.Fatal("expected error for non-string completion arguments")
	}
}

func TestCancelRequestCancelsInFlightRequest(t *testing.T) {
	t.Parallel()

	dispatcher := &Dispatcher{runtimeRegistry: mcpruntime.NewRegistry()}
	route := mcproute.MCPRoute{ID: "route-1", ServiceID: "svc-1"}
	msg := transport.Message{
		JSONRPC: "2.0",
		ID:      "req-1",
		Method:  "tools/call",
		Params: map[string]any{
			"_meta": map[string]any{"progressToken": "p1"},
		},
	}
	ctx, finish := dispatcher.beginRequest(context.Background(), route, msg)
	defer finish()

	cancelled, err := dispatcher.cancelRequest(route, "req-1", "user requested")
	if err != nil {
		t.Fatalf("cancelRequest returned error: %v", err)
	}
	if !cancelled {
		t.Fatal("expected in-flight request to be cancelled")
	}
	if reason := dispatcher.cancelReason(route, "req-1"); reason != "user requested" {
		t.Fatalf("unexpected cancel reason: %q", reason)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("expected context to be cancelled, got %v", ctx.Err())
	}
}

func TestCancelRequestRejectsInitialize(t *testing.T) {
	t.Parallel()

	dispatcher := &Dispatcher{runtimeRegistry: mcpruntime.NewRegistry()}
	route := mcproute.MCPRoute{ID: "route-1", ServiceID: "svc-1"}
	_, finish := dispatcher.beginRequest(context.Background(), route, transport.Message{
		JSONRPC: "2.0",
		ID:      "init-1",
		Method:  "initialize",
	})
	defer finish()

	cancelled, err := dispatcher.cancelRequest(route, "init-1", "bad client")
	if err == nil {
		t.Fatal("expected initialize cancellation to be rejected")
	}
	if cancelled {
		t.Fatal("expected initialize request to remain uncancelled")
	}
}

func TestHandleProgressNotificationStoresState(t *testing.T) {
	t.Parallel()

	dispatcher := &Dispatcher{runtimeRegistry: mcpruntime.NewRegistry()}
	route := mcproute.MCPRoute{ID: "route-1", ServiceID: "svc-1"}
	_, finish := dispatcher.beginRequest(context.Background(), route, transport.Message{
		JSONRPC: "2.0",
		ID:      "req-2",
		Method:  "tools/call",
		Params: map[string]any{
			"_meta": map[string]any{"progressToken": "progress-1"},
		},
	})
	defer finish()

	if err := dispatcher.handleProgressNotification(route, map[string]any{
		"progressToken": "progress-1",
		"progress":      float64(2),
		"total":         float64(5),
		"message":       "working",
	}); err != nil {
		t.Fatalf("handleProgressNotification returned error: %v", err)
	}

	progresses := dispatcher.runtimeRegistry.ListProgress()
	if len(progresses) != 1 {
		t.Fatalf("expected one progress notification, got %#v", progresses)
	}
	progress := progresses[0]
	if progress.ProgressTokenKey != mcpruntime.RouteProgressTokenKey("route-1", "progress-1") {
		t.Fatal("expected progress notification to be recorded")
	}
	if progress.RequestKey != mcpruntime.RouteRequestKey("route-1", "req-2") {
		t.Fatalf("unexpected request key: %q", progress.RequestKey)
	}
	if progress.Message != "working" {
		t.Fatalf("unexpected progress message: %q", progress.Message)
	}
	if progress.Total == nil || *progress.Total != 5 {
		t.Fatalf("unexpected total: %#v", progress.Total)
	}
}
