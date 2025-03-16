package errors

import (
	"fmt"
	"net/http"
)

// ErrorType defines the type of error
type ErrorType string

const (
	// ErrorTypeValidation represents validation errors
	ErrorTypeValidation ErrorType = "validation_error"
	
	// ErrorTypeAuth represents authentication errors
	ErrorTypeAuth ErrorType = "auth_error"
	
	// ErrorTypeNotFound represents resource not found errors
	ErrorTypeNotFound ErrorType = "not_found"
	
	// ErrorTypeForbidden represents permission denied errors
	ErrorTypeForbidden ErrorType = "forbidden"
	
	// ErrorTypeConflict represents resource conflict errors
	ErrorTypeConflict ErrorType = "conflict"
	
	// ErrorTypeRateLimit represents rate limiting errors
	ErrorTypeRateLimit ErrorType = "rate_limited"
	
	// ErrorTypeInternal represents internal server errors
	ErrorTypeInternal ErrorType = "internal_error"
	
	// ErrorTypeBadRequest represents bad request errors
	ErrorTypeBadRequest ErrorType = "bad_request"
)

// APIError represents an API error
type APIError struct {
	Type    ErrorType           `json:"-"`
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
	Err     error               `json:"-"`
}

// Error implements the error interface
func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Err.Error())
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the wrapped error
func (e *APIError) Unwrap() error {
	return e.Err
}

// StatusCode returns the HTTP status code for the error
func (e *APIError) StatusCode() int {
	switch e.Type {
	case ErrorTypeValidation:
		return http.StatusBadRequest
	case ErrorTypeAuth:
		return http.StatusUnauthorized
	case ErrorTypeNotFound:
		return http.StatusNotFound
	case ErrorTypeForbidden:
		return http.StatusForbidden
	case ErrorTypeConflict:
		return http.StatusConflict
	case ErrorTypeRateLimit:
		return http.StatusTooManyRequests
	case ErrorTypeBadRequest:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// NewValidationError creates a new validation error
func NewValidationError(message string, details map[string]interface{}, err error) *APIError {
	return &APIError{
		Type:    ErrorTypeValidation,
		Code:    "validation_error",
		Message: message,
		Details: details,
		Err:     err,
	}
}

// NewAuthenticationError creates a new authentication error
func NewAuthenticationError(message string, err error) *APIError {
        return &APIError{
                Type:    ErrorTypeAuth,
                Code:    "unauthorized",
                Message: message,
                Err:     err,
        }
}

// NewForbiddenError creates a new forbidden error
func NewForbiddenError(message string, err error) *APIError {
	return &APIError{
		Type:    ErrorTypeForbidden,
		Code:    "forbidden",
		Message: message,
		Err:     err,
	}
}

// NewConflictError creates a new conflict error
func NewConflictError(resourceType, resourceID string, err error) *APIError {
	return &APIError{
		Type:    ErrorTypeConflict,
		Code:    "conflict",
		Message: fmt.Sprintf("%s %s already exists", resourceType, resourceID),
		Details: map[string]interface{}{
			"resourceType": resourceType,
			"resourceId":   resourceID,
		},
		Err: err,
	}
}

// NewRateLimitError creates a new rate limit error
func NewRateLimitError(message string, limit int, reset int64, err error) *APIError {
	return &APIError{
		Type:    ErrorTypeRateLimit,
		Code:    "rate_limited",
		Message: message,
		Details: map[string]interface{}{
			"limit": limit,
			"reset": reset,
		},
		Err: err,
	}
}

// NewInternalError creates a new internal server error
func NewInternalError(message string, err error) *APIError {
	return &APIError{
		Type:    ErrorTypeInternal,
		Code:    "internal_error",
		Message: message,
		Err:     err,
	}
}

// NewBadRequestError creates a new bad request error
func NewBadRequestError(message string, err error) *APIError {
	return &APIError{
		Type:    ErrorTypeBadRequest,
		Code:    "bad_request",
		Message: message,
		Err:     err,
	}
}

// IsSandboxNotFoundError checks if the error is a SandboxNotFoundError
func IsSandboxNotFoundError(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.Type == ErrorTypeNotFound && apiErr.Details["resourceType"] == "sandbox"
	}
	return false
}

// IsWarmPoolNotFoundError checks if the error is a WarmPoolNotFoundError
func IsWarmPoolNotFoundError(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.Type == ErrorTypeNotFound && apiErr.Details["resourceType"] == "warmpool"
	}
	return false
}
