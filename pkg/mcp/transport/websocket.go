package transport

import "context"

// WebSocketTransport implements MCP transport over WebSocket.
type WebSocketTransport struct {
	url string
	out chan *Message
}

// NewWebSocketTransport creates a new WebSocket transport.
func NewWebSocketTransport(url string) *WebSocketTransport {
	return &WebSocketTransport{
		url: url,
		out: make(chan *Message, 64),
	}
}

func (t *WebSocketTransport) Connect(ctx context.Context) error {
	// TODO: implement using golang.org/x/net/websocket or nhooyr.io/websocket
	return nil
}

func (t *WebSocketTransport) Close() error {
	return nil
}

func (t *WebSocketTransport) Send(ctx context.Context, msg *Message) error {
	// TODO: implement
	return nil
}

func (t *WebSocketTransport) Receive() <-chan *Message {
	return t.out
}
