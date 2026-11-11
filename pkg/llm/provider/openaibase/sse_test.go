package openaibase

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// Reject reads after EOF so a regression cannot spin forever in the test.
type eofCheckedBody struct {
	io.Reader
	eof    bool
	closed bool
}

func (b *eofCheckedBody) Read(p []byte) (int, error) {
	if b.eof {
		return 0, errors.New("unexpected read after EOF")
	}
	n, err := b.Reader.Read(p)
	b.eof = err == io.EOF
	return n, err
}

func (b *eofCheckedBody) Close() error {
	b.closed = true
	return nil
}

func TestResponsesSSEOptionalSpaceAndEOF(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"space", "event: response.output_text.delta\ndata: {\"delta\":\"hello\"}\n\n", 1},
		{"no_space", "event:response.output_text.delta\ndata:{\"delta\":\"hello\"}\n\n", 1},
		{"crlf", "event:response.output_text.delta\r\ndata:{\"delta\":\"hello\"}\r\n\r\n", 1},
		{"no_final_newline", "data:{\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}", 1},
		{"empty", "", 0},
		{"blank", "\n\n", 0},
		{"comments", ": heartbeat\n\n", 0},
		{"event_only", "event:response.output_text.delta\n\n", 0},
		{"done", "data:[DONE]\n\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &eofCheckedBody{Reader: strings.NewReader(tt.body)}
			sr, sw := schema.Pipe[*provider.ResponsesStreamEvent](4)
			defer sr.Close()
			// Small fixtures fit in the pipe; synchronous execution also makes
			// resource closure observable without polling.
			new(Base).readResponsesStream(body, sw)
			var count int
			for {
				event, err := sr.Recv()
				// These fixtures intentionally have no Responses terminal event.
				if errors.Is(err, io.ErrUnexpectedEOF) {
					break
				}
				if err != nil {
					t.Fatalf("expected truncation error, got %v", err)
				}
				if event.Type != "response.output_text.delta" || event.Delta != "hello" {
					t.Fatalf("unexpected event: %+v", event)
				}
				count++
			}
			if count != tt.want {
				t.Fatalf("events = %d, want %d", count, tt.want)
			}
			if !body.closed {
				t.Fatal("upstream body was not closed")
			}
		})
	}
}
