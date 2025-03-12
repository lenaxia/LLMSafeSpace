package validation

import (
	"fmt"
	
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

// ValidateCreateSandboxRequest validates a sandbox creation request
func ValidateCreateSandboxRequest(req types.CreateSandboxRequest) error {
	if req.Runtime == "" {
		return fmt.Errorf("runtime is required")
	}
	
	if req.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	
	if req.SecurityLevel != "" && !isValidSecurityLevel(req.SecurityLevel) {
		return fmt.Errorf("invalid security level: %s", req.SecurityLevel)
	}
	
	if req.Resources != nil {
		if err := validateResourceRequirements(req.Resources); err != nil {
			return err
		}
	}
	
	if req.NetworkAccess != nil {
		if err := validateNetworkAccess(req.NetworkAccess); err != nil {
			return err
		}
	}
	
	return nil
}

// ValidateExecuteRequest validates an execution request
func ValidateExecuteRequest(req types.ExecuteRequest) error {
	if req.SandboxID == "" {
		return fmt.Errorf("sandbox ID is required")
	}
	
	if req.Type != "code" && req.Type != "command" {
		return fmt.Errorf("type must be 'code' or 'command'")
	}
	
	if req.Content == "" {
		return fmt.Errorf("content is required")
	}
	
	if req.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	
	return nil
}

// ValidateInstallPackagesRequest validates a package installation request
func ValidateInstallPackagesRequest(req types.InstallPackagesRequest) error {
	if req.SandboxID == "" {
		return fmt.Errorf("sandbox ID is required")
	}
	
	if len(req.Packages) == 0 {
		return fmt.Errorf("at least one package is required")
	}
	
	return nil
}

// Helper functions

func isValidSecurityLevel(level string) bool {
	switch level {
	case "standard", "high", "custom":
		return true
	default:
		return false
	}
}

func validateResourceRequirements(resources *types.ResourceRequirements) error {
	// Add validation for CPU, memory, etc.
	// For now, just return nil
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
