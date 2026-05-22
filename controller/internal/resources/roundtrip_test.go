package resources

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// These tests verify that CRDs produced by the API layer (apiv1 types)
// survive JSON serialization/deserialization into the controller types
// (resources types). This is the exact path data takes in production:
//
//	API service → JSON → etcd → controller-runtime → controller types
//
// GAP-1/2/3 were caused by missing fields in apiv1 types that broke this
// round-trip silently. These tests create a permanent safety net.

// --- M1: SandboxSpec.WorkspaceRef round-trip ---

func TestRoundTrip_SandboxSpec_WorkspaceRef(t *testing.T) {
	apiSandbox := &apiv1.Sandbox{
		TypeMeta: metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-roundtrip",
			Namespace: "default",
		},
		Spec: apiv1.SandboxSpec{
			Runtime:       "python:3.11",
			SecurityLevel: "standard",
			Timeout:       300,
			WorkspaceRef:  "ws-roundtrip",
		},
	}

	data, err := json.Marshal(apiSandbox)
	require.NoError(t, err)

	var ctrlSandbox Sandbox
	require.NoError(t, json.Unmarshal(data, &ctrlSandbox))

	assert.Equal(t, "ws-roundtrip", ctrlSandbox.Spec.WorkspaceRef,
		"WorkspaceRef set by API must survive JSON round-trip to controller types")
}

func TestRoundTrip_SandboxSpec_WorkspaceRefEmpty(t *testing.T) {
	apiSandbox := &apiv1.Sandbox{
		Spec: apiv1.SandboxSpec{
			Runtime: "python:3.11",
		},
	}

	data, err := json.Marshal(apiSandbox)
	require.NoError(t, err)

	var ctrlSandbox Sandbox
	require.NoError(t, json.Unmarshal(data, &ctrlSandbox))

	assert.Equal(t, "", ctrlSandbox.Spec.WorkspaceRef)
}

// --- M2: SandboxStatus.PodIP round-trip ---

func TestRoundTrip_SandboxStatus_PodIP(t *testing.T) {
	now := metav1.NewTime(time.Now())
	ctrlSandbox := &Sandbox{
		Status: SandboxStatus{
			Phase:        "Running",
			PodName:      "sb-pod-1234",
			PodNamespace: "default",
			PodIP:        "10.0.1.42",
			StartTime:    &now,
			Endpoint:     "sb-pod-1234.default.svc.cluster.local",
		},
	}

	data, err := json.Marshal(ctrlSandbox)
	require.NoError(t, err)

	var apiSandbox apiv1.Sandbox
	require.NoError(t, json.Unmarshal(data, &apiSandbox))

	assert.Equal(t, "10.0.1.42", apiSandbox.Status.PodIP,
		"PodIP set by controller must survive JSON round-trip to API types")
	assert.Equal(t, "Running", apiSandbox.Status.Phase)
	assert.Equal(t, "sb-pod-1234", apiSandbox.Status.PodName)
}

func TestRoundTrip_SandboxStatus_LastActivityAt(t *testing.T) {
	ts := metav1.NewTime(time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC))
	ctrlSandbox := &Sandbox{
		Status: SandboxStatus{
			Phase:          "Running",
			LastActivityAt: &ts,
		},
	}

	data, err := json.Marshal(ctrlSandbox)
	require.NoError(t, err)

	var apiSandbox apiv1.Sandbox
	require.NoError(t, json.Unmarshal(data, &apiSandbox))

	require.NotNil(t, apiSandbox.Status.LastActivityAt,
		"LastActivityAt set by controller must survive round-trip")
	assert.True(t, ts.Time.Equal(apiSandbox.Status.LastActivityAt.Time))
}

func TestRoundTrip_SandboxStatus_EmptyPodIP(t *testing.T) {
	ctrlSandbox := &Sandbox{
		Status: SandboxStatus{
			Phase: "Pending",
		},
	}

	data, err := json.Marshal(ctrlSandbox)
	require.NoError(t, err)

	var apiSandbox apiv1.Sandbox
	require.NoError(t, json.Unmarshal(data, &apiSandbox))

	assert.Equal(t, "", apiSandbox.Status.PodIP)
	assert.Nil(t, apiSandbox.Status.LastActivityAt)
}

// --- M3: WorkspaceSpec full fields round-trip ---

func TestRoundTrip_WorkspaceSpec_AllFields(t *testing.T) {
	apiWS := &apiv1.Workspace{
		TypeMeta: metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Workspace"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ws-roundtrip",
			Namespace: "default",
		},
		Spec: apiv1.WorkspaceSpec{
			Owner:          apiv1.WorkspaceOwner{UserID: "user-42"},
			DefaultRuntime: "python:3.11",
			SecurityLevel:  "high",
			Storage: apiv1.WorkspaceStorageConfig{
				Size:             "20Gi",
				StorageClassName: "fast-ssd",
				AccessMode:       "ReadWriteMany",
			},
			NetworkAccess: &apiv1.WorkspaceNetworkAccess{
				Egress: []apiv1.WorkspaceEgressRule{
					{Domain: "pypi.org"},
					{Domain: "files.pythonhosted.org"},
				},
				Ingress: true,
			},
			AutoSuspend: &apiv1.WorkspaceAutoSuspend{
				Enabled:            true,
				IdleTimeoutSeconds: 1800,
			},
			TTLSecondsAfterSuspended: 86400,
			Packages: []apiv1.WorkspacePackageSet{
				{Runtime: "python:3.11", Requirements: []string{"numpy", "pandas"}},
				{Runtime: "nodejs:18", Requirements: []string{"express"}},
			},
			InitScript:        "echo 'setup complete'",
			MaxActiveSessions: 3,
			Credentials: &apiv1.WorkspaceCredentialRef{
				SecretName: "workspace-creds-ws-roundtrip",
			},
		},
	}

	data, err := json.Marshal(apiWS)
	require.NoError(t, err)

	var ctrlWS Workspace
	require.NoError(t, json.Unmarshal(data, &ctrlWS))

	assert.Equal(t, "user-42", ctrlWS.Spec.Owner.UserID)
	assert.Equal(t, "python:3.11", ctrlWS.Spec.DefaultRuntime)
	assert.Equal(t, "high", ctrlWS.Spec.SecurityLevel)
	assert.Equal(t, "20Gi", ctrlWS.Spec.Storage.Size)
	assert.Equal(t, "fast-ssd", ctrlWS.Spec.Storage.StorageClassName)
	assert.Equal(t, "ReadWriteMany", ctrlWS.Spec.Storage.AccessMode)

	require.NotNil(t, ctrlWS.Spec.NetworkAccess)
	assert.Len(t, ctrlWS.Spec.NetworkAccess.Egress, 2)
	assert.Equal(t, "pypi.org", ctrlWS.Spec.NetworkAccess.Egress[0].Domain)
	assert.True(t, ctrlWS.Spec.NetworkAccess.Ingress)

	require.NotNil(t, ctrlWS.Spec.AutoSuspend)
	assert.True(t, ctrlWS.Spec.AutoSuspend.Enabled)
	assert.Equal(t, int64(1800), ctrlWS.Spec.AutoSuspend.IdleTimeoutSeconds)

	assert.Equal(t, int64(86400), ctrlWS.Spec.TTLSecondsAfterSuspended)
	assert.Len(t, ctrlWS.Spec.Packages, 2)
	assert.Equal(t, "numpy", ctrlWS.Spec.Packages[0].Requirements[0])
	assert.Equal(t, "express", ctrlWS.Spec.Packages[1].Requirements[0])
	assert.Equal(t, "echo 'setup complete'", ctrlWS.Spec.InitScript)
	assert.Equal(t, int32(3), ctrlWS.Spec.MaxActiveSessions)

	require.NotNil(t, ctrlWS.Spec.Credentials)
	assert.Equal(t, "workspace-creds-ws-roundtrip", ctrlWS.Spec.Credentials.SecretName)
}

func TestRoundTrip_WorkspaceStatus_SuspendedAt(t *testing.T) {
	suspendTime := metav1.NewTime(time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC))
	ctrlWS := &Workspace{
		Status: WorkspaceStatus{
			Phase:              WorkspacePhaseSuspended,
			PVCName:            "workspace-ws-1",
			ActiveSessions:     0,
			SuspendedAt:        &suspendTime,
			ObservedGeneration: 5,
		},
	}

	data, err := json.Marshal(ctrlWS)
	require.NoError(t, err)

	var apiWS apiv1.Workspace
	require.NoError(t, json.Unmarshal(data, &apiWS))

	assert.Equal(t, apiv1.WorkspacePhaseSuspended, apiWS.Status.Phase)
	assert.Equal(t, "workspace-ws-1", apiWS.Status.PVCName)
	assert.Equal(t, int32(0), apiWS.Status.ActiveSessions)
	require.NotNil(t, apiWS.Status.SuspendedAt,
		"SuspendedAt set by controller must survive round-trip to API types")
	assert.True(t, suspendTime.Time.Equal(apiWS.Status.SuspendedAt.Time))
	assert.Equal(t, int64(5), apiWS.Status.ObservedGeneration)
}

func TestRoundTrip_WorkspaceSpec_NilPointerFields(t *testing.T) {
	apiWS := &apiv1.Workspace{
		Spec: apiv1.WorkspaceSpec{
			Owner:   apiv1.WorkspaceOwner{UserID: "u1"},
			Storage: apiv1.WorkspaceStorageConfig{Size: "5Gi"},
		},
	}

	data, err := json.Marshal(apiWS)
	require.NoError(t, err)

	var ctrlWS Workspace
	require.NoError(t, json.Unmarshal(data, &ctrlWS))

	assert.Nil(t, ctrlWS.Spec.NetworkAccess)
	assert.Nil(t, ctrlWS.Spec.AutoSuspend)
	assert.Nil(t, ctrlWS.Spec.Packages)
	assert.Nil(t, ctrlWS.Spec.Credentials)
	assert.Equal(t, "", ctrlWS.Spec.InitScript)
}

// --- Bidirectional round-trip: API → Controller → API ---

func TestRoundTrip_Sandbox_Bidirectional(t *testing.T) {
	now := metav1.NewTime(time.Now())
	original := &apiv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-bidi",
			Namespace: "test-ns",
			Labels:    map[string]string{"llmsafespace.dev/workspace": "ws-1"},
		},
		Spec: apiv1.SandboxSpec{
			Runtime:       "python:3.11",
			SecurityLevel: "standard",
			Timeout:       300,
			WorkspaceRef:  "ws-1",
			Resources: &apiv1.ResourceRequirements{
				CPU:    "500m",
				Memory: "512Mi",
			},
			NetworkAccess: &apiv1.NetworkAccess{
				Egress: []apiv1.EgressRule{
					{Domain: "pypi.org", Ports: []apiv1.PortRule{{Port: 443, Protocol: "TCP"}}},
				},
			},
		},
	}

	apiToController, err := json.Marshal(original)
	require.NoError(t, err)

	var ctrlSandbox Sandbox
	require.NoError(t, json.Unmarshal(apiToController, &ctrlSandbox))

	ctrlSandbox.Status.Phase = "Running"
	ctrlSandbox.Status.PodName = "sb-bidi-pod"
	ctrlSandbox.Status.PodIP = "10.0.0.99"
	ctrlSandbox.Status.StartTime = &now

	controllerToAPI, err := json.Marshal(&ctrlSandbox)
	require.NoError(t, err)

	var roundTripped apiv1.Sandbox
	require.NoError(t, json.Unmarshal(controllerToAPI, &roundTripped))

	assert.Equal(t, "ws-1", roundTripped.Spec.WorkspaceRef,
		"WorkspaceRef must survive API→controller→API round-trip")
	assert.Equal(t, "10.0.0.99", roundTripped.Status.PodIP,
		"PodIP must survive controller→API round-trip")
	assert.Equal(t, "Running", roundTripped.Status.Phase)
	assert.Equal(t, "sb-bidi-pod", roundTripped.Status.PodName)
	assert.Equal(t, "pypi.org", roundTripped.Spec.NetworkAccess.Egress[0].Domain)
}
