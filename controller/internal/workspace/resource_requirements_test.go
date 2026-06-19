package workspace

import (
	"testing"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func assertQuantityEqual(t *testing.T, expected string, actual resource.Quantity) {
	t.Helper()
	exp := resource.MustParse(expected)
	if exp.Cmp(actual) != 0 {
		t.Errorf("expected %s, got %s", exp.String(), actual.String())
	}
}

func TestResourceRequirements_BurstableDefaults(t *testing.T) {
	ws := &v1.Workspace{}
	rr := resourceRequirementsFor(ws)

	assertQuantityEqual(t, "500m", rr.Requests[corev1.ResourceCPU])
	assertQuantityEqual(t, "512Mi", rr.Requests[corev1.ResourceMemory])

	// Limits should be 4× requests for CPU and memory
	assertQuantityEqual(t, "2000m", rr.Limits[corev1.ResourceCPU])
	assertQuantityEqual(t, "2Gi", rr.Limits[corev1.ResourceMemory])

	// Ephemeral storage is intentionally NOT set on the pod — see
	// resourceRequirementsFor docstring. Assert it stays absent so the
	// pod inherits node defaults / kubelet eviction behavior.
	if _, ok := rr.Requests[corev1.ResourceEphemeralStorage]; ok {
		t.Error("ephemeral-storage must not be present in Requests")
	}
	if _, ok := rr.Limits[corev1.ResourceEphemeralStorage]; ok {
		t.Error("ephemeral-storage must not be present in Limits")
	}
}

func TestResourceRequirements_CustomRequestEmptyLimit(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Resources: &v1.ResourceRequirements{
				CPU:    "1000m",
				Memory: "1Gi",
			},
		},
	}
	rr := resourceRequirementsFor(ws)

	assertQuantityEqual(t, "1000m", rr.Requests[corev1.ResourceCPU])
	assertQuantityEqual(t, "1Gi", rr.Requests[corev1.ResourceMemory])
	// 4× custom request
	assertQuantityEqual(t, "4000m", rr.Limits[corev1.ResourceCPU])
	assertQuantityEqual(t, "4Gi", rr.Limits[corev1.ResourceMemory])
}

func TestResourceRequirements_CustomBoth(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Resources: &v1.ResourceRequirements{
				CPU:         "500m",
				Memory:      "512Mi",
				CPULimit:    "1000m",
				MemoryLimit: "1Gi",
			},
		},
	}
	rr := resourceRequirementsFor(ws)

	assertQuantityEqual(t, "500m", rr.Requests[corev1.ResourceCPU])
	assertQuantityEqual(t, "512Mi", rr.Requests[corev1.ResourceMemory])
	assertQuantityEqual(t, "1000m", rr.Limits[corev1.ResourceCPU])
	assertQuantityEqual(t, "1Gi", rr.Limits[corev1.ResourceMemory])
}

func TestResourceRequirements_LimitEqualsRequest_Allowed(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Resources: &v1.ResourceRequirements{
				CPU:         "500m",
				Memory:      "512Mi",
				CPULimit:    "500m",
				MemoryLimit: "512Mi",
			},
		},
	}
	rr := resourceRequirementsFor(ws)

	// Guaranteed QoS: limit = request
	assertQuantityEqual(t, "500m", rr.Limits[corev1.ResourceCPU])
	assertQuantityEqual(t, "512Mi", rr.Limits[corev1.ResourceMemory])
}

func TestResourceRequirements_EmptyRequestCustomLimit(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Resources: &v1.ResourceRequirements{
				MemoryLimit: "4Gi",
				CPULimit:    "4000m",
			},
		},
	}
	rr := resourceRequirementsFor(ws)

	// Request uses defaults, limit uses custom
	assertQuantityEqual(t, "500m", rr.Requests[corev1.ResourceCPU])
	assertQuantityEqual(t, "512Mi", rr.Requests[corev1.ResourceMemory])
	assertQuantityEqual(t, "4000m", rr.Limits[corev1.ResourceCPU])
	assertQuantityEqual(t, "4Gi", rr.Limits[corev1.ResourceMemory])
}

func TestResourceRequirements_InvalidCPU_FallsBackToDefault(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Resources: &v1.ResourceRequirements{
				CPU: "not-a-quantity",
			},
		},
	}
	rr := resourceRequirementsFor(ws)

	// Falls back to default
	assertQuantityEqual(t, "500m", rr.Requests[corev1.ResourceCPU])
	assertQuantityEqual(t, "2000m", rr.Limits[corev1.ResourceCPU])
}

func TestResourceRequirements_InvalidLimit_FallsBackTo4xRequest(t *testing.T) {
	ws := &v1.Workspace{
		Spec: v1.WorkspaceSpec{
			Resources: &v1.ResourceRequirements{
				CPU:      "1000m",
				CPULimit: "not-valid",
			},
		},
	}
	rr := resourceRequirementsFor(ws)

	// Request is valid, limit falls back to 4× request
	assertQuantityEqual(t, "1000m", rr.Requests[corev1.ResourceCPU])
	assertQuantityEqual(t, "4000m", rr.Limits[corev1.ResourceCPU])
}
