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
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const quotaWebhookPath = "/validate-pod-tenant-quota"

// startQuotaWebhookServer starts a raw HTTPS server serving the
// PodTenantQuotaValidator at the given host:port using the envtest-generated
// TLS certs. Returns a shutdown function.
//
// We use a raw http.Server instead of the controller-runtime webhook.Server
// to avoid manager-dependent initialization quirks that make standalone
// usage fragile in tests. The admission handler is just an HTTP handler.
func startQuotaWebhookServer(t *testing.T, wopts envtest.WebhookInstallOptions, scheme *runtime.Scheme, k8sClient client.Client, maxWorkspaces int) func() {
	t.Helper()
	certFile := filepath.Join(wopts.LocalServingCertDir, "tls.crt")
	keyFile := filepath.Join(wopts.LocalServingCertDir, "tls.key")

	decoder := admission.NewDecoder(scheme)
	handler := &PodTenantQuotaValidator{
		Decoder:                decoder,
		Client:                 k8sClient,
		MaxWorkspacesPerTenant: maxWorkspaces,
	}

	mux := http.NewServeMux()
	mux.Handle(quotaWebhookPath, &admissionHandler{decoder: decoder, handler: handler})

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", wopts.LocalServingHost, wopts.LocalServingPort),
		Handler: mux,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	ln, err := net.Listen("tcp", server.Addr)
	require.NoError(t, err, "failed to listen on %s", server.Addr)

	go func() {
		_ = server.ServeTLS(ln, certFile, keyFile)
	}()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
}

// admissionHandler wraps the admission.Decoder + admission.Handler into a
// standard net/http.Handler. This is what controller-runtime's webhook
// Admission type does internally, reproduced here to avoid depending on
// the internal webhook server machinery.
type admissionHandler struct {
	decoder admission.Decoder
	handler admission.Handler
}

func (h *admissionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid content-type", http.StatusUnsupportedMediaType)
		return
	}

	var review admissionv1.AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		http.Error(w, fmt.Sprintf("failed to decode: %v", err), http.StatusBadRequest)
		return
	}

	req := admission.Request{AdmissionRequest: *review.Request}
	resp := h.handler.Handle(r.Context(), req)

	review.Response = &resp.AdmissionResponse
	review.Response.UID = req.UID

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(review)
}

// TestEnvtest_QuotaWebhook_RejectsPodOverLimit starts an envtest API server
// with the PodTenantQuotaValidator registered as a real admission webhook,
// creates workspace pods until the limit is exceeded, and verifies the API
// server rejects the over-limit pod.
func TestEnvtest_QuotaWebhook_RejectsPodOverLimit(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — run via setup-envtest or the envtest workflow")
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	sideEffectNone := admissionregv1.SideEffectClassNone
	failPolicy := admissionregv1.Fail
	timeout10 := int32(10)
	port443 := int32(443)
	pathVal := quotaWebhookPath
	namespacedScope := admissionregv1.NamespacedScope

	webhookCfg := &admissionregv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-quota-webhook"},
		Webhooks: []admissionregv1.ValidatingWebhook{{
			Name:                    "vpodtenantquota.test.local",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &sideEffectNone,
			FailurePolicy:           &failPolicy,
			TimeoutSeconds:          &timeout10,
			ClientConfig: admissionregv1.WebhookClientConfig{
				Service: &admissionregv1.ServiceReference{
					Path: &pathVal,
					Port: &port443,
				},
			},
			Rules: []admissionregv1.RuleWithOperations{{
				Rule: admissionregv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
					Scope:       &namespacedScope,
				},
				Operations: []admissionregv1.OperationType{admissionregv1.Create},
			}},
		}},
	}

	testEnv := &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			ValidatingWebhooks: []*admissionregv1.ValidatingWebhookConfiguration{webhookCfg},
		},
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err, "envtest startup")
	defer func() { _ = testEnv.Stop() }()

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	shutdown := startQuotaWebhookServer(t, testEnv.WebhookInstallOptions, scheme, k8sClient, 2)
	defer shutdown()

	// Wait for webhook server to be reachable.
	wopts := testEnv.WebhookInstallOptions
	require.Eventually(t, func() bool {
		conn, derr := net.DialTimeout("tcp",
			net.JoinHostPort(wopts.LocalServingHost, fmt.Sprintf("%d", wopts.LocalServingPort)),
			1*time.Second)
		if derr != nil {
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

	require.NoError(t, k8sClient.Create(ctx, makePod("ws-pod-1", "tenant-a")),
		"first pod for tenant-a should be allowed")
	require.NoError(t, k8sClient.Create(ctx, makePod("ws-pod-2", "tenant-a")),
		"second pod (at limit) should be allowed")

	err = k8sClient.Create(ctx, makePod("ws-pod-3", "tenant-a"))
	require.Error(t, err,
		"third pod for tenant-a must be rejected by quota webhook (count 3 > limit 2)")

	require.NoError(t, k8sClient.Create(ctx, makePod("ws-pod-b1", "tenant-b")),
		"tenant-b pod must not be affected by tenant-a's quota")
}

// TestEnvtest_QuotaWebhook_AllowsNonWorkspacePod verifies that pods without
// the tenant label pass through the webhook.
func TestEnvtest_QuotaWebhook_AllowsNonWorkspacePod(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — run via setup-envtest or the envtest workflow")
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	sideEffectNone := admissionregv1.SideEffectClassNone
	failPolicy := admissionregv1.Fail
	pathVal := quotaWebhookPath

	webhookCfg := &admissionregv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-quota-webhook-2"},
		Webhooks: []admissionregv1.ValidatingWebhook{{
			Name:                    "vpodtenantquota2.test.local",
			AdmissionReviewVersions: []string{"v1"},
			SideEffects:             &sideEffectNone,
			FailurePolicy:           &failPolicy,
			ClientConfig: admissionregv1.WebhookClientConfig{
				Service: &admissionregv1.ServiceReference{Path: &pathVal},
			},
			Rules: []admissionregv1.RuleWithOperations{{
				Rule: admissionregv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
				},
				Operations: []admissionregv1.OperationType{admissionregv1.Create},
			}},
		}},
	}

	testEnv := &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			ValidatingWebhooks: []*admissionregv1.ValidatingWebhookConfiguration{webhookCfg},
		},
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err, "envtest startup")
	defer func() { _ = testEnv.Stop() }()

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	shutdown := startQuotaWebhookServer(t, testEnv.WebhookInstallOptions, scheme, k8sClient, 1)
	defer shutdown()

	wopts := testEnv.WebhookInstallOptions
	require.Eventually(t, func() bool {
		conn, derr := net.DialTimeout("tcp",
			net.JoinHostPort(wopts.LocalServingHost, fmt.Sprintf("%d", wopts.LocalServingPort)),
			1*time.Second)
		if derr != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 500*time.Millisecond, "webhook server must become reachable")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "non-workspace-pod", Namespace: "default"},
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
