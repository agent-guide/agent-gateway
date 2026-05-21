package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// SSETransport implements MCP transport over Server-Sent Events (legacy protocol).
// Inbound messages arrive via a long-lived GET SSE stream; outbound messages are
// sent via POST to a separate endpoint.
type SSETransport struct {
	url     string
	postURL string
	client  *http.Client
	mu      sync.RWMutex
	headers http.Header
	out     chan *Message
	cancel  context.CancelFunc
}

// NewSSETransport creates a new SSE transport.
// streamURL is the GET endpoint that delivers the SSE event stream.
// postURL is the POST endpoint for sending JSON-RPC messages.
func NewSSETransport(streamURL, postURL string, client *http.Client) *SSETransport {
	if client == nil {
		client = http.DefaultClient
	}
	return &SSETransport{
		url:     streamURL,
		postURL: postURL,
		client:  client,
		headers: make(http.Header),
		out:     make(chan *Message, 64),
	}
}

// SetHeader sets a persistent header applied to all outbound HTTP requests.
func (t *SSETransport) SetHeader(key, value string) {
	if t == nil || key == "" || value == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.headers.Set(key, value)
}

func (t *SSETransport) Connect(ctx context.Context) error {
	// Use a background-rooted context so the stream outlives the Connect call's deadline.
	streamCtx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, t.url, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("sse: create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	t.mu.RLock()
	for key, values := range t.headers {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	t.mu.RUnlock()

	resp, err := t.client.Do(req)
	if err != nil {
		cancel()
		return fmt.Errorf("sse: connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("sse: unexpected status %d", resp.StatusCode)
	}

	go t.readLoop(resp)
	return nil
}

func (t *SSETransport) readLoop(resp *http.Response) {
	defer resp.Body.Close()
	defer close(t.out)
	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "" && data.Len() > 0:
			// Skip legacy "endpoint" events — post URL is configured, not derived from stream.
			if eventType != "endpoint" {
				var msg Message
				if err := json.Unmarshal([]byte(data.String()), &msg); err == nil {
					t.out <- &msg
				}
			}
			eventType = ""
			data.Reset()
		}
	}
}

func (t *SSETransport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

func (t *SSETransport) Send(ctx context.Context, msg *Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("sse: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.postURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sse: create post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	t.mu.RLock()
	for key, values := range t.headers {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	t.mu.RUnlock()

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("sse: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("sse: post returned %d: %s", resp.StatusCode, string(errBody))
	}
	return nil
}

func (t *SSETransport) Receive() <-chan *Message {
	return t.out
}

// Call implements Caller. For notifications (msg.ID == nil) it sends and returns
// immediately. For requests it posts the message and waits on the SSE stream for
// the matching reply.
func (t *SSETransport) Call(ctx context.Context, msg *Message) (*Message, error) {
	return t.CallWithProgress(ctx, msg, nil)
}

// CallWithProgress implements ProgressCaller. It behaves like Call but
// invokes handler for each notifications/progress message received while waiting.
func (t *SSETransport) CallWithProgress(ctx context.Context, msg *Message, handler ProgressHandler) (*Message, error) {
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
				return nil, fmt.Errorf("sse: connection closed while waiting for response")
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
