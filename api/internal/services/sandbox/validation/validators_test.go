package validation

import (
	"strings"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// isValidSecurityLevel
// ---------------------------------------------------------------------------

func TestIsValidSecurityLevel(t *testing.T) {
	valid := []string{"standard", "high", "custom"}
	for _, v := range valid {
		assert.True(t, isValidSecurityLevel(v), "expected %q to be valid", v)
	}

	invalid := []string{"", "root", "STANDARD", "High", "none"}
	for _, v := range invalid {
		assert.False(t, isValidSecurityLevel(v), "expected %q to be invalid", v)
	}
}

// ---------------------------------------------------------------------------
// validateNetworkAccess
// ---------------------------------------------------------------------------

func TestValidateNetworkAccess_Valid(t *testing.T) {
	na := &types.NetworkAccess{
		Egress: []types.EgressRule{
			{
				Domain: "api.example.com",
				Ports: []types.PortRule{
					{Port: 443, Protocol: "TCP"},
					{Port: 53, Protocol: "UDP"},
				},
			},
		},
	}
	assert.NoError(t, validateNetworkAccess(na))
}

func TestValidateNetworkAccess_EmptyEgress(t *testing.T) {
	na := &types.NetworkAccess{}
	assert.NoError(t, validateNetworkAccess(na))
}

func TestValidateNetworkAccess_MissingDomain(t *testing.T) {
	na := &types.NetworkAccess{
		Egress: []types.EgressRule{
			{Domain: ""},
		},
	}
	err := validateNetworkAccess(na)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domain is required")
}

func TestValidateNetworkAccess_InvalidPort_Zero(t *testing.T) {
	na := &types.NetworkAccess{
		Egress: []types.EgressRule{
			{
				Domain: "example.com",
				Ports:  []types.PortRule{{Port: 0}},
			},
		},
	}
	err := validateNetworkAccess(na)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid port number")
}

func TestValidateNetworkAccess_InvalidPort_TooHigh(t *testing.T) {
	na := &types.NetworkAccess{
		Egress: []types.EgressRule{
			{
				Domain: "example.com",
				Ports:  []types.PortRule{{Port: 65536}},
			},
		},
	}
	err := validateNetworkAccess(na)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid port number")
}

func TestValidateNetworkAccess_InvalidProtocol(t *testing.T) {
	na := &types.NetworkAccess{
		Egress: []types.EgressRule{
			{
				Domain: "example.com",
				Ports:  []types.PortRule{{Port: 80, Protocol: "HTTP"}},
			},
		},
	}
	err := validateNetworkAccess(na)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protocol must be TCP or UDP")
}

func TestValidateNetworkAccess_EmptyProtocolIsValid(t *testing.T) {
	na := &types.NetworkAccess{
		Egress: []types.EgressRule{
			{
				Domain: "example.com",
				Ports:  []types.PortRule{{Port: 80, Protocol: ""}},
			},
		},
	}
	assert.NoError(t, validateNetworkAccess(na))
}

func TestValidateNetworkAccess_MultipleErrors(t *testing.T) {
	na := &types.NetworkAccess{
		Egress: []types.EgressRule{
			{Domain: ""},
			{
				Domain: "example.com",
				Ports:  []types.PortRule{{Port: -1}},
			},
		},
	}
	err := validateNetworkAccess(na)
	require.Error(t, err)
	// Both errors should be present in the joined message
	assert.Contains(t, err.Error(), "domain is required")
	assert.Contains(t, err.Error(), "invalid port number")
}

// ---------------------------------------------------------------------------
// validateResources
// ---------------------------------------------------------------------------

func TestValidateResources_NilIsValid(t *testing.T) {
	// validateResources with nil shouldn't be called; but if non-nil, it's valid too
	r := &types.ResourceRequirements{}
	assert.NoError(t, validateResources(r))
}

// ---------------------------------------------------------------------------
// ValidateCreateSandboxRequest
// ---------------------------------------------------------------------------

func TestValidateCreateSandboxRequest_Valid(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime:       "python:3.11",
		SecurityLevel: "standard",
		Timeout:       300,
	}
	assert.NoError(t, ValidateCreateSandboxRequest(req))
}

func TestValidateCreateSandboxRequest_MissingRuntime(t *testing.T) {
	req := &types.CreateSandboxRequest{
		SecurityLevel: "high",
	}
	err := ValidateCreateSandboxRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime is required")
}

func TestValidateCreateSandboxRequest_InvalidSecurityLevel(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime:       "python:3.11",
		SecurityLevel: "none",
	}
	err := ValidateCreateSandboxRequest(req)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "security level")
}

func TestValidateCreateSandboxRequest_EmptySecurityLevelIsValid(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime: "python:3.11",
		// SecurityLevel empty — should pass (optional field)
	}
	assert.NoError(t, ValidateCreateSandboxRequest(req))
}

func TestValidateCreateSandboxRequest_TimeoutExceedsMax(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime: "nodejs:18",
		Timeout: MaxSandboxTimeout + 1,
	}
	err := ValidateCreateSandboxRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout exceeds maximum")
}

func TestValidateCreateSandboxRequest_TimeoutAtMaxIsValid(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime: "nodejs:18",
		Timeout: MaxSandboxTimeout,
	}
	assert.NoError(t, ValidateCreateSandboxRequest(req))
}

func TestValidateCreateSandboxRequest_ZeroTimeoutIsValid(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime: "go:1.22",
		Timeout: 0,
	}
	assert.NoError(t, ValidateCreateSandboxRequest(req))
}

func TestValidateCreateSandboxRequest_WithNetworkErrors(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime: "python:3.11",
		NetworkAccess: &types.NetworkAccess{
			Egress: []types.EgressRule{
				{Domain: ""},
			},
		},
	}
	err := ValidateCreateSandboxRequest(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domain is required")
}

func TestValidateCreateSandboxRequest_MultipleErrors(t *testing.T) {
	req := &types.CreateSandboxRequest{
		Runtime:       "",
		SecurityLevel: "invalid",
		Timeout:       MaxSandboxTimeout + 100,
	}
	err := ValidateCreateSandboxRequest(req)
	require.Error(t, err)
	// All three field errors should appear
	assert.Contains(t, err.Error(), "runtime is required")
	assert.Contains(t, strings.ToLower(err.Error()), "security level")
	assert.Contains(t, err.Error(), "timeout exceeds maximum")
}

func TestValidateCreateSandboxRequest_AllValidLevels(t *testing.T) {
	for _, level := range []string{"standard", "high", "custom"} {
		req := &types.CreateSandboxRequest{
			Runtime:       "python:3.11",
			SecurityLevel: level,
		}
		assert.NoError(t, ValidateCreateSandboxRequest(req), "level %q should be valid", level)
	}
}
