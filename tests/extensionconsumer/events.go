package extensionconsumer

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/event"
)

// The independently owned sink optionally records process evidence. Its path is
// fixture-only process input; it is not a Community configuration field.
type fixtureSink struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

func newFixtureSink(ctx context.Context) (event.Sink, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s := &fixtureSink{}
	if path := os.Getenv("AGW_FIXTURE_EVENT_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		s.file = f
	}
	return s, nil
}
func (s *fixtureSink) Write(ctx context.Context, e event.Envelope) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		return json.NewEncoder(s.file).Encode(e)
	}
	return nil
}
func (s *fixtureSink) Flush(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}
func (s *fixtureSink) Close(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}
