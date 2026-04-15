package httplog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/internal/httpcapture"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestResponseErrorLogsOnlyErrors(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	logger := zap.New(core)
	req := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)

	okRecorder := httpcapture.NewResponseRecorder(httptest.NewRecorder())
	okRecorder.WriteHeader(http.StatusOK)
	ResponseError(logger, "admin request failed", req, okRecorder)
	if logs.Len() != 0 {
		t.Fatalf("logged success response, entries = %d", logs.Len())
	}

	errRecorder := httpcapture.NewResponseRecorder(httptest.NewRecorder())
	if err := httpjson.Error(errRecorder, http.StatusBadRequest, "bad request"); err != nil {
		t.Fatalf("httpjson.Error() returned error: %v", err)
	}
	ResponseError(logger, "admin request failed", req, errRecorder)

	if logs.Len() != 1 {
		t.Fatalf("log entries = %d, want 1", logs.Len())
	}
	entry := logs.All()[0]
	if entry.Message != "admin request failed" {
		t.Fatalf("message = %q, want admin request failed", entry.Message)
	}
	fields := entry.ContextMap()
	if fields["status"] != int64(http.StatusBadRequest) {
		t.Fatalf("status field = %#v, want %d", fields["status"], http.StatusBadRequest)
	}
	if fields["error_message"] != "bad request" {
		t.Fatalf("error_message field = %#v, want bad request", fields["error_message"])
	}
}
