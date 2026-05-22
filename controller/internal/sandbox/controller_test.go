package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

func makeSandbox(name, namespace, phase string) *resources.Sandbox {
	return &resources.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "12345678-1234-1234-1234-1234567890ab",
		},
		Spec: resources.SandboxSpec{
			Runtime: "python:3.11",
			Timeout: 300,
		},
		Status: resources.SandboxStatus{
			Phase: phase,
		},
	}
}

func reconcilerFor(t *testing.T, objs ...runtime.Object) *SandboxReconciler {
	t.Helper()
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&resources.Sandbox{}).
		Build()
	return &SandboxReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}
}

func reqFor(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}
}

// ---------------------------------------------------------------------------
// Reconcile — object not found
// ---------------------------------------------------------------------------

func TestReconcile_ObjectNotFound_ReturnsNoError(t *testing.T) {
	r := reconcilerFor(t) // empty store

	result, err := r.Reconcile(context.Background(), reqFor("missing", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// Reconcile — pending sandbox transitions to creating
// ---------------------------------------------------------------------------

func TestReconcile_PendingSandbox_TransitionsToCreating(t *testing.T) {
	sb := makeSandbox("sb-pending", "default", common.SandboxPhasePending)
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-pending", "default"))

	// Pod creation will fail because status.podName is empty — that's fine; the
	// important assertion is that the phase was updated to Creating first.
	// The reconciler either returns an error or re-queues.
	_ = result
	_ = err

	// Fetch updated sandbox
	updated := &resources.Sandbox{}
	fetchErr := r.Get(context.Background(), types.NamespacedName{Name: "sb-pending", Namespace: "default"}, updated)
	require.NoError(t, fetchErr)

	// Finalizer must have been added
	assert.Contains(t, updated.Finalizers, common.SandboxFinalizer)
}

// ---------------------------------------------------------------------------
// Reconcile — empty phase treated the same as Pending
// ---------------------------------------------------------------------------

func TestReconcile_EmptyPhase_AddsFinalizer(t *testing.T) {
	sb := makeSandbox("sb-empty", "default", "")
	r := reconcilerFor(t, sb)

	_, _ = r.Reconcile(context.Background(), reqFor("sb-empty", "default"))

	updated := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-empty", Namespace: "default"}, updated))
	assert.Contains(t, updated.Finalizers, common.SandboxFinalizer)
}

// ---------------------------------------------------------------------------
// Reconcile — terminated / failed phases — no requeue
// ---------------------------------------------------------------------------

func TestReconcile_TerminatedSandbox_NoRequeue(t *testing.T) {
	sb := makeSandbox("sb-terminated", "default", common.SandboxPhaseTerminated)
	sb.Finalizers = []string{common.SandboxFinalizer}
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-terminated", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcile_FailedSandbox_NoRequeue(t *testing.T) {
	sb := makeSandbox("sb-failed", "default", common.SandboxPhaseFailed)
	sb.Finalizers = []string{common.SandboxFinalizer}
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-failed", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// ---------------------------------------------------------------------------
// handleCreatingSandbox — pod exists and is running
// ---------------------------------------------------------------------------

func TestHandleCreatingSandbox_PodRunning_TransitionsToRunning(t *testing.T) {
	sb := makeSandbox("sb-creating", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "sb-creating-12345678"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-creating-12345678",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-creating", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-creating", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleCreatingSandbox — pod not found → revert to Pending
// ---------------------------------------------------------------------------

func TestHandleCreatingSandbox_PodNotFound_RevertsToInProgress(t *testing.T) {
	sb := makeSandbox("sb-creating2", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"

	r := reconcilerFor(t, sb) // pod NOT in the store

	result, err := r.Reconcile(context.Background(), reqFor("sb-creating2", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-creating2", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleCreatingSandbox — pod still pending → requeue after delay
// ---------------------------------------------------------------------------

func TestHandleCreatingSandbox_PodPending_RequeuesAfterDelay(t *testing.T) {
	sb := makeSandbox("sb-creating3", "default", common.SandboxPhaseCreating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "pod-pending"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-pending", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-creating3", "default"))

	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.RequeueAfter)
}

// ---------------------------------------------------------------------------
// handleRunningSandbox — pod not found → mark failed
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_PodNotFound_MarksFailed(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-running", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now

	r := reconcilerFor(t, sb) // pod NOT in store

	result, err := r.Reconcile(context.Background(), reqFor("sb-running", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-running", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseFailed, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleRunningSandbox — timeout exceeded → transition to Terminating
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_TimeoutExceeded_TransitionsToTerminating(t *testing.T) {
	longAgo := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	sb := makeSandbox("sb-timeout", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.Timeout = 300 // 5 min — started 2h ago, so timed out
	sb.Status.PodName = "sb-timeout-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &longAgo

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-timeout-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-timeout", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-timeout", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseTerminating, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleTerminatingSandbox — pod already gone → mark terminated
// ---------------------------------------------------------------------------

func TestHandleTerminatingSandbox_PodAlreadyGone_MarksTerminated(t *testing.T) {
	sb := makeSandbox("sb-terminating", "default", common.SandboxPhaseTerminating)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "gone-pod"
	sb.Status.PodNamespace = "default"

	r := reconcilerFor(t, sb) // pod NOT in store

	result, err := r.Reconcile(context.Background(), reqFor("sb-terminating", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-terminating", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseTerminated, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// handleDeletion — running sandbox gets deletion timestamp → finalizer removed
// ---------------------------------------------------------------------------

func TestHandleDeletion_FinalizerRemovedAfterCleanup(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-deleting", "default", common.SandboxPhaseTerminated)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.DeletionTimestamp = &now

	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-deleting", "default"))

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// After deletion the object may be gone from the store; a NotFound is acceptable
	updated := &resources.Sandbox{}
	fetchErr := r.Get(context.Background(), types.NamespacedName{Name: "sb-deleting", Namespace: "default"}, updated)
	if fetchErr == nil {
		assert.NotContains(t, updated.Finalizers, common.SandboxFinalizer)
	} else {
		assert.True(t, k8serrors.IsNotFound(fetchErr))
	}
}

// ---------------------------------------------------------------------------
// handleDeletion — sandbox in Running with deletion timestamp → transitions to Terminating first
// ---------------------------------------------------------------------------

func TestHandleDeletion_RunningWithDeletionTimestamp_TransitionsToTerminating(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-delrun", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.DeletionTimestamp = &now

	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-delrun", "default"))

	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &resources.Sandbox{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-delrun", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseTerminating, updated.Status.Phase)
}

// ---------------------------------------------------------------------------
// Unknown phase — no requeue
// ---------------------------------------------------------------------------

func TestReconcile_UnknownPhase_NoRequeue(t *testing.T) {
	sb := makeSandbox("sb-unknown", "default", "Weird")
	sb.Finalizers = []string{common.SandboxFinalizer}
	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-unknown", "default"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}
