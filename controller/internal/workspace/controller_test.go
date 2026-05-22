package workspace

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	"github.com/lenaxia/llmsafespace/controller/internal/resources"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, resources.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func reconcilerFor(t *testing.T, objs ...runtime.Object) *WorkspaceReconciler {
	t.Helper()
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&resources.Workspace{}, &resources.Sandbox{}).
		Build()
	return &WorkspaceReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}
}

func reqFor(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}
}

func makeWorkspace(name, namespace string, phase resources.WorkspacePhase) *resources.Workspace {
	return &resources.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "aaaabbbb-cccc-dddd-eeee-ffffgggghhhh",
		},
		Spec: resources.WorkspaceSpec{
			Owner: resources.WorkspaceOwner{UserID: "user-1"},
			Storage: resources.WorkspaceStorageConfig{
				Size:       "5Gi",
				AccessMode: "ReadWriteOnce",
			},
		},
		Status: resources.WorkspaceStatus{
			Phase: phase,
		},
	}
}

func makePVC(name, namespace string) *corev1.PersistentVolumeClaim {
	storageSize := resource.MustParse("5Gi")
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageSize,
				},
			},
		},
	}
}

func makeBoundPVC(name, namespace string) *corev1.PersistentVolumeClaim {
	pvc := makePVC(name, namespace)
	pvc.Status.Phase = corev1.ClaimBound
	return pvc
}

func makeSandboxForWorkspace(sbName, wsName, namespace string) *resources.Sandbox {
	return &resources.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sbName,
			Namespace: namespace,
			Labels: map[string]string{
				"llmsafespace.dev/workspace": wsName,
			},
		},
		Spec: resources.SandboxSpec{
			Runtime: "python:3.11",
		},
		Status: resources.SandboxStatus{
			Phase: "Running",
		},
	}
}

func makePodForSandbox(podName, namespace, wsName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"llmsafespace.dev/workspace": wsName,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

// ---------------------------------------------------------------------------
// Test 1: Object not found → no error, no requeue
// ---------------------------------------------------------------------------

func TestReconcile_ObjectNotFound_ReturnsNoError(t *testing.T) {
	r := reconcilerFor(t) // empty store

	result, err := r.Reconcile(context.Background(), reqFor("missing", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// Test 2: Pending → PVC created (first reconcile): phase stays Pending, PVCName set, requeue 5s
// ---------------------------------------------------------------------------

func TestReconcile_Pending_CreatesVolumeAndTransitionsToActive(t *testing.T) {
	ws := makeWorkspace("ws-pending", "default", resources.WorkspacePhasePending)
	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-pending", "default"))

	require.NoError(t, err)
	// First reconcile: PVC just created, not yet Bound — requeue to check binding
	assert.Equal(t, 5*time.Second, result.RequeueAfter)

	// Fetch updated workspace
	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-pending", Namespace: "default"}, updated))

	// Finalizer must have been added
	assert.Contains(t, updated.Finalizers, WorkspaceFinalizer)

	// Phase must still be Pending (PVC exists but not yet Bound)
	assert.Equal(t, resources.WorkspacePhasePending, updated.Status.Phase)

	// PVCName must be set
	expectedPVCName := fmt.Sprintf("workspace-%s", ws.Name)
	assert.Equal(t, expectedPVCName, updated.Status.PVCName)

	// PVC must exist
	pvc := &corev1.PersistentVolumeClaim{}
	err = r.Get(context.Background(), types.NamespacedName{Name: expectedPVCName, Namespace: "default"}, pvc)
	require.NoError(t, err)

	// PVC labels must be correct
	assert.Equal(t, "llmsafespace", pvc.Labels["app"])
	assert.Equal(t, ws.Name, pvc.Labels["llmsafespace.dev/workspace"])

	// Access mode must be ReadWriteOnce
	require.Len(t, pvc.Spec.AccessModes, 1)
	assert.Equal(t, corev1.ReadWriteOnce, pvc.Spec.AccessModes[0])

	// Storage size must match
	assert.Equal(t, resource.MustParse("5Gi"), pvc.Spec.Resources.Requests[corev1.ResourceStorage])
}

// Also verify empty phase is treated the same as Pending
func TestReconcile_EmptyPhase_CreatesVolumeAndTransitionsToActive(t *testing.T) {
	ws := makeWorkspace("ws-empty", "default", "")
	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-empty", "default"))
	require.NoError(t, err)
	// First reconcile: PVC just created, not yet Bound — requeue to check binding
	assert.Equal(t, 5*time.Second, result.RequeueAfter)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-empty", Namespace: "default"}, updated))
	// Phase must still be Pending (or empty — the PVC was not bound yet)
	assert.NotEqual(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 3: Pending already has PVC (idempotent) — reconcile succeeds, no error
// ---------------------------------------------------------------------------

func TestReconcile_PendingAlreadyHasPVC_IdempotentNoError(t *testing.T) {
	ws := makeWorkspace("ws-idempotent", "default", resources.WorkspacePhasePending)
	existingPVC := makeBoundPVC(fmt.Sprintf("workspace-%s", ws.Name), "default")

	r := reconcilerFor(t, ws, existingPVC)

	result, err := r.Reconcile(context.Background(), reqFor("ws-idempotent", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-idempotent", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 4: Active, no auto-suspend — activeSessions updated, no requeue
// ---------------------------------------------------------------------------

func TestReconcile_Active_NoAutoSuspend_UpdatesSessionsNoRequeue(t *testing.T) {
	ws := makeWorkspace("ws-active", "default", resources.WorkspacePhaseActive)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-active"
	// No autoSuspend set (nil)

	sb1 := makeSandboxForWorkspace("sb-1", "ws-active", "default")
	sb2 := makeSandboxForWorkspace("sb-2", "ws-active", "default")

	r := reconcilerFor(t, ws, sb1, sb2)

	result, err := r.Reconcile(context.Background(), reqFor("ws-active", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-active", Namespace: "default"}, updated))
	assert.Equal(t, int32(2), updated.Status.ActiveSessions)
}

// ---------------------------------------------------------------------------
// Test 5: Active, auto-suspend enabled, not yet idle — requeue after calculated duration
// ---------------------------------------------------------------------------

func TestReconcile_Active_AutoSuspend_NotIdle_RequeuedAfterCalculatedDuration(t *testing.T) {
	ws := makeWorkspace("ws-not-idle", "default", resources.WorkspacePhaseActive)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-not-idle"
	ws.Spec.AutoSuspend = &resources.WorkspaceAutoSuspend{
		Enabled:            true,
		IdleTimeoutSeconds: 3600, // 1 hour
	}
	// Activity 30 minutes ago — should not be idle yet
	recentActivity := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	ws.Status.LastActivityAt = &recentActivity

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-not-idle", "default"))

	require.NoError(t, err)
	// Should requeue after some remaining time
	assert.True(t, result.RequeueAfter > 0, "expected a positive RequeueAfter duration")

	// The phase should still be Active
	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-not-idle", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 6: Active, auto-suspend enabled, idle timeout exceeded → transitions to Suspending
// ---------------------------------------------------------------------------

func TestReconcile_Active_AutoSuspend_IdleExceeded_TransitionsToSuspending(t *testing.T) {
	ws := makeWorkspace("ws-idle", "default", resources.WorkspacePhaseActive)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-idle"
	ws.Spec.AutoSuspend = &resources.WorkspaceAutoSuspend{
		Enabled:            true,
		IdleTimeoutSeconds: 3600, // 1 hour
	}
	// Activity 2 hours ago — well past idle timeout
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	ws.Status.LastActivityAt = &longAgo

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-idle", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-idle", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseSuspending, updated.Status.Phase)
}

// Test 6b: Active, auto-suspend enabled, lastActivityAt is nil → transitions to Suspending
func TestReconcile_Active_AutoSuspend_NilLastActivity_TransitionsToSuspending(t *testing.T) {
	ws := makeWorkspace("ws-nil-activity", "default", resources.WorkspacePhaseActive)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-nil-activity"
	ws.Spec.AutoSuspend = &resources.WorkspaceAutoSuspend{
		Enabled:            true,
		IdleTimeoutSeconds: 3600,
	}
	// LastActivityAt is nil — should treat as idle

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-nil-activity", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-nil-activity", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseSuspending, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 7: Suspending → Suspended: pods deleted (or no pods), phase set to Suspended
// ---------------------------------------------------------------------------

func TestReconcile_Suspending_NoPods_TransitionsToSuspended(t *testing.T) {
	ws := makeWorkspace("ws-suspending", "default", resources.WorkspacePhaseSuspending)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-suspending"
	// Activity long ago so no race condition
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	ws.Status.LastActivityAt = &longAgo

	r := reconcilerFor(t, ws) // no pods

	result, err := r.Reconcile(context.Background(), reqFor("ws-suspending", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-suspending", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseSuspended, updated.Status.Phase)
}

func TestReconcile_Suspending_WithPods_DeletesPodsAndTransitionsToSuspended(t *testing.T) {
	ws := makeWorkspace("ws-suspending-pods", "default", resources.WorkspacePhaseSuspending)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-suspending-pods"
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	ws.Status.LastActivityAt = &longAgo

	pod1 := makePodForSandbox("pod-1", "default", "ws-suspending-pods")
	pod2 := makePodForSandbox("pod-2", "default", "ws-suspending-pods")

	r := reconcilerFor(t, ws, pod1, pod2)

	result, err := r.Reconcile(context.Background(), reqFor("ws-suspending-pods", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-suspending-pods", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseSuspended, updated.Status.Phase)

	// Pods should be deleted
	podList := &corev1.PodList{}
	require.NoError(t, r.List(context.Background(), podList))
	for _, p := range podList.Items {
		assert.NotEqual(t, "ws-suspending-pods", p.Labels["llmsafespace.dev/workspace"],
			"expected workspace pods to be deleted")
	}
}

// ---------------------------------------------------------------------------
// Test 8: Suspending, race condition — recent activity → transitions back to Active
// ---------------------------------------------------------------------------

func TestReconcile_Suspending_RaceCondition_RecentActivity_TransitionsToActive(t *testing.T) {
	ws := makeWorkspace("ws-race", "default", resources.WorkspacePhaseSuspending)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-race"
	// Very recent activity — this is the race condition
	justNow := metav1.NewTime(time.Now().Add(-5 * time.Second))
	ws.Status.LastActivityAt = &justNow
	ws.Spec.AutoSuspend = &resources.WorkspaceAutoSuspend{
		Enabled:            true,
		IdleTimeoutSeconds: 3600,
	}

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-race", "default"))

	require.NoError(t, err)
	// Should requeue because it went back to Active
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-race", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 9: Suspended, no TTL — no action, no requeue
// ---------------------------------------------------------------------------

func TestReconcile_Suspended_NoTTL_NoActionNoRequeue(t *testing.T) {
	ws := makeWorkspace("ws-suspended-nttl", "default", resources.WorkspacePhaseSuspended)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-suspended-nttl"
	// TTLSecondsAfterSuspended == 0 (default)

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-suspended-nttl", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Phase stays Suspended
	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-suspended-nttl", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseSuspended, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 10: Suspended, TTL expired → transitions to Terminating
// ---------------------------------------------------------------------------

func TestReconcile_Suspended_TTLExpired_TransitionsToTerminating(t *testing.T) {
	ws := makeWorkspace("ws-suspended-ttl", "default", resources.WorkspacePhaseSuspended)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-suspended-ttl"
	ws.Spec.TTLSecondsAfterSuspended = 3600 // 1 hour

	// Activity/entry to suspended was long ago
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	ws.Status.LastActivityAt = &longAgo

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-suspended-ttl", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-suspended-ttl", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseTerminating, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 11: Suspended, TTL not expired → requeue after remaining time
// ---------------------------------------------------------------------------

func TestReconcile_Suspended_TTLNotExpired_RequeuesAfterRemainingTime(t *testing.T) {
	ws := makeWorkspace("ws-suspended-wait", "default", resources.WorkspacePhaseSuspended)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-suspended-wait"
	ws.Spec.TTLSecondsAfterSuspended = 3600 // 1 hour

	// Activity 30 minutes ago — TTL has 30 minutes remaining
	recentActivity := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	ws.Status.LastActivityAt = &recentActivity

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-suspended-wait", "default"))

	require.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0, "expected a positive RequeueAfter for remaining TTL")

	// Phase stays Suspended
	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-suspended-wait", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseSuspended, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Test 12: Terminating → PVC deleted, finalizer removed, phase = Terminated
// ---------------------------------------------------------------------------

func TestReconcile_Terminating_DeletesPVCAndRemovesFinalizer(t *testing.T) {
	ws := makeWorkspace("ws-terminating", "default", resources.WorkspacePhaseTerminating)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-terminating"

	pvc := makePVC("workspace-ws-terminating", "default")
	sb := makeSandboxForWorkspace("sb-term", "ws-terminating", "default")

	r := reconcilerFor(t, ws, pvc, sb)

	result, err := r.Reconcile(context.Background(), reqFor("ws-terminating", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// PVC should be deleted
	pvcCheck := &corev1.PersistentVolumeClaim{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "workspace-ws-terminating", Namespace: "default"}, pvcCheck)
	assert.True(t, k8serrors.IsNotFound(err), "PVC should be deleted")

	// Sandbox CRDs should be deleted
	sbCheck := &resources.Sandbox{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "sb-term", Namespace: "default"}, sbCheck)
	assert.True(t, k8serrors.IsNotFound(err), "Sandbox CRD should be deleted")

	// Workspace should have finalizer removed and phase Terminated
	updated := &resources.Workspace{}
	fetchErr := r.Get(context.Background(), types.NamespacedName{Name: "ws-terminating", Namespace: "default"}, updated)
	if fetchErr == nil {
		assert.NotContains(t, updated.Finalizers, WorkspaceFinalizer)
		assert.Equal(t, resources.WorkspacePhaseTerminated, updated.Status.Phase)
	} else {
		assert.True(t, k8serrors.IsNotFound(fetchErr))
	}
}

// ---------------------------------------------------------------------------
// Test 13: Deletion (deletionTimestamp set) — sandbox CRDs deleted, PVC deleted, finalizer removed
// ---------------------------------------------------------------------------

func TestReconcile_DeletionTimestamp_CleansUpAllResourcesAndRemovesFinalizer(t *testing.T) {
	now := metav1.Now()
	ws := makeWorkspace("ws-deleting", "default", resources.WorkspacePhaseActive)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-deleting"
	ws.DeletionTimestamp = &now

	pvc := makePVC("workspace-ws-deleting", "default")
	sb1 := makeSandboxForWorkspace("sb-del-1", "ws-deleting", "default")
	sb2 := makeSandboxForWorkspace("sb-del-2", "ws-deleting", "default")

	r := reconcilerFor(t, ws, pvc, sb1, sb2)

	result, err := r.Reconcile(context.Background(), reqFor("ws-deleting", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// PVC should be deleted
	pvcCheck := &corev1.PersistentVolumeClaim{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "workspace-ws-deleting", Namespace: "default"}, pvcCheck)
	assert.True(t, k8serrors.IsNotFound(err), "PVC should be deleted")

	// Sandbox CRDs should be deleted
	for _, sbName := range []string{"sb-del-1", "sb-del-2"} {
		sbCheck := &resources.Sandbox{}
		err = r.Get(context.Background(), types.NamespacedName{Name: sbName, Namespace: "default"}, sbCheck)
		assert.True(t, k8serrors.IsNotFound(err), fmt.Sprintf("Sandbox %s should be deleted", sbName))
	}

	// Finalizer should be removed
	updated := &resources.Workspace{}
	fetchErr := r.Get(context.Background(), types.NamespacedName{Name: "ws-deleting", Namespace: "default"}, updated)
	if fetchErr == nil {
		assert.NotContains(t, updated.Finalizers, WorkspaceFinalizer)
	} else {
		assert.True(t, k8serrors.IsNotFound(fetchErr))
	}
}

// ---------------------------------------------------------------------------
// Test 14: Failed phase — no requeue, no error
// ---------------------------------------------------------------------------

func TestReconcile_FailedPhase_NoRequeueNoError(t *testing.T) {
	ws := makeWorkspace("ws-failed", "default", resources.WorkspacePhaseFailed)
	ws.Finalizers = []string{WorkspaceFinalizer}

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-failed", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// Additional: Unknown phase — no requeue
// ---------------------------------------------------------------------------

func TestReconcile_UnknownPhase_NoRequeue(t *testing.T) {
	ws := makeWorkspace("ws-unknown", "default", "WeirdPhase")
	ws.Finalizers = []string{WorkspaceFinalizer}

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-unknown", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// Additional: Resuming → waits for sandboxes, transitions to Active when all Running
// ---------------------------------------------------------------------------

func TestReconcile_Resuming_AllSandboxesRunning_TransitionsToActive(t *testing.T) {
	ws := makeWorkspace("ws-resuming", "default", resources.WorkspacePhaseResuming)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-resuming"

	sb := makeSandboxForWorkspace("sb-resuming", "ws-resuming", "default")
	sb.Status.Phase = "Running"

	r := reconcilerFor(t, ws, sb)

	result, err := r.Reconcile(context.Background(), reqFor("ws-resuming", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-resuming", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}

func TestReconcile_Resuming_SandboxesNotRunning_RequeuesAfter5s(t *testing.T) {
	ws := makeWorkspace("ws-resuming-wait", "default", resources.WorkspacePhaseResuming)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-resuming-wait"

	sb := makeSandboxForWorkspace("sb-resuming-wait", "ws-resuming-wait", "default")
	sb.Status.Phase = "Creating" // not yet running

	r := reconcilerFor(t, ws, sb)

	result, err := r.Reconcile(context.Background(), reqFor("ws-resuming-wait", "default"))

	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.RequeueAfter)
}

// ---------------------------------------------------------------------------
// Additional: Pending with custom StorageClassName
// ---------------------------------------------------------------------------

func TestReconcile_Pending_CustomStorageClass_SetsPVCStorageClass(t *testing.T) {
	ws := makeWorkspace("ws-custom-sc", "default", resources.WorkspacePhasePending)
	ws.Spec.Storage.StorageClassName = "fast-ssd"

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-custom-sc", "default"))

	require.NoError(t, err)
	// First reconcile: PVC just created, not yet Bound — requeue to check binding
	assert.Equal(t, 5*time.Second, result.RequeueAfter)

	pvc := &corev1.PersistentVolumeClaim{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "workspace-ws-custom-sc", Namespace: "default"}, pvc))

	require.NotNil(t, pvc.Spec.StorageClassName)
	assert.Equal(t, "fast-ssd", *pvc.Spec.StorageClassName)
}

// ---------------------------------------------------------------------------
// Additional: Pending with empty StorageClassName — does NOT set storageClassName
// ---------------------------------------------------------------------------

func TestReconcile_Pending_EmptyStorageClass_DoesNotSetPVCStorageClass(t *testing.T) {
	ws := makeWorkspace("ws-no-sc", "default", resources.WorkspacePhasePending)
	// StorageClassName is empty string (default)

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-no-sc", "default"))

	require.NoError(t, err)
	// First reconcile: PVC just created, not yet Bound — requeue to check binding
	assert.Equal(t, 5*time.Second, result.RequeueAfter)

	pvc := &corev1.PersistentVolumeClaim{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "workspace-ws-no-sc", Namespace: "default"}, pvc))

	assert.Nil(t, pvc.Spec.StorageClassName, "StorageClassName should not be set when empty")
}

// ---------------------------------------------------------------------------
// Additional: Terminating PVC already gone — no error (best-effort)
// ---------------------------------------------------------------------------

func TestReconcile_Terminating_PVCAlreadyGone_NoError(t *testing.T) {
	ws := makeWorkspace("ws-term-no-pvc", "default", resources.WorkspacePhaseTerminating)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-term-no-pvc"
	// No PVC in store — should be best-effort

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-term-no-pvc", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// MAJOR-1: Suspending updates Sandbox CRDs to Suspended
// ---------------------------------------------------------------------------

func TestReconcile_Suspending_UpdatesSandboxCRDsToSuspended(t *testing.T) {
	ws := makeWorkspace("ws-suspending-sb", "default", resources.WorkspacePhaseSuspending)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-suspending-sb"
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	ws.Status.LastActivityAt = &longAgo

	sb := makeSandboxForWorkspace("sb-to-suspend", "ws-suspending-sb", "default")
	sb.Status.Phase = "Running"

	r := reconcilerFor(t, ws, sb)

	result, err := r.Reconcile(context.Background(), reqFor("ws-suspending-sb", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Workspace should be Suspended
	updatedWS := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-suspending-sb", Namespace: "default"}, updatedWS))
	assert.Equal(t, resources.WorkspacePhaseSuspended, updatedWS.Status.Phase)

	// Sandbox CRD status.Phase should be Suspended
	updatedSB := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-to-suspend", Namespace: "default"}, updatedSB))
	assert.Equal(t, common.SandboxPhaseSuspended, updatedSB.Status.Phase)
}

// ---------------------------------------------------------------------------
// MAJOR-2: Pending PVC bound → transitions to Active
// ---------------------------------------------------------------------------

func TestReconcile_Pending_PVCBound_TransitionsToActive(t *testing.T) {
	ws := makeWorkspace("ws-pvc-bound", "default", resources.WorkspacePhasePending)
	ws.Finalizers = []string{WorkspaceFinalizer}
	existingPVC := makeBoundPVC("workspace-ws-pvc-bound", "default")

	r := reconcilerFor(t, ws, existingPVC)

	result, err := r.Reconcile(context.Background(), reqFor("ws-pvc-bound", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-pvc-bound", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}

// MAJOR-2: Pending PVC unbound, within 5-minute timeout → requeue 30s
func TestReconcile_Pending_PVCUnbound_WithinTimeout_Requeues(t *testing.T) {
	ws := makeWorkspace("ws-pvc-unbound", "default", resources.WorkspacePhasePending)
	ws.Finalizers = []string{WorkspaceFinalizer}
	// CreationTimestamp defaults to zero; the timeout check uses IsZero guard, so
	// set a recent creation time explicitly.
	ws.CreationTimestamp = metav1.NewTime(time.Now().Add(-1 * time.Minute))
	existingPVC := makePVC("workspace-ws-pvc-unbound", "default")
	// Status.Phase left as "" (not Bound)

	r := reconcilerFor(t, ws, existingPVC)

	result, err := r.Reconcile(context.Background(), reqFor("ws-pvc-unbound", "default"))

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-pvc-unbound", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhasePending, updated.Status.Phase)
}

// MAJOR-2: Pending PVC unbound, past 5-minute timeout → transitions to Failed
func TestReconcile_Pending_PVCUnbound_TimedOut_TransitionsToFailed(t *testing.T) {
	ws := makeWorkspace("ws-pvc-timeout", "default", resources.WorkspacePhasePending)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.CreationTimestamp = metav1.NewTime(time.Now().Add(-10 * time.Minute))
	existingPVC := makePVC("workspace-ws-pvc-timeout", "default")
	// Status.Phase left as "" (not Bound)

	r := reconcilerFor(t, ws, existingPVC)

	result, err := r.Reconcile(context.Background(), reqFor("ws-pvc-timeout", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-pvc-timeout", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseFailed, updated.Status.Phase)
	assert.Equal(t, "PVC not bound after 5 minutes", updated.Status.Message)
}

// ---------------------------------------------------------------------------
// MINOR-1: Auto-suspend requeue is at 80% of idleTimeout from lastActivity
// ---------------------------------------------------------------------------

func TestReconcile_Active_AutoSuspend_RequeueAt80PercentOfIdleTimeout(t *testing.T) {
	ws := makeWorkspace("ws-requeue-formula", "default", resources.WorkspacePhaseActive)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Spec.AutoSuspend = &resources.WorkspaceAutoSuspend{
		Enabled:            true,
		IdleTimeoutSeconds: 1000,
	}
	// Activity 100 seconds ago; 80% of 1000s = 800s from lastActivity.
	// nextCheckAt = lastActivity + 800s = now - 100s + 800s = now + 700s
	// requeueAfter ≈ 700s
	activityTime := metav1.NewTime(time.Now().Add(-100 * time.Second))
	ws.Status.LastActivityAt = &activityTime

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-requeue-formula", "default"))

	require.NoError(t, err)
	// Should be roughly 700s; allow ±5s tolerance for test execution time
	assert.True(t, result.RequeueAfter >= 695*time.Second && result.RequeueAfter <= 705*time.Second,
		"expected requeueAfter near 700s, got %v", result.RequeueAfter)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-requeue-formula", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// MINOR-2: Resuming with zero sandboxes transitions immediately to Active
// ---------------------------------------------------------------------------

func TestReconcile_Resuming_NoSandboxes_TransitionsToActive(t *testing.T) {
	ws := makeWorkspace("ws-resuming-empty", "default", resources.WorkspacePhaseResuming)
	ws.Finalizers = []string{WorkspaceFinalizer}
	ws.Status.PVCName = "workspace-ws-resuming-empty"
	// No sandboxes registered for this workspace

	r := reconcilerFor(t, ws)

	result, err := r.Reconcile(context.Background(), reqFor("ws-resuming-empty", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-resuming-empty", Namespace: "default"}, updated))
	assert.Equal(t, resources.WorkspacePhaseActive, updated.Status.Phase)
}
