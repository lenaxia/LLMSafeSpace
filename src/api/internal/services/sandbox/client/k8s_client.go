package client

import (
	"github.com/lenaxia/llmsafespace/api/internal/types"
	sandboxv1 "github.com/lenaxia/llmsafespace/apis/llmsafespace/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ConvertToCRD(req types.CreateSandboxRequest, useWarmPod bool) *sandboxv1.Sandbox {
	return &sandboxv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "sb-",
			Labels: map[string]string{
				"app":        "llmsafespace",
				"user-id":    req.UserID,
				"runtime":    sanitizeRuntimeLabel(req.Runtime),
				"warm-pool":  "requested",
			},
			Annotations: map[string]string{
				"llmsafespace.dev/use-warm-pod":  fmt.Sprintf("%t", useWarmPod),
				"llmsafespace.dev/security-level": req.SecurityLevel,
			},
		},
		Spec: sandboxv1.SandboxSpec{
			Runtime:       req.Runtime,
			SecurityLevel: req.SecurityLevel,
			Timeout:       req.Timeout,
			Resources:     convertResourceRequirements(req.Resources),
			NetworkAccess: convertNetworkAccess(req.NetworkAccess),
		},
	}
}

func ConvertFromCRD(crd *sandboxv1.Sandbox) *types.Sandbox {
	return &types.Sandbox{
		ID:        crd.Name,
		Runtime:   crd.Spec.Runtime,
		Status:    string(crd.Status.Phase),
		CreatedAt: crd.CreationTimestamp.Time,
		Endpoint:  crd.Status.Endpoint,
		// ... additional fields
	}
}

// Helper functions for converting between API types and CRD types
