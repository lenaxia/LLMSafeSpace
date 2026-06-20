// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

func newPodForQuota(t *testing.T) *corev1.Pod {
	t.Helper()
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ws-test-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app":                      "llmsafespaces",
				"component":                "workspace",
				"llmsafespaces.dev/tenant": "user-1",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
	}
}

func quotaValidatorWith(c client.Client, maxWS int, maxCPU, maxMem int64) *PodTenantQuotaValidator {
	return &PodTenantQuotaValidator{
		Decoder:                admission.NewDecoder(runtime.NewScheme()),
		Client:                 c,
		MaxWorkspacesPerTenant: maxWS,
		MaxCPUMillisPerTenant:  maxCPU,
		MaxMemoryMiPerTenant:   maxMem,
	}
}

func newPodCreateRequest(t *testing.T, pod *corev1.Pod) admission.Request {
	t.Helper()
	raw, err := json.Marshal(pod)
	require.NoError(t, err)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Namespace: pod.Namespace,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

// --- Allowing cases ---

func TestS51_2_AllowWhenNoTenantLabel(t *testing.T) {
	validator := quotaValidatorWith(nil, 5, 8000, 16384)
	pod := newPodForQuota(t)
	delete(pod.Labels, "llmsafespaces.dev/tenant")

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.True(t, resp.Allowed)
}

func TestS51_2_AllowWhenUnderQuota(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(testSchemeQuota(t)).Build()
	validator := quotaValidatorWith(fakeClient, 5, 8000, 16384)
	pod := newPodForQuota(t)

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.True(t, resp.Allowed)
}

func TestS51_2_AllowWhenAllLimitsZero(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(testSchemeQuota(t)).Build()
	validator := quotaValidatorWith(fakeClient, 0, 0, 0)
	pod := newPodForQuota(t)

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.True(t, resp.Allowed, "all limits disabled = unlimited")
}

func TestS51_2_AllowOnNonCreate(t *testing.T) {
	validator := quotaValidatorWith(nil, 1, 1, 1)
	pod := newPodForQuota(t)

	resp := validator.Handle(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Namespace: "default",
			Object:    mustMarshal(t, pod),
		},
	})
	require.True(t, resp.Allowed)
}

func TestS51_2_FailOpenOnUsageError(t *testing.T) {
	// nil client will cause List to error; webhook should fail open
	validator := &PodTenantQuotaValidator{
		Decoder:                admission.NewDecoder(runtime.NewScheme()),
		MaxWorkspacesPerTenant: 1,
	}
	pod := newPodForQuota(t)

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.True(t, resp.Allowed, "should fail open on usage query error")
}

// --- Denying cases ---

func TestS51_2_DenyExceedsWorkspaceCount(t *testing.T) {
	scheme := testSchemeQuota(t)
	existing := makeExistingPods("user-1", "default", 5)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(podsToRuntimeObjects(existing)...).Build()

	validator := quotaValidatorWith(fakeClient, 5, 0, 0)
	pod := newPodForQuota(t)

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.False(t, resp.Allowed, "should deny when workspace count exceeds limit")
}

func TestS51_2_DenyExceedsCPU(t *testing.T) {
	scheme := testSchemeQuota(t)
	existing := makeExistingPodsWithResources("user-1", "default", 1, "8000m", "1Gi")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(podsToRuntimeObjects(existing)...).Build()

	validator := quotaValidatorWith(fakeClient, 0, 8000, 0)
	pod := newPodForQuota(t) // requests 500m → total 8500m > 8000m

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.False(t, resp.Allowed, "should deny when CPU exceeds limit")
}

func TestS51_2_DenyExceedsMemory(t *testing.T) {
	scheme := testSchemeQuota(t)
	existing := makeExistingPodsWithResources("user-1", "default", 1, "100m", "16384Mi")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(podsToRuntimeObjects(existing)...).Build()

	validator := quotaValidatorWith(fakeClient, 0, 0, 16384)
	pod := newPodForQuota(t) // requests 512Mi → total 16896Mi > 16384Mi

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.False(t, resp.Allowed, "should deny when memory exceeds limit")
}

// --- Isolation between tenants ---

func TestS51_2_DifferentTenantsDoNotInterfere(t *testing.T) {
	scheme := testSchemeQuota(t)
	existingA := makeExistingPods("tenant-a", "default", 5)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(podsToRuntimeObjects(existingA)...).Build()

	// tenant-B should be allowed even though tenant-A is at the limit
	validator := quotaValidatorWith(fakeClient, 5, 0, 0)
	pod := newPodForQuota(t)
	pod.Labels["llmsafespaces.dev/tenant"] = "tenant-b"

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.True(t, resp.Allowed, "tenant-B must not be affected by tenant-A's usage")
}

// --- Terminal pods excluded ---

func TestS51_2_TerminalPodsExcluded(t *testing.T) {
	scheme := testSchemeQuota(t)
	// Create 5 pods but mark them all as Succeeded (terminal)
	existing := makeExistingPods("user-1", "default", 5)
	for _, p := range existing {
		p.Status.Phase = corev1.PodSucceeded
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(podsToRuntimeObjects(existing)...).Build()

	validator := quotaValidatorWith(fakeClient, 5, 0, 0)
	pod := newPodForQuota(t)

	resp := validator.Handle(context.Background(), newPodCreateRequest(t, pod))
	require.True(t, resp.Allowed, "terminal pods should not count toward quota")
}

// --- Helpers ---

func testSchemeQuota(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1.AddToScheme(scheme))
	return scheme
}

func makeExistingPods(tenantID, namespace string, count int) []*corev1.Pod {
	pods := make([]*corev1.Pod, count)
	for i := 0; i < count; i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tenantID + "-pod-" + string(rune('a'+i)),
				Namespace: namespace,
				Labels: map[string]string{
					"app":                      "llmsafespaces",
					"component":                "workspace",
					"llmsafespaces.dev/tenant": tenantID,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
	}
	return pods
}

func makeExistingPodsWithResources(tenantID, namespace string, count int, cpu, mem string) []*corev1.Pod {
	pods := makeExistingPods(tenantID, namespace, count)
	for _, p := range pods {
		p.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(mem),
		}
	}
	return pods
}

func podsToRuntimeObjects(pods []*corev1.Pod) []runtime.Object {
	objs := make([]runtime.Object, len(pods))
	for i, p := range pods {
		objs[i] = p
	}
	return objs
}

func mustMarshal(t *testing.T, obj interface{}) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(obj)
	require.NoError(t, err)
	return runtime.RawExtension{Raw: raw}
}
