// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"encoding/json"
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

	"github.com/lenaxia/llmsafespace/controller/internal/common"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
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
}

func (d *stubDriver) Provision(_ context.Context, _ ProvisionRequest) (*ProvisionResult, error) {
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
	return nil, nil
}

func makeRelayCR() *v1.InferenceRelay {
	return &v1.InferenceRelay{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "InferenceRelay"},
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "oci", Region: "us-ashburn-1", CredentialsRef: corev1.LocalObjectReference{Name: "oci-credentials"}},
			},
			WireGuard: v1.WireGuardConfig{
				RouterEndpoint: "relay-gw.example.com:51820",
			},
		},
	}
}

func makeRelayCRWithInstance(provider string) *v1.InferenceRelay {
	relay := makeRelayCR()
	relay.Status = v1.InferenceRelayStatus{
		Instances: []v1.RelayInstanceStatus{
			{ID: "existing-vm", Provider: provider, Region: "us-ashburn-1", State: "healthy", Healthy: true, WgIP: wgIPForProvider(provider)},
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
			"gcp": &GCPDriver{},
		},
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
	r, _ := newTestReconciler(t, relay, makeOCISecret(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: routerWGSecret, Namespace: "test-ns"},
	})

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
		ID: "gcp-vm", Provider: "gcp", State: "unhealthy", Healthy: false, WgIP: wgGCPRelay,
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
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy", PublicKey: "pub123"},
	}

	err := syncPeerConfigMap(context.Background(), fakeClient, "test-ns", nil, peers)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	_ = fakeClient.Get(context.Background(), types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm)
	assert.Contains(t, cm.Data["peers.json"], "oci-1")
	assert.Contains(t, cm.Data["peers.json"], "pub123")
}

func TestSyncPeerConfigMap_NoOpWhenSame(t *testing.T) {
	scheme := testScheme(t)
	peers := []PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
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
	err := syncPeerConfigMap(context.Background(), fakeClient, "test-ns", nil, peers)
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

func TestEnsureRouterWGKey_GeneratesIfMissing(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
	}

	pubKey := r.ensureRouterWGKey(context.Background())
	assert.NotEmpty(t, pubKey)

	// Verify the secret was created
	secret := &corev1.Secret{}
	err := fakeClient.Get(context.Background(), types.NamespacedName{Name: routerWGSecret, Namespace: "test-ns"}, secret)
	require.NoError(t, err)
	assert.Equal(t, pubKey, string(secret.Data["publicKey"]))
}
