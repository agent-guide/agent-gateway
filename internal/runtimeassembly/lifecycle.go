package runtimeassembly

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
)

const ShutdownBudget = 10 * time.Second

// Lifecycle stops runtime producers before draining usage. The host stops HTTP
// admission first. The storage adapter closes its connection after usage drains.
// A lifecycle must not be reused by a replacement Caddy configuration.
type Lifecycle struct {
	Runtime func(context.Context) error
	Usage   *usage.UsageService
	Store   io.Closer
	once    sync.Once
	done    chan struct{}
	err     error
}

func (l *Lifecycle) Close() error { return l.CloseContext(context.Background()) }

func (l *Lifecycle) CloseContext(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.done = make(chan struct{})
		shutdown, cancel := context.WithTimeout(ctx, ShutdownBudget)
		go func() {
			defer close(l.done)
			defer cancel()
			if l.Runtime != nil {
				if err := l.Runtime(shutdown); err != nil {
					l.err = fmt.Errorf("close gateway runtime: %w", err)
				}
			}
			if err := l.Usage.CloseContext(shutdown); err != nil {
				l.err = errors.Join(l.err, fmt.Errorf("close event pipeline: %w", err))
			}
			if l.Store != nil {
				if err := l.Store.Close(); err != nil {
					l.err = errors.Join(l.err, fmt.Errorf("close config store: %w", err))
				}
			}
		}()
	})
	select {
	case <-l.done:
		return l.err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(ShutdownBudget):
		return context.DeadlineExceeded
	}
}
