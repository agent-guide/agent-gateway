package provider

import (
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// UpstreamError describes an HTTP error returned by an upstream provider API.
type UpstreamError struct {
	Status     int
	StatusText string
	Body       string
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("upstream %d", e.Status)
	}
	return fmt.Sprintf("upstream %d: %s", e.Status, body)
}

func (e *UpstreamError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.Status
}

// UpstreamErrorFields extracts structured log fields from a provider UpstreamError.
func UpstreamErrorFields(err error) []zap.Field {
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) || upstreamErr == nil {
		return nil
	}

	fields := []zap.Field{zap.Int("upstream_status", upstreamErr.Status)}
	if upstreamErr.StatusText != "" {
		fields = append(fields, zap.String("upstream_status_text", upstreamErr.StatusText))
	}
	body := truncateLoggedBody(upstreamErr.Body)
	if body != "" {
		fields = append(fields, zap.String("upstream_error_body", body))
	}
	return fields
}

func truncateLoggedBody(body string) string {
	body = strings.TrimSpace(body)
	const maxLoggedUpstreamBody = 2048
	if len(body) > maxLoggedUpstreamBody {
		return body[:maxLoggedUpstreamBody] + "...(truncated)"
	}
	return body
}
