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

// Caller is a synchronous request-response abstraction over any MCP transport.
// Call sends msg and waits for the matching response. For notifications (no ID),
// it sends and returns immediately with (nil, nil).
type Caller interface {
	Call(ctx context.Context, msg *Message) (*Message, error)
	Close() error
}

// ProgressHandler is called for each notifications/progress message received from
// an upstream while waiting for a request response.
type ProgressHandler func(notification *Message)

// ProgressCaller extends Caller with the ability to relay upstream progress
// notifications while a request is in flight.
type ProgressCaller interface {
	Caller
	CallWithProgress(ctx context.Context, msg *Message, handler ProgressHandler) (*Message, error)
}
