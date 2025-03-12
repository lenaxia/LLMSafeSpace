package validation

import (
	"fmt"
	
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

// ValidateCreateWarmPoolRequest validates a warm pool creation request
func ValidateCreateWarmPoolRequest(req types.CreateWarmPoolRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	
	if req.Runtime == "" {
		return fmt.Errorf("runtime is required")
	}
	
	if req.MinSize < 0 {
		return fmt.Errorf("minSize must be non-negative")
	}
	
	if req.MaxSize < 0 {
		return fmt.Errorf("maxSize must be non-negative")
	}
	
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		return fmt.Errorf("minSize cannot be greater than maxSize")
	}
	
	if req.TTL < 0 {
		return fmt.Errorf("ttl must be non-negative")
	}
	
	if req.SecurityLevel != "" && !isValidSecurityLevel(req.SecurityLevel) {
		return fmt.Errorf("invalid security level: %s", req.SecurityLevel)
	}
	
	if req.Resources != nil {
		if err := validateResourceRequirements(req.Resources); err != nil {
			return err
		}
	}
	
	if req.AutoScaling != nil {
		if err := validateAutoScaling(req.AutoScaling); err != nil {
			return err
		}
	}
	
	return nil
}

// ValidateUpdateWarmPoolRequest validates a warm pool update request
func ValidateUpdateWarmPoolRequest(req types.UpdateWarmPoolRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	
	if req.MinSize < 0 {
		return fmt.Errorf("minSize must be non-negative")
	}
	
	if req.MaxSize < 0 {
		return fmt.Errorf("maxSize must be non-negative")
	}
	
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		return fmt.Errorf("minSize cannot be greater than maxSize")
	}
	
	if req.TTL < 0 {
		return fmt.Errorf("ttl must be non-negative")
	}
	
	if req.AutoScaling != nil {
		if err := validateAutoScaling(req.AutoScaling); err != nil {
			return err
		}
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
