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
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func (r *WorkspaceReconciler) recoverFromTransientPodLoss(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	workspace.Status.TransientFailureCount++
	now := metav1.Now()
	workspace.Status.LastTransientFailureAt = &now

	maxRetries := int32(MaxTransientFailures)
	if workspace.Spec.MaxRetries > 0 {
		maxRetries = workspace.Spec.MaxRetries
	}

	if workspace.Status.TransientFailureCount >= maxRetries {
		markFailed(workspace, v1.FailureReasonTransientPodLoss, "pod lost %d times; marking failed", workspace.Status.TransientFailureCount)
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	// Self-heal: revert to Creating.
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	workspace.Status.PodIP = ""
	workspace.Status.Endpoint = ""
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

func (r *WorkspaceReconciler) maybeResetTransientCounter(workspace *v1.Workspace) {
	if workspace.Status.TransientFailureCount == 0 {
		return
	}
	if workspace.Status.LastTransientFailureAt == nil {
		return
	}
	elapsed := time.Since(workspace.Status.LastTransientFailureAt.Time)
	if elapsed > time.Duration(TransientFailureResetWindow)*time.Second {
		workspace.Status.TransientFailureCount = 0
		workspace.Status.LastTransientFailureAt = nil
	}
}

func (r *WorkspaceReconciler) handleFailed(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Still respect declarative restartGeneration bumps.
	if workspace.Spec.RestartGeneration > workspace.Status.ObservedRestartGeneration {
		logger.Info("RestartGeneration bumped on Failed workspace; transitioning to Pending",
			"gen", workspace.Spec.RestartGeneration,
			"observed", workspace.Status.ObservedRestartGeneration)
		r.cleanupFailedWorkspaceSecrets(ctx, workspace)
		workspace.Status.Phase = v1.WorkspacePhasePending
		workspace.Status.Message = ""
		workspace.Status.FailureReason = v1.FailureReasonNone
		workspace.Status.PodName = ""
		workspace.Status.PodNamespace = ""
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		workspace.Status.TransientFailureCount = 0
		workspace.Status.LastTransientFailureAt = nil
		workspace.Status.RestartCount++
		workspace.Status.ObservedRestartGeneration = workspace.Spec.RestartGeneration
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, pod)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		logger.Info("Workspace Failed; no pod exists, retrying")
		r.cleanupFailedWorkspaceSecrets(ctx, workspace)
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		workspace.Status.TransientFailureCount = 0
		workspace.Status.Message = ""
		workspace.Status.FailureReason = v1.FailureReasonNone
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
		ready := false
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}
		if ready {
			logger.Info("Workspace Failed but pod is Running/Ready; self-healing to Active")
			now := metav1.Now()
			workspace.Status.Phase = v1.WorkspacePhaseActive
			workspace.Status.PodName = pod.Name
			workspace.Status.PodNamespace = pod.Namespace
			workspace.Status.PodIP = pod.Status.PodIP
			workspace.Status.Endpoint = fmt.Sprintf("http://%s:4096", pod.Status.PodIP)
			workspace.Status.ImageTag = imageTagFromPod(pod)
			workspace.Status.StartTime = &now
			workspace.Status.TransientFailureCount = 0
			workspace.Status.ConsecutiveHealthFailures = 0
			workspace.Status.LastTransientFailureAt = nil
			workspace.Status.Message = ""
			workspace.Status.FailureReason = v1.FailureReasonNone
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
	}

	logger.Info("Workspace Failed; pod not healthy, deleting and retrying", "podPhase", pod.Status.Phase)
	r.deletePodByName(ctx, name, workspace.Namespace)
	r.cleanupFailedWorkspaceSecrets(ctx, workspace)
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	workspace.Status.PodIP = ""
	workspace.Status.Endpoint = ""
	workspace.Status.TransientFailureCount = 0
	workspace.Status.Message = ""
	workspace.Status.FailureReason = v1.FailureReasonNone
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

// --- Pod management helpers ---
