package validation

import (
	"fmt"
	"regexp"
	"strings"
	
	"github.com/lenaxia/llmsafespace/api/internal/errors"
)

// Common validation functions

// ValidateRequired checks if a string is not empty
func ValidateRequired(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

// ValidateMinLength checks if a string has a minimum length
func ValidateMinLength(field, value string, minLength int) error {
	if len(value) < minLength {
		return fmt.Errorf("%s must be at least %d characters", field, minLength)
	}
	return nil
}

// ValidateMaxLength checks if a string has a maximum length
func ValidateMaxLength(field, value string, maxLength int) error {
	if len(value) > maxLength {
		return fmt.Errorf("%s must be at most %d characters", field, maxLength)
	}
	return nil
}

// ValidatePattern checks if a string matches a pattern
func ValidatePattern(field, value, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %v", err)
	}
	if !re.MatchString(value) {
		return fmt.Errorf("%s must match pattern %s", field, pattern)
	}
	return nil
}

// ValidateEnum checks if a string is one of the allowed values
func ValidateEnum(field, value string, allowedValues []string) error {
	for _, allowedValue := range allowedValues {
		if value == allowedValue {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of: %s", field, strings.Join(allowedValues, ", "))
}

// ValidateMin checks if a number is at least a minimum value
func ValidateMin(field string, value, min int) error {
	if value < min {
		return fmt.Errorf("%s must be at least %d", field, min)
	}
	return nil
}

// ValidateMax checks if a number is at most a maximum value
func ValidateMax(field string, value, max int) error {
	if value > max {
		return fmt.Errorf("%s must be at most %d", field, max)
	}
	return nil
}

// ValidateRange checks if a number is within a range
func ValidateRange(field string, value, min, max int) error {
	if value < min || value > max {
		return fmt.Errorf("%s must be between %d and %d", field, min, max)
	}
	return nil
}

// ValidateSecurityLevel checks if a security level is valid
func ValidateSecurityLevel(level string) error {
	return ValidateEnum("securityLevel", level, []string{"standard", "high", "custom"})
}

// ValidateResourceRequirements validates resource requirements
func ValidateResourceRequirements(resources interface{}) error {
	// This would be a more complex validation based on the resource structure
	// For now, just return nil
	return nil
}

// ValidateNetworkAccess validates network access rules
func ValidateNetworkAccess(networkAccess interface{}) error {
	// This would be a more complex validation based on the network access structure
	// For now, just return nil
	return nil
}

// ValidateAutoScaling validates auto scaling configuration
func ValidateAutoScaling(autoScaling interface{}) error {
	// This would be a more complex validation based on the auto scaling structure
	// For now, just return nil
	return nil
}

// CreateValidationError creates a validation error with details
func CreateValidationError(message string, details map[string]interface{}, err error) *errors.APIError {
	return errors.NewValidationError(message, details, err)
}
