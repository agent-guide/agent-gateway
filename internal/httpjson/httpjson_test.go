package httpjson

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type closeTrackingBody struct {
	*bytes.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestError(t *testing.T) {
	rr := httptest.NewRecorder()

	if err := Error(rr, http.StatusBadRequest, "bad request"); err != nil {
		t.Fatalf("Error() returned error: %v", err)
	}

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if got := rr.Body.String(); got != "{\"error\":\"bad request\"}\n" {
		t.Fatalf("body = %q", got)
	}
	if got := ErrorMessage(rr.Body.Bytes()); got != "bad request" {
		t.Fatalf("ErrorMessage() = %q, want bad request", got)
	}
}

func TestDecodeClosesBody(t *testing.T) {
	body := &closeTrackingBody{Reader: bytes.NewReader([]byte(`{"name":"test"}`))}
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(body))
	req.Body = body
	var payload struct {
		Name string `json:"name"`
	}

	if err := Decode(req, &payload); err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if payload.Name != "test" {
		t.Fatalf("decoded name = %q, want test", payload.Name)
	}
	if !body.closed {
		t.Fatal("Decode() did not close request body")
	}
}
