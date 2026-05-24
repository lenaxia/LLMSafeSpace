package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// Tests for fix #2: transient pod absence reverts to Pending; only Failed
// after MaxTransientFailures consecutive losses.
//
// Behaviour contract (see design/SANDBOX-LIFECYCLE.md §4.1):
//   - Pod missing while phase=Running, parent workspace NOT suspending,
//     TransientFailureCount < MaxTransientFailures
//        → revert phase to Pending, increment TransientFailureCount,
//          increment RestartCount, set LastTransientFailureAt
//   - Same conditions, TransientFailureCount == MaxTransientFailures
//        → mark phase Failed (no further retries; recovery via /retry, fix #5)
//   - Sandbox stays in Running for >= TransientFailureResetWindow
//        → reset TransientFailureCount to 0 (incident considered resolved)

// ---------------------------------------------------------------------------
// happy path: first transient loss reverts to Pending
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_PodMissing_FirstTransient_RevertsToPending(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-trans1", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	sb.Status.TransientFailureCount = 0
	sb.Status.RestartCount = 0

	r := reconcilerFor(t, sb)

	_, err := r.Reconcile(context.Background(), reqFor("sb-trans1", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-trans1", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase,
		"first transient pod-loss must revert to Pending, not Failed")
	assert.Equal(t, int32(1), updated.Status.TransientFailureCount,
		"TransientFailureCount must increment by 1")
	assert.Equal(t, int32(1), updated.Status.RestartCount,
		"RestartCount must increment by 1 on every restart cause")
	require.NotNil(t, updated.Status.LastTransientFailureAt,
		"LastTransientFailureAt must be set to track reset window")
	assert.WithinDuration(t, time.Now(), updated.Status.LastTransientFailureAt.Time, 5*time.Second)

	// PodName / PodIP should be cleared so the next Pending reconcile creates
	// a fresh pod rather than trying to attach to the ghost.
	assert.Empty(t, updated.Status.PodName, "PodName must be cleared so handlePending creates a fresh pod")
	assert.Empty(t, updated.Status.PodIP, "PodIP must be cleared")
}

// ---------------------------------------------------------------------------
// happy path: second transient loss still reverts (count = 2 < 3)
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_PodMissing_SecondTransient_RevertsToPending(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-trans2", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	sb.Status.TransientFailureCount = 1 // already had one
	sb.Status.RestartCount = 1

	r := reconcilerFor(t, sb)
	_, err := r.Reconcile(context.Background(), reqFor("sb-trans2", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-trans2", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase)
	assert.Equal(t, int32(2), updated.Status.TransientFailureCount)
	assert.Equal(t, int32(2), updated.Status.RestartCount)
}

// ---------------------------------------------------------------------------
// unhappy path: Nth (= MaxTransientFailures) loss marks Failed terminally
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_PodMissing_AtThreshold_MarksFailed(t *testing.T) {
	now := metav1.Now()
	sb := makeSandbox("sb-trans-fail", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	// Already at the threshold from prior recoveries.
	sb.Status.TransientFailureCount = int32(common.MaxTransientFailures - 1) // 2 prior; this is the 3rd
	sb.Status.RestartCount = 2

	r := reconcilerFor(t, sb)
	_, err := r.Reconcile(context.Background(), reqFor("sb-trans-fail", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-trans-fail", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhaseFailed, updated.Status.Phase,
		"Nth transient pod-loss (N=MaxTransientFailures) must mark Failed terminally")
	assert.Equal(t, int32(common.MaxTransientFailures), updated.Status.TransientFailureCount,
		"TransientFailureCount must equal MaxTransientFailures at terminal failure")

	// Conditions must reflect the persistent failure.
	var foundReady, foundPodRunning bool
	for _, c := range updated.Status.Conditions {
		if c.Type == common.ConditionReady {
			foundReady = true
			assert.Equal(t, "False", c.Status)
			assert.Equal(t, common.ReasonPodPersistentLoss, c.Reason,
				"terminal failure must use ReasonPodPersistentLoss, not ReasonPodNotRunning")
		}
		if c.Type == common.ConditionPodRunning {
			foundPodRunning = true
			assert.Equal(t, "False", c.Status)
		}
	}
	assert.True(t, foundReady, "Ready condition must be set on terminal failure")
	assert.True(t, foundPodRunning, "PodRunning condition must be set on terminal failure")
}

// ---------------------------------------------------------------------------
// unhappy path: parent workspace suspending → Suspended (NOT transient retry)
// This branch was already correct pre-fix; the new code must not regress it.
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_PodMissing_ParentSuspending_MarksSuspended(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-susp", Namespace: "default"},
		Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspending},
	}
	sb := makeSandbox("sb-susp-tr", "default", common.SandboxPhaseRunning)
	sb.Spec.WorkspaceRef = "ws-susp"
	// Pre-set the workspace label to avoid the requeue branch in Reconcile.
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-susp"}
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.TransientFailureCount = 1 // even with prior transients

	r := reconcilerFor(t, sb, ws)
	_, err := r.Reconcile(context.Background(), reqFor("sb-susp-tr", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-susp-tr", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhaseSuspended, updated.Status.Phase,
		"workspace-suspending must take precedence over transient retry")
	// TransientFailureCount must not have been changed.
	assert.Equal(t, int32(1), updated.Status.TransientFailureCount,
		"workspace-suspending path must not touch TransientFailureCount")
}

// ---------------------------------------------------------------------------
// happy path: pod is healthy, last transient was > reset window ago
//   → TransientFailureCount resets to 0
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_StableForResetWindow_ResetsTransientCount(t *testing.T) {
	now := metav1.Now()
	longAgo := metav1.NewTime(time.Now().Add(-time.Duration(common.TransientFailureResetWindow+60) * time.Second))

	sb := makeSandbox("sb-reset", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "healthy-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	sb.Status.TransientFailureCount = 2
	sb.Status.LastTransientFailureAt = &longAgo

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "healthy-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	_, err := r.Reconcile(context.Background(), reqFor("sb-reset", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-reset", Namespace: "default"}, updated))

	assert.Equal(t, common.SandboxPhaseRunning, updated.Status.Phase)
	assert.Equal(t, int32(0), updated.Status.TransientFailureCount,
		"TransientFailureCount must reset after stable Running window")
	assert.Nil(t, updated.Status.LastTransientFailureAt,
		"LastTransientFailureAt must be cleared on reset")
}

// ---------------------------------------------------------------------------
// unhappy path: pod healthy but stable < reset window → no reset yet
// ---------------------------------------------------------------------------

func TestHandleRunningSandbox_StableUnderResetWindow_KeepsTransientCount(t *testing.T) {
	now := metav1.Now()
	recent := metav1.NewTime(time.Now().Add(-30 * time.Second)) // well under window

	sb := makeSandbox("sb-no-reset", "default", common.SandboxPhaseRunning)
	sb.Finalizers = []string{common.SandboxFinalizer}
	sb.Status.PodName = "healthy-pod"
	sb.Status.PodNamespace = "default"
	sb.Status.StartTime = &now
	sb.Status.TransientFailureCount = 2
	sb.Status.LastTransientFailureAt = &recent

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "healthy-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	r := reconcilerFor(t, sb, pod)
	_, err := r.Reconcile(context.Background(), reqFor("sb-no-reset", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-no-reset", Namespace: "default"}, updated))

	assert.Equal(t, int32(2), updated.Status.TransientFailureCount,
		"TransientFailureCount must NOT reset before window elapses")
	require.NotNil(t, updated.Status.LastTransientFailureAt)
	assert.Equal(t, recent.Time.Unix(), updated.Status.LastTransientFailureAt.Time.Unix())
}

// ---------------------------------------------------------------------------
// e2e (table-driven) — covers the contract from one place
// ---------------------------------------------------------------------------

func TestRunningSandbox_PodAbsence_PhaseTransitionMatrix(t *testing.T) {
	type result struct {
		phase    string
		count    int32
		restarts int32
	}
	now := metav1.Now()

	cases := []struct {
		name             string
		priorTransient   int32
		parentSuspending bool
		want             result
	}{
		{"first loss, parent active", 0, false, result{phase: common.SandboxPhasePending, count: 1, restarts: 1}},
		{"second loss, parent active", 1, false, result{phase: common.SandboxPhasePending, count: 2, restarts: 1}},
		{"third loss = threshold, parent active", 2, false, result{phase: common.SandboxPhaseFailed, count: 3, restarts: 0}},
		{"parent suspending wins, count untouched", 1, true, result{phase: common.SandboxPhaseSuspended, count: 1, restarts: 0}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := makeSandbox("sb-x", "default", common.SandboxPhaseRunning)
			sb.Finalizers = []string{common.SandboxFinalizer}
			sb.Status.PodName = "missing"
			sb.Status.PodNamespace = "default"
			sb.Status.StartTime = &now
			sb.Status.TransientFailureCount = tc.priorTransient

			seed := []runtime.Object{sb}
			if tc.parentSuspending {
				ws := &v1.Workspace{
					ObjectMeta: metav1.ObjectMeta{Name: "ws-x", Namespace: "default"},
					Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspending},
				}
				sb.Spec.WorkspaceRef = "ws-x"
				// Pre-set the workspace label so Reconcile doesn't take the
				// label-add-and-requeue branch (controller.go:87-100). That
				// branch would shortcut before reaching the phase handler.
				sb.Labels = map[string]string{common.LabelWorkspace: "ws-x"}
				seed = append(seed, ws)
			}

			r := reconcilerFor(t, seed...)
			_, err := r.Reconcile(context.Background(), reqFor("sb-x", "default"))
			require.NoError(t, err)

			updated := &v1.Sandbox{}
			require.NoError(t, r.Get(context.Background(),
				types.NamespacedName{Name: "sb-x", Namespace: "default"}, updated))

			assert.Equal(t, tc.want.phase, updated.Status.Phase, "phase")
			assert.Equal(t, tc.want.count, updated.Status.TransientFailureCount, "transient count")
			assert.Equal(t, tc.want.restarts, updated.Status.RestartCount, "restart count delta")
		})
	}
}
