package anthropic

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNormalizeError(t *testing.T) {
	reqURL, err := url.Parse("https://api.anthropic.com/v1/messages")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	apiErr := &anthropicapi.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    &http.Request{Method: http.MethodPost, URL: reqURL},
		Response:   &http.Response{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"},
	}
	if err := apiErr.UnmarshalJSON([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"quota exceeded"}}`)); err != nil {
		t.Fatalf("unmarshal api error: %v", err)
	}

	normalized := normalizeError(apiErr)
	fields := provider.UpstreamErrorFields(normalized)
	fieldMap := fieldsToMap(fields)
	if got := fieldMap["upstream_status"]; got != int64(http.StatusTooManyRequests) {
		t.Fatalf("upstream_status = %#v, want %d", got, http.StatusTooManyRequests)
	}
	if got := fieldMap["upstream_status_text"]; got != "429 Too Many Requests" {
		t.Fatalf("upstream_status_text = %#v, want %q", got, "429 Too Many Requests")
	}
	if got := fieldMap["upstream_error_body"]; got != `{"type":"error","error":{"type":"rate_limit_error","message":"quota exceeded"}}` {
		t.Fatalf("upstream_error_body = %#v", got)
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
