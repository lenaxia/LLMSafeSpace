package validation

import (
	"fmt"
	"regexp"
	
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

var runtimeRegex = regexp.MustCompile(`^[a-z0-9]+:[0-9]+\.[0-9]+(\.[0-9]+)?$`)

func ValidateCreateRequest(req types.CreateSandboxRequest) error {
	if req.Runtime == "" {
		return fmt.Errorf("runtime is required")
	}
	
	if !runtimeRegex.MatchString(req.Runtime) {
		return fmt.Errorf("invalid runtime format")
	}

	if req.Timeout < 60 || req.Timeout > 3600 {
		return fmt.Errorf("timeout must be between 60 and 3600 seconds")
	}

	if req.Resources != nil {
		if err := validateResources(req.Resources); err != nil {
			return fmt.Errorf("invalid resources: %w", err)
		}
	}

	return nil
}

func validateResources(res *types.ResourceRequirements) error {
	// Validate CPU/memory formats
	// Validate against Kubernetes resource requirements
	return nil
}
