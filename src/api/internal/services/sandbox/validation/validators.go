package validation

import (
	"fmt"
	"regexp"
	"strings"
	
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

var (
	runtimeRegex = regexp.MustCompile(`^[a-z0-9]+:[0-9]+\.[0-9]+(\.[0-9]+)?$`)
	cpuRegex     = regexp.MustCompile(`^([0-9]+m|[0-9]+(\.[0-9]+)?)$`)
	memoryRegex  = regexp.MustCompile(`^[0-9]+(Ki|Mi|Gi)$`)
	domainRegex  = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)
)

// ValidateCreateRequest validates a sandbox creation request
func ValidateCreateRequest(req types.CreateSandboxRequest) error {
	if req.Runtime == "" {
		return fmt.Errorf("runtime is required")
	}
	
	if !runtimeRegex.MatchString(req.Runtime) {
		return fmt.Errorf("invalid runtime format, expected format: language:version (e.g., python:3.10)")
	}

	if req.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	if req.Timeout > 3600 {
		return fmt.Errorf("timeout cannot exceed 3600 seconds (1 hour)")
	}

	if req.SecurityLevel != "" && !isValidSecurityLevel(req.SecurityLevel) {
		return fmt.Errorf("invalid security level: %s (must be 'standard', 'high', or 'custom')", req.SecurityLevel)
	}

	if req.Resources != nil {
		if err := validateResources(req.Resources); err != nil {
			return fmt.Errorf("invalid resources: %w", err)
		}
	}

	if req.NetworkAccess != nil {
		if err := validateNetworkAccess(req.NetworkAccess); err != nil {
			return fmt.Errorf("invalid network access: %w", err)
		}
	}

	return nil
}

// ValidateExecuteRequest validates an execution request
func ValidateExecuteRequest(req types.ExecuteRequest) error {
	if req.SandboxID == "" {
		return fmt.Errorf("sandboxID is required")
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

	if req.Timeout > 3600 {
		return fmt.Errorf("timeout cannot exceed 3600 seconds (1 hour)")
	}

	return nil
}

// ValidateInstallPackagesRequest validates a package installation request
func ValidateInstallPackagesRequest(req types.InstallPackagesRequest) error {
	if req.SandboxID == "" {
		return fmt.Errorf("sandboxID is required")
	}

	if len(req.Packages) == 0 {
		return fmt.Errorf("at least one package is required")
	}

	for i, pkg := range req.Packages {
		if pkg == "" {
			return fmt.Errorf("package at index %d is empty", i)
		}
		
		// Check for potentially dangerous characters
		if strings.ContainsAny(pkg, ";&|><$`\\\"'") {
			return fmt.Errorf("package name contains invalid characters: %s", pkg)
		}
	}

	if req.Manager != "" && !isValidPackageManager(req.Manager) {
		return fmt.Errorf("invalid package manager: %s", req.Manager)
	}

	return nil
}

// Helper functions

func validateResources(res *types.ResourceRequirements) error {
	if res.CPU != "" && !cpuRegex.MatchString(res.CPU) {
		return fmt.Errorf("invalid CPU format: %s (must be a number followed by 'm' or a decimal)", res.CPU)
	}

	if res.Memory != "" && !memoryRegex.MatchString(res.Memory) {
		return fmt.Errorf("invalid memory format: %s (must be a number followed by Ki, Mi, or Gi)", res.Memory)
	}

	if res.EphemeralStorage != "" && !memoryRegex.MatchString(res.EphemeralStorage) {
		return fmt.Errorf("invalid ephemeral storage format: %s (must be a number followed by Ki, Mi, or Gi)", res.EphemeralStorage)
	}

	return nil
}

func validateNetworkAccess(na *types.NetworkAccess) error {
	for i, rule := range na.Egress {
		if rule.Domain == "" {
			return fmt.Errorf("domain is required for egress rule at index %d", i)
		}

		if !domainRegex.MatchString(rule.Domain) {
			return fmt.Errorf("invalid domain format for egress rule at index %d: %s", i, rule.Domain)
		}

		for j, port := range rule.Ports {
			if port.Port <= 0 || port.Port > 65535 {
				return fmt.Errorf("invalid port number for egress rule %d, port rule %d: %d (must be between 1 and 65535)", i, j, port.Port)
			}

			if port.Protocol != "" && port.Protocol != "TCP" && port.Protocol != "UDP" {
				return fmt.Errorf("invalid protocol for egress rule %d, port rule %d: %s (must be TCP or UDP)", i, j, port.Protocol)
			}
		}
	}

	return nil
}

func isValidSecurityLevel(level string) bool {
	return level == "standard" || level == "high" || level == "custom"
}

func isValidPackageManager(manager string) bool {
	return manager == "pip" || manager == "npm" || manager == "gem" || manager == "go" || manager == "apt"
}
