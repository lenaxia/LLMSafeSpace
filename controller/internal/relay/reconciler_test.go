// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/lenaxia/llmsafespaces/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// ─── Test helpers ───────────────────────────────────────────────────────────

func testScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

// stubDriver is a test ProviderDriver with configurable behavior.
type stubDriver struct {
	provisionResult *ProvisionResult
	provisionErr    error
	destroyErr      error
	statusResult    *VMStatus
	statusErr       error
	destroyCalls    []struct{ ID, Region string }
	provisionCalls  []ProvisionRequest
	listInstances   []VMInstance
	listErr         error
}

func (d *stubDriver) Provision(_ context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	d.provisionCalls = append(d.provisionCalls, req)
	if d.provisionErr != nil {
		return nil, d.provisionErr
	}
	if d.provisionResult != nil {
		return d.provisionResult, nil
	}
	return &ProvisionResult{InstanceID: "test-instance", PublicIP: "1.2.3.4"}, nil
}

func (d *stubDriver) Destroy(_ context.Context, id, region string) error {
	d.destroyCalls = append(d.destroyCalls, struct{ ID, Region string }{id, region})
	return d.destroyErr
}

func (d *stubDriver) GetStatus(_ context.Context, _, _ string) (*VMStatus, error) {
	if d.statusErr != nil {
		return nil, d.statusErr
	}
	if d.statusResult != nil {
		return d.statusResult, nil
	}
	return &VMStatus{State: VMStateRunning}, nil
}

func (d *stubDriver) ListInstances(_ context.Context, _ string) ([]VMInstance, error) {
	if d.listErr != nil {
		return nil, d.listErr
	}
	return d.listInstances, nil
}

func makeRelayCR() *v1.InferenceRelay {
	return &v1.InferenceRelay{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespaces.dev/v1", Kind: "InferenceRelay"},
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "oci", Region: "us-ashburn-1", CredentialsRef: corev1.LocalObjectReference{Name: "oci-credentials"}},
			},
		},
	}
}

func makeRelayCRWithInstance(provider string) *v1.InferenceRelay {
	relay := makeRelayCR()
	relay.Status = v1.InferenceRelayStatus{
		Instances: []v1.RelayInstanceStatus{
			{ID: "existing-vm", Provider: provider, Region: "us-ashburn-1", State: "healthy", Healthy: true, PublicIP: "203.0.113.10"},
		},
		HealthyReplicas: 1,
	}
	return relay
}

func newTestReconciler(t *testing.T, objs ...runtime.Object) (*InferenceRelayReconciler, *fake.ClientBuilder) {
	t.Helper()
	scheme := testScheme(t)
	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&v1.InferenceRelay{})

	r := &InferenceRelayReconciler{
		Client:    builder.Build(),
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers: map[string]ProviderDriver{
			"aws": &AWSDriver{},
			"oci": &stubDriver{},
		},
		ArtifactURLs: []string{
			"https://github.com/lenaxia/llmsafespace/releases/latest/download",
		},
		ArtifactSHA256Arm64: strings.Repeat("a", 64),
		ArtifactSHA256Amd64: strings.Repeat("b", 64),
	}
	return r, builder
}

func makeOCISecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-credentials", Namespace: "test-ns"},
		Data: map[string][]byte{
			"tenancy":     []byte("ocid1.tenancy.oc1..test"),
			"user":        []byte("ocid1.user.oc1..test"),
			"fingerprint": []byte("aa:bb:cc:dd:ee:ff"),
			"key":         []byte("fake-key"),
			"region":      []byte("us-ashburn-1"),
		},
	}
}

// ─── Reconcile tests ────────────────────────────────────────────────────────

func TestReconcile_NotFound_NoError(t *testing.T) {
	r, _ := newTestReconciler(t)
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "nonexistent"},
	})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcile_Paused_SkipsProvisioning(t *testing.T) {
	relay := makeRelayCR()
	relay.Annotations = map[string]string{annotationPaused: "true"}
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	r, _ := newTestReconciler(t, relay)
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)
}

func TestReconcile_AddsFinalizer(t *testing.T) {
	relay := makeRelayCR()
	r, _ := newTestReconciler(t, relay, makeOCISecret())

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	assert.True(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer))
}

func TestReconcileFleet_ProvisionsMissingRelay(t *testing.T) {
	relay := makeRelayCR()
	common.AddFinalizer(relay, InferenceRelayFinalizer)
	r, _ := newTestReconciler(t, relay, makeOCISecret())

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)
	assert.True(t, result.RequeueAfter > 0)

	// Verify instance was added to status
	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances)
	assert.Equal(t, "oci", updated.Status.Instances[0].Provider)
	assert.Equal(t, "test-instance", updated.Status.Instances[0].ID)
}

func TestReconcileFleet_ExistingHealthy_SetsReadyCondition(t *testing.T) {
	relay := makeRelayCRWithInstance("oci")
	common.AddFinalizer(relay, InferenceRelayFinalizer)
	r, _ := newTestReconciler(t, relay)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	assert.True(t, common.IsConditionTrue(updated.Status.Conditions, string(v1.InferenceRelayConditionReady)))
	assert.False(t, common.IsConditionTrue(updated.Status.Conditions, string(v1.InferenceRelayConditionDegraded)))
}

func TestReconcileFleet_ConfigError_MarksFailed(t *testing.T) {
	relay := makeRelayCR()
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	ociDriver := &stubDriver{provisionErr: ErrConfig}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay, makeOCISecret()).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": ociDriver},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances)
	assert.Equal(t, string(v1.RelayStateProvisioningFailed), updated.Status.Instances[0].State)
	assert.Contains(t, updated.Status.Instances[0].LastProvisionError, "provider configuration error")
	assert.Equal(t, 1, updated.Status.Instances[0].ProvisioningAttempts)
}

// TestReconcileFleet_UnknownProvider_FailsFastWithConfigError locks in the
// contract relied on by US-46.2 (removal of the GCP stub driver): a provider
// listed in spec that has no registered driver must fail fast as a
// configuration error (ProvisioningFailed with a clear message), not silently
// requeue forever.
func TestReconcileFleet_UnknownProvider_FailsFastWithConfigError(t *testing.T) {
	relay := makeRelayCR()
	relay.Spec.Providers = []v1.RelayProviderSpec{
		{Provider: "gcp", Region: "us-west1", CredentialsRef: corev1.LocalObjectReference{Name: "gcp-credentials"}},
	}
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"aws": &AWSDriver{}, "oci": &stubDriver{}},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances)
	assert.Equal(t, string(v1.RelayStateProvisioningFailed), updated.Status.Instances[0].State,
		"provider with no driver must be marked ProvisioningFailed")
	assert.Contains(t, updated.Status.Instances[0].LastProvisionError, "no driver for provider gcp")
}

// TestReconcileFleet_MissingArtifactSHA_FailsProvisioning verifies that when
// the controller's artifact SHA-256 for the target arch is unset, provisioning
// fails with a clear ErrConfig message — not a silent success that would
// produce a VM with a non-downloadable binary.
func TestReconcileFleet_MissingArtifactSHA_FailsProvisioning(t *testing.T) {
	relay := makeRelayCR()
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay, makeOCISecret()).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": &stubDriver{}},
		ArtifactURLs: []string{
			"https://github.com/lenaxia/llmsafespace/releases/latest/download",
		},
		ArtifactSHA256Arm64: "",
		ArtifactSHA256Amd64: "",
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances,
		"missing artifact SHA must still create a status entry so the operator sees the failure")
	assert.Equal(t, string(v1.RelayStateProvisioningFailed), updated.Status.Instances[0].State,
		"missing artifact SHA must mark the instance ProvisioningFailed")
	assert.Contains(t, updated.Status.Instances[0].LastProvisionError, "artifact SHA-256",
		"the error message must mention the artifact SHA so the operator knows what to fix")
}

func TestReconcileFleet_CapacityError_DoesNotMarkFailed(t *testing.T) {
	relay := makeRelayCR()
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	ociDriver := &stubDriver{provisionErr: ErrCapacity}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay, makeOCISecret()).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": ociDriver},
		ArtifactURLs: []string{
			"https://github.com/lenaxia/llmsafespace/releases/latest/download",
		},
		ArtifactSHA256Arm64: strings.Repeat("a", 64),
		ArtifactSHA256Amd64: strings.Repeat("b", 64),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	// Capacity errors should NOT create a failed instance
	for _, inst := range updated.Status.Instances {
		assert.NotEqual(t, string(v1.RelayStateProvisioningFailed), inst.State)
	}
}

func TestReconcileFleet_OrphanedInstance_Destroyed(t *testing.T) {
	relay := makeRelayCRWithInstance("aws") // instance exists but spec only has oci
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	awsDriver := &stubDriver{}
	ociDriver := &stubDriver{provisionResult: &ProvisionResult{InstanceID: "new-oci-vm", PublicIP: "5.6.7.8"}}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay, makeOCISecret()).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"aws": awsDriver, "oci": ociDriver},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	// AWS driver should have been called to destroy the orphaned instance
	assert.NotEmpty(t, awsDriver.destroyCalls)
	assert.Equal(t, "existing-vm", awsDriver.destroyCalls[0].ID)
}

func TestReconcileFleet_PartialHealth_SetsDegraded(t *testing.T) {
	relay := makeRelayCRWithInstance("oci")
	// Add a second provider that's unhealthy
	relay.Spec.Providers = append(relay.Spec.Providers, v1.RelayProviderSpec{
		Provider: "gcp", Region: "us-west1", CredentialsRef: corev1.LocalObjectReference{Name: "gcp-credentials"},
	})
	relay.Status.Instances = append(relay.Status.Instances, v1.RelayInstanceStatus{
		ID: "gcp-vm", Provider: "gcp", State: "unhealthy", Healthy: false, PublicIP: "203.0.113.99",
	})
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	r, _ := newTestReconciler(t, relay)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	assert.True(t, common.IsConditionTrue(updated.Status.Conditions, string(v1.InferenceRelayConditionDegraded)))
}

func TestHandleRotation_TargetFound_Destroyed(t *testing.T) {
	relay := makeRelayCRWithInstance("oci")
	common.AddFinalizer(relay, InferenceRelayFinalizer)
	relay.Annotations = map[string]string{annotationRotate: "existing-vm"}

	scheme := testScheme(t)
	ociDriver := &stubDriver{}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": ociDriver},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	assert.NotEmpty(t, ociDriver.destroyCalls)
	assert.Equal(t, "existing-vm", ociDriver.destroyCalls[0].ID)
}

func TestHandleRotation_TargetNotFound_NoOp(t *testing.T) {
	relay := makeRelayCRWithInstance("oci")
	common.AddFinalizer(relay, InferenceRelayFinalizer)
	relay.Annotations = map[string]string{annotationRotate: "nonexistent-vm"}

	scheme := testScheme(t)
	ociDriver := &stubDriver{}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": ociDriver},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)
	assert.Empty(t, ociDriver.destroyCalls)
}

func TestHandleDeletion_DestroysAllAndRemovesFinalizer(t *testing.T) {
	relay := makeRelayCRWithInstance("oci")
	common.AddFinalizer(relay, InferenceRelayFinalizer)
	now := metav1.Now()
	relay.DeletionTimestamp = &now

	scheme := testScheme(t)
	ociDriver := &stubDriver{}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": ociDriver},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	assert.NotEmpty(t, ociDriver.destroyCalls)
	assert.Equal(t, "existing-vm", ociDriver.destroyCalls[0].ID)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	assert.False(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer))
}

func TestSyncPeerConfigMap_CreatesConfigMap(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	peers := []PeerEntry{
		{ID: "oci-1", Endpoint: "203.0.113.2:8080", Provider: "oci", State: "healthy", Token: "tok123"},
	}

	err := syncPeerConfigMap(context.Background(), fakeClient, "test-ns", peers)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	_ = fakeClient.Get(context.Background(), types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm)
	assert.Contains(t, cm.Data["peers.json"], "oci-1")
	assert.Contains(t, cm.Data["peers.json"], "tok123")
}

func TestSyncPeerConfigMap_NoOpWhenSame(t *testing.T) {
	scheme := testScheme(t)
	peers := []PeerEntry{
		{ID: "oci-1", Endpoint: "203.0.113.2:8080", Provider: "oci", State: "healthy", Token: "tok123"},
	}
	data, _ := json.Marshal(PeerConfig{Relays: peers})

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: routerPeersConfigMap, Namespace: "test-ns"},
			Data:       map[string]string{"peers.json": string(data)},
		}).
		Build()

	// Same data — should not error
	err := syncPeerConfigMap(context.Background(), fakeClient, "test-ns", peers)
	require.NoError(t, err)
}

func TestProvisioningAttempts_AccumulateAcrossReconciles(t *testing.T) {
	relay := makeRelayCR()
	common.AddFinalizer(relay, InferenceRelayFinalizer)
	// Pre-existing failed instance with 2 attempts
	relay.Status.Instances = []v1.RelayInstanceStatus{
		{ID: "oci-provisioning", Provider: "oci", State: string(v1.RelayStateProvisioningFailed), ProvisioningAttempts: 2},
	}

	scheme := testScheme(t)
	ociDriver := &stubDriver{provisionErr: ErrConfig}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay, makeOCISecret()).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"oci": ociDriver},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances)
	assert.Equal(t, 3, updated.Status.Instances[0].ProvisioningAttempts,
		"should accumulate from 2 to 3")
}

// TestRelayToken_ReadWriteRoundTrip verifies that per-VM tokens persist across
// controller restarts: writeRelayTokens then readRelayTokens returns the same
// values. Critical so a controller pod restart doesn't desync from a running
// VM (which was cloud-init'd with the original token).
func TestRelayToken_ReadWriteRoundTrip(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
	}

	tokens := map[string]string{
		"aws": "aws-token-abc",
		"oci": "oci-token-def",
	}
	require.NoError(t, r.writeRelayTokens(context.Background(), tokens))

	got := r.readRelayTokens(context.Background())
	assert.Equal(t, tokens, got,
		"tokens written then read back must match (controller-restart persistence)")

	// Verify the underlying Secret exists with the expected keys
	secret := &corev1.Secret{}
	err := fakeClient.Get(context.Background(), types.NamespacedName{Name: relayTokensSecret, Namespace: "test-ns"}, secret)
	require.NoError(t, err)
	assert.Equal(t, "aws-token-abc", string(secret.Data["aws"]))
	assert.Equal(t, "oci-token-def", string(secret.Data["oci"]))
}

// TestGenerateRelayToken_RandomAndHex verifies the token generator produces
// 64-char hex strings (32 bytes entropy) and that two calls differ.
func TestGenerateRelayToken_RandomAndHex(t *testing.T) {
	t1, err := generateRelayToken()
	require.NoError(t, err)
	t2, err := generateRelayToken()
	require.NoError(t, err)

	assert.Len(t, t1, 64, "token must be 64 hex chars (32 bytes)")
	assert.Len(t, t2, 64)
	assert.NotEqual(t, t1, t2, "two generated tokens must differ (randomness)")
	for _, c := range t1 {
		assert.Contains(t, "0123456789abcdef", string(c), "token must be hex")
	}
}

// TestReconcileFleet_HealthReportApplied verifies that health data from
// the router /metrics endpoint is applied to instance status.
func TestReconcileFleet_HealthReportApplied(t *testing.T) {
	relay := makeRelayCRWithInstance("oci")
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	// Create a health checker pointing at a mock server
	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`relay_router_relay_healthy{relay="existing-vm"} 1
relay_router_active_streams{relay="existing-vm"} 5
relay_router_requests_total{relay="existing-vm",status="200"} 9992
relay_router_requests_total{relay="existing-vm",status="429"} 7
relay_router_relay_egress_bytes{relay="existing-vm"} 123456789
relay_router_fallback_active 0
`))
	}))
	defer metricsServer.Close()

	r := &InferenceRelayReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Namespace:     "test-ns",
		HealthChecker: NewHealthChecker(metricsServer.URL),
		Drivers:       map[string]ProviderDriver{"oci": &stubDriver{}},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances)
	inst := updated.Status.Instances[0]
	assert.True(t, inst.Healthy, "health report should mark instance healthy")
	assert.Equal(t, 7, inst.Requests429, "429 count should come from health report")
	assert.Equal(t, 9999, inst.TotalRequests, "request count should come from health report")
	assert.Equal(t, int64(123456789), inst.EgressBytes, "egress bytes should come from health report")
}

// TestReconcileFleet_StateTransitionsToHealthy verifies that an instance
// in "provisioning" state transitions to "healthy" once the router reports
// it healthy. Without this transition, the CR status stays misleading even
// after the relay is fully operational. See worklog 0467.
func TestReconcileFleet_StateTransitionsToHealthy(t *testing.T) {
	relay := makeRelayCR()
	relay.Status = v1.InferenceRelayStatus{
		Instances: []v1.RelayInstanceStatus{
			{ID: "existing-vm", Provider: "oci", Region: "us-ashburn-1",
				State: string(v1.RelayStateProvisioning), Healthy: false, PublicIP: "203.0.113.10"},
		},
	}
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`relay_router_relay_healthy{relay="existing-vm"} 1
relay_router_fallback_active 0
`))
	}))
	defer metricsServer.Close()

	r := &InferenceRelayReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Namespace:     "test-ns",
		HealthChecker: NewHealthChecker(metricsServer.URL),
		Drivers:       map[string]ProviderDriver{"oci": &stubDriver{}},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances)
	inst := updated.Status.Instances[0]
	assert.True(t, inst.Healthy)
	assert.Equal(t, string(v1.RelayStateHealthy), inst.State,
		"once router reports healthy, controller must transition state out of provisioning")
}

// TestReconcileFleet_StateStaysProvisioningWhenUnhealthy verifies the
// transition is one-directional during initial boot: a provisioning instance
// that is not yet healthy stays provisioning rather than flipping to unhealthy
// (which would alert prematurely while the relay-proxy is still booting).
func TestReconcileFleet_StateStaysProvisioningWhenUnhealthy(t *testing.T) {
	relay := makeRelayCR()
	relay.Status = v1.InferenceRelayStatus{
		Instances: []v1.RelayInstanceStatus{
			{ID: "existing-vm", Provider: "oci", Region: "us-ashburn-1",
				State: string(v1.RelayStateProvisioning), Healthy: false, PublicIP: "203.0.113.10"},
		},
	}
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`relay_router_relay_healthy{relay="existing-vm"} 0
relay_router_fallback_active 0
`))
	}))
	defer metricsServer.Close()

	r := &InferenceRelayReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Namespace:     "test-ns",
		HealthChecker: NewHealthChecker(metricsServer.URL),
		Drivers:       map[string]ProviderDriver{"oci": &stubDriver{}},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	require.NotEmpty(t, updated.Status.Instances)
	inst := updated.Status.Instances[0]
	assert.False(t, inst.Healthy)
	assert.Equal(t, string(v1.RelayStateProvisioning), inst.State,
		"during boot, an unhealthy provisioning instance must stay provisioning, not flip to unhealthy")
}

// TestReconcileFleet_StateTransitionsHealthyToUnhealthy verifies a relay
// that was previously healthy flips to unhealthy when the router reports
// it unhealthy (e.g. after sustained probe failures).
func TestReconcileFleet_StateTransitionsHealthyToUnhealthy(t *testing.T) {
	relay := makeRelayCR()
	relay.Status = v1.InferenceRelayStatus{
		Instances: []v1.RelayInstanceStatus{
			{ID: "existing-vm", Provider: "oci", Region: "us-ashburn-1",
				State: string(v1.RelayStateHealthy), Healthy: true, PublicIP: "203.0.113.10"},
		},
	}
	common.AddFinalizer(relay, InferenceRelayFinalizer)

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()

	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`relay_router_relay_healthy{relay="existing-vm"} 0
relay_router_fallback_active 0
`))
	}))
	defer metricsServer.Close()

	r := &InferenceRelayReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Namespace:     "test-ns",
		HealthChecker: NewHealthChecker(metricsServer.URL),
		Drivers:       map[string]ProviderDriver{"oci": &stubDriver{}},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	updated := &v1.InferenceRelay{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated)
	inst := updated.Status.Instances[0]
	assert.False(t, inst.Healthy)
	assert.Equal(t, string(v1.RelayStateUnhealthy), inst.State,
		"a previously-healthy instance must flip to unhealthy when router reports unhealthy")
}
