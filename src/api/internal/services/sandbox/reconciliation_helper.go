package sandbox

import (
	"context"
	"fmt"
	"time"
	
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
)

const (
	reconciliationInterval = 30 * time.Second
	maxRetries             = 3
	sandboxTimeoutBuffer   = 5 * time.Minute
	stuckSandboxThreshold  = 10 * time.Minute
)

type ReconciliationHelper struct {
	k8sClient interfaces.KubernetesClient
	logger    *logger.Logger
}

func NewReconciliationHelper(k8sClient interfaces.KubernetesClient, logger *logger.Logger) *ReconciliationHelper {
	return &ReconciliationHelper{
		k8sClient: k8sClient,
		logger:    logger.With("component", "reconciliation-helper"),
	}
}

func (h *ReconciliationHelper) StartReconciliationLoop(ctx context.Context) {
	ticker := time.NewTicker(reconciliationInterval)
	defer ticker.Stop()

	h.logger.Info("Starting sandbox reconciliation loop")

	for {
		select {
		case <-ticker.C:
			h.reconcileSandboxes(ctx)
		case <-ctx.Done():
			h.logger.Info("Stopping sandbox reconciliation loop")
			return
		}
	}
}

func (h *ReconciliationHelper) reconcileSandboxes(ctx context.Context) {
	sandboxes, err := h.k8sClient.LlmsafespaceV1().Sandboxes("").List(metav1.ListOptions{})
	if err != nil {
		h.logger.Error("Failed to list sandboxes for reconciliation", err)
		return
	}

	h.logger.Debug("Reconciling sandboxes", "count", len(sandboxes.Items))

	for _, sb := range sandboxes.Items {
		h.handleSandboxReconciliation(ctx, &sb)
	}
}

func (h *ReconciliationHelper) handleSandboxReconciliation(ctx context.Context, sb *types.Sandbox) {
	// Skip if sandbox is already in a terminal state
	if isTerminalState(sb.Status.Phase) {
		return
	}

	// Create a copy of the sandbox to avoid modifying the original
	sandbox := sb.DeepCopy()
	
	// Ensure namespace is set
	if sandbox.Namespace == "" {
		sandbox.Namespace = "default"
	}
	
	needsUpdate := false

	// Check for expired sandboxes
	if sandbox.Spec.Timeout > 0 && sandbox.Status.StartTime != nil {
		expirationTime := sandbox.Status.StartTime.Add(time.Duration(sandbox.Spec.Timeout) * time.Second)
		
		// Add buffer to allow for graceful termination
		expirationTime = expirationTime.Add(sandboxTimeoutBuffer)
		
		if time.Now().After(expirationTime) {
			h.logger.Info("Sandbox expired, marking for termination", 
				"sandbox", sandbox.Name, 
				"namespace", sandbox.Namespace,
				"timeout", sandbox.Spec.Timeout)
			
			// Mark sandbox as terminating
			sandbox.Status.Phase = "Terminating"
			sandbox.Status.Conditions = appendCondition(sandbox.Status.Conditions, types.SandboxCondition{
				Type:               "Expired",
				Status:             "True",
				Reason:             "Timeout",
				Message:            fmt.Sprintf("Sandbox exceeded its timeout of %d seconds", sandbox.Spec.Timeout),
				LastTransitionTime: metav1.Now(),
			})
			needsUpdate = true
		}
	}

	// Check for stuck sandboxes
	if sandbox.Status.Phase == "Creating" || sandbox.Status.Phase == "Pending" {
		creationTime := sandbox.CreationTimestamp.Time
		if time.Since(creationTime) > stuckSandboxThreshold {
			h.logger.Warn("Sandbox appears to be stuck in creation phase", 
				"sandbox", sandbox.Name, 
				"namespace", sandbox.Namespace,
				"phase", sandbox.Status.Phase,
				"creationTime", creationTime)
			
			// Mark sandbox as failed
			sandbox.Status.Phase = "Failed"
			sandbox.Status.Conditions = appendCondition(sandbox.Status.Conditions, types.SandboxCondition{
				Type:               "Stuck",
				Status:             "True",
				Reason:             "CreationTimeout",
				Message:            "Sandbox creation timed out",
				LastTransitionTime: metav1.Now(),
			})
			needsUpdate = true
		}
	}

	// Check if pod exists and update status accordingly
	if sandbox.Status.PodName != "" {
		pod, err := h.k8sClient.Clientset().CoreV1().Pods(sandbox.Status.PodNamespace).Get(ctx, sandbox.Status.PodName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				h.logger.Warn("Pod for sandbox not found", 
					"sandbox", sandbox.Name, 
					"namespace", sandbox.Namespace,
					"podName", sandbox.Status.PodName,
					"podNamespace", sandbox.Status.PodNamespace)
				
				// Mark sandbox as failed if pod is missing
				sandbox.Status.Phase = "Failed"
				sandbox.Status.Conditions = appendCondition(sandbox.Status.Conditions, types.SandboxCondition{
					Type:               "PodMissing",
					Status:             "True",
					Reason:             "PodNotFound",
					Message:            fmt.Sprintf("Pod %s not found", sandbox.Status.PodName),
					LastTransitionTime: metav1.Now(),
				})
				needsUpdate = true
			} else {
				h.logger.Error("Failed to get pod for sandbox", err, 
					"sandbox", sandbox.Name, 
					"namespace", sandbox.Namespace,
					"podName", sandbox.Status.PodName,
					"podNamespace", sandbox.Status.PodNamespace)
			}
		} else {
			// Update sandbox status based on pod status
			newPhase := mapPodPhaseToSandboxPhase(string(pod.Status.Phase))
			if newPhase != sandbox.Status.Phase {
				h.logger.Info("Updating sandbox phase", 
					"sandbox", sandbox.Name, 
					"namespace", sandbox.Namespace,
					"oldPhase", sandbox.Status.Phase,
					"newPhase", newPhase)
				
				sandbox.Status.Phase = newPhase
				sandbox.Status.Conditions = appendCondition(sandbox.Status.Conditions, types.SandboxCondition{
					Type:               "PhaseChanged",
					Status:             "True",
					Reason:             "PodPhaseChanged",
					Message:            fmt.Sprintf("Pod phase changed to %s", pod.Status.Phase),
					LastTransitionTime: metav1.Now(),
				})
				needsUpdate = true
			}
			
			// Update resource usage if pod is running
			if pod.Status.Phase == "Running" {
				// In a real implementation, we would get resource usage from metrics server
				// For now, we'll just set placeholder values
				if sandbox.Status.Resources == nil {
					sandbox.Status.Resources = &types.ResourceStatus{}
				}
				
				// These would be populated with actual metrics in a real implementation
				sandbox.Status.Resources.CPUUsage = "0.1"
				sandbox.Status.Resources.MemoryUsage = "256Mi"
				needsUpdate = true
			}
		}
	}

	// Update sandbox if needed
	if needsUpdate {
		_, err := h.k8sClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(sandbox)
		if err != nil {
			h.logger.Error("Failed to update sandbox status", err, 
				"sandbox", sandbox.Name, 
				"namespace", sandbox.Namespace)
		}
	}
}

// Helper functions

func isTerminalState(phase string) bool {
	return phase == "Terminated" || phase == "Failed"
}

func mapPodPhaseToSandboxPhase(podPhase string) string {
	switch podPhase {
	case "Pending":
		return "Creating"
	case "Running":
		return "Running"
	case "Succeeded":
		return "Terminated"
	case "Failed":
		return "Failed"
	case "Unknown":
		return "Unknown"
	default:
		return "Unknown"
	}
}

func appendCondition(conditions []types.SandboxCondition, condition types.SandboxCondition) []types.SandboxCondition {
	// Check if condition already exists
	for i, c := range conditions {
		if c.Type == condition.Type {
			// Update existing condition
			conditions[i] = condition
			return conditions
		}
	}
	
	// Add new condition
	return append(conditions, condition)
}
