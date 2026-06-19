// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package errors

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestStatusError_Error_NoCause(t *testing.T) {
	e := &StatusError{Status: 404, Code: "not_found", Message: "resource not found"}
	if got := e.Error(); got != "resource not found" {
		t.Errorf("Error() = %q, want %q", got, "resource not found")
	}
}

func TestStatusError_Error_WithCause(t *testing.T) {
	cause := fmt.Errorf("db timeout")
	e := &StatusError{Status: 500, Code: "internal", Message: "internal error", Cause: cause}
	got := e.Error()
	if got != "internal error: db timeout" {
		t.Errorf("Error() = %q, want %q", got, "internal error: db timeout")
	}
}

func TestStatusError_StatusCode(t *testing.T) {
	e := &StatusError{Status: http.StatusConflict}
	if got := e.StatusCode(); got != http.StatusConflict {
		t.Errorf("StatusCode() = %d, want %d", got, http.StatusConflict)
	}
}

func TestStatusError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("underlying")
	e := &StatusError{Status: 400, Message: "bad", Cause: cause}
	if got := e.Unwrap(); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestStatusError_Unwrap_Nil(t *testing.T) {
	e := &StatusError{Status: 404}
	if got := e.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
}

func TestStatusError_Wrap(t *testing.T) {
	original := &StatusError{Status: 404, Code: "not_found", Message: "not found"}
	cause := fmt.Errorf("query failed")
	wrapped := original.Wrap(cause)

	if wrapped.Status != original.Status {
		t.Errorf("Wrap().Status = %d, want %d", wrapped.Status, original.Status)
	}
	if wrapped.Code != original.Code {
		t.Errorf("Wrap().Code = %q, want %q", wrapped.Code, original.Code)
	}
	if wrapped.Message != original.Message {
		t.Errorf("Wrap().Message = %q, want %q", wrapped.Message, original.Message)
	}
	if wrapped.Cause != cause {
		t.Errorf("Wrap().Cause = %v, want %v", wrapped.Cause, cause)
	}
	// Original must be unchanged
	if original.Cause != nil {
		t.Errorf("original.Cause = %v, want nil (Wrap must not mutate)", original.Cause)
	}
}

func TestStatusError_ErrorsIs_SentinelPointer(t *testing.T) {
	sentinel := &StatusError{Status: 404, Code: "secret_not_found", Message: "secret not found"}
	wrapped := fmt.Errorf("lookup failed: %w", sentinel)

	if !errors.Is(wrapped, sentinel) {
		t.Error("errors.Is should find sentinel through fmt.Errorf wrapping")
	}
}

func TestStatusError_ErrorsAs(t *testing.T) {
	sentinel := &StatusError{Status: 409, Code: "duplicate", Message: "already exists"}
	wrapped := fmt.Errorf("create %s: %w", "test-secret", sentinel)

	var se *StatusError
	if !errors.As(wrapped, &se) {
		t.Fatal("errors.As should find StatusError in chain")
	}
	if se.StatusCode() != 409 {
		t.Errorf("StatusCode() = %d, want 409", se.StatusCode())
	}
	if se.Message != "already exists" {
		t.Errorf("Message = %q, want %q", se.Message, "already exists")
	}
}

func TestNewStatusError(t *testing.T) {
	e := NewStatusError(http.StatusBadRequest, "bad_request", "invalid input")
	if e.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", e.Status, http.StatusBadRequest)
	}
	if e.Code != "bad_request" {
		t.Errorf("Code = %q, want %q", e.Code, "bad_request")
	}
	if e.Message != "invalid input" {
		t.Errorf("Message = %q, want %q", e.Message, "invalid input")
	}
}
