package common

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/resources"
)

// SetCondition updates or creates a condition in the provided slice
func SetCondition(conditions *[]metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.NewTime(time.Now())
	existingCondition := FindCondition(*conditions, conditionType)
	
	if existingCondition == nil {
		// Create new condition
		newCondition := metav1.Condition{
			Type:               conditionType,
			Status:             status,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		}
		*conditions = append(*conditions, newCondition)
		return
	}
	
	// Update existing condition
	if existingCondition.Status != status {
		existingCondition.LastTransitionTime = now
	}
	existingCondition.Status = status
	existingCondition.Reason = reason
	existingCondition.Message = message
}

// FindCondition finds a condition by type in the provided slice
func FindCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// IsConditionTrue checks if a condition with the given type exists and has status True
func IsConditionTrue(conditions []metav1.Condition, conditionType string) bool {
	condition := FindCondition(conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// AddFinalizer adds a finalizer to an object if it doesn't already exist
func AddFinalizer(obj client.Object, finalizer string) bool {
	finalizers := obj.GetFinalizers()
	for _, f := range finalizers {
		if f == finalizer {
			return false
		}
	}
	obj.SetFinalizers(append(finalizers, finalizer))
	return true
}

// RemoveFinalizer removes a finalizer from an object if it exists
func RemoveFinalizer(obj client.Object, finalizer string) bool {
	finalizers := obj.GetFinalizers()
	for i, f := range finalizers {
		if f == finalizer {
			obj.SetFinalizers(append(finalizers[:i], finalizers[i+1:]...))
			return true
		}
	}
	return false
}

// IsPodReady checks if a pod is ready
func IsPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// FindWarmPodForSandbox finds an available warm pod for a sandbox
func FindWarmPodForSandbox(ctx context.Context, c client.Client, sandbox *resources.Sandbox) (*resources.WarmPod, error) {
	warmPodList := &resources.WarmPodList{}
	
	// List all warm pods in the same namespace
	if err := c.List(ctx, warmPodList, client.InNamespace(sandbox.Namespace)); err != nil {
		return nil, err
	}
	
	// Find a warm pod that matches the sandbox requirements and is in Ready state
	for _, warmPod := range warmPodList.Items {
		if warmPod.Status.Phase == WarmPodPhaseReady {
			// Get the warm pool to check if it matches the sandbox requirements
			warmPool := &resources.WarmPool{}
			if err := c.Get(ctx, types.NamespacedName{
				Namespace: warmPod.Spec.PoolRef.Namespace,
				Name:      warmPod.Spec.PoolRef.Name,
			}, warmPool); err != nil {
				continue
			}
			
			// Check if the warm pool runtime matches the sandbox runtime
			if warmPool.Spec.Runtime == sandbox.Spec.Runtime {
				// Check if security levels match
				if warmPool.Spec.SecurityLevel == sandbox.Spec.SecurityLevel {
					return &warmPod, nil
				}
			}
		}
	}
	
	return nil, fmt.Errorf("no suitable warm pod found for sandbox %s/%s", sandbox.Namespace, sandbox.Name)
}

// CreateSandboxPod creates a pod for a sandbox
func CreateSandboxPod(sandbox *resources.Sandbox, warmPod *resources.WarmPod) *corev1.Pod {
	// Implementation will be added in the sandbox controller
	return nil
}

// CreateWarmPod creates a pod for a warm pod
func CreateWarmPod(warmPool *resources.WarmPool, warmPod *resources.WarmPod) *corev1.Pod {
	// Implementation will be added in the warm pod controller
	return nil
}
