// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build envtest

package webhooks

// Epic 51 S51.2 — envtest integration test for the tenant quota webhook.
//
// This is the FIRST webhook integration test in the codebase. It verifies
// that the PodTenantQuotaValidator actually intercepts pod creates via the
// real Kubernetes API server (envtest), not just when called directly via
// Handle(). This catches the class of bug where the webhook handler is
// correct but the ValidatingWebhookConfiguration routing/path/selector is
// broken (e.g., the objectSelector matchLabels bug from PR #317).
//
// Build tag: envtest. Requires KUBEBUILDER_ASSETS.
// See .github/workflows/envtest.yml for CI setup.

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// quotaWebhookPath is the path the API server sends admission requests to.
const quotaWebhookPath = "/validate-pod-tenant-quota"

// TestEnvtest_QuotaWebhook_RejectsPodOverLimit starts an envtest API server
// with the PodTenantQuotaValidator registered as a real admission webhook,
// creates workspace pods until the limit is exceeded, and verifies the API
// server rejects the over-limit pod. This proves the end-to-end admission
// chain: ValidatingWebhookConfiguration → webhook server → handler → deny.
func TestEnvtest_QuotaWebhook_RejectsPodOverLimit(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — run via setup-envtest or the envtest workflow")
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	// Define the ValidatingWebhookConfiguration programmatically. envtest
	// will rewrite the clientConfig to point at its local webhook server.
	sideEffectNone := admissionv1.SideEffectClassNone
	failPolicy := admissionv1.Fail
	timeout10 := int32(10)
	port443 := int32(443)
	pathVal := quotaWebhookPath
	namespacedScope := admissionv1.NamespacedScope

	webhookCfg := &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-quota-webhook"},
		Webhooks: []admissionv1.ValidatingWebhook{{
			Name:                    "vpodtenantquota.test.local",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &sideEffectNone,
			FailurePolicy:           &failPolicy,
			TimeoutSeconds:          &timeout10,
			ClientConfig: admissionv1.WebhookClientConfig{
				Service: &admissionv1.ServiceReference{
					Name:      "webhook-service",
					Namespace: "default",
					Path:      &pathVal,
					Port:      &port443,
				},
			},
			Rules: []admissionv1.RuleWithOperations{{
				Rule: admissionv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
					Scope:       &namespacedScope,
				},
				Operations: []admissionv1.OperationType{admissionv1.Create},
			}},
		}},
	}

	// Start envtest with webhook install options. envtest generates TLS
	// certs, picks a local port, and installs the VWC pointing the API
	// server at our local webhook listener.
	testEnv := &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			ValidatingWebhooks: []*admissionv1.ValidatingWebhookConfiguration{webhookCfg},
		},
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err, "envtest startup")
	defer func() { _ = testEnv.Stop() }()

	// Build the controller-runtime client.
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	// Start the webhook server using the certs envtest generated.
	wopts := testEnv.WebhookInstallOptions

	// Register the handler.
	server := webhook.NewServer(webhook.Options{
		Port:    wopts.LocalServingPort,
		Host:    wopts.LocalServingHost,
		CertDir: wopts.LocalServingCertDir,
	})
	server.Register(quotaWebhookPath, &webhook.Admission{
		Handler: &PodTenantQuotaValidator{
			Decoder:                admission.NewDecoder(scheme),
			Client:                 k8sClient,
			MaxWorkspacesPerTenant: 2,
		},
	})

	// Start the webhook server in the background. Use a dedicated context
	// so the server lifecycle is independent from the test-operations ctx.
	serverCtx, serverCancel := context.WithCancel(context.Background())
	go func() {
		_ = server.Start(serverCtx)
	}()
	defer func() {
		serverCancel()
		time.Sleep(500 * time.Millisecond) // allow graceful shutdown
	}()

	// Wait for the webhook server to be reachable.
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp",
			net.JoinHostPort(wopts.LocalServingHost, intToStr(wopts.LocalServingPort)),
			1*time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 500*time.Millisecond, "webhook server must become reachable")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	makePod := func(name, tenant string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Labels: map[string]string{
					"app":                      "llmsafespaces",
					"component":                "workspace",
					"llmsafespaces.dev/tenant": tenant,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "main",
					Image: "busybox:latest",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				}},
			},
		}
	}

	// Pod 1: should succeed (count 1, limit 2)
	require.NoError(t, k8sClient.Create(ctx, makePod("ws-pod-1", "tenant-a")),
		"first pod for tenant-a should be allowed")

	// Pod 2: should succeed (count 2 = limit)
	require.NoError(t, k8sClient.Create(ctx, makePod("ws-pod-2", "tenant-a")),
		"second pod (at limit) should be allowed")

	// Pod 3: should be REJECTED (count 3 > limit 2)
	err = k8sClient.Create(ctx, makePod("ws-pod-3", "tenant-a"))
	require.Error(t, err,
		"third pod for tenant-a must be rejected by quota webhook (count 3 > limit 2)")

	// Verify a different tenant is NOT affected.
	require.NoError(t, k8sClient.Create(ctx, makePod("ws-pod-b1", "tenant-b")),
		"tenant-b pod must not be affected by tenant-a's quota")
}

// TestEnvtest_QuotaWebhook_AllowsNonWorkspacePod verifies that pods without
// the tenant label pass the webhook (objectSelector/handler-level filtering).
func TestEnvtest_QuotaWebhook_AllowsNonWorkspacePod(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — run via setup-envtest or the envtest workflow")
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	sideEffectNone2 := admissionv1.SideEffectClassNone
	failPolicy2 := admissionv1.Fail
	pathVal2 := quotaWebhookPath

	webhookCfg2 := &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-quota-webhook-2"},
		Webhooks: []admissionv1.ValidatingWebhook{{
			Name:                    "vpodtenantquota2.test.local",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &sideEffectNone2,
			FailurePolicy:           &failPolicy2,
			ClientConfig: admissionv1.WebhookClientConfig{
				Service: &admissionv1.ServiceReference{
					Path: &pathVal2,
				},
			},
			Rules: []admissionv1.RuleWithOperations{{
				Rule: admissionv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
				},
				Operations: []admissionv1.OperationType{admissionv1.Create},
			}},
		}},
	}

	testEnv := &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			ValidatingWebhooks: []*admissionv1.ValidatingWebhookConfiguration{webhookCfg2},
		},
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err, "envtest startup")
	defer func() { _ = testEnv.Stop() }()

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	wopts := testEnv.WebhookInstallOptions
	server := webhook.NewServer(webhook.Options{
		Port:    wopts.LocalServingPort,
		Host:    wopts.LocalServingHost,
		CertDir: wopts.LocalServingCertDir,
	})
	server.Register(quotaWebhookPath, &webhook.Admission{
		Handler: &PodTenantQuotaValidator{
			Decoder:                admission.NewDecoder(scheme),
			Client:                 k8sClient,
			MaxWorkspacesPerTenant: 1,
		},
	})

	serverCtx2, serverCancel2 := context.WithCancel(context.Background())
	go func() {
		_ = server.Start(serverCtx2)
	}()
	defer func() {
		serverCancel2()
		time.Sleep(500 * time.Millisecond)
	}()

	require.Eventually(t, func() bool {
		conn, derr := net.DialTimeout("tcp",
			net.JoinHostPort(wopts.LocalServingHost, intToStr(wopts.LocalServingPort)),
			1*time.Second)
		if derr != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 500*time.Millisecond, "webhook server must become reachable")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Pod without tenant label should be allowed even when limit is 1.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "non-workspace-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "busybox:latest"}},
		},
	}

	require.NoError(t, k8sClient.Create(ctx, pod),
		"pod without tenant label must be allowed")

	fetched := &corev1.Pod{}
	require.NoError(t, k8sClient.Get(ctx,
		types.NamespacedName{Name: "non-workspace-pod", Namespace: "default"}, fetched))
}

// intToStr converts int to string without importing strconv (avoids unused
// import if we later remove this helper).
func intToStr(i int) string {
	return string(rune('0'+i/10%10)) + string(rune('0'+i%10))
}

// _ ensures tls import is used (the webhook server uses it internally).
var _ = tls.Config{}

// _ ensures intstr import is available for future webhook config construction.
var _ = intstr.FromInt
