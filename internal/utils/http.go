package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"go.uber.org/zap"
)

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// WriteError writes a JSON error response.
func WriteError(w http.ResponseWriter, status int, message string) error {
	return WriteJSON(w, status, map[string]string{"error": message})
}

// StatusCode returns the HTTP status from an error when available.
func StatusCode(err error) int {
	type statusCoder interface {
		StatusCode() int
	}
	var sc statusCoder
	if errors.As(err, &sc) {
		return sc.StatusCode()
	}
	return http.StatusBadGateway
}

// LogHTTPError writes a structured HTTP error log entry.
func LogHTTPError(logger *zap.Logger, message string, r *http.Request, status int, cause error, fields ...zap.Field) {
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

// WriteLoggedError logs an HTTP error and writes the standard JSON error response.
func WriteLoggedError(logger *zap.Logger, w http.ResponseWriter, r *http.Request, status int, clientMessage string, cause error, fields ...zap.Field) error {
	LogHTTPError(logger, "http request failed", r, status, cause, fields...)
	return WriteError(w, status, clientMessage)
}

// DecodeJSON decodes a JSON request body into dest and closes the body.
func DecodeJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dest)
}

// LoggingResponseWriter records response metadata for later inspection.
type LoggingResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

// NewLoggingResponseWriter wraps a response writer with status/body capture.
func NewLoggingResponseWriter(w http.ResponseWriter) *LoggingResponseWriter {
	return &LoggingResponseWriter{ResponseWriter: w}
}

func (w *LoggingResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *LoggingResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.body.Len() < 4096 {
		remaining := 4096 - w.body.Len()
		if len(p) > remaining {
			_, _ = w.body.Write(p[:remaining])
		} else {
			_, _ = w.body.Write(p)
		}
	}
	return w.ResponseWriter.Write(p)
}

// StatusCode returns the written status code, defaulting to 200 when none was set explicitly.
func (w *LoggingResponseWriter) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// BodyBytes returns the buffered prefix of the response body.
func (w *LoggingResponseWriter) BodyBytes() []byte {
	return w.body.Bytes()
}

// ExtractErrorMessage returns the `error` field from a JSON error body when present.
func ExtractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return payload.Error
	}
	return ""
}

// LogHTTPResponseError logs a completed error response captured by LoggingResponseWriter.
func LogHTTPResponseError(logger *zap.Logger, message string, r *http.Request, w *LoggingResponseWriter, fields ...zap.Field) {
	if logger == nil || w == nil {
		return
	}
	status := w.StatusCode()
	if status < http.StatusBadRequest {
		return
	}
	if errorMessage := ExtractErrorMessage(w.BodyBytes()); errorMessage != "" {
		fields = append(fields, zap.String("error_message", errorMessage))
	}
	LogHTTPError(logger, message, r, status, nil, fields...)
}
