package v1

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestSchemeRegistration verifies all CRD kinds are registered with the scheme.
func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))

	gv := schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"}

	tests := []struct {
		name string
		obj  runtime.Object
		kind string
	}{
		{"Sandbox", &Sandbox{}, "Sandbox"},
		{"SandboxList", &SandboxList{}, "SandboxList"},
		{"SandboxProfile", &SandboxProfile{}, "SandboxProfile"},
		{"SandboxProfileList", &SandboxProfileList{}, "SandboxProfileList"},
		{"RuntimeEnvironment", &RuntimeEnvironment{}, "RuntimeEnvironment"},
		{"RuntimeEnvironmentList", &RuntimeEnvironmentList{}, "RuntimeEnvironmentList"},
		{"Workspace", &Workspace{}, "Workspace"},
		{"WorkspaceList", &WorkspaceList{}, "WorkspaceList"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvks, _, err := scheme.ObjectKinds(tt.obj)
			require.NoError(t, err)
			require.Len(t, gvks, 1)
			assert.Equal(t, gv.Group, gvks[0].Group)
			assert.Equal(t, gv.Version, gvks[0].Version)
			assert.Equal(t, tt.kind, gvks[0].Kind)
		})
	}
}

func TestGroupVersionConstants(t *testing.T) {
	assert.Equal(t, "llmsafespace.dev", GroupName)
	assert.Equal(t, "v1", GroupVersion)
	assert.Equal(t, schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"}, SchemeGroupVersion)
	assert.Equal(t, "llmsafespace.dev", Resource("sandboxes").Group)
	assert.Equal(t, "sandboxes", Resource("sandboxes").Resource)
}

// TestSandboxSpec_HasSecurityContextField verifies the field is named
// `SecurityContext` (Go) and serializes as `securityContext` (JSON).
// This catches regression of the old `SecurityCtx` Go field name.
func TestSandboxSpec_HasSecurityContextField(t *testing.T) {
	typ := reflect.TypeOf(SandboxSpec{})
	field, ok := typ.FieldByName("SecurityContext")
	require.True(t, ok, "SandboxSpec must have Go field named SecurityContext")
	assert.Equal(t, "securityContext,omitempty", field.Tag.Get("json"))

	_, hasOldName := typ.FieldByName("SecurityCtx")
	assert.False(t, hasOldName, "SandboxSpec must NOT have legacy SecurityCtx field")
}

// TestResourceRequirements_HasCPUPinning verifies the field is present
// and serializes as `cpuPinning`. Catches regression of the previously
// missing field.
func TestResourceRequirements_HasCPUPinning(t *testing.T) {
	typ := reflect.TypeOf(ResourceRequirements{})
	field, ok := typ.FieldByName("CPUPinning")
	require.True(t, ok, "ResourceRequirements must have CPUPinning field")
	assert.Equal(t, "cpuPinning,omitempty", field.Tag.Get("json"))
	assert.Equal(t, reflect.Bool, field.Type.Kind())
}

// TestRuntimeEnvironmentSpec_FieldShape verifies the unified RuntimeEnvironment
// schema matches the deployed YAML and not the dead apis-side shape.
func TestRuntimeEnvironmentSpec_FieldShape(t *testing.T) {
	typ := reflect.TypeOf(RuntimeEnvironmentSpec{})

	tests := []struct {
		goField string
		jsonTag string
	}{
		{"Image", "image"},
		{"Language", "language"},
		{"Version", "version,omitempty"},
		{"Tags", "tags,omitempty"},
		{"PreInstalledPackages", "preInstalledPackages,omitempty"},
		{"PackageManager", "packageManager,omitempty"},
		{"SecurityFeatures", "securityFeatures,omitempty"},
		{"ResourceRequirements", "resourceRequirements,omitempty"},
	}
	for _, tt := range tests {
		t.Run(tt.goField, func(t *testing.T) {
			field, ok := typ.FieldByName(tt.goField)
			require.True(t, ok, "RuntimeEnvironmentSpec must have field %s", tt.goField)
			assert.Equal(t, tt.jsonTag, field.Tag.Get("json"))
		})
	}

	// Dead fields from old apis schema must be gone.
	for _, gone := range []string{"BaseImage", "Packages"} {
		_, found := typ.FieldByName(gone)
		assert.False(t, found, "RuntimeEnvironmentSpec must NOT have legacy field %s", gone)
	}
}

func TestRuntimeEnvironmentStatus_FieldShape(t *testing.T) {
	typ := reflect.TypeOf(RuntimeEnvironmentStatus{})

	available, ok := typ.FieldByName("Available")
	require.True(t, ok)
	assert.Equal(t, "available,omitempty", available.Tag.Get("json"))

	lastValidated, ok := typ.FieldByName("LastValidated")
	require.True(t, ok)
	assert.Equal(t, "lastValidated,omitempty", lastValidated.Tag.Get("json"))

	for _, gone := range []string{"Ready", "LastUpdateTime"} {
		_, found := typ.FieldByName(gone)
		assert.False(t, found, "RuntimeEnvironmentStatus must NOT have legacy field %s", gone)
	}
}

// TestSandboxProfileSpec_FieldShape verifies the unified SandboxProfile
// schema matches the deployed YAML and not the dead apis-side shape.
func TestSandboxProfileSpec_FieldShape(t *testing.T) {
	typ := reflect.TypeOf(SandboxProfileSpec{})

	expected := []struct {
		goField string
		jsonTag string
	}{
		{"Language", "language"},
		{"SecurityLevel", "securityLevel,omitempty"},
		{"SeccompProfile", "seccompProfile,omitempty"},
		{"NetworkPolicies", "networkPolicies,omitempty"},
		{"PreInstalledPackages", "preInstalledPackages,omitempty"},
		{"ResourceDefaults", "resourceDefaults,omitempty"},
		{"FilesystemConfig", "filesystemConfig,omitempty"},
	}
	for _, tt := range expected {
		t.Run(tt.goField, func(t *testing.T) {
			field, ok := typ.FieldByName(tt.goField)
			require.True(t, ok, "SandboxProfileSpec must have field %s", tt.goField)
			assert.Equal(t, tt.jsonTag, field.Tag.Get("json"))
		})
	}

	// Dead fields from old apis schema must be gone.
	for _, gone := range []string{"Resources", "NetworkAccess", "Filesystem", "Storage", "SecurityCtx"} {
		_, found := typ.FieldByName(gone)
		assert.False(t, found, "SandboxProfileSpec must NOT have legacy field %s", gone)
	}
}

// TestWorkspaceCondition_StatusIsString verifies WorkspaceCondition.Status
// is plain string (not corev1.ConditionStatus). Removes heavyweight import.
func TestWorkspaceCondition_StatusIsString(t *testing.T) {
	typ := reflect.TypeOf(WorkspaceCondition{})
	field, ok := typ.FieldByName("Status")
	require.True(t, ok)
	assert.Equal(t, reflect.String, field.Type.Kind())
	assert.Equal(t, "string", field.Type.Name(),
		"WorkspaceCondition.Status must be plain string, not corev1.ConditionStatus")
}

func TestWorkspaceConditionType_TypedefAndConstants(t *testing.T) {
	assert.Equal(t, "Ready", string(WorkspaceConditionReady))
	assert.Equal(t, "PVCReady", string(WorkspaceConditionPVCReady))
	assert.Equal(t, "Suspended", string(WorkspaceConditionSuspended))
}

func TestWorkspacePhase_Constants(t *testing.T) {
	cases := map[WorkspacePhase]string{
		WorkspacePhasePending:     "Pending",
		WorkspacePhaseActive:      "Active",
		WorkspacePhaseSuspending:  "Suspending",
		WorkspacePhaseSuspended:   "Suspended",
		WorkspacePhaseResuming:    "Resuming",
		WorkspacePhaseTerminating: "Terminating",
		WorkspacePhaseTerminated:  "Terminated",
		WorkspacePhaseFailed:      "Failed",
	}
	for got, want := range cases {
		assert.Equal(t, want, string(got))
	}
}

// TestSandbox_JSONRoundTrip verifies a fully-populated Sandbox round-trips
// through JSON without losing any field.
func TestSandbox_JSONRoundTrip(t *testing.T) {
	now := metav1.NewTime(time.Now().Truncate(time.Second))
	original := &Sandbox{
		TypeMeta: metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
			Labels:    map[string]string{"user-id": "u1"},
		},
		Spec: SandboxSpec{
			Runtime:       "python:3.11",
			SecurityLevel: "high",
			Timeout:       600,
			Resources: &ResourceRequirements{
				CPU:              "500m",
				Memory:           "512Mi",
				EphemeralStorage: "1Gi",
				CPUPinning:       true,
			},
			NetworkAccess: &NetworkAccess{
				Egress: []EgressRule{
					{Domain: "pypi.org", Ports: []PortRule{{Port: 443, Protocol: "TCP"}}},
				},
				Ingress: false,
			},
			Filesystem: &FilesystemConfig{
				ReadOnlyRoot:  true,
				WritablePaths: []string{"/tmp", "/workspace"},
			},
			Storage: &StorageConfig{
				Persistent: true,
				VolumeSize: "10Gi",
			},
			SecurityContext: &SecurityContext{
				RunAsUser:  1000,
				RunAsGroup: 1000,
				SeccompProfile: &SeccompProfile{
					Type:             "Localhost",
					LocalhostProfile: "/profiles/agent.json",
				},
			},
			ProfileRef: &ProfileReference{
				Name:      "default",
				Namespace: "llmsafespace",
			},
			WorkspaceRef: "ws-1",
		},
		Status: SandboxStatus{
			Phase:        "Running",
			PodName:      "test-sandbox-pod",
			PodNamespace: "default",
			PodIP:        "10.0.0.5",
			StartTime:    &now,
			Endpoint:     "http://10.0.0.5:4096",
			Resources: &ResourceStatus{
				CPUUsage:    "100m",
				MemoryUsage: "256Mi",
			},
			LastActivityAt: &now,
			Conditions: []SandboxCondition{
				{
					Type:               "Ready",
					Status:             "True",
					Reason:             "PodReady",
					Message:            "Pod is ready",
					LastTransitionTime: now,
				},
			},
		},
	}

	bytes, err := json.Marshal(original)
	require.NoError(t, err)

	// Verify expected JSON keys are present; catches regressions in JSON tags.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bytes, &raw))
	assert.Contains(t, raw, "spec")

	var roundTrip Sandbox
	require.NoError(t, json.Unmarshal(bytes, &roundTrip))

	assert.Equal(t, original.Spec.Runtime, roundTrip.Spec.Runtime)
	assert.Equal(t, original.Spec.Resources.CPUPinning, roundTrip.Spec.Resources.CPUPinning)
	assert.NotNil(t, roundTrip.Spec.SecurityContext)
	assert.Equal(t, original.Spec.SecurityContext.SeccompProfile.Type, roundTrip.Spec.SecurityContext.SeccompProfile.Type)
	assert.Equal(t, original.Spec.WorkspaceRef, roundTrip.Spec.WorkspaceRef)
	assert.Equal(t, original.Status.PodIP, roundTrip.Status.PodIP)
	assert.Equal(t, original.Status.Endpoint, roundTrip.Status.Endpoint)
}

// TestSandbox_JSONUsesSecurityContextKey ensures the JSON tag survived the
// SecurityCtx → SecurityContext field rename.
func TestSandbox_JSONUsesSecurityContextKey(t *testing.T) {
	s := &Sandbox{
		Spec: SandboxSpec{
			SecurityContext: &SecurityContext{RunAsUser: 1000},
		},
	}
	b, err := json.Marshal(s.Spec)
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &raw))
	_, hasSecurityContext := raw["securityContext"]
	assert.True(t, hasSecurityContext, "JSON must contain securityContext key")
	_, hasSecurityCtx := raw["securityCtx"]
	assert.False(t, hasSecurityCtx, "JSON must not contain securityCtx key")
}

// TestRuntimeEnvironment_JSONRoundTrip verifies the unified RuntimeEnvironment
// round-trips correctly with the ctrl-side field names.
func TestRuntimeEnvironment_JSONRoundTrip(t *testing.T) {
	now := metav1.NewTime(time.Now().Truncate(time.Second))
	original := &RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "RuntimeEnvironment"},
		ObjectMeta: metav1.ObjectMeta{Name: "python-311"},
		Spec: RuntimeEnvironmentSpec{
			Image:                "ghcr.io/example/python:3.11",
			Language:             "python",
			Version:              "3.11",
			Tags:                 []string{"ml", "data"},
			PreInstalledPackages: []string{"numpy", "pandas"},
			PackageManager:       "pip",
			SecurityFeatures:     []string{"seccomp"},
			ResourceRequirements: &RuntimeResourceRequirements{
				MinCPU:            "100m",
				MinMemory:         "128Mi",
				RecommendedCPU:    "500m",
				RecommendedMemory: "512Mi",
			},
		},
		Status: RuntimeEnvironmentStatus{
			Available:     true,
			LastValidated: &now,
		},
	}

	bytes, err := json.Marshal(original)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bytes, &raw))
	specRaw := raw["spec"]
	var spec map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(specRaw, &spec))
	_, hasImage := spec["image"]
	assert.True(t, hasImage, "spec must contain 'image' key")
	_, hasBaseImage := spec["baseImage"]
	assert.False(t, hasBaseImage, "spec must NOT contain legacy 'baseImage' key")
	_, hasPreInstalled := spec["preInstalledPackages"]
	assert.True(t, hasPreInstalled)
	_, hasOldPackages := spec["packages"]
	assert.False(t, hasOldPackages, "spec must NOT contain legacy 'packages' key")

	var roundTrip RuntimeEnvironment
	require.NoError(t, json.Unmarshal(bytes, &roundTrip))
	assert.Equal(t, original.Spec.Image, roundTrip.Spec.Image)
	assert.Equal(t, original.Spec.PreInstalledPackages, roundTrip.Spec.PreInstalledPackages)
	assert.Equal(t, original.Status.Available, roundTrip.Status.Available)
}

// TestSandboxProfile_JSONRoundTrip verifies the unified SandboxProfile shape.
func TestSandboxProfile_JSONRoundTrip(t *testing.T) {
	original := &SandboxProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "SandboxProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: "python-default"},
		Spec: SandboxProfileSpec{
			Language:       "python",
			SecurityLevel:  "high",
			SeccompProfile: "/profiles/python.json",
			NetworkPolicies: []NetworkPolicy{
				{
					Type: "egress",
					Rules: []NetworkRule{
						{Domain: "pypi.org", Ports: []PortRule{{Port: 443, Protocol: "TCP"}}},
						{CIDR: "10.0.0.0/8"},
					},
				},
			},
			PreInstalledPackages: []string{"numpy"},
			ResourceDefaults: &ResourceDefaults{
				CPU:              "500m",
				Memory:           "512Mi",
				EphemeralStorage: "1Gi",
			},
			FilesystemConfig: &ProfileFilesystemConfig{
				ReadOnlyPaths: []string{"/etc"},
				WritablePaths: []string{"/tmp"},
			},
		},
	}

	bytes, err := json.Marshal(original)
	require.NoError(t, err)

	var roundTrip SandboxProfile
	require.NoError(t, json.Unmarshal(bytes, &roundTrip))
	assert.Equal(t, original.Spec.Language, roundTrip.Spec.Language)
	assert.Equal(t, original.Spec.NetworkPolicies[0].Type, roundTrip.Spec.NetworkPolicies[0].Type)
	assert.Equal(t, original.Spec.NetworkPolicies[0].Rules[0].Domain, roundTrip.Spec.NetworkPolicies[0].Rules[0].Domain)
	assert.Equal(t, original.Spec.ResourceDefaults.CPU, roundTrip.Spec.ResourceDefaults.CPU)
}

// TestWorkspace_JSONRoundTrip verifies the Workspace round-trips and that
// WorkspaceCondition.Status is rendered as a plain string enum.
func TestWorkspace_JSONRoundTrip(t *testing.T) {
	now := metav1.NewTime(time.Now().Truncate(time.Second))
	original := &Workspace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Workspace"},
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec: WorkspaceSpec{
			Owner:          WorkspaceOwner{UserID: "user-1"},
			Runtime: "python:3.11",
			SecurityLevel:  "standard",
			Storage: WorkspaceStorageConfig{
				Size:             "10Gi",
				StorageClassName: "fast",
				AccessMode:       "ReadWriteOnce",
			},
			NetworkAccess: &WorkspaceNetworkAccess{
				Egress:  []WorkspaceEgressRule{{Domain: "pypi.org"}},
				Ingress: false,
			},
			AutoSuspend: &WorkspaceAutoSuspend{
				Enabled:            true,
				IdleTimeoutSeconds: 1800,
			},
			TTLSecondsAfterSuspended: 86400,
			Packages: []WorkspacePackageSet{
				{Runtime: "python:3.11", Requirements: []string{"numpy"}},
			},
			InitScript:        "echo init",
			MaxActiveSessions: 5,
			Credentials:       &WorkspaceCredentialRef{SecretName: "ws-creds"},
		},
		Status: WorkspaceStatus{
			Phase:          WorkspacePhaseActive,
			PVCName:        "pvc-ws-1",
			ActiveSessions: 1,
			LastActivityAt: &now,
			Conditions: []WorkspaceCondition{
				{
					Type:               WorkspaceConditionReady,
					Status:             "True",
					LastTransitionTime: now,
					Reason:             "PVCBound",
					Message:            "ok",
				},
			},
			Message:            "active",
			ObservedGeneration: 7,
		},
	}

	bytes, err := json.Marshal(original)
	require.NoError(t, err)

	var roundTrip Workspace
	require.NoError(t, json.Unmarshal(bytes, &roundTrip))
	assert.Equal(t, original.Spec.Owner.UserID, roundTrip.Spec.Owner.UserID)
	assert.Equal(t, original.Spec.MaxActiveSessions, roundTrip.Spec.MaxActiveSessions)
	assert.Equal(t, original.Status.Phase, roundTrip.Status.Phase)
	require.Len(t, roundTrip.Status.Conditions, 1)
	assert.Equal(t, "True", roundTrip.Status.Conditions[0].Status)
	assert.Equal(t, WorkspaceConditionReady, roundTrip.Status.Conditions[0].Type)
}

// TestSandbox_DeepCopy verifies generated DeepCopy creates an independent copy.
func TestSandbox_DeepCopy(t *testing.T) {
	original := &Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "s1", Labels: map[string]string{"a": "b"}},
		Spec: SandboxSpec{
			Runtime: "python:3.11",
			Resources: &ResourceRequirements{
				CPU:        "500m",
				CPUPinning: true,
			},
			NetworkAccess: &NetworkAccess{
				Egress: []EgressRule{
					{Domain: "pypi.org", Ports: []PortRule{{Port: 443, Protocol: "TCP"}}},
				},
			},
			SecurityContext: &SecurityContext{
				RunAsUser:      1000,
				SeccompProfile: &SeccompProfile{Type: "RuntimeDefault"},
			},
		},
	}
	copy := original.DeepCopy()
	require.NotNil(t, copy)
	require.NotSame(t, original, copy)

	// Mutate copy; original must be unaffected.
	copy.Spec.Runtime = "node:20"
	assert.Equal(t, "python:3.11", original.Spec.Runtime)

	copy.Spec.Resources.CPUPinning = false
	assert.True(t, original.Spec.Resources.CPUPinning)

	copy.Spec.NetworkAccess.Egress[0].Domain = "modified"
	assert.Equal(t, "pypi.org", original.Spec.NetworkAccess.Egress[0].Domain)

	copy.Spec.NetworkAccess.Egress[0].Ports[0].Port = 80
	assert.Equal(t, 443, original.Spec.NetworkAccess.Egress[0].Ports[0].Port)

	copy.Spec.SecurityContext.SeccompProfile.Type = "Localhost"
	assert.Equal(t, "RuntimeDefault", original.Spec.SecurityContext.SeccompProfile.Type)

	copy.Labels["a"] = "modified"
	assert.Equal(t, "b", original.Labels["a"])
}

func TestSandbox_DeepCopyObject(t *testing.T) {
	s := &Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "s1"}}
	obj := s.DeepCopyObject()
	require.NotNil(t, obj)
	copy, ok := obj.(*Sandbox)
	require.True(t, ok)
	assert.Equal(t, "s1", copy.Name)
}

func TestSandbox_NilSafe(t *testing.T) {
	var s *Sandbox
	assert.Nil(t, s.DeepCopy())
	var sl *SandboxList
	assert.Nil(t, sl.DeepCopy())
}

func TestWorkspace_DeepCopy(t *testing.T) {
	original := &Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1"},
		Spec: WorkspaceSpec{
			Owner:   WorkspaceOwner{UserID: "u1"},
			Storage: WorkspaceStorageConfig{Size: "10Gi"},
			Packages: []WorkspacePackageSet{
				{Runtime: "python:3.11", Requirements: []string{"numpy"}},
			},
			NetworkAccess: &WorkspaceNetworkAccess{
				Egress: []WorkspaceEgressRule{{Domain: "pypi.org"}},
			},
		},
		Status: WorkspaceStatus{
			Phase: WorkspacePhaseActive,
			Conditions: []WorkspaceCondition{
				{Type: WorkspaceConditionReady, Status: "True"},
			},
		},
	}
	copy := original.DeepCopy()
	require.NotNil(t, copy)

	copy.Spec.Packages[0].Requirements[0] = "modified"
	assert.Equal(t, "numpy", original.Spec.Packages[0].Requirements[0])

	copy.Spec.NetworkAccess.Egress[0].Domain = "modified"
	assert.Equal(t, "pypi.org", original.Spec.NetworkAccess.Egress[0].Domain)

	copy.Status.Conditions[0].Status = "False"
	assert.Equal(t, "True", original.Status.Conditions[0].Status)
}

func TestRuntimeEnvironment_DeepCopy(t *testing.T) {
	original := &RuntimeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "py311"},
		Spec: RuntimeEnvironmentSpec{
			Image:                "img",
			PreInstalledPackages: []string{"numpy", "pandas"},
			ResourceRequirements: &RuntimeResourceRequirements{MinCPU: "100m"},
		},
		Status: RuntimeEnvironmentStatus{Available: true},
	}
	copy := original.DeepCopy()
	require.NotNil(t, copy)

	copy.Spec.PreInstalledPackages[0] = "modified"
	assert.Equal(t, "numpy", original.Spec.PreInstalledPackages[0])

	copy.Spec.ResourceRequirements.MinCPU = "1000m"
	assert.Equal(t, "100m", original.Spec.ResourceRequirements.MinCPU)
}

func TestSandboxProfile_DeepCopy(t *testing.T) {
	original := &SandboxProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p1"},
		Spec: SandboxProfileSpec{
			Language: "python",
			NetworkPolicies: []NetworkPolicy{
				{Type: "egress", Rules: []NetworkRule{{Domain: "pypi.org"}}},
			},
			PreInstalledPackages: []string{"numpy"},
			ResourceDefaults:     &ResourceDefaults{CPU: "500m"},
			FilesystemConfig:     &ProfileFilesystemConfig{ReadOnlyPaths: []string{"/etc"}},
		},
	}
	copy := original.DeepCopy()
	require.NotNil(t, copy)

	copy.Spec.NetworkPolicies[0].Rules[0].Domain = "modified"
	assert.Equal(t, "pypi.org", original.Spec.NetworkPolicies[0].Rules[0].Domain)

	copy.Spec.PreInstalledPackages[0] = "modified"
	assert.Equal(t, "numpy", original.Spec.PreInstalledPackages[0])

	copy.Spec.ResourceDefaults.CPU = "1000m"
	assert.Equal(t, "500m", original.Spec.ResourceDefaults.CPU)

	copy.Spec.FilesystemConfig.ReadOnlyPaths[0] = "/modified"
	assert.Equal(t, "/etc", original.Spec.FilesystemConfig.ReadOnlyPaths[0])
}

// TestList_DeepCopy verifies DeepCopy on List types.
func TestList_DeepCopy(t *testing.T) {
	t.Run("SandboxList", func(t *testing.T) {
		l := &SandboxList{Items: []Sandbox{{ObjectMeta: metav1.ObjectMeta{Name: "s1"}}}}
		c := l.DeepCopy()
		c.Items[0].Name = "modified"
		assert.Equal(t, "s1", l.Items[0].Name)
	})
	t.Run("WorkspaceList", func(t *testing.T) {
		l := &WorkspaceList{Items: []Workspace{{ObjectMeta: metav1.ObjectMeta{Name: "w1"}}}}
		c := l.DeepCopy()
		c.Items[0].Name = "modified"
		assert.Equal(t, "w1", l.Items[0].Name)
	})
	t.Run("RuntimeEnvironmentList", func(t *testing.T) {
		l := &RuntimeEnvironmentList{Items: []RuntimeEnvironment{{ObjectMeta: metav1.ObjectMeta{Name: "r1"}}}}
		c := l.DeepCopy()
		c.Items[0].Name = "modified"
		assert.Equal(t, "r1", l.Items[0].Name)
	})
	t.Run("SandboxProfileList", func(t *testing.T) {
		l := &SandboxProfileList{Items: []SandboxProfile{{ObjectMeta: metav1.ObjectMeta{Name: "p1"}}}}
		c := l.DeepCopy()
		c.Items[0].Name = "modified"
		assert.Equal(t, "p1", l.Items[0].Name)
	})
}
