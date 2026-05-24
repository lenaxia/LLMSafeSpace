package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// Tests for fix #5: Failed sandbox reset to Pending via
// POST /sandboxes/:id/retry (API) → controller reconciles Pending → Creating.

func TestReconcile_FailedResetToPending_CreatesNewPod(t *testing.T) {
	sb := makeSandbox("sb-retry-e2e", "default", common.SandboxPhasePending)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.RestartCount = 1
	sb.Status.PodName = ""
	sb.Status.PodNamespace = ""

	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-retry-e2e", "default"))
	require.NoError(t, err)
	assert.True(t, result.Requeue || result.RequeueAfter > 0,
		"Pending sandbox must requeue for pod creation")

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-retry-e2e", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhaseCreating, updated.Status.Phase,
		"Pending must transition to Creating")
	assert.NotEmpty(t, updated.Status.PodName,
		"Creating must assign a pod name")
}

func TestReconcile_FailedSandbox_StaysFailed(t *testing.T) {
	sb := makeSandbox("sb-stay-failed", "default", common.SandboxPhaseFailed)
	sb.Finalizers = []string{common.SandboxFinalizer}

	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-stay-failed", "default"))
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result, "Failed is terminal; no requeue")

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-stay-failed", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseFailed, updated.Status.Phase)
}

func TestReconcile_FailedResetToPending_FullLifecycleToRunning(t *testing.T) {
	sb := makeSandbox("sb-lifecycle", "default", common.SandboxPhasePending)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.RestartCount = 1

	r := reconcilerFor(t, sb)

	result, err := r.Reconcile(context.Background(), reqFor("sb-lifecycle", "default"))
	require.NoError(t, err)
	assert.True(t, result.Requeue || result.RequeueAfter > 0)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-lifecycle", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseCreating, updated.Status.Phase)
	podName := updated.Status.PodName
	require.NotEmpty(t, podName)

	existingPod := &corev1.Pod{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: podName, Namespace: "default"}, existingPod))
	existingPod.Status.Phase = corev1.PodRunning
	existingPod.Status.PodIP = "10.0.1.2"
	require.NoError(t, r.Status().Update(context.Background(), existingPod))

	result, err = r.Reconcile(context.Background(), reqFor("sb-lifecycle", "default"))
	require.NoError(t, err)

	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-lifecycle", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase,
		"Creating with running pod must transition to Running")
	assert.Equal(t, "10.0.1.2", updated.Status.PodIP)
}
