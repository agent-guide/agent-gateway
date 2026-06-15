package dispatcher

import "net/http"

// ResponseFlusher flushes through ResponseController so wrapped middleware
// writers that expose capabilities via Unwrap still stream incrementally.
type ResponseFlusher struct {
	rc *http.ResponseController
}

// NewResponseFlusher creates a best-effort flusher for streaming responses.
func NewResponseFlusher(w http.ResponseWriter) ResponseFlusher {
	return ResponseFlusher{rc: http.NewResponseController(w)}
}

// Flush attempts to flush pending response bytes and reports whether it worked.
func (f ResponseFlusher) Flush() bool {
	return f.rc != nil && f.rc.Flush() == nil
}
