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

type Message struct {
	Method string
	Params json.RawMessage
}

type Transport struct {
	cmd *exec.Cmd
	in  io.WriteCloser

	writeMu sync.Mutex
	seq     atomic.Uint64

	pendingMu sync.Mutex
	pending   map[string]chan response

	updatesMu sync.Mutex
	updates   map[chan Message]struct{}

	done chan struct{}
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

func Open(ctx context.Context, cfg ProcessConfig) (*Transport, error) {
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
	t := &Transport{
		cmd:     cmd,
		in:      stdin,
		pending: map[string]chan response{},
		updates: map[chan Message]struct{}{},
		done:    make(chan struct{}),
	}
	go t.readLoop(stdout, cfg.PermissionMode)
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

func (t *Transport) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
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

func (t *Transport) Notify(method string, params any) error {
	if t == nil {
		return fmt.Errorf("acp transport is nil")
	}
	return t.write(wireMessage{JSONRPC: "2.0", Method: method, Params: mustMarshal(params)})
}

func (t *Transport) Updates(buffer int) (<-chan Message, func()) {
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

func (t *Transport) Close() error {
	if t == nil {
		return nil
	}
	_ = t.in.Close()
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return nil
}

func (t *Transport) readLoop(r io.Reader, permissionMode PermissionMode) {
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
		if len(msg.ID) > 0 && msg.Method == "" {
			t.handleResponse(msg)
			continue
		}
		if strings.TrimSpace(msg.Method) == "session/request_permission" && len(msg.ID) > 0 {
			_ = t.write(permissionResponse(msg.ID, permissionMode))
			continue
		}
		if strings.TrimSpace(msg.Method) != "" {
			t.publish(Message{Method: msg.Method, Params: msg.Params})
		}
	}
}

func (t *Transport) handleResponse(msg wireMessage) {
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

func (t *Transport) publish(msg Message) {
	t.updatesMu.Lock()
	defer t.updatesMu.Unlock()
	for ch := range t.updates {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (t *Transport) write(msg wireMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.in.Write(append(data, '\n'))
	return err
}

func (t *Transport) deletePending(id string) {
	t.pendingMu.Lock()
	delete(t.pending, id)
	t.pendingMu.Unlock()
}

func (t *Transport) closePending(err error) {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	for id, ch := range t.pending {
		delete(t.pending, id)
		ch <- response{err: err}
	}
}

func permissionResponse(id json.RawMessage, mode PermissionMode) wireMessage {
	outcome := "denied"
	if mode == PermissionModeAutoApprove {
		outcome = "approved"
	}
	return wireMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  mustMarshal(map[string]any{"outcome": outcome}),
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
