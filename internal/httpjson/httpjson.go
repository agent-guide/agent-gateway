package httpjson

import (
	"encoding/json"
	"net/http"
)

// Write writes a JSON response with the given status code.
func Write(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// Error writes a standard JSON error response.
func Error(w http.ResponseWriter, status int, message string) error {
	return Write(w, status, map[string]string{"error": message})
}

// Decode decodes a JSON request body into dest and closes the body.
func Decode(r *http.Request, dest any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dest)
}

// ErrorMessage returns the error field from a JSON error body when present.
func ErrorMessage(body []byte) string {
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
