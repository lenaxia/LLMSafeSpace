package warmpod

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/common"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/metrics"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/resources"
)

// WarmPodReconciler reconciles a WarmPod object
type WarmPodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles the reconciliation loop for WarmPod resources
func (r *WarmPodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", req.NamespacedName)
	logger.Info("Reconciling WarmPod")

	startTime := time.Now()
	defer func() {
		metrics.ReconciliationDurationSeconds.WithLabelValues("warmpod", "success").Observe(time.Since(startTime).Seconds())
	}()

	// Fetch the WarmPod instance
	warmPod := &resources.WarmPod{}
	err := r.Get(ctx, req.NamespacedName, warmPod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return
			logger.Info("WarmPod resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object
		logger.Error(err, "Failed to get WarmPod")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "get").Inc()
		return ctrl.Result{}, err
	}

	// Check if the warm pod is being deleted
	if !warmPod.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, warmPod)
	}

	// Add finalizer if it doesn't exist
	if common.AddFinalizer(warmPod, common.WarmPodFinalizer) {
		if err := r.Update(ctx, warmPod); err != nil {
			logger.Error(err, "Failed to update WarmPod with finalizer")
			metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_finalizer").Inc()
			return ctrl.Result{}, err
		}
	}

	// Handle the warm pod based on its phase
	switch warmPod.Status.Phase {
	case "", common.WarmPodPhasePending:
		return r.handlePendingWarmPod(ctx, warmPod)
	case common.WarmPodPhaseReady:
		return r.handleReadyWarmPod(ctx, warmPod)
	case common.WarmPodPhaseAssigned:
		return r.handleAssignedWarmPod(ctx, warmPod)
	default:
		logger.Info("Unknown warm pod phase", "phase", warmPod.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// handlePendingWarmPod handles a warm pod in the Pending phase
func (r *WarmPodReconciler) handlePendingWarmPod(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Handling pending warm pod")

	// Create the underlying pod if it doesn't exist
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.PodName, Namespace: warmPod.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod doesn't exist, create it
			logger.Info("Creating pod for warm pod")
			return r.createPodForWarmPod(ctx, warmPod)
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "get_pod").Inc()
		return ctrl.Result{}, err
	}

	// Check if the pod is running
	if pod.Status.Phase == corev1.PodRunning {
		// Check if the pod is ready
		isReady := common.IsPodReady(pod)
		if isReady {
			// Update the warm pod status to Ready
			warmPod.Status.Phase = common.WarmPodPhaseReady
			warmPod.Spec.LastHeartbeat = &metav1.Time{Time: time.Now()}
			if err := r.Status().Update(ctx, warmPod); err != nil {
				logger.Error(err, "Failed to update WarmPod status to Ready")
				metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_status").Inc()
				return ctrl.Result{}, err
			}
			logger.Info("Warm pod is now ready")
			return ctrl.Result{}, nil
		}
	}

	// Pod is not ready yet, requeue
	logger.Info("Pod is not ready yet", "podPhase", pod.Status.Phase)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// handleReadyWarmPod handles a warm pod in the Ready phase
func (r *WarmPodReconciler) handleReadyWarmPod(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Handling ready warm pod")

	// Check if the pod exists
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.PodName, Namespace: warmPod.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod doesn't exist, revert to pending
			logger.Info("Pod not found, reverting to pending")
			warmPod.Status.Phase = common.WarmPodPhasePending
			warmPod.Status.PodName = ""
			warmPod.Status.PodNamespace = ""
			if err := r.Status().Update(ctx, warmPod); err != nil {
				logger.Error(err, "Failed to update WarmPod status to Pending")
				metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_status").Inc()
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "get_pod").Inc()
		return ctrl.Result{}, err
	}

	// Check if the pod is still running and ready
	if pod.Status.Phase != corev1.PodRunning || !common.IsPodReady(pod) {
		// Pod is not running or ready, revert to pending
		logger.Info("Pod is not running or ready", "podPhase", pod.Status.Phase)
		warmPod.Status.Phase = common.WarmPodPhasePending
		if err := r.Status().Update(ctx, warmPod); err != nil {
			logger.Error(err, "Failed to update WarmPod status to Pending")
			metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_status").Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Update the heartbeat
	warmPod.Spec.LastHeartbeat = &metav1.Time{Time: time.Now()}
	if err := r.Update(ctx, warmPod); err != nil {
		logger.Error(err, "Failed to update WarmPod heartbeat")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_heartbeat").Inc()
		return ctrl.Result{}, err
	}

	// Update metrics
	if warmPod.Labels != nil {
		poolName := warmPod.Labels[common.LabelPoolName]
		runtime := warmPod.Labels[common.LabelRuntime]
		if poolName != "" && runtime != "" {
			metrics.WarmPoolSizeGauge.WithLabelValues(poolName, runtime, "ready").Inc()
		}
	}

	// Requeue to periodically check the warm pod
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleAssignedWarmPod handles a warm pod in the Assigned phase
func (r *WarmPodReconciler) handleAssignedWarmPod(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Handling assigned warm pod")

	// Check if the pod exists
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.PodName, Namespace: warmPod.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod doesn't exist, mark as terminated
			logger.Info("Pod not found, marking as terminated")
			warmPod.Status.Phase = common.WarmPodPhaseTerminating
			if err := r.Status().Update(ctx, warmPod); err != nil {
				logger.Error(err, "Failed to update WarmPod status to Terminating")
				metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_status").Inc()
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "get_pod").Inc()
		return ctrl.Result{}, err
	}

	// Check if the pod is still running
	if pod.Status.Phase != corev1.PodRunning {
		// Pod is not running, mark as terminated
		logger.Info("Pod is not running", "podPhase", pod.Status.Phase)
		warmPod.Status.Phase = common.WarmPodPhaseTerminating
		if err := r.Status().Update(ctx, warmPod); err != nil {
			logger.Error(err, "Failed to update WarmPod status to Terminating")
			metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_status").Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Update metrics
	if warmPod.Labels != nil {
		poolName := warmPod.Labels[common.LabelPoolName]
		runtime := warmPod.Labels[common.LabelRuntime]
		if poolName != "" && runtime != "" {
			metrics.WarmPoolSizeGauge.WithLabelValues(poolName, runtime, "assigned").Inc()
		}
	}

	// Pod is still running, nothing to do
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleDeletion handles the deletion of a warm pod
func (r *WarmPodReconciler) handleDeletion(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Handling warm pod deletion")

	// Check if the finalizer exists
	if controllerutil.ContainsFinalizer(warmPod, common.WarmPodFinalizer) {
		// Delete the underlying pod if it exists
		if warmPod.Status.PodName != "" {
			pod := &corev1.Pod{}
			err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.PodName, Namespace: warmPod.Status.PodNamespace}, pod)
			if err == nil {
				// Pod exists, delete it
				logger.Info("Deleting pod", "pod", pod.Name)
				if err := r.Delete(ctx, pod); err != nil {
					if !errors.IsNotFound(err) {
						logger.Error(err, "Failed to delete Pod")
						metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "delete_pod").Inc()
						return ctrl.Result{}, err
					}
				}
				// Requeue to check if the pod has been deleted
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			} else if !errors.IsNotFound(err) {
				// Error reading the pod
				logger.Error(err, "Failed to get Pod")
				metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "get_pod").Inc()
				return ctrl.Result{}, err
			}
		}

		// Remove the finalizer
		controllerutil.RemoveFinalizer(warmPod, common.WarmPodFinalizer)
		if err := r.Update(ctx, warmPod); err != nil {
			logger.Error(err, "Failed to remove finalizer from WarmPod")
			metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "remove_finalizer").Inc()
			return ctrl.Result{}, err
		}
	}

	// Warm pod is being deleted, no need to requeue
	logger.Info("Warm pod deletion handled successfully")
	return ctrl.Result{}, nil
}

// createPodForWarmPod creates a new pod for a warm pod
func (r *WarmPodReconciler) createPodForWarmPod(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Creating new pod for warm pod")

	// Get the warm pool
	warmPool := &resources.WarmPool{}
	err := r.Get(ctx, types.NamespacedName{Name: warmPod.Spec.PoolRef.Name, Namespace: warmPod.Spec.PoolRef.Namespace}, warmPool)
	if err != nil {
		logger.Error(err, "Failed to get WarmPool")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "get_warmpool").Inc()
		return ctrl.Result{}, err
	}

	// Create the pod
	podManager := common.NewPodManager(r.Client, r.Scheme)
	pod, err := podManager.CreateWarmPodPod(ctx, warmPod, warmPool)
	if err != nil {
		logger.Error(err, "Failed to create Pod")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "create_pod").Inc()
		return ctrl.Result{}, err
	}

	// Update the warm pod status
	warmPod.Status.PodName = pod.Name
	warmPod.Status.PodNamespace = pod.Namespace
	if err := r.Status().Update(ctx, warmPod); err != nil {
		logger.Error(err, "Failed to update WarmPod status with pod information")
		metrics.ReconciliationErrorsTotal.WithLabelValues("warmpod", "update_status").Inc()
		return ctrl.Result{}, err
	}

	// Requeue to check if the pod is running
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// generateRandomString generates a random string of the specified length
func generateRandomString(length int) string {
	return common.GenerateRandomString(length)
}

// isPodReady checks if a pod is ready
func isPodReady(pod *corev1.Pod) bool {
	return common.IsPodReady(pod)
}

// SetupWithManager sets up the controller with the Manager
func (r *WarmPodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resources.WarmPod{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
