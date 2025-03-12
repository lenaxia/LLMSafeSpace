package validation

import (
	"fmt"
	
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

// ValidateCreateWarmPoolRequest validates a warm pool creation request
func ValidateCreateWarmPoolRequest(req types.CreateWarmPoolRequest) error {
	validationErrors := make(map[string]string)
	
	// Validate name
	if err := ValidateRequired("name", req.Name); err != nil {
		validationErrors["name"] = err.Error()
	} else if err := ValidatePattern("name", req.Name, "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"); err != nil {
		validationErrors["name"] = "name must consist of lowercase alphanumeric characters or '-', and must start and end with an alphanumeric character"
	}
	
	// Validate runtime
	if err := ValidateRequired("runtime", req.Runtime); err != nil {
		validationErrors["runtime"] = err.Error()
	}
	
	// Validate minSize
	if req.MinSize < 0 {
		validationErrors["minSize"] = "minSize must be non-negative"
	}
	
	// Validate maxSize
	if req.MaxSize < 0 {
		validationErrors["maxSize"] = "maxSize must be non-negative"
	}
	
	// Validate minSize <= maxSize
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		validationErrors["minSize"] = "minSize cannot be greater than maxSize"
	}
	
	// Validate TTL
	if req.TTL < 0 {
		validationErrors["ttl"] = "ttl must be non-negative"
	}
	
	// Validate security level
	if req.SecurityLevel != "" {
		if err := ValidateSecurityLevel(req.SecurityLevel); err != nil {
			validationErrors["securityLevel"] = err.Error()
		}
	}
	
	// Validate resources
	if req.Resources != nil {
		if err := validateResourceRequirements(req.Resources); err != nil {
			validationErrors["resources"] = err.Error()
		}
	}
	
	// Validate auto scaling
	if req.AutoScaling != nil {
		if err := validateAutoScaling(req.AutoScaling); err != nil {
			validationErrors["autoScaling"] = err.Error()
		}
	}
	
	// Return validation error if any errors were found
	if len(validationErrors) > 0 {
		details := make(map[string]interface{})
		for k, v := range validationErrors {
			details[k] = v
		}
		return errors.NewValidationError("Invalid warm pool creation request", details, nil)
	}
	
	return nil
}

// ValidateUpdateWarmPoolRequest validates a warm pool update request
func ValidateUpdateWarmPoolRequest(req types.UpdateWarmPoolRequest) error {
	validationErrors := make(map[string]string)
	
	// Validate name
	if err := ValidateRequired("name", req.Name); err != nil {
		validationErrors["name"] = err.Error()
	}
	
	// Validate minSize
	if req.MinSize < 0 {
		validationErrors["minSize"] = "minSize must be non-negative"
	}
	
	// Validate maxSize
	if req.MaxSize < 0 {
		validationErrors["maxSize"] = "maxSize must be non-negative"
	}
	
	// Validate minSize <= maxSize
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		validationErrors["minSize"] = "minSize cannot be greater than maxSize"
	}
	
	// Validate TTL
	if req.TTL < 0 {
		validationErrors["ttl"] = "ttl must be non-negative"
	}
	
	// Validate auto scaling
	if req.AutoScaling != nil {
		if err := validateAutoScaling(req.AutoScaling); err != nil {
			validationErrors["autoScaling"] = err.Error()
		}
	}
	
	// Return validation error if any errors were found
	if len(validationErrors) > 0 {
		details := make(map[string]interface{})
		for k, v := range validationErrors {
			details[k] = v
		}
		return errors.NewValidationError("Invalid warm pool update request", details, nil)
	}
	
	return nil
}

// Helper functions

func validateAutoScaling(autoScaling *types.AutoScalingConfig) error {
	if autoScaling.TargetUtilization < 0 || autoScaling.TargetUtilization > 100 {
		return fmt.Errorf("targetUtilization must be between 0 and 100")
	}
	
	if autoScaling.ScaleDownDelay < 0 {
		return fmt.Errorf("scaleDownDelay must be non-negative")
	}
	
	return nil
}
