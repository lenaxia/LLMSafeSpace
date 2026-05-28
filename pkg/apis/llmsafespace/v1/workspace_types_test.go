package v1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspacePhaseCreating(t *testing.T) {
	// Creating phase must exist and be distinct from Pending and Active.
	assert.Equal(t, WorkspacePhase("Creating"), WorkspacePhaseCreating)
	assert.NotEqual(t, WorkspacePhasePending, WorkspacePhaseCreating)
	assert.NotEqual(t, WorkspacePhaseActive, WorkspacePhaseCreating)
}

func TestWorkspaceSpec_RuntimeField(t *testing.T) {
	// Runtime replaces DefaultRuntime. JSON key is "runtime".
	spec := WorkspaceSpec{
		Owner:   WorkspaceOwner{UserID: "u1"},
		Runtime: "python:3.11",
		Storage: WorkspaceStorageConfig{Size: "10Gi"},
	}

	data, err := json.Marshal(spec)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "python:3.11", raw["runtime"])
	_, hasDefaultRuntime := raw["defaultRuntime"]
	assert.False(t, hasDefaultRuntime, "defaultRuntime field must not exist")
}

func TestWorkspaceSpec_PodLifecycleFields(t *testing.T) {
	spec := WorkspaceSpec{
		Owner:             WorkspaceOwner{UserID: "u1"},
		Runtime:           "python:3.11",
		Storage:           WorkspaceStorageConfig{Size: "10Gi"},
		Timeout:           3600,
		RestartGeneration: 2,
		MaxRetries:        5,
		Resources: &ResourceRequirements{
			CPU:              "1000m",
			Memory:           "1Gi",
			EphemeralStorage: "2Gi",
		},
		PodSecurityContext: &PodSecurityContext{
			RunAsUser:      1001,
			RunAsGroup:     1001,
			SeccompProfile: "RuntimeDefault",
		},
	}

	data, err := json.Marshal(spec)
	require.NoError(t, err)

	var roundtrip WorkspaceSpec
	require.NoError(t, json.Unmarshal(data, &roundtrip))

	assert.Equal(t, 3600, roundtrip.Timeout)
	assert.Equal(t, int64(2), roundtrip.RestartGeneration)
	assert.Equal(t, int32(5), roundtrip.MaxRetries)
	assert.Equal(t, "1000m", roundtrip.Resources.CPU)
	assert.Equal(t, "1Gi", roundtrip.Resources.Memory)
	assert.Equal(t, int64(1001), roundtrip.PodSecurityContext.RunAsUser)
	assert.Equal(t, "RuntimeDefault", roundtrip.PodSecurityContext.SeccompProfile)
}

func TestWorkspaceStatus_PodFields(t *testing.T) {
	status := WorkspaceStatus{
		Phase:                     WorkspacePhaseActive,
		PodName:                   "ws-abc-pod",
		PodNamespace:              "llmsafespace",
		PodIP:                     "10.0.1.5",
		Endpoint:                  "http://10.0.1.5:4096",
		RestartCount:              1,
		TransientFailureCount:     0,
		ObservedRestartGeneration: 2,
		CredentialSecretHash:      "sha256:abc123",
	}

	data, err := json.Marshal(status)
	require.NoError(t, err)

	var roundtrip WorkspaceStatus
	require.NoError(t, json.Unmarshal(data, &roundtrip))

	assert.Equal(t, "ws-abc-pod", roundtrip.PodName)
	assert.Equal(t, "llmsafespace", roundtrip.PodNamespace)
	assert.Equal(t, "10.0.1.5", roundtrip.PodIP)
	assert.Equal(t, "http://10.0.1.5:4096", roundtrip.Endpoint)
	assert.Equal(t, int32(1), roundtrip.RestartCount)
	assert.Equal(t, int64(2), roundtrip.ObservedRestartGeneration)
	assert.Equal(t, "sha256:abc123", roundtrip.CredentialSecretHash)
}

func TestWorkspaceStatus_PodFieldsOmitEmpty(t *testing.T) {
	// Pod fields should be omitempty — not present when zero.
	status := WorkspaceStatus{Phase: WorkspacePhaseSuspended}

	data, err := json.Marshal(status)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	_, hasPodName := raw["podName"]
	_, hasPodIP := raw["podIP"]
	assert.False(t, hasPodName, "podName should be omitted when empty")
	assert.False(t, hasPodIP, "podIP should be omitted when empty")
}

func TestPodSecurityContext_Defaults(t *testing.T) {
	// Zero value should be valid (controller applies defaults).
	psc := PodSecurityContext{}
	data, err := json.Marshal(psc)
	require.NoError(t, err)
	assert.Contains(t, string(data), "{")
}

func TestResourceRequirements_InWorkspaceSpec(t *testing.T) {
	// ResourceRequirements must be usable from WorkspaceSpec.
	spec := WorkspaceSpec{
		Owner:   WorkspaceOwner{UserID: "u1"},
		Runtime: "go:1.23",
		Storage: WorkspaceStorageConfig{Size: "5Gi"},
		Resources: &ResourceRequirements{
			CPU:    "2000m",
			Memory: "4Gi",
		},
	}
	assert.Equal(t, "2000m", spec.Resources.CPU)
	assert.Equal(t, "4Gi", spec.Resources.Memory)
}

func TestWorkspaceDeepCopy_NewFields(t *testing.T) {
	ws := &Workspace{
		Spec: WorkspaceSpec{
			Owner:   WorkspaceOwner{UserID: "u1"},
			Runtime: "python:3.11",
			Storage: WorkspaceStorageConfig{Size: "10Gi"},
			Resources: &ResourceRequirements{
				CPU:    "500m",
				Memory: "512Mi",
			},
			PodSecurityContext: &PodSecurityContext{
				RunAsUser:  1000,
				RunAsGroup: 1000,
			},
			RestartGeneration: 3,
			MaxRetries:        5,
			Timeout:           1800,
		},
		Status: WorkspaceStatus{
			Phase:                     WorkspacePhaseActive,
			PodName:                   "ws-pod-1",
			PodIP:                     "10.0.0.1",
			TransientFailureCount:     1,
			ObservedRestartGeneration: 3,
		},
	}

	copy := ws.DeepCopy()

	// Verify deep copy is independent.
	assert.Equal(t, ws.Spec.Runtime, copy.Spec.Runtime)
	assert.Equal(t, ws.Spec.Resources.CPU, copy.Spec.Resources.CPU)
	assert.Equal(t, ws.Status.PodIP, copy.Status.PodIP)

	// Mutate copy, verify original unchanged.
	copy.Spec.Resources.CPU = "2000m"
	assert.Equal(t, "500m", ws.Spec.Resources.CPU)

	copy.Spec.PodSecurityContext.RunAsUser = 9999
	assert.Equal(t, int64(1000), ws.Spec.PodSecurityContext.RunAsUser)
}
