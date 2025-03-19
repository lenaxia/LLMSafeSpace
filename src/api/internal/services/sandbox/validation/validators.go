package validation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

const (
	MaxSandboxTimeout = 3600 // 1 hour
	MaxCPU            = "4000m"
	MaxMemory         = "8Gi"
)

// ValidateCreateSandboxRequest validates a sandbox creation request
func ValidateCreateSandboxRequest(req *types.CreateSandboxRequest) error {
	var validationErrors []string

	// Validate runtime
	if req.Runtime == "" {
		validationErrors = append(validationErrors, "runtime is required")
	}

	// Validate security level
	if req.SecurityLevel != "" && !isValidSecurityLevel(req.SecurityLevel) {
		validationErrors = append(validationErrors, "invalid security level: must be 'standard', 'high', or 'custom'")
	}

	// Validate timeout
	if req.Timeout > MaxSandboxTimeout {
		validationErrors = append(validationErrors, fmt.Sprintf("timeout exceeds maximum allowed value of %d seconds", MaxSandboxTimeout))
	}

	// Validate resources if provided
	if req.Resources != nil {
		if err := validateResources(req.Resources); err != nil {
			validationErrors = append(validationErrors, err.Error())
		}
	}

	// Validate network access if provided
	if req.NetworkAccess != nil {
		if err := validateNetworkAccess(req.NetworkAccess); err != nil {
			validationErrors = append(validationErrors, err.Error())
		}
	}

	if len(validationErrors) > 0 {
		return errors.New(strings.Join(validationErrors, "; "))
	}

	return nil
}

// isValidSecurityLevel checks if the security level is valid
func isValidSecurityLevel(level string) bool {
	validLevels := map[string]bool{
		"standard": true,
		"high":     true,
		"custom":   true,
	}
	return validLevels[level]
}

// validateResources validates resource requirements
func validateResources(resources *types.ResourceRequirements) error {
	var validationErrors []string

	// Add resource validation logic here
	// For example, check CPU and memory limits

	if len(validationErrors) > 0 {
		return errors.New(strings.Join(validationErrors, "; "))
	}

	return nil
}

// validateNetworkAccess validates network access configuration
func validateNetworkAccess(networkAccess *types.NetworkAccess) error {
	var validationErrors []string

	// Validate egress rules
	for i, rule := range networkAccess.Egress {
		if rule.Domain == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("egress rule %d: domain is required", i))
		}

		// Validate ports
		for j, port := range rule.Ports {
			if port.Port <= 0 || port.Port > 65535 {
				validationErrors = append(validationErrors, fmt.Sprintf("egress rule %d, port %d: invalid port number", i, j))
			}

			if port.Protocol != "" && port.Protocol != "TCP" && port.Protocol != "UDP" {
				validationErrors = append(validationErrors, fmt.Sprintf("egress rule %d, port %d: protocol must be TCP or UDP", i, j))
			}
		}
	}

	if len(validationErrors) > 0 {
		return errors.New(strings.Join(validationErrors, "; "))
	}

	return nil
}
