package transport

import "context"

// Message is an MCP protocol message.
type Message struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method,omitempty"`
	Params  any    `json:"params,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error is an MCP protocol error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Transport defines the MCP transport interface.
type Transport interface {
	Connect(ctx context.Context) error
	Close() error
	Send(ctx context.Context, msg *Message) error
	Receive() <-chan *Message
}
