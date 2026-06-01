package workspace

import (
	"fmt"

	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

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
		// Defensive self-heal: ensure the workspace's bcrypt password
		// Secret exists before we build the pod. handlePending also
		// calls this when transitioning Pending -> Creating, but a
		// workspace can land in Creating without going through that
		// path (e.g. when restored from etcd after a controller
		// version that didn't create the Secret, or when an external
		// actor or an earlier controller left phase=Creating with the
		// Secret missing). Without the Secret the pod's pw-secret
		// volume mount fails with FailedMount and the pod is stuck
		// in Init forever. Idempotent: returns nil if Secret already
		// exists.
		if err := r.ensurePasswordSecret(ctx, workspace); err != nil {
			logger.Error(err, "Failed to ensure password secret in Creating phase")
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
