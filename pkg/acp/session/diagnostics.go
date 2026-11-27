package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.uber.org/zap"
)

type diagnosticKey struct{}

type turnDiagnostics struct {
	logger *zap.Logger
	start  time.Time
}

// WithDiagnosticLogger attaches a request-scoped logger. Callers supply any
// gateway correlation fields; the native host does not interpret their identity.
// Diagnostic records never include protocol payloads or raw error strings.
func WithDiagnosticLogger(ctx context.Context, logger *zap.Logger) context.Context {
	return context.WithValue(ctx, diagnosticKey{}, turnDiagnostics{logger: logger, start: time.Now()})
}

func diagnostic(ctx context.Context, phase string, fields ...zap.Field) {
	d, ok := ctx.Value(diagnosticKey{}).(turnDiagnostics)
	if !ok || d.logger == nil {
		return
	}
	fields = append(fields, zap.String("phase", phase),
		zap.String("timestamp", time.Now().UTC().Format(time.RFC3339Nano)),
		zap.Int64("elapsed_ms", time.Since(d.start).Milliseconds()))
	d.logger.Debug("acp: "+phase, fields...)
}

func diagnosticPhase(ctx context.Context, phase string, fields ...zap.Field) func(error) {
	start := time.Now()
	diagnostic(ctx, phase+" started", fields...)
	return func(err error) {
		completed := append([]zap.Field{}, fields...)
		completed = append(completed, zap.Int64("duration_ms", time.Since(start).Milliseconds()), zap.Bool("success", err == nil), zap.Bool("cancelled", errors.Is(err, context.Canceled)), zap.Bool("deadline_exceeded", errors.Is(err, context.DeadlineExceeded)))
		diagnostic(ctx, phase+" completed", completed...)
	}
}

func (i *instance) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	finish := diagnosticPhase(ctx, "request", zap.String("method", method), zap.String("session_id", i.sessionID))
	raw, err := i.t.Request(ctx, method, params)
	finish(err)
	return raw, err
}

// A remote cancellation can complete normally while the local context is live.
// Keep this diagnostic outcome separate from the client-facing return value.
func cancelledDiagnosticError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}
