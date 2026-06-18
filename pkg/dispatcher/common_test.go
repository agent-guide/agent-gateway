package dispatcher

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestBodyErrorStatusDetectsMaxBytes(t *testing.T) {
	w := httptest.NewRecorder()
	body := http.MaxBytesReader(w, io.NopCloser(strings.NewReader("0123456789")), 4)
	_, err := io.ReadAll(body)
	if err == nil {
		t.Fatal("expected MaxBytesReader to error on oversized body")
	}

	if got := RequestBodyErrorStatus(err, http.StatusBadRequest); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", got, http.StatusRequestEntityTooLarge)
	}

	// Wrapped errors must still be detected via errors.As.
	wrapped := fmt.Errorf("decode request: %w", err)
	if got := RequestBodyErrorStatus(wrapped, http.StatusBadRequest); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("wrapped status = %d, want %d", got, http.StatusRequestEntityTooLarge)
	}
}

func TestRequestBodyErrorStatusFallsBack(t *testing.T) {
	if got := RequestBodyErrorStatus(nil, http.StatusBadRequest); got != http.StatusBadRequest {
		t.Fatalf("nil status = %d, want %d", got, http.StatusBadRequest)
	}
	if got := RequestBodyErrorStatus(fmt.Errorf("some other parse error"), http.StatusBadRequest); got != http.StatusBadRequest {
		t.Fatalf("other-error status = %d, want %d", got, http.StatusBadRequest)
	}
}
