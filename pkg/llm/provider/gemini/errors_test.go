package gemini

import (
	"errors"
	"net/http"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/genai"
)

func TestNormalizeError(t *testing.T) {
	normalized := normalizeError(genai.APIError{
		Code:    http.StatusBadRequest,
		Status:  "400 Bad Request",
		Message: "bad request",
		Details: []map[string]any{{"field": "value"}},
	})

	fields := provider.UpstreamErrorFields(normalized)
	fieldMap := fieldsToMap(fields)
	if got := fieldMap["upstream_status"]; got != int64(http.StatusBadRequest) {
		t.Fatalf("upstream_status = %#v, want %d", got, http.StatusBadRequest)
	}
	if got := fieldMap["upstream_status_text"]; got != "400 Bad Request" {
		t.Fatalf("upstream_status_text = %#v, want %q", got, "400 Bad Request")
	}
	if got := fieldMap["upstream_error_body"]; got != "bad request" {
		t.Fatalf("upstream_error_body = %#v, want %q", got, "bad request")
	}
}

func TestNormalizeErrorIgnoresOtherErrors(t *testing.T) {
	err := errors.New("boom")
	if got := normalizeError(err); got != err {
		t.Fatalf("normalizeError returned %v, want original error", got)
	}
}

func fieldsToMap(fields []zap.Field) map[string]any {
	enc := zapcore.NewMapObjectEncoder()
	for _, field := range fields {
		field.AddTo(enc)
	}
	return enc.Fields
}
