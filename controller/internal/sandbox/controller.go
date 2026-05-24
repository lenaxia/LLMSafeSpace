package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/utilities"
)

// Label sanitization for runtime values lives in
// controller/internal/common.SanitizeLabelValue. It is called from both
// the API service (when creating Sandbox CRDs) and from this controller
// (when projecting labels onto child Pods), so it must be in a shared
// package — otherwise the API and the controller will disagree.

// parentWorkspaceIsSuspending returns true if the sandbox's referenced
// Workspace exists and is currently in a Suspending or Suspended phase.
// Used to disambiguate "pod was deleted because the workspace asked us to"
// from "pod failed unexpectedly". In the former case, the sandbox should
// land in Suspended; in the latter, Failed.
//
// Returns false on lookup error or if WorkspaceRef is empty — degrades to
// the original Failed-on-pod-loss behavior, which is the conservative
// choice for unparented sandboxes.
func (r *SandboxReconciler) parentWorkspaceIsSuspending(ctx context.Context, sandbox *v1.Sandbox) bool {
	if sandbox.Spec.WorkspaceRef == "" {
		return false
	}
	ws := &v1.Workspace{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      sandbox.Spec.WorkspaceRef,
		Namespace: sandbox.Namespace,
	}, ws); err != nil {
		return false
	}
	switch ws.Status.Phase {
	case v1.WorkspacePhaseSuspending, v1.WorkspacePhaseSuspended:
		return true
	}
	return false
}

// SandboxReconciler reconciles a Sandbox object
type SandboxReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles the reconciliation loop for Sandbox resources
func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", req.NamespacedName)
	logger.Info("Reconciling Sandbox")

	sandbox := &v1.Sandbox{}
	err := r.Get(ctx, req.NamespacedName, sandbox)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Sandbox resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Sandbox")
		return ctrl.Result{}, err
	}

	if !sandbox.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, sandbox)
	}

	// Tag the Sandbox CRD itself with its WorkspaceRef so the workspace
	// controller's listSandboxesForWorkspace selector finds it. Without
	// this, Workspace suspend/resume cannot enumerate dependent sandboxes
	// and updateSandboxesToSuspended is a silent no-op.
	if sandbox.Spec.WorkspaceRef != "" {
		if sandbox.Labels == nil {
			sandbox.Labels = map[string]string{}
		}
		if existing, ok := sandbox.Labels[common.LabelWorkspace]; !ok || existing != sandbox.Spec.WorkspaceRef {
			sandbox.Labels[common.LabelWorkspace] = sandbox.Spec.WorkspaceRef
			if err := r.Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to add workspace label to Sandbox")
				return ctrl.Result{}, err
			}
			// Requeue so the next reconcile sees the updated object.
			return ctrl.Result{Requeue: true}, nil
		}
	}

	if common.AddFinalizer(sandbox, common.SandboxFinalizer) {
		if err := r.Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox with finalizer")
			return ctrl.Result{}, err
		}
	}

	switch sandbox.Status.Phase {
	case "", common.SandboxPhasePending:
		return r.handlePendingSandbox(ctx, sandbox)
	case common.SandboxPhaseCreating:
		return r.handleCreatingSandbox(ctx, sandbox)
	case common.SandboxPhaseRunning:
		return r.handleRunningSandbox(ctx, sandbox)
	case common.SandboxPhaseSuspending:
		return r.handleSuspendingSandbox(ctx, sandbox)
	case common.SandboxPhaseResuming:
		return r.handleResumingSandbox(ctx, sandbox)
	case common.SandboxPhaseTerminating:
		return r.handleTerminatingSandbox(ctx, sandbox)
	case common.SandboxPhaseTerminated, common.SandboxPhaseFailed, common.SandboxPhaseSuspended:
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown sandbox phase", "phase", sandbox.Status.Phase)
		return ctrl.Result{}, nil
	}
}

func (r *SandboxReconciler) handlePendingSandbox(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling pending sandbox")

	sandbox.Status.Phase = common.SandboxPhaseCreating
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Creating")
		return ctrl.Result{}, err
	}

	return r.createSandboxPod(ctx, sandbox)
}

func (r *SandboxReconciler) handleCreatingSandbox(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling creating sandbox")

	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod not found, reverting to pending")
			sandbox.Status.Phase = common.SandboxPhasePending
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Pending")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	if pod.Status.Phase == corev1.PodRunning {
		sandbox.Status.Phase = common.SandboxPhaseRunning
		sandbox.Status.StartTime = &metav1.Time{Time: time.Now()}
		sandbox.Status.PodIP = pod.Status.PodIP
		sandbox.Status.Endpoint = fmt.Sprintf("%s.%s.svc.cluster.local", pod.Name, pod.Namespace)

		conditions := []v1.SandboxCondition{}
		common.SetSandboxCondition(&conditions, common.ConditionPodRunning, "True", common.ReasonPodRunning, "Pod is running")
		common.SetSandboxCondition(&conditions, common.ConditionReady, "True", common.ReasonPodRunning, "Sandbox is ready")
		sandbox.Status.Conditions = conditions

		if err := r.Status().Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox status to Running")
			return ctrl.Result{}, err
		}

		logger.Info("Sandbox is now running")
		return ctrl.Result{}, nil
	}

	logger.Info("Pod is not running yet", "podPhase", pod.Status.Phase)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *SandboxReconciler) handleRunningSandbox(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling running sandbox")

	// Fix #1: detect user-initiated or credential-triggered restart request.
	// Spec.RestartGeneration > Status.ObservedRestartGeneration means the API
	// (or fix #3's credential watcher) requested a pod recycle.
	if sandbox.Spec.RestartGeneration > sandbox.Status.ObservedRestartGeneration {
		return r.handleRestartRequest(ctx, sandbox, logger)
	}

	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// If the parent workspace is suspending or suspended, the
			// pod was deleted intentionally by the workspace controller.
			// Mark the sandbox Suspended so it can be resumed cleanly,
			// rather than Failed (which would require manual recovery).
			//
			// This branch takes precedence over transient-failure recovery:
			// a workspace-driven pod absence is not "transient", it's
			// expected. We must not consume a transient-retry slot.
			if r.parentWorkspaceIsSuspending(ctx, sandbox) {
				logger.Info("Pod not found and parent workspace is suspending; marking Sandbox Suspended")
				sandbox.Status.Phase = common.SandboxPhaseSuspended
				if err := r.Status().Update(ctx, sandbox); err != nil {
					logger.Error(err, "Failed to update Sandbox status to Suspended")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}

			// Fix #2: transient pod absence — self-heal up to MaxTransientFailures
			// times by reverting to Pending (which causes handlePending to
			// create a fresh pod). Only the Nth occurrence is terminal.
			//
			// At the threshold, mark Failed; recovery requires explicit
			// POST /sandboxes/:id/retry (fix #5).
			if sandbox.Status.TransientFailureCount+1 < int32(common.MaxTransientFailures) {
				return r.recoverFromTransientPodLoss(ctx, sandbox, logger)
			}
			return r.markPodPersistentLossFailed(ctx, sandbox, logger)
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	if pod.Status.Phase != corev1.PodRunning {
		// Same race-protection as the not-found branch above: a pod
		// transitioning through Failed during workspace suspend should
		// land the sandbox in Suspended, not Failed.
		if r.parentWorkspaceIsSuspending(ctx, sandbox) {
			logger.Info("Pod not running and parent workspace is suspending; marking Sandbox Suspended",
				"podPhase", pod.Status.Phase)
			sandbox.Status.Phase = common.SandboxPhaseSuspended
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Suspended")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		logger.Info("Pod is not running", "podPhase", pod.Status.Phase)
		sandbox.Status.Phase = common.SandboxPhaseFailed

		conditions := sandbox.Status.Conditions
		common.SetSandboxCondition(&conditions, common.ConditionPodRunning, "False", common.ReasonPodNotRunning, fmt.Sprintf("Pod is %s", pod.Status.Phase))
		common.SetSandboxCondition(&conditions, common.ConditionReady, "False", common.ReasonPodNotRunning, "Sandbox failed")
		sandbox.Status.Conditions = conditions

		if err := r.Status().Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox status to Failed")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Fix #2: pod is healthy. If we previously self-healed transient
	// failures, check whether the recovery-stable window has elapsed and
	// reset the transient counter. This prevents long-lived sandboxes from
	// accumulating false "near-failed" state across unrelated incidents
	// hours or days apart.
	r.maybeResetTransientCounter(sandbox)

	if sandbox.Spec.Timeout > 0 && sandbox.Status.StartTime != nil {
		timeout := time.Duration(sandbox.Spec.Timeout) * time.Second
		if time.Since(sandbox.Status.StartTime.Time) > timeout {
			logger.Info("Sandbox has exceeded its timeout, terminating")
			sandbox.Status.Phase = common.SandboxPhaseTerminating
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	if pod.Status.Phase == corev1.PodRunning {
		if err := r.Status().Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox resource usage")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// recoverFromTransientPodLoss reverts a Running sandbox whose pod has
// disappeared back to Pending so that handlePending can create a fresh pod.
// It increments the transient-failure counter and the cumulative restart
// counter, records the wall-clock time of this loss, and clears stale
// pod-tracking fields so the next reconcile starts clean.
//
// This is the self-heal branch of fix #2. Applies up to
// MaxTransientFailures-1 times consecutively before
// markPodPersistentLossFailed takes over.
func (r *SandboxReconciler) recoverFromTransientPodLoss(ctx context.Context, sandbox *v1.Sandbox, logger logr.Logger) (ctrl.Result, error) {
	now := metav1.Now()
	sandbox.Status.Phase = common.SandboxPhasePending
	sandbox.Status.TransientFailureCount++
	sandbox.Status.RestartCount++
	sandbox.Status.LastTransientFailureAt = &now

	// Clear stale pod fields. handlePendingSandbox will create a new pod
	// and populate them.
	sandbox.Status.PodName = ""
	sandbox.Status.PodNamespace = ""
	sandbox.Status.PodIP = ""

	conditions := sandbox.Status.Conditions
	common.SetSandboxCondition(&conditions, common.ConditionReady, "False",
		common.ReasonPodTransientLoss,
		fmt.Sprintf("Pod missing; self-healing (%d/%d transient retries used)",
			sandbox.Status.TransientFailureCount, common.MaxTransientFailures))
	sandbox.Status.Conditions = conditions

	logger.Info("Pod missing while Running; reverting to Pending for self-heal",
		"transientFailureCount", sandbox.Status.TransientFailureCount,
		"restartCount", sandbox.Status.RestartCount,
		"maxTransientFailures", common.MaxTransientFailures)

	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Pending (transient recovery)")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// markPodPersistentLossFailed transitions a sandbox to Failed after the
// transient-retry threshold has been exhausted. This is the terminal branch
// of fix #2; recovery requires explicit POST /sandboxes/:id/retry (fix #5).
func (r *SandboxReconciler) markPodPersistentLossFailed(ctx context.Context, sandbox *v1.Sandbox, logger logr.Logger) (ctrl.Result, error) {
	sandbox.Status.Phase = common.SandboxPhaseFailed
	sandbox.Status.TransientFailureCount++

	conditions := sandbox.Status.Conditions
	common.SetSandboxCondition(&conditions, common.ConditionPodRunning, "False",
		common.ReasonPodPersistentLoss, "Pod not found after transient-retry threshold exhausted")
	common.SetSandboxCondition(&conditions, common.ConditionReady, "False",
		common.ReasonPodPersistentLoss,
		fmt.Sprintf("Sandbox failed after %d transient pod-loss events; retry via POST /sandboxes/:id/retry",
			sandbox.Status.TransientFailureCount))
	sandbox.Status.Conditions = conditions

	logger.Info("Pod missing and transient-retry threshold reached; marking Failed",
		"transientFailureCount", sandbox.Status.TransientFailureCount,
		"restartCount", sandbox.Status.RestartCount)

	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Failed (persistent loss)")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// maybeResetTransientCounter clears the running transient-failure counter
// when the sandbox has been continuously healthy for at least
// TransientFailureResetWindow seconds since the last transient event.
//
// This decouples unrelated incidents: two transient losses 12 hours apart
// should not look like a near-failed sandbox. The counter only matters for
// rapid sequential failures.
//
// Idempotent. Safe to call on every Running reconcile.
func (r *SandboxReconciler) maybeResetTransientCounter(sandbox *v1.Sandbox) {
	if sandbox.Status.TransientFailureCount == 0 {
		return
	}
	if sandbox.Status.LastTransientFailureAt == nil {
		// Defensive: counter > 0 but no timestamp means the controller
		// missed setting it on a prior recovery (or the field was added
		// after that recovery). Clear the counter to avoid stuck state.
		sandbox.Status.TransientFailureCount = 0
		return
	}
	elapsed := time.Since(sandbox.Status.LastTransientFailureAt.Time)
	if elapsed >= time.Duration(common.TransientFailureResetWindow)*time.Second {
		sandbox.Status.TransientFailureCount = 0
		sandbox.Status.LastTransientFailureAt = nil
	}
}


// handleRestartRequest gracefully deletes the sandbox pod and reverts to
// Pending when Spec.RestartGeneration > Status.ObservedRestartGeneration.
// This is the controller-side of fix #1 (POST /sandboxes/:id/restart).
// The existing Pending → Creating → Running path recreates the pod.
func (r *SandboxReconciler) handleRestartRequest(ctx context.Context, sandbox *v1.Sandbox, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Restart requested; gracefully deleting pod",
		"restartGeneration", sandbox.Spec.RestartGeneration,
		"observedRestartGeneration", sandbox.Status.ObservedRestartGeneration)

	// Delete the existing pod gracefully (uses pod's terminationGracePeriodSeconds).
	if sandbox.Status.PodName != "" {
		pod := &corev1.Pod{}
		podKey := types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}
		if err := r.Get(ctx, podKey, pod); err == nil {
			if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
				logger.Error(err, "Failed to delete pod for restart")
				return ctrl.Result{}, err
			}
		}
	}

	// Revert to Pending so handlePendingSandbox creates a fresh pod.
	sandbox.Status.Phase = common.SandboxPhasePending
	sandbox.Status.ObservedRestartGeneration = sandbox.Spec.RestartGeneration
	sandbox.Status.RestartCount++
	sandbox.Status.PodName = ""
	sandbox.Status.PodNamespace = ""
	sandbox.Status.PodIP = ""

	conditions := sandbox.Status.Conditions
	common.SetSandboxCondition(&conditions, common.ConditionReady, "False",
		"RestartRequested", "Pod recycling due to restart request")
	sandbox.Status.Conditions = conditions

	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status for restart")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *SandboxReconciler) handleSuspendingSandbox(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling suspending sandbox")

	if sandbox.Status.PodName != "" {
		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
		if err == nil {
			logger.Info("Deleting pod for suspension", "pod", pod.Name)
			if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
				logger.Error(err, "Failed to delete Pod")
				return ctrl.Result{}, err
			}
		} else if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to get Pod")
			return ctrl.Result{}, err
		}
	}

	sandbox.Status.Phase = common.SandboxPhaseSuspended
	sandbox.Status.PodIP = ""
	sandbox.Status.PodName = ""
	sandbox.Status.PodNamespace = ""
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Suspended")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) handleResumingSandbox(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling resuming sandbox")

	sandbox.Status.Phase = common.SandboxPhaseCreating
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Creating")
		return ctrl.Result{}, err
	}

	return r.createSandboxPod(ctx, sandbox)
}

func (r *SandboxReconciler) handleTerminatingSandbox(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling terminating sandbox")

	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod not found, marking sandbox as terminated")
			sandbox.Status.Phase = common.SandboxPhaseTerminated
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminated")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	logger.Info("Deleting pod", "pod", pod.Name)
	if err := r.Delete(ctx, pod); err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete Pod")
			return ctrl.Result{}, err
		}
	}

	err = r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod deleted, marking sandbox as terminated")
			sandbox.Status.Phase = common.SandboxPhaseTerminated
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminated")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	logger.Info("Pod is still being deleted")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *SandboxReconciler) handleDeletion(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling sandbox deletion")

	if controllerutil.ContainsFinalizer(sandbox, common.SandboxFinalizer) {
		if sandbox.Status.Phase != common.SandboxPhaseTerminating &&
			sandbox.Status.Phase != common.SandboxPhaseTerminated &&
			sandbox.Status.Phase != common.SandboxPhaseFailed {
			sandbox.Status.Phase = common.SandboxPhaseTerminating
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		if sandbox.Status.PodName != "" {
			pod := &corev1.Pod{}
			err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
			if err == nil {
				logger.Info("Deleting pod", "pod", pod.Name)
				if err := r.Delete(ctx, pod); err != nil {
					if !errors.IsNotFound(err) {
						logger.Error(err, "Failed to delete Pod")
						return ctrl.Result{}, err
					}
				}
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			} else if !errors.IsNotFound(err) {
				logger.Error(err, "Failed to get Pod")
				return ctrl.Result{}, err
			}
		}

		controllerutil.RemoveFinalizer(sandbox, common.SandboxFinalizer)
		if err := r.Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to remove finalizer from Sandbox")
			return ctrl.Result{}, err
		}
	}

	logger.Info("Sandbox deletion handled successfully")
	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) createSandboxPod(ctx context.Context, sandbox *v1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Creating new pod for sandbox")

	if err := r.ensurePasswordSecret(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to ensure password secret")
		return ctrl.Result{}, err
	}

	pod, err := r.buildSandboxPodWithContext(ctx, sandbox)
	if err != nil {
		logger.Error(err, "Failed to build sandbox pod")
		return ctrl.Result{}, err
	}

	if err := controllerutil.SetControllerReference(sandbox, pod, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference on Pod")
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, pod); err != nil {
		logger.Error(err, "Failed to create Pod")
		return ctrl.Result{}, err
	}

	sandbox.Status.PodName = pod.Name
	sandbox.Status.PodNamespace = pod.Namespace

	conditions := []v1.SandboxCondition{}
	common.SetSandboxCondition(&conditions, common.ConditionPodCreated, "True", common.ReasonPodCreated, "Pod created successfully")
	sandbox.Status.Conditions = conditions

	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status with pod information")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// ensurePasswordSecret creates the sandbox password secret if it does not exist.
func (r *SandboxReconciler) ensurePasswordSecret(ctx context.Context, sandbox *v1.Sandbox) error {
	secretName := fmt.Sprintf("sandbox-pw-%s", sandbox.Name)
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: sandbox.Namespace}, secret)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}

	password := common.GenerateRandomString(32)
	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: sandbox.Namespace,
		},
		Data: map[string][]byte{
			"password": []byte(password),
		},
	}
	if err := controllerutil.SetControllerReference(sandbox, newSecret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on password secret: %w", err)
	}
	return r.Create(ctx, newSecret)
}

// buildSandboxPodWithContext builds a sandbox pod, looking up workspace details if needed.
func (r *SandboxReconciler) buildSandboxPodWithContext(ctx context.Context, sandbox *v1.Sandbox) (*corev1.Pod, error) {
	podName := fmt.Sprintf("%s-%s", sandbox.Name, sandbox.UID[0:8])

	labels := map[string]string{
		common.LabelApp:       "llmsafespace",
		common.LabelComponent: common.ComponentSandbox,
		common.LabelSandboxID: sandbox.Name,
		// Kubernetes label values cannot contain ':' (used by image-style
		// runtime identifiers like "python:3.11"). Sanitize ':' → '_' so the
		// runtime is preserved in label form for selectors and metrics.
		// The full unsanitized runtime string is also kept in annotations
		// (see below) for round-tripping back to the spec.
		common.LabelRuntime: utilities.SanitizeLabelValue(sandbox.Spec.Runtime),
	}
	// Tag pods with their parent workspace so the workspace controller's
	// deleteWorkspacePods (which selects by `llmsafespace.dev/workspace`)
	// can find them on suspend. Without this label, suspending a workspace
	// is a no-op for its sandboxes' pods.
	if sandbox.Spec.WorkspaceRef != "" {
		labels[common.LabelWorkspace] = sandbox.Spec.WorkspaceRef
	}

	annotations := map[string]string{
		common.AnnotationCreatedBy: common.ControllerName,
		common.AnnotationSandboxID: sandbox.Name,
	}

	// Resolve sandbox.Spec.Runtime → concrete container image via
	// RuntimeEnvironment lookup. The Sandbox spec deliberately does NOT
	// take an image directly: the platform constrains which images can be
	// used to runtime-environments registered cluster-wide. See
	// resolveRuntimeImage for the lookup strategy and escape hatches.
	runtimeImage, runtimeEnvName, err := resolveRuntimeImage(ctx, r.Client, sandbox.Spec.Runtime)
	if err != nil {
		return nil, fmt.Errorf("resolving runtime image: %w", err)
	}
	if runtimeEnvName != "" {
		annotations[common.AnnotationRuntimeEnv] = runtimeEnvName
	}

	trueVal := true
	falseVal := false

	mainContainer := corev1.Container{
		Name:    "sandbox",
		Image:   runtimeImage,
		Command: []string{"/usr/local/bin/entrypoint-opencode.sh"},
		Ports: []corev1.ContainerPort{
			{ContainerPort: 4096, Name: "opencode", Protocol: corev1.ProtocolTCP},
		},
		Env: []corev1.EnvVar{
			{Name: "SANDBOX_ID", Value: sandbox.Name},
			{Name: "WORKSPACE_DIR", Value: "/workspace"},
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "sandbox-cfg", MountPath: "/sandbox-cfg", ReadOnly: true},
			{Name: "tmp", MountPath: "/tmp"},
			{Name: "sandbox-home", MountPath: "/home/sandbox"},
		},
	}

	volumes := []corev1.Volume{
		{Name: "sandbox-cfg", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "sandbox-home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}

	var initContainers []corev1.Container

	if sandbox.Spec.WorkspaceRef != "" {
		ws := &v1.Workspace{}
		if err := r.Get(ctx, client.ObjectKey{Name: sandbox.Spec.WorkspaceRef, Namespace: sandbox.Namespace}, ws); err != nil {
			return nil, fmt.Errorf("failed to get workspace %s: %w", sandbox.Spec.WorkspaceRef, err)
		}

		volumes = append(volumes, corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: ws.Status.PVCName,
				},
			},
		})
		mainContainer.VolumeMounts = append(mainContainer.VolumeMounts,
			corev1.VolumeMount{Name: "workspace", MountPath: "/workspace"})

		if len(ws.Spec.Packages) > 0 || ws.Spec.InitScript != "" {
			setupInit := r.buildWorkspaceSetupInit(ws, runtimeImage)
			initContainers = append(initContainers, setupInit)
		}
	}

	credInit, pwVolume, credVolume, err := r.buildCredentialSetupInit(ctx, sandbox, runtimeImage)
	if err != nil {
		return nil, err
	}
	initContainers = append(initContainers, credInit)
	volumes = append(volumes, pwVolume)
	if credVolume != nil {
		volumes = append(volumes, *credVolume)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   sandbox.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			InitContainers:  initContainers,
			Containers:      []corev1.Container{mainContainer},
			Volumes:         volumes,
			SecurityContext: buildPodSecurityContext(sandbox),
		},
	}

	return pod, nil
}

// buildPodSecurityContext returns the pod-level SecurityContext applied to
// every sandbox pod. Pod-level settings are inherited by all containers
// that don't set their own RunAsUser/RunAsGroup.
//
// We set RunAsUser/RunAsGroup explicitly (defaulting to 1000) because every
// container in the pod is built with RunAsNonRoot=true. Without an explicit
// numeric uid, kubelet's runAsNonRoot check fails with:
//
//	container has runAsNonRoot and image has non-numeric user (sandbox),
//	cannot verify user is non-root
//
// This is because the runtime-base Dockerfile uses `USER sandbox` (a name,
// not a uid). Kubelet only resolves names to uids inside the container at
// runtime; for the runAsNonRoot pre-check it requires a numeric value at
// the API level.
//
// Defaults match the runtime-base Dockerfile's `useradd -u 1000 sandbox`.
// The Sandbox CRD's securityContext.runAsUser/runAsGroup override these.
func buildPodSecurityContext(sandbox *v1.Sandbox) *corev1.PodSecurityContext {
	runAsUser := int64(1000)
	runAsGroup := int64(1000)
	if sc := sandbox.Spec.SecurityContext; sc != nil {
		if sc.RunAsUser != 0 {
			runAsUser = sc.RunAsUser
		}
		if sc.RunAsGroup != 0 {
			runAsGroup = sc.RunAsGroup
		}
	}
	return &corev1.PodSecurityContext{
		RunAsUser:  &runAsUser,
		RunAsGroup: &runAsGroup,
		FSGroup:    &runAsGroup,
	}
}

// buildCredentialSetupInit builds the credential-setup init container and the
// pw-secret projected volume it needs. Returns the container, the pw-secret volume,
// and an optional cred-secret volume (non-nil only when workspace credentials exist).
func (r *SandboxReconciler) buildCredentialSetupInit(ctx context.Context, sandbox *v1.Sandbox, runtimeImage string) (corev1.Container, corev1.Volume, *corev1.Volume, error) {
	credScript := `
if [ -f /mnt/secrets/credentials/provider-config ]; then
  cp /mnt/secrets/credentials/provider-config /sandbox-cfg/credentials
else
  echo '{}' > /sandbox-cfg/credentials
fi
cp /mnt/secrets/password/password /sandbox-cfg/password
`

	pwSecretName := fmt.Sprintf("sandbox-pw-%s", sandbox.Name)

	pwVolume := corev1.Volume{
		Name: "pw-secret",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: pwSecretName,
			},
		},
	}

	credInitMounts := []corev1.VolumeMount{
		{Name: "sandbox-cfg", MountPath: "/sandbox-cfg"},
		{Name: "pw-secret", MountPath: "/mnt/secrets/password", ReadOnly: true},
	}

	var credVolume *corev1.Volume

	if sandbox.Spec.WorkspaceRef != "" {
		credsSecretName := fmt.Sprintf("workspace-creds-%s", sandbox.Spec.WorkspaceRef)
		credSecret := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{Name: credsSecretName, Namespace: sandbox.Namespace}, credSecret)
		if err == nil {
			v := corev1.Volume{
				Name: "cred-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: credsSecretName,
					},
				},
			}
			credVolume = &v
			credInitMounts = append(credInitMounts, corev1.VolumeMount{
				Name:      "cred-secret",
				MountPath: "/mnt/secrets/credentials",
				ReadOnly:  true,
			})
		} else if !errors.IsNotFound(err) {
			return corev1.Container{}, corev1.Volume{}, nil, fmt.Errorf("failed to check credentials secret: %w", err)
		}
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
		VolumeMounts: credInitMounts,
	}

	return credInit, pwVolume, credVolume, nil
}

// buildWorkspaceSetupInit builds the workspace-setup init container that installs
// packages and/or runs the initScript before the main container starts.
func (r *SandboxReconciler) buildWorkspaceSetupInit(ws *v1.Workspace, runtimeImage string) corev1.Container {
	trueVal := true
	falseVal := false

	return corev1.Container{
		Name:    "workspace-setup",
		Image:   runtimeImage,
		Command: []string{"/bin/sh", "-c", buildWorkspaceSetupScript(ws)},
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

// buildWorkspaceSetupScript constructs the shell script for the workspace-setup init container.
func buildWorkspaceSetupScript(ws *v1.Workspace) string {
	script := "#!/bin/sh\nset -e\nmkdir -p /workspace/packages\n"

	for _, pkgSet := range ws.Spec.Packages {
		if len(pkgSet.Requirements) == 0 {
			continue
		}
		args := ""
		for _, req := range pkgSet.Requirements {
			args += " " + req
		}
		rt := pkgSet.Runtime
		switch {
		case len(rt) >= 6 && rt[:6] == "nodejs":
			script += "cd /workspace/packages && npm install" + args + "\n"
		case len(rt) >= 2 && rt[:2] == "go":
			for _, req := range pkgSet.Requirements {
				script += "cd /workspace/packages && go install " + req + "\n"
			}
		default:
			script += "pip install --target=/workspace/packages" + args + "\n"
		}
	}

	if ws.Spec.InitScript != "" {
		script += "cat > /tmp/init-script.sh << 'INITSCRIPT'\n"
		script += ws.Spec.InitScript + "\n"
		script += "INITSCRIPT\n"
		script += "chmod +x /tmp/init-script.sh\n"
		script += "/tmp/init-script.sh\n"
	}

	return script
}

// SetupWithManager sets up the controller with the Manager
func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
