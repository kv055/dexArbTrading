// Package cexclient contains the small contracts shared by provider-specific
// exchange clients. Provider SDK types deliberately do not escape their package.
package cexclient

import (
	"errors"
	"fmt"
)

// Error is a normalized exchange failure. ResponseBody is intentionally not
// retained: exchange responses can contain account and request information.
type Error struct {
	Provider          string
	Operation         string
	HTTPStatus        int
	Code              string
	Message           string
	Retryable         bool
	AmbiguousMutation bool
	Cause             error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	detail := e.Message
	if e.Code != "" {
		detail = e.Code + ": " + detail
	}
	if detail == "" {
		detail = "request failed"
	}
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s: %s (HTTP %d): %s", e.Provider, e.Operation, e.HTTPStatus, detail)
	}
	return fmt.Sprintf("%s: %s: %s", e.Provider, e.Operation, detail)
}

func (e *Error) Unwrap() error { return e.Cause }

// AsError returns a structured exchange error when err contains one.
func AsError(err error) (*Error, bool) {
	var exchangeErr *Error
	ok := errors.As(err, &exchangeErr)
	return exchangeErr, ok
}
