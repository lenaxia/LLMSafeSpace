package validation

import (
	"fmt"
	
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// ValidateCreateSandboxRequest validates a sandbox creation request
func ValidateCreateSandboxRequest(req types.CreateSandboxRequest) error {
	validationErrors := make(map[string]string)
	
	// Validate runtime
	if err := ValidateRequired("runtime", req.Runtime); err != nil {
		validationErrors["runtime"] = err.Error()
	}
	
	// Validate timeout
	if req.Timeout < 0 {
		validationErrors["timeout"] = "timeout must be non-negative"
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
	
	// Validate network access
	if req.NetworkAccess != nil {
		if err := validateNetworkAccess(req.NetworkAccess); err != nil {
			validationErrors["networkAccess"] = err.Error()
		}
	}
	
	// Return validation error if any errors were found
	if len(validationErrors) > 0 {
		details := make(map[string]interface{})
		for k, v := range validationErrors {
			details[k] = v
		}
		return errors.NewValidationError("Invalid sandbox creation request", details, nil)
	}
	
	return nil
}

// ValidateExecuteRequest validates an execution request
func ValidateExecuteRequest(req types.ExecuteRequest) error {
	validationErrors := make(map[string]string)
	
	// Validate sandbox ID
	if err := ValidateRequired("sandboxId", req.SandboxID); err != nil {
		validationErrors["sandboxId"] = err.Error()
	}
	
	// Validate type
	if req.Type != "code" && req.Type != "command" {
		validationErrors["type"] = "type must be 'code' or 'command'"
	}
	
	// Validate content
	if err := ValidateRequired("content", req.Content); err != nil {
		validationErrors["content"] = err.Error()
	}
	
	// Validate timeout
	if req.Timeout < 0 {
		validationErrors["timeout"] = "timeout must be non-negative"
	}
	
	// Return validation error if any errors were found
	if len(validationErrors) > 0 {
		details := make(map[string]interface{})
		for k, v := range validationErrors {
			details[k] = v
		}
		return errors.NewValidationError("Invalid execution request", details, nil)
	}
	
	return nil
}

// ValidateInstallPackagesRequest validates a package installation request
func ValidateInstallPackagesRequest(req types.InstallPackagesRequest) error {
	validationErrors := make(map[string]string)
	
	// Validate sandbox ID
	if err := ValidateRequired("sandboxId", req.SandboxID); err != nil {
		validationErrors["sandboxId"] = err.Error()
	}
	
	// Validate packages
	if len(req.Packages) == 0 {
		validationErrors["packages"] = "at least one package is required"
	}
	
	// Return validation error if any errors were found
	if len(validationErrors) > 0 {
		details := make(map[string]interface{})
		for k, v := range validationErrors {
			details[k] = v
		}
		return errors.NewValidationError("Invalid package installation request", details, nil)
	}
	
	return nil
}

// Helper functions

func validateResourceRequirements(resources *types.ResourceRequirements) error {
	// Validate CPU format (e.g., "100m" or "0.1")
	if resources.CPU != "" {
		cpuPattern := `^([0-9]+m|[0-9]+\.[0-9]+)$`
		if err := ValidatePattern("cpu", resources.CPU, cpuPattern); err != nil {
			return err
		}
	}
	
	// Validate memory format (e.g., "100Mi" or "1Gi")
	if resources.Memory != "" {
		memoryPattern := `^[0-9]+(Ki|Mi|Gi)$`
		if err := ValidatePattern("memory", resources.Memory, memoryPattern); err != nil {
			return err
		}
	}
	
	// Validate ephemeral storage format
	if resources.EphemeralStorage != "" {
		storagePattern := `^[0-9]+(Ki|Mi|Gi)$`
		if err := ValidatePattern("ephemeralStorage", resources.EphemeralStorage, storagePattern); err != nil {
			return err
		}
	}
	
	return nil
}

func validateNetworkAccess(networkAccess *types.NetworkAccess) error {
	if networkAccess.Egress != nil {
		for i, rule := range networkAccess.Egress {
			if rule.Domain == "" {
				return fmt.Errorf("egress rule %d: domain is required", i)
			}
			
			if rule.Ports != nil {
				for j, port := range rule.Ports {
					if port.Port <= 0 || port.Port > 65535 {
						return fmt.Errorf("egress rule %d, port %d: port must be between 1 and 65535", i, j)
					}
					
					if port.Protocol != "" && port.Protocol != "TCP" && port.Protocol != "UDP" {
						return fmt.Errorf("egress rule %d, port %d: protocol must be 'TCP' or 'UDP'", i, j)
					}
				}
			}
		}
	}
	
	return nil
}
