package httpcapture

import (
	"bytes"
	"net/http"
)

const bodyLimit = 4096

// ResponseRecorder records response metadata for later inspection.
type ResponseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

// NewResponseRecorder wraps a response writer with status/body capture.
func NewResponseRecorder(w http.ResponseWriter) *ResponseRecorder {
	return &ResponseRecorder{ResponseWriter: w}
}

func (w *ResponseRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *ResponseRecorder) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.body.Len() < bodyLimit {
		remaining := bodyLimit - w.body.Len()
		if len(p) > remaining {
			_, _ = w.body.Write(p[:remaining])
		} else {
			_, _ = w.body.Write(p)
		}
	}
	return w.ResponseWriter.Write(p)
}

// StatusCode returns the written status code, defaulting to 200 when none was set explicitly.
func (w *ResponseRecorder) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// BodyBytes returns the buffered prefix of the response body.
func (w *ResponseRecorder) BodyBytes() []byte {
	return w.body.Bytes()
}
