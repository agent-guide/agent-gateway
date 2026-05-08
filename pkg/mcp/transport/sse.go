package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SSETransport implements MCP transport over Server-Sent Events.
type SSETransport struct {
	url    string
	client *http.Client
	out    chan *Message
}

// NewSSETransport creates a new SSE transport.
func NewSSETransport(url string, client *http.Client) *SSETransport {
	if client == nil {
		client = http.DefaultClient
	}
	return &SSETransport{
		url:    url,
		client: client,
		out:    make(chan *Message, 64),
	}
}

func (t *SSETransport) Connect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
	if err != nil {
		return fmt.Errorf("sse: create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("sse: connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("sse: unexpected status %d", resp.StatusCode)
	}

	go t.readLoop(ctx, resp)
	return nil
}

func (t *SSETransport) readLoop(ctx context.Context, resp *http.Response) {
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data.WriteString(strings.TrimPrefix(line, "data: "))
		} else if line == "" && data.Len() > 0 {
			var msg Message
			if err := json.Unmarshal([]byte(data.String()), &msg); err == nil {
				t.out <- &msg
			}
			data.Reset()
		}
	}
	close(t.out)
}

func (t *SSETransport) Close() error {
	return nil
}

func (t *SSETransport) Send(ctx context.Context, msg *Message) error {
	// TODO: implement POST to MCP server endpoint
	return fmt.Errorf("sse: Send not implemented")
}

func (t *SSETransport) Receive() <-chan *Message {
	return t.out
}
