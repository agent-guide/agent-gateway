package anthropicbase

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMessagesSSEOptionalSpace(t *testing.T) {
	for _, space := range []string{" ", ""} {
		t.Run("space="+space, func(t *testing.T) {
			payload := `{"index":0,"delta":{"type":"text_delta","text":"hello"}}`
			body := "event:" + space + "content_block_delta\r\ndata:" + space + payload + "\r\n\r\n"
			sr, sw := schema.Pipe[*schema.Message](4)
			defer sr.Close()
			ReadMessageStream(io.NopCloser(strings.NewReader(body)), sw, "test")
			var text string
			var relays []AnthropicStreamEvent
			for {
				msg, err := sr.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				text += msg.Content
				relays = append(relays, AnthropicRelayStreamEventsFromMessage(msg)...)
			}
			if text != "hello" {
				t.Fatalf("text = %q, want hello", text)
			}
			if len(relays) != 1 || relays[0].Event != "content_block_delta" || string(relays[0].Data) != payload {
				t.Fatalf("unexpected relay events: %+v", relays)
			}
		})
	}
}

func TestMessagesSSEFinalEventAtEOF(t *testing.T) {
	for _, suffix := range []string{"", "\n", "\n\n"} {
		t.Run("suffix="+suffix, func(t *testing.T) {
			body := "event:message_start\ndata:{\"type\":\"message_start\"}\n\nevent:message_stop\ndata:{\"type\":\"message_stop\"}" + suffix
			sr, sw := schema.Pipe[*schema.Message](4)
			defer sr.Close()
			go ReadMessageStream(io.NopCloser(strings.NewReader(body)), sw, "test")
			var relays []AnthropicStreamEvent
			for {
				msg, err := sr.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				relays = append(relays, AnthropicRelayStreamEventsFromMessage(msg)...)
			}
			if len(relays) != 2 || relays[1].Event != "message_stop" {
				t.Fatalf("events=%+v", relays)
			}
		})
	}
}

type failedSSEReader struct{}

func (failedSSEReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestMessagesSSEReadErrorDoesNotFlushPendingEvent(t *testing.T) {
	body := io.MultiReader(strings.NewReader("event:message_stop\ndata:{\"type\":\"message_stop\"}"), failedSSEReader{})
	sr, sw := schema.Pipe[*schema.Message](2)
	defer sr.Close()
	go ReadMessageStream(io.NopCloser(body), sw, "test")
	msg, err := sr.Recv()
	if msg != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("message=%+v err=%v", msg, err)
	}
}
