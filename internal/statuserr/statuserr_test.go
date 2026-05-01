package statuserr

import (
	"errors"
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

func TestWrap(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := Wrap(nil, http.StatusBadGateway); got != nil {
			t.Fatalf("Wrap(nil) = %v, want nil", got)
		}
	})

	t.Run("preserve status error", func(t *testing.T) {
		err := New(http.StatusTooManyRequests, "rate limit")
		if got := Wrap(err, http.StatusBadGateway); !errors.Is(got, err) {
			t.Fatalf("Wrap(statuserr) did not preserve original error")
		}
	})

	t.Run("wrap plain error", func(t *testing.T) {
		err := errors.New("boom")
		got := Wrap(err, http.StatusBadGateway)
		if got.Error() != "boom" {
			t.Fatalf("Wrap(plain).Error() = %q, want %q", got.Error(), "boom")
		}
		if status := StatusCode(got, 0); status != http.StatusBadGateway {
			t.Fatalf("Wrap(plain) status = %d, want %d", status, http.StatusBadGateway)
		}
	})
}
