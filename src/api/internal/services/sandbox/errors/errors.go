package errors

import "fmt"

type SandboxNotFoundError struct {
	ID string
}

func (e *SandboxNotFoundError) Error() string {
	return fmt.Sprintf("sandbox %s not found", e.ID)
}

type ConflictError struct {
	Resource string
	ID       string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s %s already exists", e.Resource, e.ID)
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed for %s: %s", e.Field, e.Message)
}
