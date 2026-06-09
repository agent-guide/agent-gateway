package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStdioTransportPermissionHandlerPreservesSelectedOptionID(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := append(os.Environ(), "AGW_ACP_STDIO_HELPER=permission")
	tr, err := Open(ctx, ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioTransportHelper", "--"},
		Env:     env,
	}, Handlers{
		Permission: func(context.Context, json.RawMessage) PermissionResponse {
			return PermissionResponse{Outcome: PermissionOutcomeSelected, SelectedOptionID: "allow_once"}
		},
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer tr.Close()

	// The helper exits non-zero unless the permission response uses the nested
	// ACP RequestPermissionOutcome shape, so a successful Request proves the
	// wire format, and the echoed fields prove the option id round-trips.
	result, err := tr.Request(ctx, "ping", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	var payload struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.Outcome != PermissionOutcomeSelected {
		t.Fatalf("permission outcome = %q, want %q", payload.Outcome, PermissionOutcomeSelected)
	}
	if payload.OptionID != "allow_once" {
		t.Fatalf("selected option id = %q, want allow_once", payload.OptionID)
	}
}

func TestStdioTransportDeniedPermissionIsCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := append(os.Environ(), "AGW_ACP_STDIO_HELPER=permission")
	tr, err := Open(ctx, ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioTransportHelper", "--"},
		Env:     env,
	}, Handlers{
		// Anything that is not an explicit "selected" with an option id must
		// fail closed to "cancelled" on the wire.
		Permission: func(context.Context, json.RawMessage) PermissionResponse {
			return PermissionResponse{Outcome: "declined", SelectedOptionID: "allow_once"}
		},
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer tr.Close()

	result, err := tr.Request(ctx, "ping", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	var payload struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.Outcome != PermissionOutcomeCancelled {
		t.Fatalf("permission outcome = %q, want %q", payload.Outcome, PermissionOutcomeCancelled)
	}
	if payload.OptionID != "" {
		t.Fatalf("cancelled outcome carried option id %q, want empty", payload.OptionID)
	}
}

func TestStdioTransportRepliesMethodNotFoundForUnhandledServerRequest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := append(os.Environ(), "AGW_ACP_STDIO_HELPER=unhandled")
	tr, err := Open(ctx, ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioTransportHelper", "--"},
		Env:     env,
	}, Handlers{})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer tr.Close()

	// The helper issues a server-initiated fs/read_text_file request and only
	// answers the ping once it has received a JSON-RPC error response. A timeout
	// here would mean the transport dropped the request and deadlocked the agent.
	result, err := tr.Request(ctx, "ping", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	var payload struct {
		ErrorCode int `json:"errorCode"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.ErrorCode != -32601 {
		t.Fatalf("error code = %d, want -32601", payload.ErrorCode)
	}
}

func TestStdioTransportPreflightRejectsMissingCommand(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Open(ctx, ProcessConfig{Command: "agw-nonexistent-acp-binary"}, Handlers{})
	if err == nil {
		t.Fatal("Open returned nil error for a missing command")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want it to mention the command was not found", err)
	}
}

func TestStdioTransportPermissionTimeoutFailsClosed(t *testing.T) {
	prev := permissionTimeout
	permissionTimeout = 50 * time.Millisecond
	defer func() { permissionTimeout = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	release := make(chan struct{})
	defer close(release)

	env := append(os.Environ(), "AGW_ACP_STDIO_HELPER=permission")
	tr, err := Open(ctx, ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioTransportHelper", "--"},
		Env:     env,
	}, Handlers{
		// Block past the timeout, ignoring ctx, to prove the transport itself
		// fails closed rather than waiting on a wedged handler.
		Permission: func(context.Context, json.RawMessage) PermissionResponse {
			<-release
			return PermissionResponse{Outcome: PermissionOutcomeSelected, SelectedOptionID: "allow_once"}
		},
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer tr.Close()

	result, err := tr.Request(ctx, "ping", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	var payload struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.Outcome != PermissionOutcomeCancelled {
		t.Fatalf("permission outcome = %q, want %q on timeout", payload.Outcome, PermissionOutcomeCancelled)
	}
}

func TestStdioTransportSurfacesStderrOnExit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := append(os.Environ(), "AGW_ACP_STDIO_HELPER=stderr")
	tr, err := Open(ctx, ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioTransportHelper", "--"},
		Env:     env,
	}, Handlers{})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer tr.Close()

	_, err = tr.Request(ctx, "ping", map[string]any{"ok": true})
	if err == nil {
		t.Fatal("Request returned nil error after the process exited")
	}
	if !strings.Contains(err.Error(), "agent-boom") {
		t.Fatalf("error = %q, want it to include captured stderr %q", err, "agent-boom")
	}
}

func TestStdioTransportAcceptsLargeJSONLFrame(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := append(os.Environ(), "AGW_ACP_STDIO_HELPER=large")
	tr, err := Open(ctx, ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioTransportHelper", "--"},
		Env:     env,
	}, Handlers{})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer tr.Close()

	result, err := tr.Request(ctx, "ping", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Request returned error for a large JSONL frame: %v", err)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(payload.Text) < 128*1024 {
		t.Fatalf("large frame text length = %d, want at least 128KiB", len(payload.Text))
	}
}

func TestStdioTransportHelper(t *testing.T) {
	mode := os.Getenv("AGW_ACP_STDIO_HELPER")
	if mode == "" {
		return
	}
	if mode == "stderr" {
		_, _ = fmt.Fprintln(os.Stderr, "agent-boom")
		os.Exit(1)
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	if !scanner.Scan() {
		os.Exit(2)
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		os.Exit(2)
	}
	if strings.TrimSpace(req.Method) != "ping" {
		os.Exit(2)
	}

	switch mode {
	case "permission":
		runPermissionHelper(scanner, writer, req.ID)
	case "unhandled":
		runUnhandledHelper(scanner, writer, req.ID)
	case "large":
		writeJSON(writer, map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
			"result":  map[string]any{"text": strings.Repeat("x", 128*1024)},
		})
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func runPermissionHelper(scanner *bufio.Scanner, writer *bufio.Writer, pingID json.RawMessage) {
	writeJSON(writer, map[string]any{
		"jsonrpc": "2.0",
		"id":      "perm-1",
		"method":  "session/request_permission",
		"params": map[string]any{
			"options": []map[string]any{
				{"id": "reject_once", "kind": "reject"},
				{"id": "allow_once", "kind": "allow"},
			},
		},
	})
	if !scanner.Scan() {
		os.Exit(2)
	}
	// Decode strictly against the nested ACP RequestPermissionResponse shape.
	// A flat {"outcome":"selected",...} response fails to unmarshal here (a
	// string cannot decode into the outcome object), so the helper exits and the
	// test sees a transport error rather than a false pass.
	var resp struct {
		Result struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		} `json:"result"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		os.Exit(3)
	}
	if strings.TrimSpace(resp.Result.Outcome.Outcome) == "" {
		os.Exit(4)
	}
	writeJSON(writer, map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(pingID),
		"result": map[string]any{
			"outcome":  resp.Result.Outcome.Outcome,
			"optionId": resp.Result.Outcome.OptionID,
		},
	})
	os.Exit(0)
}

func runUnhandledHelper(scanner *bufio.Scanner, writer *bufio.Writer, pingID json.RawMessage) {
	writeJSON(writer, map[string]any{
		"jsonrpc": "2.0",
		"id":      "fs-1",
		"method":  "fs/read_text_file",
		"params":  map[string]any{"path": "/etc/hosts"},
	})
	if !scanner.Scan() {
		os.Exit(2)
	}
	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		os.Exit(3)
	}
	code := 0
	if resp.Error != nil {
		code = resp.Error.Code
	}
	writeJSON(writer, map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(pingID),
		"result":  map[string]any{"errorCode": code},
	})
	os.Exit(0)
}

func writeJSON(w *bufio.Writer, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, _ = w.Write(append(data, '\n'))
	_ = w.Flush()
}
