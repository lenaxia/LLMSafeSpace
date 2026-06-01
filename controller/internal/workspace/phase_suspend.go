package workspace

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func (r *WorkspaceReconciler) handleSuspending(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	r.deletePodByName(ctx, name, workspace.Namespace)

	now := metav1.Now()
	workspace.Status.Phase = v1.WorkspacePhaseSuspended
	workspace.Status.PodName = ""
	workspace.Status.PodNamespace = ""
	workspace.Status.PodIP = ""
	workspace.Status.Endpoint = ""
	workspace.Status.SuspendedAt = &now
	workspace.Status.TransientFailureCount = 0
	workspace.Status.Sessions = nil
	workspace.Status.ActiveSessions = 0
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

func (r *WorkspaceReconciler) handleSuspended(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	if workspace.Spec.TTLSecondsAfterSuspended <= 0 || workspace.Status.SuspendedAt == nil {
		return ctrl.Result{}, nil
	}
	elapsed := time.Since(workspace.Status.SuspendedAt.Time)
	ttl := time.Duration(workspace.Spec.TTLSecondsAfterSuspended) * time.Second
	if elapsed >= ttl {
		workspace.Status.Phase = v1.WorkspacePhaseTerminating
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}
	return ctrl.Result{RequeueAfter: ttl - elapsed}, nil
}

func (r *WorkspaceReconciler) handleResuming(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	// Ensure password secret exists (may have been cleaned up).
	if err := r.ensurePasswordSecret(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	workspace.Status.SuspendedAt = nil
	// Reset idle clock: the workspace was idle before suspension, but the
	// resume action itself counts as user activity. Without this, handleActive
	// would compare LastActivityAt (pre-suspend) against now and immediately
	// re-suspend the workspace.
	now := metav1.Now()
	workspace.Status.LastActivityAt = &now
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}
