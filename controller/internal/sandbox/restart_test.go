package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// Tests for fix #1: POST /sandboxes/:id/restart → Spec.RestartGeneration
// incremented → controller detects spec > status → graceful pod delete →
// revert to Pending.

// ---------------------------------------------------------------------------
// happy path: restart generation increased → pod deleted, phase → Pending
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_RestartGeneration_DeletesPodAndRevertsToPending(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-restart", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.RestartGeneration = 100
	sb.Status.ObservedRestartGeneration = 50 // spec > status → restart
	sb.Status.PodName = "restart-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.PodIP = "10.0.0.1"
	sb.Status.StartTime = &now
	sb.Status.RestartCount = 2

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "restart-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	result, err := r.Reconcile(context.Background(), reqFor("sb-restart", "default"))
	require.NoError(t, err)
	assert.True(t, result.Requeue, "restart must request immediate requeue for pod recreation")

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-restart", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase,
		"restart must revert phase to Pending")
	assert.Equal(t, int64(100), updated.Status.ObservedRestartGeneration,
		"ObservedRestartGeneration must be updated to match spec")
	assert.Equal(t, int32(3), updated.Status.RestartCount,
		"RestartCount must increment")
	assert.Empty(t, updated.Status.PodName, "PodName must be cleared")
	assert.Empty(t, updated.Status.PodIP, "PodIP must be cleared")

	// Pod should be deleted.
	deletedPod := &corev1.Pod{}
	err = r.Get(context.Background(),
		types.NamespacedName{Name: "restart-pod", Namespace: "default"}, deletedPod)
	assert.Error(t, err, "pod must be deleted after restart")
}

// ---------------------------------------------------------------------------
// happy path: restart generation equal → no restart (idempotent)
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_RestartGeneration_Equal_NoRestart(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-noop", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.RestartGeneration = 100
	sb.Status.ObservedRestartGeneration = 100 // equal → no restart
	sb.Status.PodName = "healthy-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "healthy-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	_, err := r.Reconcile(context.Background(), reqFor("sb-noop", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-noop", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase,
		"equal restart generation must not trigger restart")
}

// ---------------------------------------------------------------------------
// edge case: restart requested but pod already gone (e.g. race with transient)
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_RestartGeneration_PodAlreadyGone(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-restart-nopod", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.RestartGeneration = 200
	sb.Status.ObservedRestartGeneration = 100
	sb.Status.PodName = "ghost-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now

	// No pod object in the fake client — pod is already gone.
	r := reconcilerFor(t, sb)
	result, err := r.Reconcile(context.Background(), reqFor("sb-restart-nopod", "default"))
	require.NoError(t, err)
	assert.True(t, result.Requeue)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-restart-nopod", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase,
		"restart with missing pod must still revert to Pending")
	assert.Equal(t, int64(200), updated.Status.ObservedRestartGeneration)
}

// ---------------------------------------------------------------------------
// unhappy path: restart generation zero on both sides → no restart
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_RestartGeneration_BothZero_NoRestart(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-zero", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Spec.RestartGeneration = 0
	sb.Status.ObservedRestartGeneration = 0
	sb.Status.PodName = "healthy-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "healthy-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	_, err := r.Reconcile(context.Background(), reqFor("sb-zero", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-zero", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase,
		"both zero must not trigger restart")
}
