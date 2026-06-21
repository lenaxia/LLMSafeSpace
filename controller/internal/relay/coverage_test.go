// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// ─── handleDeletion coverage ────────────────────────────────────────────────

func TestHandleDeletion_NoFinalizer(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &InferenceRelayReconciler{Client: fakeClient, Scheme: scheme, Namespace: "test-ns"}

	relay := &v1.InferenceRelay{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	result, err := r.handleDeletion(context.Background(), relay)
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestHandleDeletion_DestroysAllInstances(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Status: v1.InferenceRelayStatus{
			Instances: []v1.RelayInstanceStatus{
				{ID: "oci-1", Provider: "oci", Region: "us-ashburn-1", State: "healthy"},
				{ID: "aws-1", Provider: "aws", Region: "us-east-1", State: "terminated"},
			},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)

	ociDriver := &stubDriver{}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).WithStatusSubresource(&v1.InferenceRelay{}).Build()
	r := &InferenceRelayReconciler{Client: fakeClient, Scheme: scheme, Namespace: "test-ns",
		Drivers: map[string]ProviderDriver{"oci": ociDriver}}

	_, err := r.handleDeletion(context.Background(), relay)
	require.NoError(t, err)
	require.Len(t, ociDriver.destroyCalls, 1, "only the non-terminated instance should be destroyed")
	assert.Equal(t, "oci-1", ociDriver.destroyCalls[0].ID)

	updated := &v1.InferenceRelay{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	assert.False(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer))
}

func TestHandleDeletion_DestroyError_Retries(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Status: v1.InferenceRelayStatus{
			Instances: []v1.RelayInstanceStatus{
				{ID: "oci-1", Provider: "oci", Region: "us-ashburn-1", State: "healthy"},
			},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)
	ociDriver := &stubDriver{destroyErr: errors.New("provider unavailable")}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).WithStatusSubresource(&v1.InferenceRelay{}).Build()
	r := &InferenceRelayReconciler{Client: fakeClient, Scheme: scheme, Namespace: "test-ns",
		Drivers: map[string]ProviderDriver{"oci": ociDriver}}

	result, err := r.handleDeletion(context.Background(), relay)
	require.Error(t, err)
	assert.True(t, result.RequeueAfter > 0)

	updated := &v1.InferenceRelay{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	assert.True(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer),
		"finalizer must stay so the next reconcile retries")
}

func TestHandleDeletion_NoDriver_LogsAndRetries(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Status: v1.InferenceRelayStatus{
			Instances: []v1.RelayInstanceStatus{
				{ID: "gcp-1", Provider: "gcp", Region: "us-west1", State: "healthy"},
			},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).WithStatusSubresource(&v1.InferenceRelay{}).Build()
	r := &InferenceRelayReconciler{Client: fakeClient, Scheme: scheme, Namespace: "test-ns",
		Drivers: map[string]ProviderDriver{}}

	_, err := r.handleDeletion(context.Background(), relay)
	require.Error(t, err)

	updated := &v1.InferenceRelay{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	assert.True(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer))
}

// ─── syncPeerConfigMap error-path coverage ──────────────────────────────────

func TestSyncPeerConfigMap_UpdatePath(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: routerPeersConfigMap, Namespace: "test-ns"},
			Data:       map[string]string{"peers.json": `{"relays":[{"id":"old"}]}`},
		},
	).Build()

	peers := []PeerEntry{{ID: "new", Endpoint: "1.2.3.4:8080", Provider: "aws", State: "healthy", Token: "t"}}
	require.NoError(t, syncPeerConfigMap(context.Background(), fakeClient, "test-ns", peers))

	cm := &corev1.ConfigMap{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm))
	assert.Contains(t, cm.Data["peers.json"], `"id":"new"`)
	assert.NotContains(t, cm.Data["peers.json"], "old")
}

// TestSyncPeerConfigMap_NoOwnerRef verifies that the resulting ConfigMap
// does NOT have an ownerReference. This is intentional — the CM lifecycle
// is managed by the controller's reconcile loop alone, not by Kubernetes
// garbage collection. Owner-ref would race with kubelet's volume-mount
// sync on CR deletion and orphan relays in the router's in-memory fleet
// (see worklog 0468).
func TestSyncPeerConfigMap_NoOwnerRef(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	peers := []PeerEntry{{ID: "x", Endpoint: "1.2.3.4:8080", Provider: "oci", State: "healthy", Token: "t"}}
	require.NoError(t, syncPeerConfigMap(context.Background(), fakeClient, "test-ns", peers))

	cm := &corev1.ConfigMap{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm))
	assert.Empty(t, cm.OwnerReferences,
		"peer ConfigMap must have NO ownerReference — controller manages "+
			"its lifecycle directly to avoid the GC-vs-kubelet-sync race "+
			"that orphans relays on CR deletion (worklog 0468)")
}

func TestSyncPeerConfigMap_NilDataMap(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: routerPeersConfigMap, Namespace: "test-ns"},
			Data:       nil,
		},
	).Build()

	peers := []PeerEntry{{ID: "x", Endpoint: "1.2.3.4:8080", Provider: "oci", State: "healthy", Token: "t"}}
	require.NoError(t, syncPeerConfigMap(context.Background(), fakeClient, "test-ns", peers))

	cm := &corev1.ConfigMap{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm))
	assert.NotNil(t, cm.Data)
	assert.Contains(t, cm.Data["peers.json"], `"id":"x"`)
}

// ─── health.go Scrape coverage ──────────────────────────────────────────────

func TestHealthChecker_Scrape_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("relay_router_relay_healthy{relay=\"oci-1\"} 1\nrelay_router_fallback_active 0\n"))
	}))
	t.Cleanup(srv.Close)

	report, err := NewHealthChecker(srv.URL).Scrape(context.Background())
	require.NoError(t, err)
	assert.False(t, report.FallbackActive)
	require.Contains(t, report.Relays, "oci-1")
	assert.True(t, report.Relays["oci-1"].Healthy)
}

func TestHealthChecker_Scrape_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	_, err := NewHealthChecker(srv.URL).Scrape(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestHealthChecker_Scrape_Unreachable(t *testing.T) {
	_, err := NewHealthChecker("http://127.0.0.1:1").Scrape(context.Background())
	require.Error(t, err)
}

func TestExtractMetricValue_NoValue(t *testing.T) {
	assert.Equal(t, int64(0), extractMetricValue("just_a_name"))
}

func TestExtractMetricValue_NonNumeric(t *testing.T) {
	assert.Equal(t, int64(0), extractMetricValue("some_metric abc"))
}

// ─── Reconcile paused + deletion branches ────────────────────────────────────

func TestReconcile_DeletionTriggered(t *testing.T) {
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", DeletionTimestamp: &metav1.Time{}},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)
	r, _ := newTestReconciler(t, relay)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)
}

func TestReconcile_RotationAnnotation(t *testing.T) {
	relay := makeRelayCR()
	relay.Status = v1.InferenceRelayStatus{
		Instances: []v1.RelayInstanceStatus{
			{ID: "oci-vm", Provider: "oci", Region: "us-ashburn-1", State: "healthy", Healthy: true, PublicIP: "1.2.3.4"},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)
	relay.Annotations = map[string]string{annotationRotate: "oci-vm"}
	r, _ := newTestReconciler(t, relay)
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	assert.Equal(t, "", updated.Annotations[annotationRotate])
}

// ─── writeRelayTokens update path ───────────────────────────────────────────

func TestWriteRelayTokens_UpdatesExisting(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: relayTokensSecret, Namespace: "test-ns"},
			Data:       map[string][]byte{"oci": []byte("old-tok")},
		},
	).Build()
	r := &InferenceRelayReconciler{Client: fakeClient, Scheme: scheme, Namespace: "test-ns"}

	require.NoError(t, r.writeRelayTokens(context.Background(), map[string]string{"oci": "new-tok", "aws": "aws-tok"}))

	secret := &corev1.Secret{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: relayTokensSecret, Namespace: "test-ns"}, secret))
	assert.Equal(t, "new-tok", string(secret.Data["oci"]))
	assert.Equal(t, "aws-tok", string(secret.Data["aws"]))
}

// ─── RenderCloudInit BinaryName default + happy path ───────────────────────

func TestRenderCloudInit_DefaultsBinaryName(t *testing.T) {
	cfg := validCloudInitConfig()
	cfg.BinaryName = ""
	b64, err := RenderCloudInit(cfg)
	require.NoError(t, err)
	raw, _ := base64.StdEncoding.DecodeString(b64)
	assert.Contains(t, string(raw), "relay-proxy-arm64")
}

func TestRenderCloudInit_AllValidationPass(t *testing.T) {
	b64, err := RenderCloudInit(CloudInitConfig{
		UpstreamURL:    "https://example.com",
		Token:          "t",
		ArtifactURLs:   []string{"https://mirror/repo"},
		ArtifactSHA256: "abc",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, b64)
}
