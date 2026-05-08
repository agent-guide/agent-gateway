package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

// StdioTransport implements MCP transport over stdio (local process).
type StdioTransport struct {
	command string
	args    []string
	env     []string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	out     chan *Message
}

// NewStdioTransport creates a new stdio transport.
func NewStdioTransport(command string, args, env []string) *StdioTransport {
	return &StdioTransport{
		command: command,
		args:    args,
		env:     env,
		out:     make(chan *Message, 64),
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
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		t.out <- &msg
	}
	close(t.out)
}

func (t *StdioTransport) Close() error {
	if t.stdin != nil {
		t.stdin.Close()
	}
	if t.cmd != nil {
		return t.cmd.Wait()
	}
	return nil
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
