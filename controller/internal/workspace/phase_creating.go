package workspace

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/metrics"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// maxStartupAnchorAge is the upper bound on a valid PendingAt or ResumedAt
// anchor. If more than this has elapsed when the workspace reaches Active
// (e.g. after a controller restart that left the anchor in etcd), the
// observation is silently dropped and the anchor cleared. This prevents
// multi-hour spurious values from inflating the histograms.
const maxStartupAnchorAge = 10 * time.Minute

func (r *WorkspaceReconciler) handleCreating(ctx context.Context, workspace *v1.Workspace) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	uid := string(workspace.UID)
	name := podName(workspace.Name, uid)

	// F19: restartGeneration bump bypasses backoff — user wants immediate retry.
	if workspace.Spec.RestartGeneration > workspace.Status.ObservedRestartGeneration {
		logger.Info("RestartGeneration bumped in Creating phase; clearing recovery state",
			"gen", workspace.Spec.RestartGeneration)
		workspace.Status.ConsecutiveFailures = 0
		workspace.Status.NextRetryAt = nil
		workspace.Status.LastFailureClass = ""
		workspace.Status.LastFailureAt = nil
		workspace.Status.SafeMode = false
		workspace.Status.RestartCount++
		workspace.Status.ObservedRestartGeneration = workspace.Spec.RestartGeneration
		// Fall through to pod creation below.
	}

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

		// F1/F16: enforce backoff — if NextRetryAt is set and not yet
		// elapsed, requeue without creating a pod.
		if wait := timeUntilNextRetry(workspace); wait > 0 {
			return ctrl.Result{RequeueAfter: wait}, nil
		}
		// Clear NextRetryAt once elapsed (avoid stale value on next cycle).
		workspace.Status.NextRetryAt = nil
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
			return r.enterRecovery(ctx, workspace, FailureClassConfiguration)
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

	if existingPod.Status.Phase == corev1.PodRunning && existingPod.Status.PodIP != "" && allContainersReady(existingPod) {
		now := metav1.Now()

		// Record startup latency metrics and clear anchors.
		recordStartupMetrics(workspace, existingPod)

		workspacePhaseTransitions.WithLabelValues(string(workspace.Status.Phase), string(v1.WorkspacePhaseActive)).Inc()
		// Increment active workspace gauge.
		runtime := workspace.Spec.Runtime
		secLevel := string(workspace.Spec.SecurityLevel)
		metrics.WorkspacesRunning.WithLabelValues(runtime, secLevel).Inc()
		metrics.WorkspacesCreatedTotal.WithLabelValues(runtime, secLevel).Inc()
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
		// F49: delete the failed pod BEFORE setting recovery state
		// to prevent re-observing the same Failed pod on next reconcile.
		obs := observePod(existingPod)
		class := classifyFailure(obs)
		r.deletePodByName(ctx, existingPod.Name, existingPod.Namespace)
		return r.enterRecovery(ctx, workspace, class)
	}

	// FN3: Pod stuck in Pending + Unschedulable → classify as Infrastructure,
	// delete pod, enter recovery with backoff. But only if it's been stuck
	// for > 5 minutes (give the scheduler time to find a node).
	if existingPod.Status.Phase == corev1.PodPending {
		obs := observePod(existingPod)
		if obs.Unschedulable && !existingPod.CreationTimestamp.IsZero() &&
			time.Since(existingPod.CreationTimestamp.Time) > 5*time.Minute {
			logger.Info("Pod unschedulable for >5min; entering recovery", "pod", existingPod.Name)
			r.deletePodByName(ctx, existingPod.Name, existingPod.Namespace)
			return r.enterRecovery(ctx, workspace, FailureClassInfrastructure)
		}
	}

	return ctrl.Result{RequeueAfter: requeueCreating}, nil
}

// recordStartupMetrics fires once when a workspace pod first reaches Running.
// It records create or resume latency from the appropriate anchor, measures
// workspace-setup init container duration, and clears both anchors so they
// are not double-counted on the next reconcile.
//
// Stale-anchor protection: anchors older than maxStartupAnchorAge are silently
// dropped (not observed) to prevent controller-restart artifacts from inflating
// the histograms with multi-hour values.
func recordStartupMetrics(workspace *v1.Workspace, pod *corev1.Pod) {
	recordStartupMetricsInto(workspace, pod,
		metrics.WorkspaceCreateDurationSeconds,
		metrics.WorkspaceResumeDurationSeconds,
		metrics.WorkspaceInitContainerDurationSeconds,
	)
}

// recordStartupMetricsInto is the testable core of recordStartupMetrics.
// Callers inject metric objects so tests can use fresh, isolated instances.
func recordStartupMetricsInto(
	workspace *v1.Workspace,
	pod *corev1.Pod,
	createHist *prometheus.HistogramVec,
	resumeHist *prometheus.HistogramVec,
	initHist prometheus.Histogram,
) {
	// ---- init container duration ----
	if d := initContainerDuration(pod, "workspace-setup"); d > 0 {
		initHist.Observe(d.Seconds())
	}

	// ---- create vs resume path ----
	switch {
	case workspace.Status.ResumedAt != nil:
		// Resume path: anchor was set by handleResuming.
		elapsed := time.Since(workspace.Status.ResumedAt.Time)
		if elapsed <= maxStartupAnchorAge {
			resumeType := "subsequent_resume"
			if workspace.Status.RestartCount == 0 {
				resumeType = "first_resume"
			}
			resumeHist.WithLabelValues(resumeType).Observe(elapsed.Seconds())
		}
		// Clear both anchors: if PendingAt is also set (unexpected state),
		// clearing it here prevents a spurious create observation on the
		// next reconcile.
		workspace.Status.ResumedAt = nil
		workspace.Status.PendingAt = nil

	case workspace.Status.PendingAt != nil:
		// Create path: anchor was set by handlePending.
		elapsed := time.Since(workspace.Status.PendingAt.Time)
		if elapsed <= maxStartupAnchorAge {
			hasPackages := strconv.FormatBool(len(workspace.Spec.Packages) > 0)
			hasInit := strconv.FormatBool(workspace.Spec.InitScript != "")
			createHist.WithLabelValues(hasPackages, hasInit).Observe(elapsed.Seconds())
		}
		workspace.Status.PendingAt = nil
	}
}

// initContainerDuration returns the wall-clock duration of the named init
// container, derived from its StartedAt / FinishedAt timestamps. Returns 0
// if the container did not run or timestamps are unavailable.
func initContainerDuration(pod *corev1.Pod, name string) time.Duration {
	if pod == nil {
		return 0
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.Name != name {
			continue
		}
		t := cs.State.Terminated
		if t == nil {
			return 0
		}
		if t.StartedAt.IsZero() || t.FinishedAt.IsZero() {
			return 0
		}
		d := t.FinishedAt.Sub(t.StartedAt.Time)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}
