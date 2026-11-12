package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// matchID compares two JSON-RPC ID values after normalising through JSON
// so that int(1) and float64(1) compare equal.
func matchID(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// StdioTransport implements MCP transport over stdio (local process).
type StdioTransport struct {
	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
	command   string
	args      []string
	env       []string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	out       chan *Message
}

// NewStdioTransport creates a new stdio transport.
func NewStdioTransport(command string, args, env []string) *StdioTransport {
	return &StdioTransport{
		command: command,
		args:    args,
		env:     env,
		out:     make(chan *Message, 64),
		done:    make(chan struct{}),
	}
}

func (t *StdioTransport) Connect(ctx context.Context) error {
	t.cmd = exec.CommandContext(ctx, t.command, t.args...)
	t.cmd.Env = t.env

	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdio: stdin pipe: %w", err)
	}

	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdio: stdout pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("stdio: start: %w", err)
	}

	go t.readLoop(stdout)
	return nil
}

func (t *StdioTransport) readLoop(r io.Reader) {
	defer close(t.out)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		select {
		case t.out <- &msg:
		case <-t.done:
			return
		}
	}
}

func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.cmd == nil {
			return
		}
		done := make(chan error, 1)
		go func() { done <- t.cmd.Wait() }()
		select {
		case t.closeErr = <-done:
		case <-time.After(time.Second):
			if t.cmd.Process != nil {
				_ = t.cmd.Process.Kill()
			}
			t.closeErr = <-done
		}
		var exited *exec.ExitError
		if errors.As(t.closeErr, &exited) || errors.Is(t.closeErr, context.Canceled) {
			t.closeErr = nil
		}
	})
	return t.closeErr
}

func (t *StdioTransport) Send(ctx context.Context, msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = t.stdin.Write(data)
	return err
}

func (t *StdioTransport) Receive() <-chan *Message {
	return t.out
}

// Call implements Caller. For notifications (msg.ID == nil) it sends and
// returns immediately. For requests it sends and waits for the reply whose
// ID matches the request, skipping any intervening notifications.
func (t *StdioTransport) Call(ctx context.Context, msg *Message) (*Message, error) {
	return t.CallWithProgress(ctx, msg, nil)
}

// CallWithProgress implements ProgressCaller. It behaves like Call but
// invokes handler for each notifications/progress message received while waiting.
func (t *StdioTransport) CallWithProgress(ctx context.Context, msg *Message, handler ProgressHandler) (*Message, error) {
	if msg.ID == nil {
		return nil, t.Send(ctx, msg)
	}
	if err := t.Send(ctx, msg); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case reply, ok := <-t.out:
			if !ok {
				return nil, fmt.Errorf("stdio: connection closed while waiting for response")
			}
			if reply.ID == nil {
				if handler != nil && reply.Method == "notifications/progress" {
					handler(reply)
				}
				continue
			}
			if matchID(msg.ID, reply.ID) {
				return reply, nil
			}
		}
	}
}
