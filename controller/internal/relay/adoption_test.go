// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
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

// adoptionTestReconciler builds an InferenceRelayReconciler suitable for
// the adoption tests. Pre-populates artifact SHAs (otherwise provisionRelay
// errors out before reaching the driver) and wires the given stub driver
// under provider "aws".
func adoptionTestReconciler(t *testing.T, relay *v1.InferenceRelay, driver *stubDriver) *InferenceRelayReconciler {
	t.Helper()
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()
	return &InferenceRelayReconciler{
		Client:              fakeClient,
		Scheme:              scheme,
		Namespace:           "test-ns",
		Drivers:             map[string]ProviderDriver{"aws": driver},
		ArtifactURLs:        []string{"https://example.com"},
		ArtifactSHA256Arm64: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ArtifactSHA256Amd64: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
	}
}

// adoptionRelayCR builds a minimal InferenceRelay CR with a single AWS
// provider slot. Caller can override UID and Status as needed.
func adoptionRelayCR(uid string) *v1.InferenceRelay {
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{
			Name: "relay-fleet",
			UID:  types.UID(uid),
		},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2", CredentialsRef: corev1.LocalObjectReference{Name: "aws-relay-irwa"}},
			},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)
	return relay
}

// TestAdoption_StatusUpdateConflict_RecoversWithoutDuplicate is the
// regression test for the worklog 0473 production leak. Scenario:
//
//  1. Reconcile A: provisionRelay creates EC2 (driver.Provision succeeds,
//     instance is alive in cloud and tagged with CR.UID).
//  2. r.Status().Update fails with optimistic-concurrency conflict.
//     Reconcile A returns error; instance ID is NOT in Status.Instances.
//  3. Reconcile B (adopt pre-pass): list driver instances, find one
//     tagged with this UID + provider, ADOPT it. Do not call Provision
//     again. Do not orphan the original.
//
// Without the fix, reconcile B calls Provision again and the original
// EC2 leaks forever.
func TestAdoption_StatusUpdateConflict_RecoversWithoutDuplicate(t *testing.T) {
	relay := adoptionRelayCR("test-uid-leak-recovery")
	driver := &stubDriver{
		listInstances: []VMInstance{
			// Tagged with our UID + provider, in running state — must adopt.
			{
				InstanceID: "i-already-launched",
				PublicIP:   "10.0.0.1",
				State:      VMStateRunning,
				OwnerUID:   "test-uid-leak-recovery",
				Provider:   "aws",
			},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "relay-fleet"},
	})
	require.NoError(t, err)

	// CRITICAL: the driver's Provision MUST NOT have been called.
	// Adoption found the tagged VM and reused it.
	assert.Empty(t, driver.provisionCalls,
		"adoption MUST prevent Provision being called when a tagged VM "+
			"already exists for this CR's UID + provider — otherwise the "+
			"first reconcile's EC2 is leaked (worklog 0473)")

	// CRITICAL: the adopted instance ID is in Status.
	updated := &v1.InferenceRelay{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	require.Len(t, updated.Status.Instances, 1)
	assert.Equal(t, "i-already-launched", updated.Status.Instances[0].ID)
	assert.Equal(t, "aws", updated.Status.Instances[0].Provider)
}

// TestAdoption_DuplicateTaggedVMs_DestroysExtras pins the cleanup-of-
// duplicates path: if a prior leak left N tagged VMs for one provider
// slot, adopt one and destroy the rest.
func TestAdoption_DuplicateTaggedVMs_DestroysExtras(t *testing.T) {
	relay := adoptionRelayCR("test-uid-duplicates")
	driver := &stubDriver{
		listInstances: []VMInstance{
			{InstanceID: "i-aaa", State: VMStateRunning, OwnerUID: "test-uid-duplicates", Provider: "aws"},
			{InstanceID: "i-bbb", State: VMStateRunning, OwnerUID: "test-uid-duplicates", Provider: "aws"},
			{InstanceID: "i-ccc", State: VMStateRunning, OwnerUID: "test-uid-duplicates", Provider: "aws"},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)

	assert.Empty(t, driver.provisionCalls, "must not provision when tagged VMs already exist")
	assert.Len(t, driver.destroyCalls, 2,
		"two duplicates must be destroyed (one is adopted, the rest are extras)")
}

// TestAdoption_DifferentUID_NotAdopted ensures cross-CR isolation: a
// VM tagged with a different InferenceRelay's UID must NOT be adopted
// or destroyed by this reconciler.
func TestAdoption_DifferentUID_NotAdopted(t *testing.T) {
	relay := adoptionRelayCR("my-uid")
	driver := &stubDriver{
		listInstances: []VMInstance{
			{InstanceID: "i-other", State: VMStateRunning, OwnerUID: "different-cr-uid", Provider: "aws"},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)

	assert.Empty(t, driver.destroyCalls,
		"must NOT destroy a tagged VM owned by a different CR — that would break tenant isolation")
	assert.Len(t, driver.provisionCalls, 1,
		"must provision a new VM for our CR since the other-CR VM cannot be adopted")
}

// TestAdoption_TerminatedTaggedVM_NotAdopted verifies that terminated
// VMs (State=terminated) are skipped during adoption — a leftover
// terminated record shouldn't prevent fresh provisioning.
func TestAdoption_TerminatedTaggedVM_NotAdopted(t *testing.T) {
	relay := adoptionRelayCR("my-uid")
	driver := &stubDriver{
		listInstances: []VMInstance{
			{InstanceID: "i-old", State: VMStateTerminated, OwnerUID: "my-uid", Provider: "aws"},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)

	assert.Len(t, driver.provisionCalls, 1,
		"must provision a fresh VM — terminated tagged VMs are not adoptable")
	assert.Empty(t, driver.destroyCalls,
		"must NOT destroy already-terminated VMs (no-op AWS calls add cost / quota churn)")
}

// TestProvisionRequest_TagsContainOwnerUID verifies the wire contract:
// when provisionRelay is called, the underlying driver receives a
// ProvisionRequest with OwnerUID + Provider set. Without this, drivers
// can't tag instances and adoption is impossible.
func TestProvisionRequest_TagsContainOwnerUID(t *testing.T) {
	relay := adoptionRelayCR("verify-uid-passed")
	driver := &stubDriver{
		// No tagged VMs — adoption finds nothing, must provision fresh.
		listInstances: []VMInstance{},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)

	require.Len(t, driver.provisionCalls, 1)
	req := driver.provisionCalls[0]
	assert.Equal(t, "verify-uid-passed", req.OwnerUID,
		"provisionRelay must pass relay.UID to the driver so the cloud VM "+
			"is tagged for adoption (worklog 0474)")
	assert.Equal(t, "aws", req.Provider,
		"provisionRelay must pass providerSpec.Provider to the driver")
}

// TestHandleDeletion_TagSweep_DestroysOrphans pins the deletion-side
// cleanup. If Status.Instances doesn't reflect all running VMs (because
// of a prior Status conflict), handleDeletion's tag-based sweep must
// find them and terminate them before removing the finalizer.
func TestHandleDeletion_TagSweep_DestroysOrphans(t *testing.T) {
	now := metav1.Now()
	relay := adoptionRelayCR("deletion-uid")
	relay.DeletionTimestamp = &now
	// Status.Instances stays empty — simulates a prior Status conflict
	// that left the tagged VM unrecorded.

	driver := &stubDriver{
		listInstances: []VMInstance{
			{InstanceID: "i-leaked", State: VMStateRunning, OwnerUID: "deletion-uid", Provider: "aws"},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.handleDeletion(context.Background(), relay)
	require.NoError(t, err)

	require.Len(t, driver.destroyCalls, 1,
		"deletion tag sweep must destroy VMs tagged with the CR's UID even "+
			"when they're missing from Status.Instances (worklog 0474)")
	assert.Equal(t, "i-leaked", driver.destroyCalls[0].ID)

	// After successful deletion, fake client may have removed the CR
	// (since finalizer was removed and DeletionTimestamp was set). What
	// matters is the destroy was called.
}

// TestAdoption_OCIStyle_EmptyPublicIP_AdoptsThenRefreshesNextCycle
// pins the OCI adoption fix from PR #344 review. Scenario:
//
//  1. First reconcile: ListInstances returns a VM matching this CR's UID
//     but with PublicIP="" (the listed VM is in pending state — IP not
//     yet attached). adoptOrphanedInstances synthesizes a Status entry
//     with empty PublicIP.
//  2. Next reconcile: ListInstances returns the SAME instance, now with
//     PublicIP="203.0.113.10". The existing-Status path must refresh
//     the IP, otherwise the relay-router could never reach the VM.
//
// Without the refresh, the empty IP persists forever (the main loop
// copies existing forward without re-querying).
func TestAdoption_OCIStyle_EmptyPublicIP_AdoptsThenRefreshesNextCycle(t *testing.T) {
	relay := adoptionRelayCR("oci-style-uid")
	driver := &stubDriver{
		listInstances: []VMInstance{
			{InstanceID: "i-pending", State: VMStateRunning, OwnerUID: "oci-style-uid", Provider: "aws", PublicIP: ""},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	// First reconcile: adopts with empty IP.
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)
	updated := &v1.InferenceRelay{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	require.Len(t, updated.Status.Instances, 1)
	assert.Equal(t, "i-pending", updated.Status.Instances[0].ID)
	assert.Empty(t, updated.Status.Instances[0].PublicIP,
		"first cycle: IP not yet attached, status should reflect that")

	// Now the cloud assigns the IP — driver returns the same instance with IP.
	driver.listInstances = []VMInstance{
		{InstanceID: "i-pending", State: VMStateRunning, OwnerUID: "oci-style-uid", Provider: "aws", PublicIP: "203.0.113.10"},
	}

	// Second reconcile: must refresh the IP on the existing Status entry.
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	require.Len(t, updated.Status.Instances, 1)
	assert.Equal(t, "203.0.113.10", updated.Status.Instances[0].PublicIP,
		"second cycle: ListInstances now returns IP, refresh must propagate to Status — "+
			"otherwise the router can never reach the adopted relay (PR #344 review)")
	assert.Empty(t, driver.provisionCalls,
		"adoption must reuse the same VM across reconcile cycles, never re-provision")
}

// TestAdoption_MultiProvider_AdoptsCorrectSlot pins multi-provider
// behavior. With AWS and OCI both in spec, an AWS-tagged VM must be
// adopted under the AWS slot and an OCI-tagged VM under the OCI slot.
// Cross-provider mismatches must NOT cause adoption.
func TestAdoption_MultiProvider_AdoptsCorrectSlot(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", UID: types.UID("multi-uid")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2", CredentialsRef: corev1.LocalObjectReference{Name: "aws-relay-irwa"}},
				{Provider: "oci", Region: "us-ashburn-1", CredentialsRef: corev1.LocalObjectReference{Name: "oci-credentials"}},
			},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)

	awsDriver := &stubDriver{
		listInstances: []VMInstance{
			{InstanceID: "i-aws-1", State: VMStateRunning, OwnerUID: "multi-uid", Provider: "aws", PublicIP: "10.0.0.1"},
		},
	}
	ociDriver := &stubDriver{
		listInstances: []VMInstance{
			{InstanceID: "ocid-1", State: VMStateRunning, OwnerUID: "multi-uid", Provider: "oci", PublicIP: "10.0.0.2"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(relay).WithStatusSubresource(&v1.InferenceRelay{}).Build()
	r := &InferenceRelayReconciler{
		Client:              fakeClient,
		Scheme:              scheme,
		Namespace:           "test-ns",
		Drivers:             map[string]ProviderDriver{"aws": awsDriver, "oci": ociDriver},
		ArtifactURLs:        []string{"https://example.com"},
		ArtifactSHA256Arm64: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ArtifactSHA256Amd64: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)

	// Neither driver should have provisioned — both adopted their tagged VM.
	assert.Empty(t, awsDriver.provisionCalls,
		"AWS slot must adopt i-aws-1, not provision")
	assert.Empty(t, ociDriver.provisionCalls,
		"OCI slot must adopt ocid-1, not provision")

	// Both instances must end up in Status under the correct provider slots.
	updated := &v1.InferenceRelay{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	require.Len(t, updated.Status.Instances, 2,
		"both providers must have one Status entry each after adoption")

	byProvider := make(map[string]v1.RelayInstanceStatus)
	for _, inst := range updated.Status.Instances {
		byProvider[inst.Provider] = inst
	}
	assert.Equal(t, "i-aws-1", byProvider["aws"].ID, "AWS slot must contain i-aws-1")
	assert.Equal(t, "ocid-1", byProvider["oci"].ID, "OCI slot must contain ocid-1")
}

// TestHandleDeletion_StatusInstancesAndTaggedOrphans_NoDoubleDestroy
// pins the deletion-sweep correctness: when Status.Instances has entries
// AND the cloud has additional tagged VMs, each VM must be destroyed
// exactly once. The Status loop processes its known IDs; the tag sweep
// processes only IDs not already-processed by the Status loop.
func TestHandleDeletion_StatusInstancesAndTaggedOrphans_NoDoubleDestroy(t *testing.T) {
	now := metav1.Now()
	relay := adoptionRelayCR("mixed-uid")
	relay.DeletionTimestamp = &now
	// Status has one entry; cloud also has a second tagged orphan.
	relay.Status.Instances = []v1.RelayInstanceStatus{
		{ID: "i-known", Provider: "aws", Region: "us-west-2", State: string(v1.RelayStateHealthy), Healthy: true},
	}

	driver := &stubDriver{
		listInstances: []VMInstance{
			// Cloud reports both, including the one already in Status.
			{InstanceID: "i-known", State: VMStateRunning, OwnerUID: "mixed-uid", Provider: "aws"},
			{InstanceID: "i-orphan", State: VMStateRunning, OwnerUID: "mixed-uid", Provider: "aws"},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.handleDeletion(context.Background(), relay)
	require.NoError(t, err)

	// EXACTLY 2 destroys: i-known once (from Status loop), i-orphan once
	// (from tag sweep). No double-destroy.
	require.Len(t, driver.destroyCalls, 2,
		"each VM must be destroyed exactly once; no double-destroy when cloud "+
			"reports a still-running VM that the Status loop already terminated")
	destroyedIDs := []string{driver.destroyCalls[0].ID, driver.destroyCalls[1].ID}
	assert.Contains(t, destroyedIDs, "i-known")
	assert.Contains(t, destroyedIDs, "i-orphan")
}

// TestAdoption_LegacyEmptyProvider_StillAdoptsByUID pins the transition-
// period contract: a tagged VM with OwnerUID set but Provider="" (e.g.
// from a controller version that tagged UID first, before we added the
// Provider tag) must STILL be adoptable. The filter in
// adoptOrphanedInstances explicitly allows empty Provider:
//
//	if vm.Provider != "" && vm.Provider != providerSpec.Provider { continue }
//
// Without this guard, an upgrade from old → new controller would
// orphan all the previously-tagged VMs because they'd be silently
// excluded from adoption. (Reviewer-flagged regression vector for
// PR #344.)
func TestAdoption_LegacyEmptyProvider_StillAdoptsByUID(t *testing.T) {
	relay := adoptionRelayCR("legacy-uid")
	driver := &stubDriver{
		listInstances: []VMInstance{
			// OwnerUID set (good) but Provider tag is empty (legacy).
			{InstanceID: "i-legacy", State: VMStateRunning, OwnerUID: "legacy-uid", Provider: "", PublicIP: "10.0.0.99"},
		},
	}
	r := adoptionTestReconciler(t, relay, driver)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "relay-fleet"}})
	require.NoError(t, err)

	assert.Empty(t, driver.provisionCalls,
		"a legacy-tagged VM (UID set, Provider empty) MUST still be adopted; "+
			"any future change to the filter that strictly requires non-empty Provider "+
			"would orphan all VMs from older controller versions")
	updated := &v1.InferenceRelay{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	require.Len(t, updated.Status.Instances, 1)
	assert.Equal(t, "i-legacy", updated.Status.Instances[0].ID)
}
