package dispatcher

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	acpruntime "github.com/agent-guide/agent-gateway/pkg/acp/runtime"
	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *flushRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

// wrappedResponseWriter exposes Flush only through Unwrap, reproducing the
// Caddy v2.7+ ResponseWriter that no longer satisfies a direct http.Flusher
// assertion.
type wrappedResponseWriter struct {
	inner *flushRecorder
}

func (w *wrappedResponseWriter) Header() http.Header         { return w.inner.Header() }
func (w *wrappedResponseWriter) Write(p []byte) (int, error) { return w.inner.Write(p) }
func (w *wrappedResponseWriter) WriteHeader(status int)      { w.inner.WriteHeader(status) }
func (w *wrappedResponseWriter) Unwrap() http.ResponseWriter { return w.inner }

func TestACPSSESinkFlushesThroughUnwrapChain(t *testing.T) {
	t.Parallel()

	inner := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := &wrappedResponseWriter{inner: inner}
	if _, ok := any(w).(http.Flusher); ok {
		t.Fatal("wrapped response writer must not directly implement http.Flusher")
	}

	emit := newACPSSESink(w)
	if err := emit(acpruntime.TurnEvent{Event: "content", Text: "hello"}); err != nil {
		t.Fatalf("emit returned error: %v", err)
	}

	if inner.flushes == 0 {
		t.Fatal("expected SSE frame to flush through wrapped response writer Unwrap chain")
	}
	body := inner.Body.String()
	if !strings.HasPrefix(body, "event: content\n") {
		t.Fatalf("unexpected SSE event line: %q", body)
	}
	if !strings.Contains(body, `"text":"hello"`) {
		t.Fatalf("expected event payload in SSE frame, got %q", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("expected SSE frame to end with a blank line, got %q", body)
	}
}

func TestACPSSESinkDefaultsEventNameToDelta(t *testing.T) {
	t.Parallel()

	inner := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := &wrappedResponseWriter{inner: inner}

	emit := newACPSSESink(w)
	if err := emit(acpruntime.TurnEvent{Text: "chunk"}); err != nil {
		t.Fatalf("emit returned error: %v", err)
	}

	if !strings.HasPrefix(inner.Body.String(), "event: delta\n") {
		t.Fatalf("expected empty event name to default to delta, got %q", inner.Body.String())
	}
}

func TestACPRequestErrorStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "service not configured", err: acpservice.ErrServiceNotConfigured, want: http.StatusNotFound},
		{name: "store not found", err: configstore.ErrNotFound, want: http.StatusNotFound},
		{name: "wrapped service not configured", err: fmt.Errorf("get service: %w", acpservice.ErrServiceNotConfigured), want: http.StatusNotFound},
		{name: "invalid request", err: acpruntime.ErrInvalidRequest, want: http.StatusBadRequest},
		{name: "wrapped invalid request", err: fmt.Errorf("%w: cwd is outside allowed_roots", acpruntime.ErrInvalidRequest), want: http.StatusBadRequest},
		{name: "upstream failure", err: errors.New("initialize: transport closed"), want: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := acpRequestErrorStatus(tt.err); got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMatchACPRouteEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		wantMatch   bool
		wantKind    string
		wantSession string
	}{
		{name: "turn", path: "/turn", wantMatch: true, wantKind: "turn"},
		{name: "permission", path: "/permission", wantMatch: true, wantKind: "permission"},
		{name: "sessions", path: "/sessions", wantMatch: true, wantKind: "sessions"},
		{name: "transcript", path: "/sessions/sess-1/transcript", wantMatch: true, wantKind: "transcript", wantSession: "sess-1"},
		{name: "escaped transcript", path: "/sessions/sess%201/transcript", wantMatch: true, wantKind: "transcript", wantSession: "sess 1"},
		{name: "unknown", path: "/health", wantMatch: false},
		{name: "missing session id", path: "/sessions//transcript", wantMatch: false},
		{name: "nested session id", path: "/sessions/a/b/transcript", wantMatch: false},
		{name: "sessions child", path: "/sessions/sess-1", wantMatch: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotKind, gotSession, gotMatch := matchACPRouteEndpoint(tt.path)
			if gotMatch != tt.wantMatch {
				t.Fatalf("match = %v, want %v", gotMatch, tt.wantMatch)
			}
			if gotKind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", gotKind, tt.wantKind)
			}
			if gotSession != tt.wantSession {
				t.Fatalf("session = %q, want %q", gotSession, tt.wantSession)
			}
		})
	}
}
