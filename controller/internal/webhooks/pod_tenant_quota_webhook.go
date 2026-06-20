// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-pod-tenant-quota,mutating=false,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,name=vpodtenantquota.kb.io,sideEffects=None,admissionReviewVersions=v1

// PodTenantQuotaValidator enforces per-tenant resource quotas on workspace
// pods (Epic 51 S51.2). It prevents noisy-neighbor resource exhaustion in
// multi-tenant deployments by rejecting pod creation when the tenant's
// aggregate running resource usage would exceed configured limits.
//
// The webhook is keyed on the llmsafespaces.dev/tenant pod label (set by
// the controller's pod builder per S51.3). Pods without this label are
// allowed unconditionally (filtered by objectSelector in the webhook config).
//
// Quota limits are instance-level defaults from operator flags:
//   - MaxWorkspacesPerTenant: maximum concurrent workspace pods per tenant
//   - MaxCPUMillisPerTenant: maximum aggregate CPU requests per tenant
//   - MaxMemoryMiPerTenant: maximum aggregate memory requests per tenant
//
// Org-specific quota overrides (from org_policies) are a follow-up;
// billing tier → quota mapping is tracked under Epic 43.
//
// Race window: the check-and-schedule gap between admission and pod
// scheduling is acceptable — workspace pods are long-lived; this guards
// against gross overage, not precise concurrency control.
type PodTenantQuotaValidator struct {
	Decoder                admission.Decoder
	Client                 client.Client
	MaxWorkspacesPerTenant int
	MaxCPUMillisPerTenant  int64
	MaxMemoryMiPerTenant   int64
}

// tenantLabelKey must match controller/internal/workspace/constants.go:LabelTenant.
const tenantLabelKey = "llmsafespaces.dev/tenant"

// workspaceComponentLabel matches the pod selector used by workspace pods.
const workspaceComponentLabel = "component"

// Handle implements admission.Handler. It validates that creating this
// pod does not exceed the tenant's resource quota.
func (v *PodTenantQuotaValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if v.Decoder == nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("decoder not configured"))
	}

	pod := &corev1.Pod{}
	if err := v.Decoder.Decode(req, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Only enforce on CREATE; UPDATE doesn't add new resource consumption.
	if req.Operation != "CREATE" {
		return admission.Allowed("quota check skipped for non-create operation")
	}

	tenantID, ok := pod.Labels[tenantLabelKey]
	if !ok || tenantID == "" || tenantID == "unspecified" {
		// No tenant label — not a workspace pod, or tenant identity unknown.
		// Allow (objectSelector should have filtered these, but be defensive).
		return admission.Allowed("pod has no tenant label — quota check skipped")
	}

	// Count existing workspace pods for this tenant and sum their resource requests.
	currentCount, currentCPU, currentMem, err := v.tenantUsage(ctx, tenantID, pod.Namespace)
	if err != nil {
		// Fail open on usage-query errors — denying all pods on a transient
		// API server hiccup is worse than allowing a potential overage.
		// The TOCTOU window is already accepted per the design doc.
		return admission.Allowed(fmt.Sprintf("quota check skipped due to usage query error: %v", err))
	}
	// Add the incoming pod's requests.
	newCPU, newMem := podResourceRequests(pod)
	projectedCPU := currentCPU + newCPU
	projectedMem := currentMem + newMem
	projectedCount := currentCount + 1

	// Check limits. A limit of 0 means "unlimited" (disabled).
	if v.MaxWorkspacesPerTenant > 0 && projectedCount > v.MaxWorkspacesPerTenant {
		return admission.Denied(fmt.Sprintf(
			"tenant %q workspace count %d would exceed limit %d",
			tenantID, projectedCount, v.MaxWorkspacesPerTenant,
		))
	}
	if v.MaxCPUMillisPerTenant > 0 && projectedCPU > v.MaxCPUMillisPerTenant {
		return admission.Denied(fmt.Sprintf(
			"tenant %q CPU %dm would exceed limit %dm",
			tenantID, projectedCPU, v.MaxCPUMillisPerTenant,
		))
	}
	if v.MaxMemoryMiPerTenant > 0 && projectedMem > v.MaxMemoryMiPerTenant {
		return admission.Denied(fmt.Sprintf(
			"tenant %q memory %dMi would exceed limit %dMi",
			tenantID, projectedMem, v.MaxMemoryMiPerTenant,
		))
	}

	return admission.Allowed(fmt.Sprintf(
		"tenant %q within quota (workspaces=%d/%d, cpu=%dm/%dm, mem=%dMi/%dMi)",
		tenantID, projectedCount, v.MaxWorkspacesPerTenant,
		projectedCPU, v.MaxCPUMillisPerTenant,
		projectedMem, v.MaxMemoryMiPerTenant,
	))
}

// tenantUsage returns (podCount, cpuMillis, memoryMi) for all workspace
// pods belonging to the given tenant in the given namespace. Only counts
// pods that are Running or Pending (not Terminated/Succeeded/Failed).
//
// NOTE: Usage is scoped to the pod's namespace, not aggregated across all
// namespaces. For single-namespace deployments (the standard architecture),
// this is equivalent to per-tenant scoping. For multi-namespace deployments,
// a tenant could theoretically span namespaces and exceed the aggregate
// limit. This is accepted given the single-namespace architecture; if
// multi-namespace is ever adopted, change to a cluster-wide List with
// the tenant label selector only (drop InNamespace).
func (v *PodTenantQuotaValidator) tenantUsage(ctx context.Context, tenantID, namespace string) (int, int64, int64, error) {
	if v.Client == nil {
		return 0, 0, 0, fmt.Errorf("client not configured")
	}
	podList := &corev1.PodList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{
			tenantLabelKey:          tenantID,
			workspaceComponentLabel: "workspace",
		},
	}
	if err := v.Client.List(ctx, podList, opts...); err != nil {
		return 0, 0, 0, fmt.Errorf("listing workspace pods: %w", err)
	}

	var count int
	var cpuMillis int64
	var memMi int64
	for i := range podList.Items {
		pod := &podList.Items[i]
		// Skip terminal pods — they're releasing resources.
		switch pod.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			continue
		}
		count++
		c, m := podResourceRequests(pod)
		cpuMillis += c
		memMi += m
	}
	return count, cpuMillis, memMi, nil
}

// podResourceRequests sums CPU (millicores) and memory (Mi) requests
// across all containers (init + main) in the pod. If a container has no
// request, it contributes 0 (best-effort).
func podResourceRequests(pod *corev1.Pod) (cpuMillis int64, memMi int64) {
	containers := append([]corev1.Container{}, pod.Spec.Containers...)
	containers = append(containers, pod.Spec.InitContainers...)
	for _, c := range containers {
		if c.Resources.Requests.Cpu() != nil {
			cpuMillis += c.Resources.Requests.Cpu().MilliValue()
		}
		if c.Resources.Requests.Memory() != nil {
			memMi += resourceToMi(c.Resources.Requests.Memory())
		}
	}
	return
}

// resourceToMi converts a resource.Quantity (bytes) to mebibytes (integer).
func resourceToMi(q *resource.Quantity) int64 {
	if q == nil {
		return 0
	}
	return q.Value() / (1024 * 1024)
}

// Compile-time interface check.
var _ admission.Handler = &PodTenantQuotaValidator{}
