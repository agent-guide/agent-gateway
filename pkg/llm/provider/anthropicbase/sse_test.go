package anthropicbase

import (
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
