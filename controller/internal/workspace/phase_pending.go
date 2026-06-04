package workspace

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func (r *WorkspaceReconciler) handlePending(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if common.AddFinalizer(workspace, WorkspaceFinalizer) {
		if err := r.Update(ctx, workspace); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Ensure PVC.
	pvcName := fmt.Sprintf("workspace-%s", workspace.Name)
	existingPVC := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: workspace.Namespace}, existingPVC)

	if err == nil {
		if r.isPVCStale(existingPVC, workspace) {
			logger.Info("Deleting stale PVC", "pvc", pvcName, "reason", "owner mismatch or terminating")
			if delErr := r.Delete(ctx, existingPVC); delErr != nil {
				return ctrl.Result{}, delErr
			}
			err = errors.NewNotFound(corev1.Resource("persistentvolumeclaims"), pvcName)
		}
	}

	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if r.pendingTimedOut(workspace) {
			// PVC doesn't exist yet — retry PVC creation with backoff.
			// Don't call enterRecovery (which sets phase=Creating) because
			// handleCreating would attempt buildPod without a PVC.
			workspace.Status.ConsecutiveFailures++
			workspace.Status.LastFailureClass = string(FailureClassInfrastructure)
			now := metav1.Now()
			workspace.Status.LastFailureAt = &now
			policy := recoveryPolicies[FailureClassInfrastructure]
			backoff := calculateBackoff(workspace.Status.ConsecutiveFailures, policy)
			if backoff > 0 {
				nextRetry := metav1.NewTime(now.Add(backoff))
				workspace.Status.NextRetryAt = &nextRetry
			}
			workspace.CreationTimestamp = now // reset timeout clock
			return ctrl.Result{RequeueAfter: backoff}, r.Status().Update(ctx, workspace)
		}
		newPVC := r.buildPVC(workspace, pvcName)
		if err := controllerutil.SetControllerReference(workspace, newPVC, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, newPVC); err != nil {
			if errors.IsAlreadyExists(err) {
				return ctrl.Result{RequeueAfter: requeueCreating}, nil
			}
			return ctrl.Result{}, err
		}
		workspace.Status.PVCName = pvcName
		if err := r.Status().Update(ctx, workspace); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
	}

	// PVC exists — check if bound.
	if existingPVC.Status.Phase != corev1.ClaimBound {
		if r.pvcUsesWaitForFirstConsumer(ctx, existingPVC) {
			// WaitForFirstConsumer: PVC won't bind until pod mounts it.
			// Transition to Creating so pod gets created.
			workspace.Status.PVCName = pvcName
			workspace.Status.Phase = v1.WorkspacePhaseCreating
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		if r.pendingTimedOut(workspace) {
			return r.enterRecovery(ctx, workspace, FailureClassInfrastructure)
		}
		return ctrl.Result{RequeueAfter: requeueActive}, nil
	}

	// PVC bound — ensure password secret, then transition to Creating.
	if err := r.ensurePasswordSecret(ctx, workspace); err != nil {
		logger.Error(err, "Failed to ensure password secret")
		return ctrl.Result{}, err
	}

	// Set the PendingAt anchor on first entry so the controller can measure
	// end-to-end create latency. Prefer the AnnotationRequestedAt written by
	// the API (user-perceived start time); fall back to now (controller-first-
	// reconcile) if the annotation is absent or unparseable.
	if workspace.Status.PendingAt == nil {
		anchor := metav1.Now()
		if raw, ok := workspace.Annotations[v1.AnnotationRequestedAt]; ok {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				anchor = metav1.NewTime(t)
			}
		}
		workspace.Status.PendingAt = &anchor
	}

	workspace.Status.PVCName = pvcName
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}
