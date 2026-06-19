// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package errors

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// sentinel error used across tests
var errWrapped = errors.New("root cause")

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNewValidationError(t *testing.T) {
	details := map[string]interface{}{"field": "runtime"}
	e := NewValidationError("bad input", details, errWrapped)

	assert.Equal(t, ErrorTypeValidation, e.Type)
	assert.Equal(t, "validation_error", e.Code)
	assert.Equal(t, "bad input", e.Message)
	assert.Equal(t, details, e.Details)
	assert.Equal(t, errWrapped, e.Err)
}

func TestNewAuthenticationError(t *testing.T) {
	e := NewAuthenticationError("not authenticated", errWrapped)

	assert.Equal(t, ErrorTypeAuth, e.Type)
	assert.Equal(t, "unauthorized", e.Code)
	assert.Equal(t, "not authenticated", e.Message)
	assert.Equal(t, errWrapped, e.Err)
}

func TestNewNotFoundError(t *testing.T) {
	e := NewNotFoundError("sandbox", "sb-123", errWrapped)

	assert.Equal(t, ErrorTypeNotFound, e.Type)
	assert.Equal(t, "not_found", e.Code)
	assert.Contains(t, e.Message, "sandbox")
	assert.Contains(t, e.Message, "sb-123")
	assert.Equal(t, "sandbox", e.Details["resourceType"])
	assert.Equal(t, "sb-123", e.Details["resourceId"])
	assert.Equal(t, errWrapped, e.Err)
}

func TestNewForbiddenError(t *testing.T) {
	e := NewForbiddenError("access denied", nil)

	assert.Equal(t, ErrorTypeForbidden, e.Type)
	assert.Equal(t, "forbidden", e.Code)
	assert.Equal(t, "access denied", e.Message)
	assert.Nil(t, e.Err)
}

func TestNewConflictError(t *testing.T) {
	e := NewConflictError("user", "u-99", errWrapped)

	assert.Equal(t, ErrorTypeConflict, e.Type)
	assert.Equal(t, "conflict", e.Code)
	assert.Contains(t, e.Message, "user")
	assert.Contains(t, e.Message, "u-99")
	assert.Equal(t, "user", e.Details["resourceType"])
	assert.Equal(t, "u-99", e.Details["resourceId"])
}

func TestNewRateLimitError(t *testing.T) {
	e := NewRateLimitError("too fast", 100, 1234567890, nil)

	assert.Equal(t, ErrorTypeRateLimit, e.Type)
	assert.Equal(t, "rate_limited", e.Code)
	assert.Equal(t, 100, e.Details["limit"])
	assert.Equal(t, int64(1234567890), e.Details["reset"])
}

func TestNewInternalError(t *testing.T) {
	e := NewInternalError("something went wrong", errWrapped)

	assert.Equal(t, ErrorTypeInternal, e.Type)
	assert.Equal(t, "internal_error", e.Code)
	assert.Equal(t, errWrapped, e.Err)
}

func TestNewBadRequestError(t *testing.T) {
	e := NewBadRequestError("bad request body", nil)

	assert.Equal(t, ErrorTypeBadRequest, e.Type)
	assert.Equal(t, "bad_request", e.Code)
}

func TestNewNotImplementedError(t *testing.T) {
	e := NewNotImplementedError("not_implemented", "feature not ready", nil)

	assert.Equal(t, ErrorTypeInternal, e.Type)
	assert.Equal(t, "not_implemented", e.Code)
	assert.Equal(t, "feature not ready", e.Message)
}

// ---------------------------------------------------------------------------
// Error() string tests
// ---------------------------------------------------------------------------

func TestError_WithWrappedErr(t *testing.T) {
	e := NewInternalError("something bad", errWrapped)
	s := e.Error()

	assert.Contains(t, s, "internal_error")
	assert.Contains(t, s, "something bad")
	assert.Contains(t, s, "root cause")
}

func TestError_WithoutWrappedErr(t *testing.T) {
	e := NewForbiddenError("no access", nil)
	s := e.Error()

	assert.Contains(t, s, "forbidden")
	assert.Contains(t, s, "no access")
	assert.NotContains(t, s, "root cause")
}

// ---------------------------------------------------------------------------
// Unwrap tests
// ---------------------------------------------------------------------------

func TestUnwrap_ReturnsWrappedErr(t *testing.T) {
	e := NewInternalError("outer", errWrapped)
	assert.Equal(t, errWrapped, e.Unwrap())
}

func TestUnwrap_NilWhenNoWrappedErr(t *testing.T) {
	e := NewForbiddenError("no inner", nil)
	assert.Nil(t, e.Unwrap())
}

func TestErrorsIs_WorksThroughUnwrap(t *testing.T) {
	e := NewInternalError("outer", errWrapped)
	assert.True(t, errors.Is(e, errWrapped))
}

// ---------------------------------------------------------------------------
// StatusCode tests
// ---------------------------------------------------------------------------

func TestStatusCode(t *testing.T) {
	cases := []struct {
		name     string
		err      *APIError
		expected int
	}{
		{"validation", NewValidationError("v", nil, nil), http.StatusUnprocessableEntity},
		{"auth", NewAuthenticationError("a", nil), http.StatusUnauthorized},
		{"not_found", NewNotFoundError("x", "1", nil), http.StatusNotFound},
		{"forbidden", NewForbiddenError("f", nil), http.StatusForbidden},
		{"conflict", NewConflictError("x", "1", nil), http.StatusConflict},
		{"rate_limit", NewRateLimitError("r", 0, 0, nil), http.StatusTooManyRequests},
		{"bad_request", NewBadRequestError("b", nil), http.StatusBadRequest},
		{"internal", NewInternalError("i", nil), http.StatusInternalServerError},
		{"not_implemented (uses internal type)", NewNotImplementedError("c", "m", nil), http.StatusInternalServerError},
		{"unknown type", &APIError{Type: "unknown_type"}, http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.err.StatusCode())
		})
	}
}

// ---------------------------------------------------------------------------
// IsWorkspaceNotFoundError tests
// ---------------------------------------------------------------------------

func TestIsWorkspaceNotFoundError_True(t *testing.T) {
	e := NewNotFoundError("workspace", "ws-001", nil)
	assert.True(t, IsWorkspaceNotFoundError(e))
}

func TestIsWorkspaceNotFoundError_FalseWrongResourceType(t *testing.T) {
	e := NewNotFoundError("user", "u-001", nil)
	assert.False(t, IsWorkspaceNotFoundError(e))
}

func TestIsWorkspaceNotFoundError_FalseWrongErrorType(t *testing.T) {
	e := NewForbiddenError("forbidden", nil)
	assert.False(t, IsWorkspaceNotFoundError(e))
}

func TestIsWorkspaceNotFoundError_FalsePlainError(t *testing.T) {
	assert.False(t, IsWorkspaceNotFoundError(errors.New("plain error")))
}

func TestIsWorkspaceNotFoundError_FalseNil(t *testing.T) {
	assert.False(t, IsWorkspaceNotFoundError(nil))
}

// ---------------------------------------------------------------------------
// Sentinel-error migration tests (US-46.4)
//
// ErrNoAgentStateRow was a plain sentinel (errors.New). It is now a *APIError
// value so it carries its own HTTP status code and category, enabling
// centralized error mapping. These tests lock in the contract:
//   - errors.Is still works (backwards compat for existing callers)
//   - errors.As now works (new typed-error capability)
//   - StatusCode returns the mapped HTTP status
// ---------------------------------------------------------------------------

func TestErrNoAgentStateRow_IsAPIError(t *testing.T) {
	var apiErr *APIError
	assert.True(t, errors.As(ErrNoAgentStateRow, &apiErr),
		"ErrNoAgentStateRow must satisfy errors.As(*APIError)")
}

func TestErrNoAgentStateRow_ErrorsIsPreserved(t *testing.T) {
	var err error = ErrNoAgentStateRow
	assert.True(t, errors.Is(err, ErrNoAgentStateRow),
		"errors.Is must still work after sentinel → *APIError migration")
}

func TestErrNoAgentStateRow_Category(t *testing.T) {
	assert.Equal(t, ErrorTypeConflict, ErrNoAgentStateRow.Type)
}

func TestErrNoAgentStateRow_StatusCode(t *testing.T) {
	assert.Equal(t, http.StatusConflict, ErrNoAgentStateRow.StatusCode())
}

func TestErrNoAgentStateRow_WrappedErrorsAsStillWorks(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrNoAgentStateRow)
	var apiErr *APIError
	assert.True(t, errors.As(wrapped, &apiErr),
		"errors.As must find *APIError through a wrapped error chain")
	assert.Equal(t, http.StatusConflict, apiErr.StatusCode())
}
