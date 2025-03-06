package warmpod

import (
	"context"
	"fmt"
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

	"github.com/llmsafespace/sandbox-controller/internal/common"
	"github.com/llmsafespace/sandbox-controller/internal/resources"
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
			return ctrl.Result{}, err
		}
	}

	// Handle warm pod based on its phase
	switch warmPod.Status.Phase {
	case "", common.WarmPodPhasePending:
		return r.handlePendingWarmPod(ctx, warmPod)
	case common.WarmPodPhaseReady:
		return r.handleReadyWarmPod(ctx, warmPod)
	case common.WarmPodPhaseAssigned:
		return r.handleAssignedWarmPod(ctx, warmPod)
	case common.WarmPodPhaseTerminating:
		return r.handleTerminatingWarmPod(ctx, warmPod)
	default:
		logger.Info("Unknown warm pod phase", "phase", warmPod.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// handlePendingWarmPod handles a warm pod in the Pending phase
func (r *WarmPodReconciler) handlePendingWarmPod(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Handling pending warm pod")

	// Check if the pod exists
	if warmPod.Status.PodName != "" {
		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.PodName, Namespace: warmPod.Status.PodNamespace}, pod)
		if err == nil {
			// Pod exists, check if it's ready
			if common.IsPodReady(pod) {
				// Pod is ready, update the warm pod status
				warmPod.Status.Phase = common.WarmPodPhaseReady
				warmPod.Spec.LastHeartbeat = &metav1.Time{Time: time.Now()}
				if err := r.Status().Update(ctx, warmPod); err != nil {
					logger.Error(err, "Failed to update WarmPod status to Ready")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			// Pod is not ready yet, requeue
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		} else if !errors.IsNotFound(err) {
			// Error reading the pod
			logger.Error(err, "Failed to get Pod")
			return ctrl.Result{}, err
		}
	}

	// Get the warm pool
	warmPool := &resources.WarmPool{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: warmPod.Spec.PoolRef.Namespace,
		Name:      warmPod.Spec.PoolRef.Name,
	}, warmPool)
	if err != nil {
		logger.Error(err, "Failed to get WarmPool")
		return ctrl.Result{}, err
	}

	// Create a new pod for the warm pod
	pod := r.buildWarmPodPod(warmPool, warmPod)
	
	// Set the owner reference
	if err := controllerutil.SetControllerReference(warmPod, pod, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference on Pod")
		return ctrl.Result{}, err
	}
	
	// Create the pod
	if err := r.Create(ctx, pod); err != nil {
		logger.Error(err, "Failed to create Pod")
		return ctrl.Result{}, err
	}
	
	// Update the warm pod status
	warmPod.Status.PodName = pod.Name
	warmPod.Status.PodNamespace = pod.Namespace
	if err := r.Status().Update(ctx, warmPod); err != nil {
		logger.Error(err, "Failed to update WarmPod status with pod information")
		return ctrl.Result{}, err
	}
	
	// Requeue to check if the pod is ready
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
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	// Check if the pod is still ready
	if !common.IsPodReady(pod) {
		// Pod is not ready, revert to pending
		logger.Info("Pod is not ready, reverting to pending")
		warmPod.Status.Phase = common.WarmPodPhasePending
		if err := r.Status().Update(ctx, warmPod); err != nil {
			logger.Error(err, "Failed to update WarmPod status to Pending")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Update the heartbeat
	warmPod.Spec.LastHeartbeat = &metav1.Time{Time: time.Now()}
	if err := r.Update(ctx, warmPod); err != nil {
		logger.Error(err, "Failed to update WarmPod heartbeat")
		return ctrl.Result{}, err
	}

	// Get the warm pool
	warmPool := &resources.WarmPool{}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: warmPod.Spec.PoolRef.Namespace,
		Name:      warmPod.Spec.PoolRef.Name,
	}, warmPool)
	if err != nil {
		logger.Error(err, "Failed to get WarmPool")
		return ctrl.Result{}, err
	}

	// Check if the warm pod has exceeded its TTL
	if warmPool.Spec.TTL > 0 && warmPod.Spec.CreationTimestamp != nil {
		ttl := time.Duration(warmPool.Spec.TTL) * time.Second
		if time.Since(warmPod.Spec.CreationTimestamp.Time) > ttl {
			// Warm pod has exceeded its TTL, terminate it
			logger.Info("Warm pod has exceeded its TTL, terminating")
			warmPod.Status.Phase = common.WarmPodPhaseTerminating
			if err := r.Status().Update(ctx, warmPod); err != nil {
				logger.Error(err, "Failed to update WarmPod status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
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
			// Pod doesn't exist, mark as terminating
			logger.Info("Pod not found, marking as terminating")
			warmPod.Status.Phase = common.WarmPodPhaseTerminating
			if err := r.Status().Update(ctx, warmPod); err != nil {
				logger.Error(err, "Failed to update WarmPod status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	// Check if the sandbox still exists
	if warmPod.Status.AssignedTo != "" {
		sandbox := &resources.Sandbox{}
		err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.AssignedTo, Namespace: warmPod.Namespace}, sandbox)
		if err != nil {
			if errors.IsNotFound(err) {
				// Sandbox doesn't exist, mark as terminating
				logger.Info("Assigned sandbox not found, marking as terminating")
				warmPod.Status.Phase = common.WarmPodPhaseTerminating
				if err := r.Status().Update(ctx, warmPod); err != nil {
					logger.Error(err, "Failed to update WarmPod status to Terminating")
					return ctrl.Result{}, err
				}
				return ctrl.Result{Requeue: true}, nil
			}
			// Error reading the sandbox
			logger.Error(err, "Failed to get Sandbox")
			return ctrl.Result{}, err
		}

		// Check if the sandbox is still using this warm pod
		if sandbox.Status.PodName != warmPod.Status.PodName || sandbox.Status.PodNamespace != warmPod.Status.PodNamespace {
			// Sandbox is not using this warm pod anymore, mark as terminating
			logger.Info("Sandbox is not using this warm pod anymore, marking as terminating")
			warmPod.Status.Phase = common.WarmPodPhaseTerminating
			if err := r.Status().Update(ctx, warmPod); err != nil {
				logger.Error(err, "Failed to update WarmPod status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// Requeue to periodically check the warm pod
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleTerminatingWarmPod handles a warm pod in the Terminating phase
func (r *WarmPodReconciler) handleTerminatingWarmPod(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Handling terminating warm pod")

	// Check if the pod exists
	if warmPod.Status.PodName != "" {
		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.PodName, Namespace: warmPod.Status.PodNamespace}, pod)
		if err == nil {
			// Pod exists, delete it
			logger.Info("Deleting pod", "pod", pod.Name)
			if err := r.Delete(ctx, pod); err != nil {
				if !errors.IsNotFound(err) {
					logger.Error(err, "Failed to delete Pod")
					return ctrl.Result{}, err
				}
			}
			// Requeue to check if the pod has been deleted
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		} else if !errors.IsNotFound(err) {
			// Error reading the pod
			logger.Error(err, "Failed to get Pod")
			return ctrl.Result{}, err
		}
	}

	// Delete the warm pod
	logger.Info("Deleting warm pod")
	if err := r.Delete(ctx, warmPod); err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete WarmPod")
			return ctrl.Result{}, err
		}
	}

	// Warm pod is being deleted, no need to requeue
	return ctrl.Result{}, nil
}

// handleDeletion handles the deletion of a warm pod
func (r *WarmPodReconciler) handleDeletion(ctx context.Context, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace})
	logger.Info("Handling warm pod deletion")

	// Check if the finalizer exists
	if controllerutil.ContainsFinalizer(warmPod, common.WarmPodFinalizer) {
		// Check if the pod exists
		if warmPod.Status.PodName != "" {
			pod := &corev1.Pod{}
			err := r.Get(ctx, types.NamespacedName{Name: warmPod.Status.PodName, Namespace: warmPod.Status.PodNamespace}, pod)
			if err == nil {
				// Pod exists, delete it
				logger.Info("Deleting pod", "pod", pod.Name)
				if err := r.Delete(ctx, pod); err != nil {
					if !errors.IsNotFound(err) {
						logger.Error(err, "Failed to delete Pod")
						return ctrl.Result{}, err
					}
				}
				// Requeue to check if the pod has been deleted
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			} else if !errors.IsNotFound(err) {
				// Error reading the pod
				logger.Error(err, "Failed to get Pod")
				return ctrl.Result{}, err
			}
		}

		// Remove the finalizer
		controllerutil.RemoveFinalizer(warmPod, common.WarmPodFinalizer)
		if err := r.Update(ctx, warmPod); err != nil {
			logger.Error(err, "Failed to remove finalizer from WarmPod")
			return ctrl.Result{}, err
		}
	}

	// Warm pod is being deleted, no need to requeue
	logger.Info("Warm pod deletion handled successfully")
	return ctrl.Result{}, nil
}

// buildWarmPodPod builds a pod for a warm pod
func (r *WarmPodReconciler) buildWarmPodPod(warmPool *resources.WarmPool, warmPod *resources.WarmPod) *corev1.Pod {
	// Create a unique name for the pod
	podName := fmt.Sprintf("warm-%s-%s", warmPool.Name, warmPod.Name[len(warmPool.Name)+1:])
	
	// Define labels and annotations
	labels := map[string]string{
		common.LabelApp:        "llmsafespace",
		common.LabelComponent:  common.ComponentWarmPod,
		common.LabelPoolName:   warmPool.Name,
		common.LabelWarmPodID:  warmPod.Name,
		common.LabelRuntime:    warmPool.Spec.Runtime,
	}
	
	annotations := map[string]string{
		common.AnnotationCreatedBy: common.ControllerName,
		common.AnnotationPoolName:  warmPool.Name,
		common.AnnotationWarmPodID: warmPod.Name,
	}
	
	// Define the pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   warmPod.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "warmpod",
					Image: warmPool.Spec.Runtime,
					// Add more container configuration based on warm pool spec
				},
			},
			// Add more pod configuration based on warm pool spec
		},
	}
	
	// Configure resources if specified
	if warmPool.Spec.Resources != nil {
		pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
			// Configure resource limits and requests
		}
	}
	
	// Configure preload packages if specified
	if len(warmPool.Spec.PreloadPackages) > 0 {
		// Add init container to preload packages
	}
	
	// Configure preload scripts if specified
	if len(warmPool.Spec.PreloadScripts) > 0 {
		// Add init container to run preload scripts
	}
	
	return pod
}

// SetupWithManager sets up the controller with the Manager
func (r *WarmPodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resources.WarmPod{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
