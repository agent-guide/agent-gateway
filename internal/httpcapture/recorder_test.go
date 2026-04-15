package httpcapture

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseRecorderCapturesStatusAndBodyPrefix(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := NewResponseRecorder(rr)
	body := strings.Repeat("x", bodyLimit+10)

	rec.WriteHeader(http.StatusBadRequest)
	if _, err := rec.Write([]byte(body)); err != nil {
		t.Fatalf("Write() returned error: %v", err)
	}

	if rec.StatusCode() != http.StatusBadRequest {
		t.Fatalf("StatusCode() = %d, want %d", rec.StatusCode(), http.StatusBadRequest)
	}
	if len(rec.BodyBytes()) != bodyLimit {
		t.Fatalf("captured body length = %d, want %d", len(rec.BodyBytes()), bodyLimit)
	}
	if rr.Body.Len() != len(body) {
		t.Fatalf("underlying body length = %d, want %d", rr.Body.Len(), len(body))
	}
}
