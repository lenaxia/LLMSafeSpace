package resources

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fullyPopulatedWorkspace returns a Workspace with every field set so DeepCopy
// tests can verify independence between original and copy.
func fullyPopulatedWorkspace() *Workspace {
	now := metav1.NewTime(time.Now())
	return &Workspace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "llmsafespace.dev/v1",
			Kind:       "Workspace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-workspace",
			Namespace: "default",
			Labels:    map[string]string{"env": "test"},
		},
		Spec: WorkspaceSpec{
			Owner: WorkspaceOwner{
				UserID: "user-abc",
			},
			DefaultRuntime: "python:3.11",
			SecurityLevel:  "standard",
			Storage: WorkspaceStorageConfig{
				Size:             "10Gi",
				StorageClassName: "fast",
				AccessMode:       "ReadWriteOnce",
			},
			NetworkAccess: &WorkspaceNetworkAccess{
				Egress: []WorkspaceEgressRule{
					{Domain: "pypi.org"},
					{Domain: "files.pythonhosted.org"},
				},
				Ingress: false,
			},
			AutoSuspend: &WorkspaceAutoSuspend{
				Enabled:            true,
				IdleTimeoutSeconds: 1800,
			},
			TTLSecondsAfterSuspended: 86400,
			Packages: []WorkspacePackageSet{
				{
					Runtime:      "python:3.11",
					Requirements: []string{"numpy", "pandas"},
				},
			},
			InitScript:        "echo hello",
			MaxActiveSessions: 3,
			Credentials: &WorkspaceCredentialRef{
				SecretName: "workspace-creds",
			},
		},
		Status: WorkspaceStatus{
			Phase:          WorkspacePhaseActive,
			PVCName:        "workspace-test-workspace",
			ActiveSessions: 2,
			LastActivityAt: &now,
			Conditions: []WorkspaceCondition{
				{
					Type:               WorkspaceConditionReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now,
					Reason:             "PVCBound",
					Message:            "PVC is bound",
				},
				{
					Type:               WorkspaceConditionPVCReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: now,
					Reason:             "Bound",
					Message:            "PVC bound successfully",
				},
			},
			Message:            "workspace is active",
			ObservedGeneration: 3,
		},
	}
}

func TestWorkspaceDeepCopy_FullyPopulated(t *testing.T) {
	original := fullyPopulatedWorkspace()
	copy := original.DeepCopy()

	if copy == original {
		t.Fatal("DeepCopy returned the same pointer as original")
	}

	// Mutate copy and verify original is unchanged.
	copy.Spec.Owner.UserID = "mutated-user"
	if original.Spec.Owner.UserID == "mutated-user" {
		t.Error("mutating copy.Spec.Owner.UserID affected original")
	}

	copy.Spec.Storage.Size = "99Gi"
	if original.Spec.Storage.Size == "99Gi" {
		t.Error("mutating copy.Spec.Storage.Size affected original")
	}

	copy.Spec.NetworkAccess.Egress[0].Domain = "mutated.domain"
	if original.Spec.NetworkAccess.Egress[0].Domain == "mutated.domain" {
		t.Error("mutating copy.Spec.NetworkAccess.Egress[0].Domain affected original")
	}

	copy.Spec.Packages[0].Requirements[0] = "mutated-pkg"
	if original.Spec.Packages[0].Requirements[0] == "mutated-pkg" {
		t.Error("mutating copy.Spec.Packages[0].Requirements[0] affected original")
	}

	copy.Spec.Credentials.SecretName = "mutated-secret"
	if original.Spec.Credentials.SecretName == "mutated-secret" {
		t.Error("mutating copy.Spec.Credentials.SecretName affected original")
	}

	copy.Status.Phase = WorkspacePhaseSuspended
	if original.Status.Phase == WorkspacePhaseSuspended {
		t.Error("mutating copy.Status.Phase affected original")
	}

	copy.Status.Conditions[0].Reason = "mutated-reason"
	if original.Status.Conditions[0].Reason == "mutated-reason" {
		t.Error("mutating copy.Status.Conditions[0].Reason affected original")
	}
}

func TestWorkspaceDeepCopy_NilPointerFields(t *testing.T) {
	w := &Workspace{
		Spec: WorkspaceSpec{
			Owner:       WorkspaceOwner{UserID: "u1"},
			Storage:     WorkspaceStorageConfig{Size: "5Gi"},
			Credentials: nil, // nil pointer — must not panic
		},
		Status: WorkspaceStatus{
			Phase:          WorkspacePhasePending,
			LastActivityAt: nil, // nil pointer — must not panic
		},
	}

	// Must not panic.
	copy := w.DeepCopy()

	if copy.Spec.Credentials != nil {
		t.Error("expected nil Credentials in copy")
	}
	if copy.Status.LastActivityAt != nil {
		t.Error("expected nil LastActivityAt in copy")
	}
}

func TestWorkspaceDeepCopy_NilNetworkAccess(t *testing.T) {
	w := &Workspace{
		Spec: WorkspaceSpec{
			Owner:         WorkspaceOwner{UserID: "u1"},
			Storage:       WorkspaceStorageConfig{Size: "5Gi"},
			NetworkAccess: nil,
			AutoSuspend:   nil,
		},
	}

	copy := w.DeepCopy()

	if copy.Spec.NetworkAccess != nil {
		t.Error("expected nil NetworkAccess in copy")
	}
	if copy.Spec.AutoSuspend != nil {
		t.Error("expected nil AutoSuspend in copy")
	}
}

func TestWorkspaceDeepCopy_EmptyConditionsSlice(t *testing.T) {
	w := &Workspace{
		Status: WorkspaceStatus{
			Conditions: []WorkspaceCondition{},
		},
	}

	copy := w.DeepCopy()

	// Empty slice must copy as empty (not nil).
	if copy.Status.Conditions == nil {
		t.Error("expected empty slice for Conditions, got nil")
	}
	if len(copy.Status.Conditions) != 0 {
		t.Errorf("expected 0 conditions, got %d", len(copy.Status.Conditions))
	}
}

func TestWorkspaceDeepCopy_NilConditionsSlice(t *testing.T) {
	w := &Workspace{
		Status: WorkspaceStatus{
			Conditions: nil,
		},
	}

	copy := w.DeepCopy()

	if copy.Status.Conditions != nil {
		t.Error("expected nil Conditions in copy when original is nil")
	}
}

func TestWorkspaceDeepCopy_NilWorkspace(t *testing.T) {
	var w *Workspace
	result := w.DeepCopy()
	if result != nil {
		t.Error("DeepCopy of nil Workspace should return nil")
	}
}

func TestWorkspaceListDeepCopy_Empty(t *testing.T) {
	wl := &WorkspaceList{
		Items: []Workspace{},
	}

	copy := wl.DeepCopy()

	if copy == wl {
		t.Fatal("DeepCopy returned the same pointer as original")
	}
	if copy.Items == nil {
		t.Error("expected empty Items slice, got nil")
	}
	if len(copy.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(copy.Items))
	}
}

func TestWorkspaceListDeepCopy_NilItems(t *testing.T) {
	wl := &WorkspaceList{}

	copy := wl.DeepCopy()

	if copy.Items != nil {
		t.Error("expected nil Items in copy when original is nil")
	}
}

func TestWorkspaceListDeepCopy_WithItems(t *testing.T) {
	wl := &WorkspaceList{
		Items: []Workspace{
			*fullyPopulatedWorkspace(),
		},
	}

	copy := wl.DeepCopy()

	if len(copy.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(copy.Items))
	}

	// Mutate copy — original must be unaffected.
	copy.Items[0].Spec.Owner.UserID = "mutated"
	if wl.Items[0].Spec.Owner.UserID == "mutated" {
		t.Error("mutating WorkspaceList copy item affected original")
	}
}

func TestWorkspaceListDeepCopy_Nil(t *testing.T) {
	var wl *WorkspaceList
	result := wl.DeepCopy()
	if result != nil {
		t.Error("DeepCopy of nil WorkspaceList should return nil")
	}
}

func TestWorkspaceDeepCopyObject(t *testing.T) {
	w := fullyPopulatedWorkspace()
	obj := w.DeepCopyObject()

	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	wCopy, ok := obj.(*Workspace)
	if !ok {
		t.Fatal("DeepCopyObject did not return *Workspace")
	}
	if wCopy.Name != w.Name {
		t.Errorf("expected Name %q, got %q", w.Name, wCopy.Name)
	}
}

func TestWorkspaceDeepCopyObject_Nil(t *testing.T) {
	var w *Workspace
	obj := w.DeepCopyObject()
	if obj != nil {
		t.Error("DeepCopyObject of nil Workspace should return nil")
	}
}

func TestWorkspaceListDeepCopyObject(t *testing.T) {
	wl := &WorkspaceList{
		Items: []Workspace{*fullyPopulatedWorkspace()},
	}
	obj := wl.DeepCopyObject()

	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	_, ok := obj.(*WorkspaceList)
	if !ok {
		t.Fatal("DeepCopyObject did not return *WorkspaceList")
	}
}

func TestWorkspaceListDeepCopyObject_Nil(t *testing.T) {
	var wl *WorkspaceList
	obj := wl.DeepCopyObject()
	if obj != nil {
		t.Error("DeepCopyObject of nil WorkspaceList should return nil")
	}
}

func TestWorkspacePhaseConstants(t *testing.T) {
	tests := []struct {
		phase    WorkspacePhase
		expected string
	}{
		{WorkspacePhasePending, "Pending"},
		{WorkspacePhaseActive, "Active"},
		{WorkspacePhaseSuspending, "Suspending"},
		{WorkspacePhaseSuspended, "Suspended"},
		{WorkspacePhaseResuming, "Resuming"},
		{WorkspacePhaseTerminating, "Terminating"},
		{WorkspacePhaseTerminated, "Terminated"},
		{WorkspacePhaseFailed, "Failed"},
	}

	for _, tc := range tests {
		if string(tc.phase) != tc.expected {
			t.Errorf("WorkspacePhase %q: expected %q", tc.phase, tc.expected)
		}
	}
}

func TestWorkspaceConditionTypeConstants(t *testing.T) {
	tests := []struct {
		condType WorkspaceConditionType
		expected string
	}{
		{WorkspaceConditionReady, "Ready"},
		{WorkspaceConditionPVCReady, "PVCReady"},
		{WorkspaceConditionSuspended, "Suspended"},
	}

	for _, tc := range tests {
		if string(tc.condType) != tc.expected {
			t.Errorf("WorkspaceConditionType %q: expected %q", tc.condType, tc.expected)
		}
	}
}

func TestWorkspaceDeepCopy_PackagesIndependence(t *testing.T) {
	original := &Workspace{
		Spec: WorkspaceSpec{
			Owner:   WorkspaceOwner{UserID: "u1"},
			Storage: WorkspaceStorageConfig{Size: "5Gi"},
			Packages: []WorkspacePackageSet{
				{Runtime: "python:3.11", Requirements: []string{"numpy", "scipy"}},
				{Runtime: "node:18", Requirements: []string{"express"}},
			},
		},
	}

	copy := original.DeepCopy()

	// Append to copy's package set requirements — original must be unchanged.
	copy.Spec.Packages[0].Requirements = append(copy.Spec.Packages[0].Requirements, "added-pkg")
	if len(original.Spec.Packages[0].Requirements) != 2 {
		t.Errorf("expected 2 requirements in original, got %d", len(original.Spec.Packages[0].Requirements))
	}

	// Append to packages slice — original must be unchanged.
	copy.Spec.Packages = append(copy.Spec.Packages, WorkspacePackageSet{Runtime: "go:1.23"})
	if len(original.Spec.Packages) != 2 {
		t.Errorf("expected 2 package sets in original, got %d", len(original.Spec.Packages))
	}
}

func TestWorkspaceDeepCopy_EgressRulesIndependence(t *testing.T) {
	original := &Workspace{
		Spec: WorkspaceSpec{
			Owner:   WorkspaceOwner{UserID: "u1"},
			Storage: WorkspaceStorageConfig{Size: "5Gi"},
			NetworkAccess: &WorkspaceNetworkAccess{
				Egress: []WorkspaceEgressRule{
					{Domain: "pypi.org"},
				},
			},
		},
	}

	copy := original.DeepCopy()

	// Append to copy's egress — original must be unchanged.
	copy.Spec.NetworkAccess.Egress = append(copy.Spec.NetworkAccess.Egress, WorkspaceEgressRule{Domain: "extra.org"})
	if len(original.Spec.NetworkAccess.Egress) != 1 {
		t.Errorf("expected 1 egress rule in original, got %d", len(original.Spec.NetworkAccess.Egress))
	}
}

func TestWorkspaceDeepCopy_LastActivityAtIndependence(t *testing.T) {
	t1 := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	original := &Workspace{
		Status: WorkspaceStatus{
			LastActivityAt: &t1,
		},
	}

	copy := original.DeepCopy()

	// Mutate copy's LastActivityAt — original must be unchanged.
	t2 := metav1.NewTime(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	copy.Status.LastActivityAt = &t2

	if !original.Status.LastActivityAt.Equal(&t1) {
		t.Error("mutating copy.Status.LastActivityAt affected original")
	}
}
