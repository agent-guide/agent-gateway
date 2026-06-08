package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type PermissionMode string

const (
	PermissionModeDeny        PermissionMode = "deny"
	PermissionModeAutoApprove PermissionMode = "auto_approve"
)

type ProcessConfig struct {
	Command        string
	Args           []string
	Dir            string
	Env            []string
	PermissionMode PermissionMode
}

const (
	// PermissionOutcomeSelected and PermissionOutcomeCancelled are the only two
	// ACP RequestPermissionOutcome discriminators.
	PermissionOutcomeSelected  = "selected"
	PermissionOutcomeCancelled = "cancelled"
)

// PermissionResponse is the host's decision for a session/request_permission.
// Outcome is the ACP outcome discriminator ("selected" or "cancelled");
// SelectedOptionID is required when Outcome == "selected".
type PermissionResponse struct {
	Outcome          string
	SelectedOptionID string
}

// permissionResultWire is the on-wire ACP RequestPermissionResponse shape:
// {"outcome":{"outcome":"selected","optionId":"..."}} or {"outcome":{"outcome":"cancelled"}}.
type permissionResultWire struct {
	Outcome permissionOutcomeWire `json:"outcome"`
}

type permissionOutcomeWire struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

type Handlers struct {
	Permission func(ctx context.Context, params json.RawMessage) PermissionResponse
}

type Message struct {
	Method string
	Params json.RawMessage
}

type Transport interface {
	Request(ctx context.Context, method string, params any) (json.RawMessage, error)
	Notify(method string, params any) error
	Updates(buffer int) (<-chan Message, func())
	// Alive reports whether the underlying connection is still usable. A pooled
	// transport whose process has exited must not be reused.
	Alive() bool
	Close() error
}

type StdioTransport struct {
	cmd *exec.Cmd
	in  io.WriteCloser

	writeMu sync.Mutex
	seq     atomic.Uint64

	pendingMu sync.Mutex
	pending   map[string]chan response

	updatesMu sync.Mutex
	updates   map[chan Message]struct{}

	done chan struct{}

	handlers       Handlers
	permissionMode PermissionMode
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	result json.RawMessage
	err    error
}

func Open(ctx context.Context, cfg ProcessConfig, handlers Handlers) (Transport, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, fmt.Errorf("acp command is required")
	}
	cmd := exec.Command(command, cfg.Args...)
	cmd.Dir = strings.TrimSpace(cfg.Dir)
	if len(cfg.Env) > 0 {
		cmd.Env = cfg.Env
	} else {
		cmd.Env = os.Environ()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &StdioTransport{
		cmd:            cmd,
		in:             stdin,
		pending:        map[string]chan response{},
		updates:        map[chan Message]struct{}{},
		done:           make(chan struct{}),
		handlers:       handlers,
		permissionMode: cfg.PermissionMode,
	}
	go t.readLoop(stdout)
	go func() {
		_ = cmd.Wait()
		t.closePending(fmt.Errorf("acp process exited"))
		close(t.done)
	}()
	select {
	case <-ctx.Done():
		_ = t.Close()
		return nil, ctx.Err()
	default:
		return t, nil
	}
}

func (t *StdioTransport) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if t == nil {
		return nil, fmt.Errorf("acp transport is nil")
	}
	id := strconv.FormatUint(t.seq.Add(1), 10)
	ch := make(chan response, 1)
	t.pendingMu.Lock()
	t.pending[id] = ch
	t.pendingMu.Unlock()
	if err := t.write(wireMessage{JSONRPC: "2.0", ID: json.RawMessage(strconv.Quote(id)), Method: method, Params: mustMarshal(params)}); err != nil {
		t.deletePending(id)
		return nil, err
	}
	select {
	case res := <-ch:
		return res.result, res.err
	case <-ctx.Done():
		t.deletePending(id)
		return nil, ctx.Err()
	case <-t.done:
		return nil, fmt.Errorf("acp process exited")
	}
}

func (t *StdioTransport) Notify(method string, params any) error {
	if t == nil {
		return fmt.Errorf("acp transport is nil")
	}
	return t.write(wireMessage{JSONRPC: "2.0", Method: method, Params: mustMarshal(params)})
}

func (t *StdioTransport) Updates(buffer int) (<-chan Message, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan Message, buffer)
	t.updatesMu.Lock()
	t.updates[ch] = struct{}{}
	t.updatesMu.Unlock()
	return ch, func() {
		t.updatesMu.Lock()
		delete(t.updates, ch)
		close(ch)
		t.updatesMu.Unlock()
	}
}

func (t *StdioTransport) Alive() bool {
	if t == nil {
		return false
	}
	select {
	case <-t.done:
		return false
	default:
		return true
	}
}

func (t *StdioTransport) Close() error {
	if t == nil {
		return nil
	}
	_ = t.in.Close()
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return nil
}

func (t *StdioTransport) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg wireMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		method := strings.TrimSpace(msg.Method)
		if len(msg.ID) > 0 && method == "" {
			t.handleResponse(msg)
			continue
		}
		if len(msg.ID) > 0 {
			// Server-initiated request. It carries an id, so the agent blocks
			// until we answer. Resolve session/request_permission through the
			// permission handler; reply method-not-found to anything else rather
			// than dropping it (which would deadlock the agent).
			if method == "session/request_permission" {
				_ = t.write(t.permissionResponse(msg.ID, msg.Params))
				continue
			}
			_ = t.write(methodNotFound(msg.ID, method))
			continue
		}
		if method != "" {
			t.publish(Message{Method: method, Params: msg.Params})
		}
	}
}

func (t *StdioTransport) handleResponse(msg wireMessage) {
	id := strings.Trim(string(msg.ID), `"`)
	t.pendingMu.Lock()
	ch := t.pending[id]
	delete(t.pending, id)
	t.pendingMu.Unlock()
	if ch == nil {
		return
	}
	if msg.Error != nil {
		ch <- response{err: fmt.Errorf("acp error %d: %s", msg.Error.Code, msg.Error.Message)}
		return
	}
	ch <- response{result: msg.Result}
}

func (t *StdioTransport) publish(msg Message) {
	t.updatesMu.Lock()
	defer t.updatesMu.Unlock()
	for ch := range t.updates {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (t *StdioTransport) write(msg wireMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.in.Write(append(data, '\n'))
	return err
}

func (t *StdioTransport) deletePending(id string) {
	t.pendingMu.Lock()
	delete(t.pending, id)
	t.pendingMu.Unlock()
}

func (t *StdioTransport) closePending(err error) {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	for id, ch := range t.pending {
		delete(t.pending, id)
		ch <- response{err: err}
	}
}

func (t *StdioTransport) permissionResponse(id json.RawMessage, params json.RawMessage) wireMessage {
	res := PermissionResponse{Outcome: PermissionOutcomeCancelled}
	if t.handlers.Permission != nil {
		res = t.handlers.Permission(context.Background(), params)
	} else if t.permissionMode == PermissionModeAutoApprove {
		res = autoApprovePermission(params)
	}
	// Fail closed: only emit "selected" when the host picked a concrete option;
	// every other case (including an empty/invalid response) becomes "cancelled".
	wire := permissionResultWire{Outcome: permissionOutcomeWire{Outcome: PermissionOutcomeCancelled}}
	if strings.TrimSpace(res.Outcome) == PermissionOutcomeSelected {
		if optionID := strings.TrimSpace(res.SelectedOptionID); optionID != "" {
			wire.Outcome.Outcome = PermissionOutcomeSelected
			wire.Outcome.OptionID = optionID
		}
	}
	return wireMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  mustMarshal(wire),
	}
}

func autoApprovePermission(params json.RawMessage) PermissionResponse {
	if id := AllowOptionID(params); id != "" {
		return PermissionResponse{Outcome: PermissionOutcomeSelected, SelectedOptionID: id}
	}
	return PermissionResponse{Outcome: PermissionOutcomeCancelled}
}

// AllowOptionID returns the id of the permission option that grants access, or
// "" when none of the offered options is an allow/approve option. It never falls
// back to an arbitrary option, so callers fail closed when no allow option
// exists.
func AllowOptionID(params json.RawMessage) string {
	var payload struct {
		Options []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"options"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return ""
	}
	for _, option := range payload.Options {
		id := strings.TrimSpace(option.ID)
		if id == "" {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(option.Kind))
		name := strings.ToLower(strings.TrimSpace(option.Name))
		if strings.Contains(kind, "allow") || strings.Contains(name, "allow") || strings.Contains(kind, "approve") || strings.Contains(name, "approve") {
			return id
		}
	}
	return ""
}

func methodNotFound(id json.RawMessage, method string) wireMessage {
	return wireMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &wireError{
			Code:    -32601,
			Message: fmt.Sprintf("method not found: %s", method),
		},
	}
}

func mustMarshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}
