package client

import (
	"fmt"
	"strings"

	"github.com/lenaxia/llmsafespace/api/internal/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConvertToCRD converts an API sandbox request to a Kubernetes CRD
func ConvertToCRD(req types.CreateSandboxRequest, useWarmPod bool) *types.Sandbox {
	// Create labels
	labels := map[string]string{
		"app":           "llmsafespace",
		"user-id":       req.UserID,
		"runtime":       sanitizeRuntimeLabel(req.Runtime),
	}

	// Create annotations
	annotations := map[string]string{
		"llmsafespace.dev/use-warm-pod":  fmt.Sprintf("%t", useWarmPod),
		"llmsafespace.dev/security-level": req.SecurityLevel,
	}

	// Create sandbox object
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "sb-",
			Namespace:    req.Namespace,
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: types.SandboxSpec{
			Runtime:       req.Runtime,
			SecurityLevel: req.SecurityLevel,
			Timeout:       req.Timeout,
			Resources:     req.Resources,
			NetworkAccess: req.NetworkAccess,
		},
	}

	// Set default timeout if not specified
	if sandbox.Spec.Timeout == 0 {
		sandbox.Spec.Timeout = 300 // 5 minutes default
	}

	// Set default security level if not specified
	if sandbox.Spec.SecurityLevel == "" {
		sandbox.Spec.SecurityLevel = "standard"
	}

	return sandbox
}

// ConvertFromCRD converts a Kubernetes CRD to an API sandbox
func ConvertFromCRD(crd *types.Sandbox) *types.Sandbox {
	// Create a copy to avoid modifying the original
	sandbox := crd.DeepCopy()
	
	// Ensure namespace is set
	if sandbox.Namespace == "" {
		sandbox.Namespace = "default"
	}
	
	return sandbox
}

// Helper functions

// sanitizeRuntimeLabel converts a runtime string to a valid label value
func sanitizeRuntimeLabel(runtime string) string {
	// Replace invalid characters with dashes
	return strings.Replace(runtime, ":", "-", -1)
}
