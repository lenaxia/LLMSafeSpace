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

// On first reconcile, a Sandbox with WorkspaceRef set must have the
// workspace label propagated to its own metadata.labels. Otherwise the
// workspace controller's listSandboxesForWorkspace selector cannot find
// it, so updateSandboxesToSuspended is a silent no-op on suspend.
func TestReconcile_Pending_PropagatesWorkspaceLabel(t *testing.T) {
	ws := makeWorkspace("my-ws", "default", "pvc-my-ws")
	sb := makeSandbox("sb-needs-label", "default", common.SandboxPhasePending)
	sb.Spec.WorkspaceRef = "my-ws"
	// no labels on sb yet — controller should add one

	r := reconcilerFor(t, sb, ws)

	_, err := r.Reconcile(context.Background(), reqFor("sb-needs-label", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-needs-label", Namespace: "default"}, updated))
	got, ok := updated.Labels[common.LabelWorkspace]
	require.True(t, ok, "Sandbox must have %s label after first reconcile", common.LabelWorkspace)
	assert.Equal(t, "my-ws", got)
}

// When the parent workspace is in Suspending phase, a sandbox whose pod
// has been deleted (NotFound) must transition to Suspended, not Failed.
// This avoids a phase-label race between the workspace controller and the
// sandbox controller during suspend.
func TestHandleRunning_PodGone_WorkspaceSuspending_SetsSuspended(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-susp", Namespace: "default"},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "u1"},
			Storage: v1.WorkspaceStorageConfig{Size: "1Gi"},
		},
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspending},
	}
	sb := makeSandbox("sb-running", "default", common.SandboxPhaseRunning)
	sb.Spec.WorkspaceRef = "ws-susp"
	// Add the workspace label up front so the propagation step doesn't
	// short-circuit the test.
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-susp"}
	// status.podName references a pod that does NOT exist
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"

	r := reconcilerFor(t, sb, ws)

	_, err := r.Reconcile(context.Background(), reqFor("sb-running", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-running", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseSuspended, updated.Status.Phase,
		"missing-pod under a Suspending workspace must yield Suspended, not Failed")
}

// Pod NotFound when the parent workspace is NOT suspending → still Failed.
// This is the regression check that the suspend-aware path doesn't conflate
// active-workspace pod-loss with workspace-driven pod deletion.
//
// Post-fix #2 the first occurrence reverts to Pending (not Failed). Multi-loss
// terminal failure is covered in transient_failure_test.go. The point of this
// test in the workspace-label suite is to confirm an Active parent workspace
// does NOT take the suspend-precedence branch.
func TestHandleRunning_PodGone_WorkspaceActive_FirstTransient_RevertsToPending(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-act", Namespace: "default"},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "u1"},
			Storage: v1.WorkspaceStorageConfig{Size: "1Gi"},
		},
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	}
	sb := makeSandbox("sb-crashed", "default", common.SandboxPhaseRunning)
	sb.Spec.WorkspaceRef = "ws-act"
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-act"}
	sb.Status.PodName = "missing-pod"
	sb.Status.PodNamespace = "default"

	r := reconcilerFor(t, sb, ws)

	_, err := r.Reconcile(context.Background(), reqFor("sb-crashed", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-crashed", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhasePending, updated.Status.Phase,
		"missing-pod under an Active workspace must self-heal to Pending (fix #2), not Failed nor Suspended")
	assert.Equal(t, int32(1), updated.Status.TransientFailureCount,
		"transient counter must increment on first occurrence")
	_ = corev1.PodRunning // keep import
}

// Running pod transitions to Failed phase mid-reconcile — same suspend
// awareness applies. Without this, even briefly-flapping pods during
// suspend would mark the sandbox Failed.
func TestHandleRunning_PodFailedPhase_WorkspaceSuspending_SetsSuspended(t *testing.T) {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-flap", Namespace: "default"},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "u1"},
			Storage: v1.WorkspaceStorageConfig{Size: "1Gi"},
		},
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseSuspending},
	}
	sb := makeSandbox("sb-flap", "default", common.SandboxPhaseRunning)
	sb.Spec.WorkspaceRef = "ws-flap"
	sb.Labels = map[string]string{common.LabelWorkspace: "ws-flap"}
	sb.Status.PodName = "pod-flap"
	sb.Status.PodNamespace = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-flap", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	}

	r := reconcilerFor(t, sb, ws, pod)

	_, err := r.Reconcile(context.Background(), reqFor("sb-flap", "default"))
	require.NoError(t, err)

	updated := &v1.Sandbox{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "sb-flap", Namespace: "default"}, updated))
	assert.Equal(t, common.SandboxPhaseSuspended, updated.Status.Phase)
}
