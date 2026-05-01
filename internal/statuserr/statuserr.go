package statuserr

import (
	"errors"
	"net/http"
)

// StatusCoder is implemented by errors that carry an HTTP status code.
type StatusCoder interface {
	StatusCode() int
}

// StatusError is implemented by errors that carry an HTTP status code.
type StatusError interface {
	error
	StatusCoder
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

// HTTPStatusError describes a failure with an HTTP status code.
type HTTPStatusError struct {
	status int
	msg    string
}

func (e *HTTPStatusError) Error() string   { return e.msg }
func (e *HTTPStatusError) StatusCode() int { return e.status }

// New constructs an error with the given HTTP status and message.
func New(status int, msg string) *HTTPStatusError {
	return &HTTPStatusError{status: status, msg: msg}
}

// Wrap returns err unchanged if it already carries an HTTP status code.
// Otherwise it wraps err with fallback so callers can handle it uniformly.
func Wrap(err error, fallback int) error {
	if err == nil {
		return nil
	}
	var se StatusError
	if errors.As(err, &se) {
		return err
	}
	return New(fallback, err.Error())
}
