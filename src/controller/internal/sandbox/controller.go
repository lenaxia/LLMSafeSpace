package sandbox

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

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/common"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/resources"
)

// SandboxReconciler reconciles a Sandbox object
type SandboxReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles the reconciliation loop for Sandbox resources
func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", req.NamespacedName)
	logger.Info("Reconciling Sandbox")

	// Fetch the Sandbox instance
	sandbox := &resources.Sandbox{}
	err := r.Get(ctx, req.NamespacedName, sandbox)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return
			logger.Info("Sandbox resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object
		logger.Error(err, "Failed to get Sandbox")
		return ctrl.Result{}, err
	}

	// Check if the sandbox is being deleted
	if !sandbox.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, sandbox)
	}

	// Add finalizer if it doesn't exist
	if common.AddFinalizer(sandbox, common.SandboxFinalizer) {
		if err := r.Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox with finalizer")
			return ctrl.Result{}, err
		}
	}

	// Handle sandbox based on its phase
	switch sandbox.Status.Phase {
	case "", common.SandboxPhasePending:
		return r.handlePendingSandbox(ctx, sandbox)
	case common.SandboxPhaseCreating:
		return r.handleCreatingSandbox(ctx, sandbox)
	case common.SandboxPhaseRunning:
		return r.handleRunningSandbox(ctx, sandbox)
	case common.SandboxPhaseTerminating:
		return r.handleTerminatingSandbox(ctx, sandbox)
	case common.SandboxPhaseTerminated, common.SandboxPhaseFailed:
		// Nothing to do for terminated or failed sandboxes
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown sandbox phase", "phase", sandbox.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// handlePendingSandbox handles a sandbox in the Pending phase
func (r *SandboxReconciler) handlePendingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling pending sandbox")

	// Update the sandbox status to Creating
	sandbox.Status.Phase = common.SandboxPhaseCreating
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status to Creating")
		return ctrl.Result{}, err
	}

	// Try to find a suitable warm pod
	warmPod, err := common.FindWarmPodForSandbox(ctx, r.Client, sandbox)
	if err == nil {
		// Found a suitable warm pod, use it
		logger.Info("Found suitable warm pod", "warmPod", warmPod.Name)
		return r.assignWarmPodToSandbox(ctx, sandbox, warmPod)
	}

	// No suitable warm pod found, create a new pod
	logger.Info("No suitable warm pod found, creating new pod")
	return r.createSandboxPod(ctx, sandbox)
}

// handleCreatingSandbox handles a sandbox in the Creating phase
func (r *SandboxReconciler) handleCreatingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling creating sandbox")

	// Check if the pod exists
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod doesn't exist, revert to pending
			logger.Info("Pod not found, reverting to pending")
			sandbox.Status.Phase = common.SandboxPhasePending
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Pending")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	// Check if the pod is running
	if pod.Status.Phase == corev1.PodRunning {
		// Update the sandbox status to Running
		sandbox.Status.Phase = common.SandboxPhaseRunning
		sandbox.Status.StartTime = &metav1.Time{Time: time.Now()}
		
		// Set the endpoint
		sandbox.Status.Endpoint = fmt.Sprintf("%s.%s.svc.cluster.local", pod.Name, pod.Namespace)
		
		// Update conditions
		conditions := []resources.SandboxCondition{}
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

	// Pod is not running yet, requeue
	logger.Info("Pod is not running yet", "podPhase", pod.Status.Phase)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// handleRunningSandbox handles a sandbox in the Running phase
func (r *SandboxReconciler) handleRunningSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling running sandbox")

	// Check if the pod exists
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod doesn't exist, mark as failed
			logger.Info("Pod not found, marking sandbox as failed")
			sandbox.Status.Phase = common.SandboxPhaseFailed
			
			// Update conditions
			conditions := sandbox.Status.Conditions
			common.SetSandboxCondition(&conditions, common.ConditionPodRunning, "False", common.ReasonPodNotRunning, "Pod not found")
			common.SetSandboxCondition(&conditions, common.ConditionReady, "False", common.ReasonPodNotRunning, "Sandbox failed")
			sandbox.Status.Conditions = conditions
			
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Failed")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	// Check if the pod is still running
	if pod.Status.Phase != corev1.PodRunning {
		// Pod is not running, mark as failed
		logger.Info("Pod is not running", "podPhase", pod.Status.Phase)
		sandbox.Status.Phase = common.SandboxPhaseFailed
		
		// Update conditions
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

	// Check if the sandbox has exceeded its timeout
	if sandbox.Spec.Timeout > 0 && sandbox.Status.StartTime != nil {
		timeout := time.Duration(sandbox.Spec.Timeout) * time.Second
		if time.Since(sandbox.Status.StartTime.Time) > timeout {
			// Sandbox has exceeded its timeout, terminate it
			logger.Info("Sandbox has exceeded its timeout, terminating")
			sandbox.Status.Phase = common.SandboxPhaseTerminating
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminating")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// Update resource usage
	if pod.Status.Phase == corev1.PodRunning {
		// In a real implementation, we would get resource usage from metrics server
		// For now, we'll just set placeholder values
		if sandbox.Status.Resources == nil {
			sandbox.Status.Resources = &resources.ResourceStatus{}
		}
		sandbox.Status.Resources.CPUUsage = "100m"
		sandbox.Status.Resources.MemoryUsage = "256Mi"
		
		if err := r.Status().Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to update Sandbox resource usage")
			return ctrl.Result{}, err
		}
	}

	// Requeue to periodically check the sandbox
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleTerminatingSandbox handles a sandbox in the Terminating phase
func (r *SandboxReconciler) handleTerminatingSandbox(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling terminating sandbox")

	// Check if the pod exists
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod doesn't exist, mark as terminated
			logger.Info("Pod not found, marking sandbox as terminated")
			sandbox.Status.Phase = common.SandboxPhaseTerminated
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminated")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	// Delete the pod
	logger.Info("Deleting pod", "pod", pod.Name)
	if err := r.Delete(ctx, pod); err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete Pod")
			return ctrl.Result{}, err
		}
	}

	// Check if the pod has been deleted
	err = r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod has been deleted, mark as terminated
			logger.Info("Pod deleted, marking sandbox as terminated")
			sandbox.Status.Phase = common.SandboxPhaseTerminated
			if err := r.Status().Update(ctx, sandbox); err != nil {
				logger.Error(err, "Failed to update Sandbox status to Terminated")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		// Error reading the pod
		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	// Pod is still being deleted, requeue
	logger.Info("Pod is still being deleted")
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// handleDeletion handles the deletion of a sandbox
func (r *SandboxReconciler) handleDeletion(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Handling sandbox deletion")

	// Check if the finalizer exists
	if controllerutil.ContainsFinalizer(sandbox, common.SandboxFinalizer) {
		// Update the sandbox status to Terminating if it's not already
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

		// Check if the pod exists
		if sandbox.Status.PodName != "" {
			pod := &corev1.Pod{}
			err := r.Get(ctx, types.NamespacedName{Name: sandbox.Status.PodName, Namespace: sandbox.Status.PodNamespace}, pod)
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

		// Check if the sandbox was using a warm pod
		if warmPodName := sandbox.Annotations[common.AnnotationWarmPodID]; warmPodName != "" {
			// Get the warm pod
			warmPod := &resources.WarmPod{}
			err := r.Get(ctx, types.NamespacedName{Name: warmPodName, Namespace: sandbox.Namespace}, warmPod)
			if err == nil {
				// Check if the warm pod can be recycled
				if sandbox.Annotations[common.AnnotationRecyclable] == "true" {
					// Recycle the warm pod
					logger.Info("Recycling warm pod", "warmPod", warmPod.Name)
					warmPod.Status.Phase = common.WarmPodPhaseReady
					warmPod.Status.AssignedTo = ""
					warmPod.Status.AssignedAt = nil
					if err := r.Status().Update(ctx, warmPod); err != nil {
						logger.Error(err, "Failed to update WarmPod status")
						return ctrl.Result{}, err
					}
				} else {
					// Delete the warm pod
					logger.Info("Deleting warm pod", "warmPod", warmPod.Name)
					if err := r.Delete(ctx, warmPod); err != nil {
						if !errors.IsNotFound(err) {
							logger.Error(err, "Failed to delete WarmPod")
							return ctrl.Result{}, err
						}
					}
				}
			} else if !errors.IsNotFound(err) {
				// Error reading the warm pod
				logger.Error(err, "Failed to get WarmPod")
				return ctrl.Result{}, err
			}
		}

		// Remove the finalizer
		controllerutil.RemoveFinalizer(sandbox, common.SandboxFinalizer)
		if err := r.Update(ctx, sandbox); err != nil {
			logger.Error(err, "Failed to remove finalizer from Sandbox")
			return ctrl.Result{}, err
		}
	}

	// Sandbox is being deleted, no need to requeue
	logger.Info("Sandbox deletion handled successfully")
	return ctrl.Result{}, nil
}

// assignWarmPodToSandbox assigns a warm pod to a sandbox
func (r *SandboxReconciler) assignWarmPodToSandbox(ctx context.Context, sandbox *resources.Sandbox, warmPod *resources.WarmPod) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues(
		"sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace},
		"warmPod", types.NamespacedName{Name: warmPod.Name, Namespace: warmPod.Namespace},
	)
	logger.Info("Assigning warm pod to sandbox")

	// Update the warm pod status
	warmPod.Status.Phase = common.WarmPodPhaseAssigned
	warmPod.Status.AssignedTo = sandbox.Name
	warmPod.Status.AssignedAt = &metav1.Time{Time: time.Now()}
	if err := r.Status().Update(ctx, warmPod); err != nil {
		logger.Error(err, "Failed to update WarmPod status")
		return ctrl.Result{}, err
	}

	// Update the sandbox status
	sandbox.Status.PodName = warmPod.Status.PodName
	sandbox.Status.PodNamespace = warmPod.Status.PodNamespace
	
	// Add annotations to track the warm pod
	if sandbox.Annotations == nil {
		sandbox.Annotations = make(map[string]string)
	}
	sandbox.Annotations[common.AnnotationWarmPodID] = warmPod.Name
	sandbox.Annotations[common.AnnotationRecyclable] = "true"
	
	if err := r.Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox with warm pod information")
		return ctrl.Result{}, err
	}

	// Requeue to check if the pod is running
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// createSandboxPod creates a new pod for a sandbox
func (r *SandboxReconciler) createSandboxPod(ctx context.Context, sandbox *resources.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sandbox", types.NamespacedName{Name: sandbox.Name, Namespace: sandbox.Namespace})
	logger.Info("Creating new pod for sandbox")

	// Create a new pod for the sandbox
	pod := r.buildSandboxPod(sandbox)
	
	// Set the owner reference
	if err := controllerutil.SetControllerReference(sandbox, pod, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference on Pod")
		return ctrl.Result{}, err
	}
	
	// Create the pod
	if err := r.Create(ctx, pod); err != nil {
		logger.Error(err, "Failed to create Pod")
		return ctrl.Result{}, err
	}
	
	// Update the sandbox status
	sandbox.Status.PodName = pod.Name
	sandbox.Status.PodNamespace = pod.Namespace
	
	// Update conditions
	conditions := []resources.SandboxCondition{}
	common.SetSandboxCondition(&conditions, common.ConditionPodCreated, "True", common.ReasonPodCreated, "Pod created successfully")
	sandbox.Status.Conditions = conditions
	
	if err := r.Status().Update(ctx, sandbox); err != nil {
		logger.Error(err, "Failed to update Sandbox status with pod information")
		return ctrl.Result{}, err
	}
	
	// Requeue to check if the pod is running
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// buildSandboxPod builds a pod for a sandbox
func (r *SandboxReconciler) buildSandboxPod(sandbox *resources.Sandbox) *corev1.Pod {
	// Create a unique name for the pod
	podName := fmt.Sprintf("%s-%s", sandbox.Name, sandbox.UID[0:8])
	
	// Define labels and annotations
	labels := map[string]string{
		common.LabelApp:       "llmsafespace",
		common.LabelComponent: common.ComponentSandbox,
		common.LabelSandboxID: sandbox.Name,
		common.LabelRuntime:   sandbox.Spec.Runtime,
	}
	
	annotations := map[string]string{
		common.AnnotationCreatedBy: common.ControllerName,
		common.AnnotationSandboxID: sandbox.Name,
	}
	
	// Define the pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   sandbox.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "sandbox",
					Image: sandbox.Spec.Runtime,
					// Add more container configuration based on sandbox spec
				},
			},
			// Add more pod configuration based on sandbox spec
		},
	}
	
	// Configure resources if specified
	if sandbox.Spec.Resources != nil {
		pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
			// Configure resource limits and requests
		}
	}
	
	// Configure security context if specified
	if sandbox.Spec.SecurityContext != nil {
		pod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
			// Configure security context
		}
	}
	
	// Configure filesystem if specified
	if sandbox.Spec.Filesystem != nil {
		// Configure volumes and volume mounts
	}
	
	// Configure network if specified
	if sandbox.Spec.NetworkAccess != nil {
		// Configure network policies
	}
	
	return pod
}

// SetupWithManager sets up the controller with the Manager
func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resources.Sandbox{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
