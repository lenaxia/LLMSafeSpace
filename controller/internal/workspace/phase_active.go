package workspace

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"

	"github.com/lenaxia/llmsafespace/controller/internal/metrics"
)

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
		runtime := workspace.Spec.Runtime
		secLevel := string(workspace.Spec.SecurityLevel)
		metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Dec()
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		workspace.Status.RestartCount++
		workspace.Status.ObservedRestartGeneration = workspace.Spec.RestartGeneration
		if err := r.Status().Update(ctx, workspace); err != nil {
			metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Ensure password secret still exists (can be lost during crash cycles
	// or cleanup). If missing, recycle pod so handleCreating regenerates it.
	pwSec := &corev1.Secret{}
	if pwErr := r.Get(ctx, types.NamespacedName{Name: passwordSecretName(workspace.Name), Namespace: workspace.Namespace}, pwSec); pwErr != nil {
		if errors.IsNotFound(pwErr) {
			logger.Info("Password secret missing in Active phase; recycling pod to regenerate", "secret", passwordSecretName(workspace.Name))
			r.deletePodByName(ctx, name, workspace.Namespace)
			runtime := workspace.Spec.Runtime
			secLevel := string(workspace.Spec.SecurityLevel)
			metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Dec()
			workspace.Status.Phase = v1.WorkspacePhaseCreating
			workspace.Status.PodIP = ""
			workspace.Status.Endpoint = ""
			workspace.Status.RestartCount++
			if err := r.Status().Update(ctx, workspace); err != nil {
				metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	// Check pod exists and is running.
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: workspace.Namespace}, pod)
	if err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		runtime := workspace.Spec.Runtime
		secLevel := string(workspace.Spec.SecurityLevel)
		metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Dec()
		result, err := r.enterRecovery(ctx, workspace, FailureClassInfrastructure)
		if err != nil {
			metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
		}
		return result, err
	}

	// US-23.1: A pod with DeletionTimestamp set is being terminated by the
	// controller itself (e.g., checkAgentHealth deleted it). Transition to
	// Creating so a new pod is built once the old one is reaped. Do NOT
	// count this as a transient failure — the controller initiated the delete.
	if isPodTerminating(pod) {
		logger.V(1).Info("Pod is terminating in Active phase; transitioning to Creating", "pod", pod.Name)
		runtime := workspace.Spec.Runtime
		secLevel := string(workspace.Spec.SecurityLevel)
		metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Dec()
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		if err := r.Status().Update(ctx, workspace); err != nil {
			metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueCreating}, nil
	}

	if pod.Status.Phase != corev1.PodRunning {
		obs := observePod(pod)
		class := classifyFailure(obs)
		runtime := workspace.Spec.Runtime
		secLevel := string(workspace.Spec.SecurityLevel)
		metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Dec()
		result, err := r.enterRecovery(ctx, workspace, class)
		if err != nil {
			metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
		}
		return result, err
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
		runtime := workspace.Spec.Runtime
		secLevel := string(workspace.Spec.SecurityLevel)
		metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Dec()
		workspace.Status.Phase = v1.WorkspacePhaseCreating
		workspace.Status.PodIP = ""
		workspace.Status.Endpoint = ""
		if err := r.Status().Update(ctx, workspace); err != nil {
			metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			obs := observePod(pod)
			class := classifyFailure(obs)
			runtime := workspace.Spec.Runtime
			secLevel := string(workspace.Spec.SecurityLevel)
			metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Dec()
			result, err := r.enterRecovery(ctx, workspace, class)
			if err != nil {
				metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
			}
			return result, err
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

	// Reset failure counter if stable long enough (2 min).
	maybeResetConsecutiveFailures(workspace)
	// Accumulate active compute seconds for billing metrics (requeueActive is the elapsed window).
	accumulateActiveSeconds(workspace, requeueActive)
	// Set PVC-allocated storage gauge (idempotent set; cheap).
	setStorageBytes(workspace)
	phaseBefore := workspace.Status.Phase
	r.checkAgentHealth(ctx, workspace)
	r.maybeEnrichAgentStatus(ctx, workspace)

	if err := r.Status().Update(ctx, workspace); err != nil {
		if phaseBefore == v1.WorkspacePhaseActive && workspace.Status.Phase == v1.WorkspacePhaseCreating {
			metrics.WorkspacesRunning.WithLabelValues(workspace.Spec.Runtime, string(workspace.Spec.SecurityLevel)).Inc()
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueActive}, nil
}
