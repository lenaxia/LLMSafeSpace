// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package llmsafespace

import "fmt"

// APIError represents an error response from the LLMSafeSpace API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("llmsafespace: %d %s", e.Status, e.Message)
}

// IsNotFound returns true if the error is a 404.
func IsNotFound(err error) bool {
	if e, ok := err.(*APIError); ok {
		return e.Status == 404
	}
	return false
}

// IsAuth returns true if the error is a 401 or 403.
func IsAuth(err error) bool {
	if e, ok := err.(*APIError); ok {
		return e.Status == 401 || e.Status == 403
	}
	return false
}

// IsConflict returns true if the error is a 409.
func IsConflict(err error) bool {
	if e, ok := err.(*APIError); ok {
		return e.Status == 409
	}
	return false
}

// IsRateLimit returns true if the error is a 429.
func IsRateLimit(err error) bool {
	if e, ok := err.(*APIError); ok {
		return e.Status == 429
	}
	return false
}
