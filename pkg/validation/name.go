// Package validation provides shared validation primitives used by both
// the API layer (pkg/secrets) and the in-pod materializer (pkg/agentd/secrets).
// Keeping validation here prevents drift between the two code paths.
package validation

import (
	"errors"
	"fmt"
	"regexp"
)

// SecretNamePattern is the regex pattern (as a string) that secret names
// must match. Frontend teams SHOULD copy this exact string into their
// client-side validation (e.g., React Hook Form pattern or zod .regex()).
// This package is the single source of truth — file an issue against
// pkg/validation if the pattern needs to change.
const SecretNamePattern = `^[a-z0-9._-]+$`

// SecretNameRE is the compiled regex for SecretNamePattern.
// Exported so callers can use it directly in Go.
var SecretNameRE = regexp.MustCompile(SecretNamePattern)

// ValidateSecretName validates a secret name against the shared rules.
// Returns nil if valid, or a descriptive error.
func ValidateSecretName(s string) error {
	if s == "" {
		return errors.New("name is empty")
	}
	if len(s) > 255 {
		return fmt.Errorf("name length %d exceeds maximum of 255", len(s))
	}
	if s[0] == '.' {
		return fmt.Errorf("name %q must not start with a dot", s)
	}
	if s[0] == '-' {
		return fmt.Errorf("name %q must not start with a hyphen", s)
	}
	if !SecretNameRE.MatchString(s) {
		return fmt.Errorf("name %q contains invalid characters (allowed: lowercase a-z, 0-9, dots, underscores, hyphens)", s)
	}
	return nil
}
