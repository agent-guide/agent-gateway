package statuserr

import (
	"errors"
	"net/http"
)

// StatusCoder is implemented by errors that carry an HTTP status code.
type StatusCoder interface {
	StatusCode() int
}

// StatusCode returns the HTTP status carried by err, or fallback when none is available.
func StatusCode(err error, fallback int) int {
	var sc StatusCoder
	if errors.As(err, &sc) {
		status := sc.StatusCode()
		if status != 0 {
			return status
		}
	}
	if fallback != 0 {
		return fallback
	}
	return http.StatusBadGateway
}

// Error describes a failure with an HTTP status code.
type Error struct {
	status int
	msg    string
}

func (e *Error) Error() string   { return e.msg }
func (e *Error) StatusCode() int { return e.status }

// New constructs an error with the given HTTP status and message.
func New(status int, msg string) error {
	return &Error{status: status, msg: msg}
}
