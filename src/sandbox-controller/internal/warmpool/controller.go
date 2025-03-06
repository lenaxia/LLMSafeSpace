package warmpool

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/common"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/resources"
)

// WarmPoolReconciler reconciles a WarmPool object
type WarmPoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles the reconciliation loop for WarmPool resources
func (r *WarmPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpool", req.NamespacedName)
	logger.Info("Reconciling WarmPool")

	// Fetch the WarmPool instance
	warmPool := &resources.WarmPool{}
	err := r.Get(ctx, req.NamespacedName, warmPool)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return
			logger.Info("WarmPool resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object
		logger.Error(err, "Failed to get WarmPool")
		return ctrl.Result{}, err
	}

	// Check if the warm pool is being deleted
	if !warmPool.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, warmPool)
	}

	// Add finalizer if it doesn't exist
	if common.AddFinalizer(warmPool, common.WarmPoolFinalizer) {
		if err := r.Update(ctx, warmPool); err != nil {
			logger.Error(err, "Failed to update WarmPool with finalizer")
			return ctrl.Result{}, err
		}
	}

	// Get the current warm pods for this pool
	warmPodList := &resources.WarmPodList{}
	if err := r.List(ctx, warmPodList, client.InNamespace(warmPool.Namespace), client.MatchingLabels{
		common.LabelPoolName: warmPool.Name,
	}); err != nil {
		logger.Error(err, "Failed to list WarmPods")
		return ctrl.Result{}, err
	}

	// Count the number of pods in each state
	var availablePods, assignedPods, pendingPods int
	for _, pod := range warmPodList.Items {
		switch pod.Status.Phase {
		case common.WarmPodPhaseReady:
			availablePods++
		case common.WarmPodPhaseAssigned:
			assignedPods++
		case common.WarmPodPhasePending:
			pendingPods++
		}
	}

	// Update the warm pool status
	warmPool.Status.AvailablePods = availablePods
	warmPool.Status.AssignedPods = assignedPods
	warmPool.Status.PendingPods = pendingPods

	// Check if we need to scale up or down
	needsScaling := false
	
	// Scale up if we have fewer available pods than the minimum size
	if availablePods < warmPool.Spec.MinSize {
		logger.Info("Scaling up warm pool", "availablePods", availablePods, "minSize", warmPool.Spec.MinSize)
		needsScaling = true
		
		// Update conditions
		conditions := warmPool.Status.Conditions
		common.SetCondition(&conditions, common.ConditionScalingUp, metav1.ConditionTrue, common.ReasonScalingUp, fmt.Sprintf("Scaling up to meet minimum size of %d", warmPool.Spec.MinSize))
		warmPool.Status.Conditions = conditions
		
		// Create new warm pods
		podsToCreate := warmPool.Spec.MinSize - availablePods - pendingPods
		for i := 0; i < podsToCreate; i++ {
			if err := r.createWarmPod(ctx, warmPool); err != nil {
				logger.Error(err, "Failed to create WarmPod")
				return ctrl.Result{}, err
			}
		}
		
		// Update the last scale time
		warmPool.Status.LastScaleTime = &metav1.Time{Time: time.Now()}
	}
	
	// Scale down if we have more available pods than the maximum size (if set)
	if warmPool.Spec.MaxSize > 0 && availablePods > warmPool.Spec.MaxSize {
		logger.Info("Scaling down warm pool", "availablePods", availablePods, "maxSize", warmPool.Spec.MaxSize)
		needsScaling = true
		
		// Update conditions
		conditions := warmPool.Status.Conditions
		common.SetCondition(&conditions, common.ConditionScalingDown, metav1.ConditionTrue, common.ReasonScalingDown, fmt.Sprintf("Scaling down to meet maximum size of %d", warmPool.Spec.MaxSize))
		warmPool.Status.Conditions = conditions
		
		// Delete excess warm pods
		podsToDelete := availablePods - warmPool.Spec.MaxSize
		deletedCount := 0
		for _, pod := range warmPodList.Items {
			if pod.Status.Phase == common.WarmPodPhaseReady && deletedCount < podsToDelete {
				if err := r.Delete(ctx, &pod); err != nil {
					logger.Error(err, "Failed to delete WarmPod")
					return ctrl.Result{}, err
				}
				deletedCount++
			}
		}
		
		// Update the last scale time
		warmPool.Status.LastScaleTime = &metav1.Time{Time: time.Now()}
	}
	
	// Check if auto-scaling is enabled
	if warmPool.Spec.AutoScaling != nil && warmPool.Spec.AutoScaling.Enabled {
		// Implement auto-scaling logic here
		// For now, we'll just use the min and max size
	}
	
	// Update the pool status
	if needsScaling {
		if err := r.Status().Update(ctx, warmPool); err != nil {
			logger.Error(err, "Failed to update WarmPool status")
			return ctrl.Result{}, err
		}
	}
	
	// Check if the pool is ready
	isReady := availablePods >= warmPool.Spec.MinSize
	conditions := warmPool.Status.Conditions
	if isReady {
		common.SetCondition(&conditions, common.ConditionPoolReady, metav1.ConditionTrue, common.ReasonPoolReady, "Warm pool has enough available pods")
	} else {
		common.SetCondition(&conditions, common.ConditionPoolReady, metav1.ConditionFalse, common.ReasonPoolNotReady, "Warm pool does not have enough available pods")
	}
	warmPool.Status.Conditions = conditions
	
	if err := r.Status().Update(ctx, warmPool); err != nil {
		logger.Error(err, "Failed to update WarmPool status")
		return ctrl.Result{}, err
	}
	
	// Requeue to periodically check the warm pool
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleDeletion handles the deletion of a warm pool
func (r *WarmPoolReconciler) handleDeletion(ctx context.Context, warmPool *resources.WarmPool) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("warmpool", types.NamespacedName{Name: warmPool.Name, Namespace: warmPool.Namespace})
	logger.Info("Handling warm pool deletion")

	// Check if the finalizer exists
	if controllerutil.ContainsFinalizer(warmPool, common.WarmPoolFinalizer) {
		// Get all warm pods for this pool
		warmPodList := &resources.WarmPodList{}
		if err := r.List(ctx, warmPodList, client.InNamespace(warmPool.Namespace), client.MatchingLabels{
			common.LabelPoolName: warmPool.Name,
		}); err != nil {
			logger.Error(err, "Failed to list WarmPods")
			return ctrl.Result{}, err
		}
		
		// Delete all warm pods
		for _, pod := range warmPodList.Items {
			if err := r.Delete(ctx, &pod); err != nil {
				if !errors.IsNotFound(err) {
					logger.Error(err, "Failed to delete WarmPod")
					return ctrl.Result{}, err
				}
			}
		}
		
		// Check if all warm pods have been deleted
		if len(warmPodList.Items) > 0 {
			// Requeue to check again
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		
		// Remove the finalizer
		controllerutil.RemoveFinalizer(warmPool, common.WarmPoolFinalizer)
		if err := r.Update(ctx, warmPool); err != nil {
			logger.Error(err, "Failed to remove finalizer from WarmPool")
			return ctrl.Result{}, err
		}
	}

	// Warm pool is being deleted, no need to requeue
	logger.Info("Warm pool deletion handled successfully")
	return ctrl.Result{}, nil
}

// createWarmPod creates a new warm pod for a warm pool
func (r *WarmPoolReconciler) createWarmPod(ctx context.Context, warmPool *resources.WarmPool) error {
	logger := log.FromContext(ctx).WithValues("warmpool", types.NamespacedName{Name: warmPool.Name, Namespace: warmPool.Namespace})
	logger.Info("Creating new warm pod")

	// Create a unique name for the warm pod
	warmPodName := fmt.Sprintf("%s-%s", warmPool.Name, generateRandomString(8))
	
	// Create the warm pod
	warmPod := &resources.WarmPod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      warmPodName,
			Namespace: warmPool.Namespace,
			Labels: map[string]string{
				common.LabelApp:       "llmsafespace",
				common.LabelComponent: common.ComponentWarmPod,
				common.LabelPoolName:  warmPool.Name,
				common.LabelRuntime:   warmPool.Spec.Runtime,
			},
		},
		Spec: resources.WarmPodSpec{
			PoolRef: resources.PoolReference{
				Name:      warmPool.Name,
				Namespace: warmPool.Namespace,
			},
			CreationTimestamp: &metav1.Time{Time: time.Now()},
		},
		Status: resources.WarmPodStatus{
			Phase: common.WarmPodPhasePending,
		},
	}
	
	// Set the owner reference
	if err := controllerutil.SetControllerReference(warmPool, warmPod, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference on WarmPod")
		return err
	}
	
	// Create the warm pod
	if err := r.Create(ctx, warmPod); err != nil {
		logger.Error(err, "Failed to create WarmPod")
		return err
	}
	
	return nil
}

// generateRandomString generates a random string of the specified length
func generateRandomString(length int) string {
	// In a real implementation, this would generate a random string
	// For simplicity, we'll just use the current timestamp
	return fmt.Sprintf("%d", time.Now().UnixNano())[:length]
}

// SetupWithManager sets up the controller with the Manager
func (r *WarmPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resources.WarmPool{}).
		Owns(&resources.WarmPod{}).
		Complete(r)
}
