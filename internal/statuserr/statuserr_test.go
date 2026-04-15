package statuserr

import (
	"fmt"
	"net/http"
	"testing"
)

func TestStatusCode(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", New(http.StatusForbidden, "forbidden"))

	if got := StatusCode(err, http.StatusBadGateway); got != http.StatusForbidden {
		t.Fatalf("StatusCode() = %d, want %d", got, http.StatusForbidden)
	}

	if got := StatusCode(fmt.Errorf("plain"), http.StatusServiceUnavailable); got != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode() fallback = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestNew(t *testing.T) {
	err := New(http.StatusUnauthorized, "unauthorized")

	if err.Error() != "unauthorized" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "unauthorized")
	}
	if got := StatusCode(err, http.StatusBadGateway); got != http.StatusUnauthorized {
		t.Fatalf("StatusCode() = %d, want %d", got, http.StatusUnauthorized)
	}
}
