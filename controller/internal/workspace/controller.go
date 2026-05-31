// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/agentd"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

type WorkspaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// HostResolver is used by the per-workspace NetworkPolicy generator
	// (network_policy.go) to resolve declared FQDNs to /32 ipBlocks at
	// reconcile time. Tests inject a stub; production uses
	// defaultHostResolver (net.DefaultResolver) when nil.
	HostResolver HostResolver
}

func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("workspace", req.NamespacedName)

	workspace := &v1.Workspace{}
	if err := r.Get(ctx, req.NamespacedName, workspace); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !workspace.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, workspace)
	}

	switch workspace.Status.Phase {
	case "", v1.WorkspacePhasePending:
		return r.handlePending(ctx, workspace)
	case v1.WorkspacePhaseCreating:
		return r.handleCreating(ctx, workspace)
	case v1.WorkspacePhaseActive:
		return r.handleActive(ctx, workspace)
	case v1.WorkspacePhaseSuspending:
		return r.handleSuspending(ctx, workspace)
	case v1.WorkspacePhaseSuspended:
		return r.handleSuspended(ctx, workspace)
	case v1.WorkspacePhaseResuming:
		return r.handleResuming(ctx, workspace)
	case v1.WorkspacePhaseTerminating:
		return r.handleTerminating(ctx, workspace)
	case v1.WorkspacePhaseFailed:
		// Clean up associated K8s Secrets so a Failed workspace does
		// not leak workspace-creds-* / workspace-pw-* / workspace-secrets-*
		// indefinitely (Bug 12 in worklog 0085). The cleanup helpers
		// are no-ops if the Secret is already gone, so this is safe to
		// call on every reconcile.
		r.cleanupFailedWorkspaceSecrets(ctx, workspace)

		// Epic 21 Change A — declarative recovery from Failed.
		// If the operator (or API) bumps spec.restartGeneration past
		// status.observedRestartGeneration, treat that as a request
		// to retry: clear stale status fields and walk back to
		// Pending so handlePending re-creates PVC + password Secret +
		// pod from scratch. handlePending is idempotent and creates
		// missing resources, so it doesn't matter that
		// cleanupFailedWorkspaceSecrets just deleted the password
		// Secret — handlePending will recreate it (with a freshly
		// generated password, which is the desired security posture
		// after a failure).
		if workspace.Spec.RestartGeneration > workspace.Status.ObservedRestartGeneration {
			logger.Info("RestartGeneration bumped on Failed workspace; transitioning to Pending",
				"gen", workspace.Spec.RestartGeneration,
				"observed", workspace.Status.ObservedRestartGeneration)
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

		logger.V(1).Info("Workspace in Failed phase; bump spec.restartGeneration to retry")
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown workspace phase", "phase", workspace.Status.Phase)
		return ctrl.Result{}, nil
	}
}

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
			markFailed(workspace, v1.FailureReasonPendingTimeout, "workspace timed out in Pending phase")
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
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
			markFailed(workspace, v1.FailureReasonPVCBindTimeout, "PVC not bound after timeout")
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		return ctrl.Result{RequeueAfter: requeueActive}, nil
	}

	// PVC bound — ensure password secret, then transition to Creating.
	if err := r.ensurePasswordSecret(ctx, workspace); err != nil {
		logger.Error(err, "Failed to ensure password secret")
		return ctrl.Result{}, err
	}

	workspace.Status.PVCName = pvcName
	workspace.Status.Phase = v1.WorkspacePhaseCreating
	return ctrl.Result{}, r.Status().Update(ctx, workspace)
}

func (r *WorkspaceReconciler) handleCreating(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	// Check if pod already exists.
	existingPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, existingPod)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// Ensure per-workspace egress NetworkPolicy BEFORE pod creation
		// (F1.2.4 / G4 part 2). Built from spec.networkAccess.egress;
		// no-op when the field is nil/empty (chart-wide policy applies).
		// Failure is non-fatal: if DNS is flaky we still want the pod
		// to come up; the next reconcile will retry.
		if err := r.ensureWorkspaceEgressNetworkPolicy(ctx, workspace); err != nil {
			logger.Error(err, "Failed to ensure per-workspace egress NetworkPolicy (continuing)")
		}
		// Pod doesn't exist — create it.
		pod, buildErr := r.buildPod(ctx, workspace)
		if buildErr != nil {
			logger.Error(buildErr, "Failed to build pod")
			markFailed(workspace, v1.FailureReasonPodBuildFailed, "pod build failed: %v", buildErr)
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
		if err := controllerutil.SetControllerReference(workspace, pod, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, pod); err != nil {
			if errors.IsAlreadyExists(err) {
				return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		workspace.Status.PodName = pod.Name
		workspace.Status.PodNamespace = pod.Namespace
		if err := r.Status().Update(ctx, workspace); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
	}

	// Delete ephemeral secrets as soon as init containers complete (minimize etcd exposure).
	if allInitContainersComplete(existingPod) {
		r.deleteEphemeralSecretsSecret(ctx, workspace)
	}

	// Pod exists — check if running.

	// US-23.1: A pod with DeletionTimestamp set is being terminated (e.g.,
	// the controller itself just deleted it via checkAgentHealth). Its
	// Status.Phase is unreliable during this window — a SIGKILLed container
	// makes the pod briefly observable as Failed. Wait for it to finish
	// terminating rather than misclassifying it as a genuine failure.
	if isPodTerminating(existingPod) {
		logger.V(1).Info("Pod is terminating; waiting for reaping", "pod", existingPod.Name)
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
	}

	if existingPod.Status.Phase == corev1.PodRunning && existingPod.Status.PodIP != "" {
		now := metav1.Now()
		workspace.Status.Phase = v1.WorkspacePhaseActive
		workspace.Status.PodName = existingPod.Name
		workspace.Status.PodNamespace = existingPod.Namespace
		workspace.Status.PodIP = existingPod.Status.PodIP
		workspace.Status.ImageTag = imageTagFromPod(existingPod)
		workspace.Status.Endpoint = fmt.Sprintf("http://%s:4096", existingPod.Status.PodIP)
		workspace.Status.StartTime = &now
		workspace.Status.Message = ""
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	if existingPod.Status.Phase == corev1.PodFailed {
		markFailed(workspace, v1.FailureReasonPodFailedDuringCreation, "pod entered Failed phase during creation")
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	return ctrl.Result{RequeueAfter: requeueCreating}, nil
}

func (r *WorkspaceReconciler) handleActive(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	// Refresh per-workspace egress NetPol on every Active reconcile so
	// (a) spec.networkAccess.egress changes take effect without a pod
	// restart, (b) DNS-resolved /32 ipBlocks self-refresh as CDN IPs
	// rotate, and (c) toggling NetworkAccess off cleanly deletes the
	// policy. Failure is non-fatal — log and continue. (F1.2.4 / G4
	// part 2 — validator pass 2 catch.)
	if err := r.ensureWorkspaceEgressNetworkPolicy(ctx, workspace); err != nil {
		logger.Error(err, "Failed to refresh per-workspace egress NetworkPolicy (continuing)")
	}

	// Check restart generation.
	if workspace.Spec.RestartGeneration > workspace.Status.ObservedRestartGeneration {
		logger.Info("Restart generation bumped; deleting pod", "gen", workspace.Spec.RestartGeneration)
		r.deletePodByName(ctx, name, workspace.Namespace)
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		workspace.Status.RestartCount++
		workspace.Status.ObservedRestartGeneration = workspace.Spec.RestartGeneration
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	// Check pod exists and is running.
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, pod)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// Pod missing — transient recovery.
		return r.recoverFromTransientPodLoss(ctx, workspace)
	}

	// US-23.1: A pod with DeletionTimestamp set is being terminated by the
	// controller itself (e.g., checkAgentHealth deleted it). Transition to
	// Creating so a new pod is built once the old one is reaped. Do NOT
	// count this as a transient failure — the controller initiated the delete.
	if isPodTerminating(pod) {
		logger.V(1).Info("Pod is terminating in Active phase; transitioning to Creating", "pod", pod.Name)
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		return ctrl.Result{RequeueAfter: requeueCreating}, r.Status().Update(ctx, workspace)
	}

	if pod.Status.Phase != corev1.PodRunning {
		return r.recoverFromTransientPodLoss(ctx, workspace)
	}

	// Detect architecture drift: if the running pod's nodeSelector doesn't
	// match the desired architecture, delete the pod so it gets recreated
	// on a node with the correct arch. Skip if pod has no nodeSelector
	// (legacy pod created before multi-arch support).
	desiredArch := workspace.Spec.Architecture
	if desiredArch == "" {
		desiredArch = "amd64"
	}
	if pod.Spec.NodeSelector != nil && pod.Spec.NodeSelector["kubernetes.io/arch"] != desiredArch {
		logger.Info("Architecture changed; recreating pod", "desired", desiredArch, "current", pod.Spec.NodeSelector["kubernetes.io/arch"])
		r.deletePodByName(ctx, name, workspace.Namespace)
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		return ctrl.Result{}, r.Status().Update(ctx, workspace)
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return r.recoverFromTransientPodLoss(ctx, workspace)
		}
	}

	// Clean up ephemeral secrets Secret (safety net — should already be deleted in handleCreating).
	r.deleteEphemeralSecretsSecret(ctx, workspace)

	// Pod running — check timeout.
	if workspace.Spec.Timeout > 0 && workspace.Status.StartTime != nil {
		elapsed := time.Since(workspace.Status.StartTime.Time)
		if elapsed > time.Duration(workspace.Spec.Timeout)*time.Second {
			logger.Info("Pod timeout exceeded; suspending")
			workspace.Status.Phase = v1.WorkspacePhaseSuspending
			return ctrl.Result{}, r.Status().Update(ctx, workspace)
		}
	}

	if workspace.Status.PodIP != "" && workspace.Status.StartTime != nil {
		if workspace.Status.LastHealthCheckAt != nil && workspace.Status.LastHealthCheckAt.Before(workspace.Status.StartTime) {
			workspace.Status.ConsecutiveHealthFailures = 0
			workspace.Status.LastHealthCheckAt = nil
		}
	}

	// Check idle auto-suspend.
	if workspace.Spec.AutoSuspend != nil && workspace.Spec.AutoSuspend.Enabled {
		timeout := workspace.Spec.AutoSuspend.IdleTimeoutSeconds
		if timeout <= 0 {
			timeout = 86400
		}
		if workspace.Status.LastActivityAt != nil {
			idle := time.Since(workspace.Status.LastActivityAt.Time)
			if idle > time.Duration(timeout)*time.Second {
				logger.Info("Workspace idle timeout exceeded; suspending",
					"lastActivity", workspace.Status.LastActivityAt, "idle", idle, "timeout", time.Duration(timeout)*time.Second)
				workspace.Status.Phase = v1.WorkspacePhaseSuspending
				return ctrl.Result{}, r.Status().Update(ctx, workspace)
			}
		}
	}

	// Reset transient failure counter if stable long enough.
	r.maybeResetTransientCounter(workspace)
	// Check agent liveness (HTTP to /v1/healthz — rate-limited, cheap).
	r.checkAgentHealth(ctx, workspace)
	// US-22.6: Deep-status enrichment on a slower cadence (60s).
	// Failures do NOT trigger pod restarts.
	r.maybeEnrichAgentStatus(ctx, workspace)

	if err := r.Status().Update(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueActive}, nil
}

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

func (r *WorkspaceReconciler) handleTerminating(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	// Delete pod.
	r.deletePodByName(ctx, name, workspace.Namespace)

	// Delete PVC.
	if workspace.Status.PVCName != "" {
		pvc := &corev1.PersistentVolumeClaim{}
		pvc.Name = workspace.Status.PVCName
		pvc.Namespace = workspace.Namespace
		if err := r.Delete(ctx, pvc); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// Delete password secret.
	pwSecret := &corev1.Secret{}
	pwSecret.Name = passwordSecretName(workspace.Name)
	pwSecret.Namespace = workspace.Namespace
	if err := r.Delete(ctx, pwSecret); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	workspace.Status.Phase = v1.WorkspacePhaseTerminated
	workspace.Status.PodName = ""
	workspace.Status.PodIP = ""
	workspace.Status.Endpoint = ""
	workspace.Status.Sessions = nil
	workspace.Status.ActiveSessions = 0
	workspace.Status.DiskUsedBytes = 0
	workspace.Status.DiskTotalBytes = 0
	if err := r.Status().Update(ctx, workspace); err != nil {
		return ctrl.Result{}, err
	}

	common.RemoveFinalizer(workspace, WorkspaceFinalizer)
	return ctrl.Result{}, r.Update(ctx, workspace)
}

func (r *WorkspaceReconciler) handleDeletion(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(workspace, WorkspaceFinalizer) {
		return ctrl.Result{}, nil
	}
	// Reuse terminating logic.
	workspace.Status.Phase = v1.WorkspacePhaseTerminating
	return r.handleTerminating(ctx, workspace)
}

// --- Transient recovery ---

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

// --- Pod management helpers ---

func (r *WorkspaceReconciler) deletePodByName(ctx context.Context, name, namespace string) {
	pod := &corev1.Pod{}
	pod.Name = name
	pod.Namespace = namespace
	_ = r.Delete(ctx, pod)
}

func (r *WorkspaceReconciler) deleteEphemeralSecretsSecret(ctx context.Context, workspace *v1.Workspace) {
	secretName := fmt.Sprintf("workspace-secrets-%s", workspace.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: workspace.Namespace}, secret); err != nil {
		return // doesn't exist, nothing to do
	}
	if err := r.Delete(ctx, secret); err != nil {
		log.FromContext(ctx).V(1).Info("Failed to delete ephemeral secrets secret", "name", secretName, "error", err.Error())
	}
}

// cleanupFailedWorkspaceSecrets deletes the per-workspace K8s Secrets
// (`workspace-creds-*`, `workspace-pw-*`, `workspace-secrets-*`) when a
// workspace has entered the Failed phase. The Secrets are useless once
// the workspace is non-recoverable; leaving them behind is the symptom
// of Bug 12 in worklog 0085 (Secrets persisting 45+ hours after pod
// disappeared). Each Get is best-effort: missing-Secret is the desired
// end state.
func (r *WorkspaceReconciler) cleanupFailedWorkspaceSecrets(ctx context.Context, workspace *v1.Workspace) {
	logger := log.FromContext(ctx)
	for _, secretName := range []string{
		fmt.Sprintf("workspace-secrets-%s", workspace.Name),
		fmt.Sprintf("workspace-creds-%s", workspace.Name),
		fmt.Sprintf("workspace-pw-%s", workspace.Name),
	} {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: workspace.Namespace}, secret); err != nil {
			continue // already gone or not found
		}
		if err := r.Delete(ctx, secret); err != nil {
			logger.V(1).Info("Failed to delete Secret for Failed workspace",
				"name", secretName, "error", err.Error())
		}
	}
}

func allInitContainersComplete(pod *corev1.Pod) bool {
	if len(pod.Status.InitContainerStatuses) == 0 {
		return false
	}
	for _, s := range pod.Status.InitContainerStatuses {
		if s.State.Terminated == nil || s.State.Terminated.ExitCode != 0 {
			return false
		}
	}
	return true
}

func (r *WorkspaceReconciler) ensurePasswordSecret(ctx context.Context, workspace *v1.Workspace) error {
	name := passwordSecretName(workspace.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, secret); err == nil {
		return nil
	}
	password := common.GenerateRandomString(32)
	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: workspace.Namespace,
		},
		Data: map[string][]byte{"password": []byte(password)},
	}
	if err := controllerutil.SetControllerReference(workspace, newSecret, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, newSecret)
}

// --- PVC helpers ---

func (r *WorkspaceReconciler) isPVCStale(pvc *corev1.PersistentVolumeClaim, workspace *v1.Workspace) bool {
	if !pvc.DeletionTimestamp.IsZero() {
		return true
	}
	if len(pvc.OwnerReferences) > 0 {
		for _, owner := range pvc.OwnerReferences {
			if owner.UID == workspace.UID {
				return false
			}
		}
		return true
	}
	return false
}

func (r *WorkspaceReconciler) pendingTimedOut(workspace *v1.Workspace) bool {
	return !workspace.CreationTimestamp.IsZero() && time.Since(workspace.CreationTimestamp.Time) > pendingPhaseTimeout
}

func (r *WorkspaceReconciler) pvcUsesWaitForFirstConsumer(ctx context.Context, pvc *corev1.PersistentVolumeClaim) bool {
	scName := ""
	if pvc.Spec.StorageClassName != nil {
		scName = *pvc.Spec.StorageClassName
	}
	if scName == "" {
		return false
	}
	sc := &storagev1.StorageClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: scName}, sc); err != nil {
		return false
	}
	if sc.VolumeBindingMode == nil {
		return false
	}
	return *sc.VolumeBindingMode == storagev1.VolumeBindingWaitForFirstConsumer
}

func (r *WorkspaceReconciler) buildPVC(workspace *v1.Workspace, pvcName string) *corev1.PersistentVolumeClaim {
	storageSize := resource.MustParse(workspace.Spec.Storage.Size)
	accessMode := corev1.ReadWriteOnce
	if workspace.Spec.Storage.AccessMode == "ReadWriteMany" {
		accessMode = corev1.ReadWriteMany
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: workspace.Namespace,
			Labels: map[string]string{
				LabelApp:       AppName,
				LabelComponent: ComponentWorkspace,
				LabelWorkspace: workspace.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: storageSize},
			},
		},
	}
	if workspace.Spec.Storage.StorageClassName != "" {
		pvc.Spec.StorageClassName = &workspace.Spec.Storage.StorageClassName
	}
	return pvc
}

// --- Pod building ---

func (r *WorkspaceReconciler) buildPod(ctx context.Context, workspace *v1.Workspace) (*corev1.Pod, error) {
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	runtimeImage, runtimeEnvName, err := resolveRuntimeImage(ctx, r.Client, workspace.Spec.Runtime)
	if err != nil {
		return nil, fmt.Errorf("resolving runtime image: %w", err)
	}

	// F1.4.2 (Epic 17): Read the per-workspace admin token from the
	// password Secret. Used as the `Authorization: Bearer <token>`
	// header for the readiness probe so kubelet can hit the
	// authenticated /v1/readyz endpoint. ensurePasswordSecret() runs
	// in handlePending before buildPod is reached, so the Secret
	// is guaranteed to exist; if Get fails we fall back to omitting
	// the header (probe will fail closed and the pod won't be ready
	// — observable + safe).
	adminToken := ""
	pwSec := &corev1.Secret{}
	if pwErr := r.Get(ctx, types.NamespacedName{Name: passwordSecretName(workspace.Name), Namespace: workspace.Namespace}, pwSec); pwErr == nil {
		if v, ok := pwSec.Data["password"]; ok {
			adminToken = string(v)
		}
	}

	labels := map[string]string{
		LabelApp:       AppName,
		LabelComponent: ComponentWorkspace,
		LabelWorkspace: workspace.Name,
		LabelRuntime:   sanitizeLabelValue(workspace.Spec.Runtime),
	}

	annotations := map[string]string{
		"llmsafespace.dev/created-by": "controller",
	}
	if runtimeEnvName != "" {
		annotations["llmsafespace.dev/runtime-env"] = runtimeEnvName
	}

	trueVal := true
	falseVal := false

	mainContainer := corev1.Container{
		Name:    "workspace",
		Image:   runtimeImage,
		Command: []string{"/usr/local/bin/entrypoint-opencode.sh"},
		Ports: []corev1.ContainerPort{
			{ContainerPort: agentd.AgentPort, Name: "opencode", Protocol: corev1.ProtocolTCP},
			{ContainerPort: agentd.AgentdPort, Name: "agentd", Protocol: corev1.ProtocolTCP},
			{ContainerPort: agentd.AgentdAdminPort, Name: "agentd-admin", Protocol: corev1.ProtocolTCP},
		},
		Env: []corev1.EnvVar{
			{Name: "WORKSPACE_ID", Value: workspace.Name},
			{Name: "WORKSPACE_DIR", Value: agentd.WorkspacePath},
			// F1.4.2 (Epic 17): Bearer token for agentd's /v1/readyz
			// and /v1/statusz endpoints. Sourced from the same per-
			// workspace password Secret the controller already
			// generates. The controller sends this token when polling
			// /v1/statusz; the kubelet's readiness probe must also
			// carry it via httpHeaders (set on the probe spec below).
			{Name: "AGENTD_ADMIN_TOKEN", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: passwordSecretName(workspace.Name),
					},
					Key: "password",
				},
			}},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/v1/readyz",
					Port: intstr.FromInt(agentd.AgentdAdminPort),
					HTTPHeaders: func() []corev1.HTTPHeader {
						if adminToken == "" {
							return nil
						}
						return []corev1.HTTPHeader{
							{Name: "Authorization", Value: "Bearer " + adminToken},
						}
					}(),
				},
			},
			InitialDelaySeconds: 10, PeriodSeconds: 15, TimeoutSeconds: 3, FailureThreshold: 5,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/v1/healthz",
					Port: intstr.FromInt(agentd.AgentdAdminPort),
				},
			},
			InitialDelaySeconds: 15, PeriodSeconds: 30, TimeoutSeconds: 5, FailureThreshold: 6,
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "sandbox-cfg", MountPath: "/sandbox-cfg", ReadOnly: true},
			{Name: "tmp", MountPath: "/tmp"},
			{Name: "sandbox-home", MountPath: "/home/sandbox"},
		},
		Resources: resourceRequirementsFor(workspace),
	}

	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.Status.PVCName},
		}},
		{Name: "sandbox-cfg", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "sandbox-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}

	var initContainers []corev1.Container

	// Workspace setup init (packages + initScript).
	if len(workspace.Spec.Packages) > 0 || workspace.Spec.InitScript != "" {
		initContainers = append(initContainers, buildWorkspaceSetupInit(workspace, runtimeImage))
	}

	// Credential setup init.
	credInit, pwVolume, userSecretsVol, err := r.buildCredentialSetupInit(ctx, workspace, runtimeImage)
	if err != nil {
		return nil, err
	}
	initContainers = append(initContainers, credInit)
	volumes = append(volumes, pwVolume)
	if userSecretsVol != nil {
		volumes = append(volumes, *userSecretsVol)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   workspace.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			InitContainers: initContainers,
			Containers:     []corev1.Container{mainContainer},
			Volumes:        volumes,
			NodeSelector:   buildNodeSelector(workspace),
			// G17 (Epic 17): Sandbox pods MUST NOT automount the default
			// ServiceAccount token. The agent has no business calling the
			// K8s API; mounting the token only widens the blast radius for
			// a compromised sandbox. Without this, kubelet writes a JWT to
			// /var/run/secrets/kubernetes.io/serviceaccount/token that any
			// process inside the pod can read. See
			// `controller/internal/workspace/security_test.go` for the
			// regression that locks this in.
			AutomountServiceAccountToken: &falseVal,
			SecurityContext:              buildPodSecurityContext(workspace),
		},
	}
	return pod, nil
}

// resourceRequirementsFor maps the Workspace's spec.resources to a
// corev1.ResourceRequirements block. Closes F1.2.3 (Epic 17): pre-fix
// the controller never applied the operator-supplied resource limits,
// so workspace pods ran without quota and could DoS the node.
//
// Behavior:
//   - If spec.resources is nil, fall back to a sane default (matches
//     the kubebuilder defaults documented on `WorkspaceSpec`):
//     500m CPU, 512Mi memory, 1Gi ephemeral-storage. This guarantees
//     every workspace carries at least basic limits even when the
//     operator submits a minimal YAML.
//   - Limits and Requests are set to the SAME value (1:1 mapping)
//     because the workspace is a single-tenant interactive
//     environment; QoS=Guaranteed simplifies eviction reasoning.
//   - Quantity parsing failures fall back to the default rather than
//     panicking. The CRD pattern + (future) webhook caps protect
//     against bad input; if both are bypassed (e.g. CRD validation
//     disabled cluster-wide), we degrade gracefully.
func resourceRequirementsFor(workspace *v1.Workspace) corev1.ResourceRequirements {
	const (
		defaultCPU       = "500m"
		defaultMemory    = "512Mi"
		defaultEphemeral = "1Gi"
	)
	cpu := defaultCPU
	memory := defaultMemory
	ephemeral := defaultEphemeral
	if r := workspace.Spec.Resources; r != nil {
		if r.CPU != "" {
			cpu = r.CPU
		}
		if r.Memory != "" {
			memory = r.Memory
		}
		if r.EphemeralStorage != "" {
			ephemeral = r.EphemeralStorage
		}
	}
	parseOrDefault := func(s, fallback string) resource.Quantity {
		if q, err := resource.ParseQuantity(s); err == nil {
			return q
		}
		return resource.MustParse(fallback) // defaults are constants; safe
	}
	rl := corev1.ResourceList{
		corev1.ResourceCPU:              parseOrDefault(cpu, defaultCPU),
		corev1.ResourceMemory:           parseOrDefault(memory, defaultMemory),
		corev1.ResourceEphemeralStorage: parseOrDefault(ephemeral, defaultEphemeral),
	}
	return corev1.ResourceRequirements{
		Limits:   rl,
		Requests: rl.DeepCopy(),
	}
}

func buildPodSecurityContext(workspace *v1.Workspace) *corev1.PodSecurityContext {
	runAsUser := int64(1000)
	runAsGroup := int64(1000)
	if psc := workspace.Spec.PodSecurityContext; psc != nil {
		if psc.RunAsUser != 0 {
			runAsUser = psc.RunAsUser
		}
		if psc.RunAsGroup != 0 {
			runAsGroup = psc.RunAsGroup
		}
	}
	return &corev1.PodSecurityContext{
		RunAsUser:  &runAsUser,
		RunAsGroup: &runAsGroup,
		FSGroup:    &runAsGroup,
	}
}

func buildNodeSelector(workspace *v1.Workspace) map[string]string {
	arch := workspace.Spec.Architecture
	if arch == "" {
		arch = "amd64"
	}
	return map[string]string{
		"kubernetes.io/arch": arch,
	}
}

func (r *WorkspaceReconciler) buildCredentialSetupInit(ctx context.Context, workspace *v1.Workspace, runtimeImage string) (corev1.Container, corev1.Volume, *corev1.Volume, error) {
	credScript := `
if [ -f /mnt/secrets/user-secrets/secrets.json ]; then
  cp /mnt/secrets/user-secrets/secrets.json /sandbox-cfg/secrets.json
fi
cp /mnt/secrets/password/password /sandbox-cfg/password
`
	pwVolume := corev1.Volume{
		Name: "pw-secret",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: passwordSecretName(workspace.Name)},
		},
	}

	credMounts := []corev1.VolumeMount{
		{Name: "sandbox-cfg", MountPath: "/sandbox-cfg"},
		{Name: "pw-secret", MountPath: "/mnt/secrets/password", ReadOnly: true},
	}

	// Epic 10: mount user-secrets if the ephemeral Secret exists.
	userSecretsName := fmt.Sprintf("workspace-secrets-%s", workspace.Name)
	userSecretsSecret := &corev1.Secret{}
	var userSecretsVolume *corev1.Volume
	if err := r.Get(ctx, types.NamespacedName{Name: userSecretsName, Namespace: workspace.Namespace}, userSecretsSecret); err == nil {
		v := corev1.Volume{
			Name: "user-secrets",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: userSecretsName},
			},
		}
		userSecretsVolume = &v
		credMounts = append(credMounts, corev1.VolumeMount{
			Name: "user-secrets", MountPath: "/mnt/secrets/user-secrets", ReadOnly: true,
		})
	} else if !errors.IsNotFound(err) {
		return corev1.Container{}, corev1.Volume{}, nil, fmt.Errorf("checking user-secrets secret: %w", err)
	}

	trueVal := true
	falseVal := false
	credInit := corev1.Container{
		Name:    "credential-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", credScript},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: credMounts,
	}
	return credInit, pwVolume, userSecretsVolume, nil
}

func buildWorkspaceSetupInit(workspace *v1.Workspace, runtimeImage string) corev1.Container {
	trueVal := true
	falseVal := false
	return corev1.Container{
		Name:    "workspace-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", buildWorkspaceSetupScript(workspace)},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "tmp", MountPath: "/tmp"},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

// shellQuoteSingle wraps an argument in POSIX single quotes, escaping
// any embedded single-quote bytes via the standard `'\”` pattern.
// The result is safe to pass to /bin/sh as a single positional
// argument: nothing inside the quotes is interpreted by the shell.
//
// Closes F1.2.5 (Epic 17): pre-fix the controller did
//
//	args += " " + req
//	script += "pip install --target=... " + args
//
// which let an adversarial requirement string contain shell
// metacharacters (`;`, `|`, `\“, `$()`) and break out of the pip
// invocation. Post-fix every requirement is wrapped in single quotes,
// so the only thing pip / npm / go install ever sees is the literal
// requirement bytes — which they will reject as a parse error if
// adversarial. Defense in depth: the admission webhook also rejects
// these payloads at CREATE/UPDATE.
func shellQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func buildWorkspaceSetupScript(ws *v1.Workspace) string {
	script := "#!/bin/sh\nset -e\nmkdir -p /workspace/packages\n"
	for _, pkgSet := range ws.Spec.Packages {
		if len(pkgSet.Requirements) == 0 {
			continue
		}
		args := ""
		for _, req := range pkgSet.Requirements {
			args += " " + shellQuoteSingle(req)
		}
		rt := pkgSet.Runtime
		// `--` after the package-manager flags terminates argv parsing,
		// so even if a requirement somehow starts with `-` (admission
		// is normally blocking that), the package manager will treat
		// it as a positional argument and reject it as an unknown
		// package name rather than parsing it as a flag (RCE class —
		// see worklog 0098 / F1.2.5 validator pass 2).
		switch {
		case len(rt) >= 6 && rt[:6] == "nodejs":
			script += "cd /workspace/packages && npm install --" + args + "\n"
		case len(rt) >= 2 && rt[:2] == "go":
			for _, req := range pkgSet.Requirements {
				// `go install` does not support `--`; we rely on the
				// admission webhook + shellQuoteSingle. The webhook
				// rejects leading `-` and URL-shaped strings.
				script += "cd /workspace/packages && go install " + shellQuoteSingle(req) + "\n"
			}
		default:
			script += "pip install --target=/workspace/packages --" + args + "\n"
		}
	}
	if ws.Spec.InitScript != "" {
		// InitScript is ALREADY a multi-line shell payload deliberately
		// authored by the workspace owner. We do NOT shell-quote it (it
		// is meant to BE a script). The here-document delimiter
		// `INITSCRIPT` is literal-quoted so embedded $variables and
		// $(commands) are preserved verbatim. F1.2.5 explicitly does
		// NOT cover InitScript — that is by design a code-execution
		// surface.
		script += "cat > /tmp/init-script.sh << 'INITSCRIPT'\n"
		script += ws.Spec.InitScript + "\n"
		script += "INITSCRIPT\n"
		script += "chmod +x /tmp/init-script.sh\n"
		script += "/tmp/init-script.sh\n"
	}
	return script
}

// --- Setup ---

func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Workspace{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// sanitizeLabelValue replaces characters invalid in K8s label values.
func sanitizeLabelValue(s string) string {
	return strings.ReplaceAll(s, ":", "_")
}

// imageTagFromPod extracts the image tag (portion after the last colon) from
// the first container's image reference. Returns the full image ref if no tag
// separator is found.
func imageTagFromPod(pod *corev1.Pod) string {
	if len(pod.Spec.Containers) == 0 {
		return ""
	}
	image := pod.Spec.Containers[0].Image
	if i := strings.LastIndex(image, ":"); i >= 0 {
		return image[i+1:]
	}
	return image
}

func (r *WorkspaceReconciler) setCondition(ws *v1.Workspace, condType v1.WorkspaceConditionType, status, reason, message string) {
	for i := range ws.Status.Conditions {
		if ws.Status.Conditions[i].Type == condType {
			if ws.Status.Conditions[i].Status == status && ws.Status.Conditions[i].Reason == reason {
				ws.Status.Conditions[i].Message = message
				return
			}
			ws.Status.Conditions[i].Status = status
			ws.Status.Conditions[i].Reason = reason
			ws.Status.Conditions[i].Message = message
			ws.Status.Conditions[i].LastTransitionTime = metav1.Now()
			return
		}
	}
	ws.Status.Conditions = append(ws.Status.Conditions, v1.WorkspaceCondition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

var (
	healthCheckInterval         = 15 * time.Second
	healthCheckBackoffInterval  = 60 * time.Second
	healthCheckFailureThreshold = int32(3)
	healthCheckGracePeriod      = 30 * time.Second
	agentdPort                  = agentd.AgentdPort
	agentdAdminPort             = agentd.AgentdAdminPort
	// US-22.5/22.6: Deep-status poll interval. /v1/statusz is expensive
	// (multiple opencode calls under mutex). Failures of the deep poll do
	// NOT increment ConsecutiveHealthFailures — they only mark fields stale.
	deepStatusInterval = 60 * time.Second
)

var healthHTTPClient = &http.Client{Timeout: 5 * time.Second}

// US-22.5: Separate client for deep-status with generous timeout (statusz can be slow).
var deepStatusHTTPClient = &http.Client{Timeout: 30 * time.Second}

func (r *WorkspaceReconciler) shouldRunHealthCheck(ws *v1.Workspace) bool {
	if ws.Status.StartTime != nil && time.Since(ws.Status.StartTime.Time) < healthCheckGracePeriod {
		return false
	}
	interval := healthCheckInterval
	if ws.Status.ConsecutiveHealthFailures >= healthCheckFailureThreshold {
		interval = healthCheckBackoffInterval
	}
	if ws.Status.LastHealthCheckAt == nil {
		return true
	}
	return time.Since(ws.Status.LastHealthCheckAt.Time) >= interval
}

func (r *WorkspaceReconciler) checkAgentHealth(ctx context.Context, ws *v1.Workspace) {
	logger := log.FromContext(ctx)

	if ws.Status.PodIP != "" && ws.Status.StartTime != nil && ws.Status.LastHealthCheckAt != nil {
		if ws.Status.LastHealthCheckAt.Before(ws.Status.StartTime) {
			ws.Status.ConsecutiveHealthFailures = 0
			ws.Status.LastHealthCheckAt = nil
		}
	}

	if !r.shouldRunHealthCheck(ws) {
		return
	}
	if ws.Status.PodIP == "" {
		return
	}

	// US-22.5: Liveness check via /v1/healthz (cheap, process-only, never
	// calls opencode). This drives ConsecutiveHealthFailures and pod-restart
	// decisions. Under SSE load, /v1/healthz still responds < 100ms because
	// it has zero opencode dependency (US-22.1).
	endpoint := fmt.Sprintf("http://%s:%d/v1/healthz", ws.Status.PodIP, agentdAdminPort)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return
	}

	resp, err := healthHTTPClient.Do(req)

	now := metav1.Now()
	ws.Status.LastHealthCheckAt = &now

	if err != nil {
		ws.Status.ConsecutiveHealthFailures++
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "Unknown",
			v1.ReasonHealthCheckFailed, err.Error())
		if ws.Status.ConsecutiveHealthFailures >= healthCheckFailureThreshold {
			podN := podName(ws.Name, string(ws.UID))
			logger.Info("Agent unreachable beyond threshold; restarting pod",
				"failures", ws.Status.ConsecutiveHealthFailures, "pod", podN, "lastError", err.Error())
			r.deletePodByName(ctx, podN, ws.Namespace)
			ws.Status.Phase = v1.WorkspacePhaseCreating
			ws.Status.PodIP = ""
			ws.Status.Endpoint = ""
			ws.Status.RestartCount++
			ws.Status.ConsecutiveHealthFailures = 0
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var healthResp agentd.HealthzResponse
	if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
		ws.Status.ConsecutiveHealthFailures++
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "Unknown",
			v1.ReasonHealthCheckFailed, "failed to decode healthz response")
		return
	}

	if !healthResp.Healthy {
		ws.Status.ConsecutiveHealthFailures++
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "False",
			v1.ReasonAgentUnhealthy, "agent process not responding")
		if ws.Status.ConsecutiveHealthFailures >= healthCheckFailureThreshold {
			podN := podName(ws.Name, string(ws.UID))
			logger.Info("Agent unhealthy beyond threshold; restarting pod",
				"failures", ws.Status.ConsecutiveHealthFailures, "pod", podN)
			r.deletePodByName(ctx, podN, ws.Namespace)
			ws.Status.Phase = v1.WorkspacePhaseCreating
			ws.Status.PodIP = ""
			ws.Status.Endpoint = ""
			ws.Status.RestartCount++
			ws.Status.ConsecutiveHealthFailures = 0
		}
		return
	}

	// Liveness passed — reset failure counter.
	ws.Status.ConsecutiveHealthFailures = 0
	r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "True",
		v1.ReasonAgentHealthy, fmt.Sprintf("agentd alive, uptime=%ds", healthResp.UptimeSeconds))
}

// maybeEnrichAgentStatus calls enrichAgentStatus at most once per
// deepStatusInterval (60s). Uses LastHealthCheckAt as a proxy for timing
// since the deep-status poll piggybacks on the reconcile loop.
func (r *WorkspaceReconciler) maybeEnrichAgentStatus(ctx context.Context, ws *v1.Workspace) {
	// Rate-limit: only run if the workspace has been Active long enough
	// and we haven't polled recently. We use a simple heuristic: poll on
	// every 4th reconcile (requeueActive=15s × 4 = 60s).
	if ws.Status.StartTime == nil {
		return
	}
	uptime := time.Since(ws.Status.StartTime.Time)
	// Only poll deep-status after the grace period and at ~60s intervals.
	// The reconcile loop fires every 15s; we poll statusz every 4th cycle.
	if uptime < healthCheckGracePeriod {
		return
	}
	// Use seconds-since-start modulo deepStatusInterval to approximate cadence.
	// This avoids adding a new status field just for timing.
	secondsSinceStart := int64(uptime.Seconds())
	if secondsSinceStart%(int64(deepStatusInterval.Seconds())) > int64(requeueActive.Seconds()) {
		return
	}
	r.enrichAgentStatus(ctx, ws)
}

// enrichAgentStatus polls /v1/statusz for session/disk/provider metadata.
// It runs on a slower cadence (deepStatusInterval) and its failures are
// informational only — they never trigger pod restarts.
func (r *WorkspaceReconciler) enrichAgentStatus(ctx context.Context, ws *v1.Workspace) {
	if ws.Status.PodIP == "" {
		return
	}

	endpoint := fmt.Sprintf("http://%s:%d/v1/statusz", ws.Status.PodIP, agentdAdminPort)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return
	}

	// F1.4.2 (Epic 17): /v1/statusz now requires a Bearer token sourced
	// from the per-workspace password Secret. Read it best-effort —
	// missing Secret means failed auth on the request, which is logged
	// at V(1) like any other deep-status failure (informational only).
	pwSec := &corev1.Secret{}
	if pwErr := r.Get(ctx, types.NamespacedName{Name: passwordSecretName(ws.Name), Namespace: ws.Namespace}, pwSec); pwErr == nil {
		if v, ok := pwSec.Data["password"]; ok {
			req.Header.Set("Authorization", "Bearer "+string(v))
		}
	}

	resp, err := deepStatusHTTPClient.Do(req)
	if err != nil {
		// Deep-status failure is informational only. Log at debug level.
		log.FromContext(ctx).V(1).Info("deep-status poll failed (informational only)", "error", err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var status agentd.StatuszResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return
	}

	if !status.Healthy {
		// Agent reports unhealthy via deep-status. Don't populate metadata
		// from an unhealthy agent — the data may be stale or corrupt.
		return
	}

	if !status.Ready || len(status.Connected) == 0 {
		r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "False",
			v1.ReasonAgentDegraded, fmt.Sprintf("no providers connected (configured=%d, connected=%v)",
				status.ProvidersConfigured, status.Connected))
		// Degraded: don't populate session/disk metadata — providers aren't
		// connected so session data is meaningless.
		return
	}

	// Populate agent-reported metadata on CRD status.
	ws.Status.ActiveSessions = int32(status.SessionsActive) //nolint:gosec // G115: int->int32 bounded by pod resource limits
	if len(status.Sessions) > 0 {
		sessions := make([]v1.AgentSessionStatus, len(status.Sessions))
		for i, s := range status.Sessions {
			sessions[i] = v1.AgentSessionStatus{ID: s.ID, Title: s.Title, Status: s.Status}
		}
		ws.Status.Sessions = sessions
	} else {
		ws.Status.Sessions = nil
	}
	if status.Disk != nil {
		ws.Status.DiskUsedBytes = status.Disk.UsedBytes
		ws.Status.DiskTotalBytes = status.Disk.TotalBytes
	}

	r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "True",
		v1.ReasonAgentHealthy, fmt.Sprintf("connected=%v sessions=%d version=%s",
			status.Connected, status.SessionsActive, status.AgentVersion))
}
