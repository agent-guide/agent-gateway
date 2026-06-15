package dispatcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	acpruntime "github.com/agent-guide/agent-gateway/pkg/acp/runtime"
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
