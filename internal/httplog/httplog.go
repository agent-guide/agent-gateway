package httplog

import (
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"go.uber.org/zap"
)

// Error writes a structured HTTP error log entry.
func Error(logger *zap.Logger, message string, r *http.Request, status int, cause error, fields ...zap.Field) {
	if logger == nil {
		return
	}
	logFields := []zap.Field{
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.Int("status", status),
	}
	if cause != nil {
		logFields = append(logFields, zap.Error(cause))
	}
	logFields = append(logFields, fields...)
	logger.Error(message, logFields...)
}

// WriteError logs an HTTP error and writes the standard JSON error response.
func WriteError(logger *zap.Logger, w http.ResponseWriter, r *http.Request, status int, clientMessage string, cause error, fields ...zap.Field) error {
	Error(logger, "http request failed", r, status, cause, fields...)
	return httpjson.Error(w, status, clientMessage)
}

// RecordedResponse exposes response data captured by middleware or wrappers.
type RecordedResponse interface {
	StatusCode() int
	BodyBytes() []byte
}

// ResponseError logs a completed error response captured by a response recorder.
func ResponseError(logger *zap.Logger, message string, r *http.Request, w RecordedResponse, fields ...zap.Field) {
	if logger == nil || w == nil {
		return
	}
	status := w.StatusCode()
	if status < http.StatusBadRequest {
		return
	}
	if errorMessage := httpjson.ErrorMessage(w.BodyBytes()); errorMessage != "" {
		fields = append(fields, zap.String("error_message", errorMessage))
	}
	Error(logger, message, r, status, nil, fields...)
}
