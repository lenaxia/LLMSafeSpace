// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package errors provides a shared StatusError type that carries an HTTP
// status code, a user-facing message, and an optional wrapped cause.
//
// pkg/ packages cannot import api/internal/errors (Go internal-package
// visibility), so domain errors defined in pkg/ use StatusError instead.
// The API server's generic error handler (respondWithError in router.go)
// already has a duck-type fallback that checks for StatusCode() int +
// Error() string, so StatusError values are handled automatically without
// any handler-level switch.
//
// Usage as a sentinel:
//
//	var ErrSecretNotFound = &StatusError{
//	    Status:  http.StatusNotFound,
//	    Code:    "secret_not_found",
//	    Message: "secret not found",
//	}
//
// Wrapping with detail:
//
//	return fmt.Errorf("lookup %s: %w", name, ErrSecretNotFound)
//
// Checking at the handler:
//
//	errors.Is(err, ErrSecretNotFound)         // sentinel check
//	var se *errors.StatusError
//	errors.As(err, &se)                        // generic typed check
//	se.StatusCode()                            // 404
package errors

import "fmt"

// StatusError is a typed error that carries an HTTP status code and a
// user-facing message. It is the pkg/-side counterpart to
// api/internal/errors.APIError — both implement StatusCode() int so the
// generic error handler treats them uniformly.
type StatusError struct {
	Status  int    // HTTP status code (e.g. 404, 409, 412)
	Code    string // machine-readable error code (e.g. "secret_not_found")
	Message string // user-facing message (safe to expose — no secrets, no internal paths)
	Cause   error  // wrapped underlying error for logging/debugging
}

// Error implements the error interface. Returns the user-facing Message
// plus the Cause (if any) so server-side logs show the full chain. The
// HTTP handler uses Message directly (not Error()) so the Cause detail
// never leaks to the client.
func (e *StatusError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

// StatusCode returns the HTTP status code for this error. Used by the
// generic error handler's duck-type fallback.
func (e *StatusError) StatusCode() int {
	return e.Status
}

// Unwrap returns the wrapped cause, enabling errors.Is and errors.As to
// traverse the chain.
func (e *StatusError) Unwrap() error {
	return e.Cause
}

// NewStatusError creates a new StatusError with the given fields.
func NewStatusError(status int, code, message string) *StatusError {
	return &StatusError{
		Status:  status,
		Code:    code,
		Message: message,
	}
}
