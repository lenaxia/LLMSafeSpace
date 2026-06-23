// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// listingDriver is a stub driver that records ListInstances/Destroy
// calls and returns canned VM lists. Concurrency-safe so the detector's
// per-region sweep can run in parallel with assertions in the test.
type listingDriver struct {
	mu              sync.Mutex
	listByRegion    map[string][]VMInstance
	listErrByRegion map[string]error
	destroyCalls    []struct{ ID, Region string }
	destroyErr      error
	destroyAttempts int
}

func (d *listingDriver) Provision(_ context.Context, _ ProvisionRequest) (*ProvisionResult, error) {
	return nil, errors.New("Provision not used in OrphanDetector tests")
}

func (d *listingDriver) Destroy(_ context.Context, id, region string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.destroyAttempts++
	if d.destroyErr != nil {
		return d.destroyErr
	}
	d.destroyCalls = append(d.destroyCalls, struct{ ID, Region string }{id, region})
	return nil
}

func (d *listingDriver) GetStatus(_ context.Context, _, _ string) (*VMStatus, error) {
	return nil, errors.New("GetStatus not used in OrphanDetector tests")
}

func (d *listingDriver) ListInstances(_ context.Context, region string) ([]VMInstance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err, ok := d.listErrByRegion[region]; ok {
		return nil, err
	}
	return d.listByRegion[region], nil
}

func (d *listingDriver) destroyedIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.destroyCalls))
	for _, c := range d.destroyCalls {
		ids = append(ids, c.ID)
	}
	return ids
}

// TestOrphanDetector_NoCRsNoInstances verifies the detector is a no-op
// when there's nothing to do.
func TestOrphanDetector_NoCRsNoInstances(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	driver := &listingDriver{}

	d := &OrphanDetector{
		Client:  c,
		Drivers: map[string]ProviderDriver{"aws": driver},
	}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Empty(t, driver.destroyedIDs(),
		"empty cluster + empty cloud must not trigger any destroys")
}

// TestOrphanDetector_KeepsInstancesWithActiveCR verifies the detector
// does NOT destroy a VM whose OwnerUID matches an active CR.
func TestOrphanDetector_KeepsInstancesWithActiveCR(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", UID: types.UID("active-uid-1")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).Build()
	driver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"us-west-2": {
				{InstanceID: "i-active", State: VMStateRunning, OwnerUID: "active-uid-1", Provider: "aws"},
			},
		},
	}

	d := &OrphanDetector{Client: c, Drivers: map[string]ProviderDriver{"aws": driver}}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Empty(t, driver.destroyedIDs(),
		"VM whose UID matches an active CR must NOT be destroyed")
}

// TestOrphanDetector_DestroysOrphan_NoActiveCR verifies the headline
// case: a tagged VM exists but no active CR has its UID — destroy it.
func TestOrphanDetector_DestroysOrphan_NoActiveCR(t *testing.T) {
	scheme := testScheme(t)
	// CR exists but has a different UID than the orphan VM. Spec
	// supplies the region so the detector knows to sweep there.
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", UID: types.UID("active-uid-2")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).Build()
	driver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"us-west-2": {
				{InstanceID: "i-orphan", State: VMStateRunning, OwnerUID: "deleted-cr-uid", Provider: "aws"},
				{InstanceID: "i-active", State: VMStateRunning, OwnerUID: "active-uid-2", Provider: "aws"},
			},
		},
	}

	d := &OrphanDetector{Client: c, Drivers: map[string]ProviderDriver{"aws": driver}}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Equal(t, []string{"i-orphan"}, driver.destroyedIDs(),
		"only the orphan (UID with no active CR) must be destroyed")
}

// TestOrphanDetector_LegacyUntaggedVM_NotDestroyed pins the safety
// guarantee: VMs with empty OwnerUID (legacy / pre-fix tagging) must
// NOT be auto-destroyed. They predate the tagging contract and need
// operator audit.
func TestOrphanDetector_LegacyUntaggedVM_NotDestroyed(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", UID: types.UID("any-uid")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).Build()
	driver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"us-west-2": {
				{InstanceID: "i-legacy", State: VMStateRunning, OwnerUID: "", Provider: ""},
			},
		},
	}

	d := &OrphanDetector{Client: c, Drivers: map[string]ProviderDriver{"aws": driver}}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Empty(t, driver.destroyedIDs(),
		"legacy untagged VMs (empty OwnerUID) must NOT be auto-destroyed; "+
			"they predate the tagging contract and require operator audit")
}

// TestOrphanDetector_TerminatedVM_Skipped verifies the detector skips
// already-terminated/stopped VMs — calling Destroy on them is a wasted
// API call and may incorrectly count against quotas.
func TestOrphanDetector_TerminatedVM_Skipped(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", UID: types.UID("u")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).Build()
	driver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"us-west-2": {
				{InstanceID: "i-already-gone", State: VMStateTerminated, OwnerUID: "deleted-cr"},
				{InstanceID: "i-stopped", State: VMStateStopped, OwnerUID: "deleted-cr"},
			},
		},
	}

	d := &OrphanDetector{Client: c, Drivers: map[string]ProviderDriver{"aws": driver}}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Empty(t, driver.destroyedIDs(),
		"already-terminated/stopped VMs must be skipped (no-op API calls)")
}

// TestOrphanDetector_ListInstancesError_ContinuesOtherDrivers verifies
// per-driver isolation: a failing ListInstances on one driver must not
// abort the sweep for other drivers.
func TestOrphanDetector_ListInstancesError_ContinuesOtherDrivers(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", UID: types.UID("u")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2"},
				{Provider: "oci", Region: "us-ashburn-1"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).Build()

	awsDriver := &listingDriver{
		listErrByRegion: map[string]error{"us-west-2": errors.New("AWS API down")},
	}
	ociDriver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"us-ashburn-1": {
				{InstanceID: "ocid-orphan", State: VMStateRunning, OwnerUID: "deleted-cr"},
			},
		},
	}

	d := &OrphanDetector{
		Client:  c,
		Drivers: map[string]ProviderDriver{"aws": awsDriver, "oci": ociDriver},
	}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Empty(t, awsDriver.destroyedIDs(),
		"AWS sweep failed at ListInstances — no destroys attempted")
	assert.Equal(t, []string{"ocid-orphan"}, ociDriver.destroyedIDs(),
		"OCI sweep must continue despite AWS failure (per-driver isolation)")
}

// TestOrphanDetector_DestroyError_ContinuesSweep verifies one failing
// destroy doesn't abort the sweep for other VMs.
func TestOrphanDetector_DestroyError_ContinuesSweep(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet", UID: types.UID("u")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "aws", Region: "us-west-2"},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).Build()
	driver := &listingDriver{
		destroyErr: errors.New("permission denied"),
		listByRegion: map[string][]VMInstance{
			"us-west-2": {
				{InstanceID: "i-orphan-1", State: VMStateRunning, OwnerUID: "deleted-1"},
				{InstanceID: "i-orphan-2", State: VMStateRunning, OwnerUID: "deleted-2"},
			},
		},
	}

	d := &OrphanDetector{Client: c, Drivers: map[string]ProviderDriver{"aws": driver}}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Equal(t, 2, driver.destroyAttempts,
		"all orphans must have Destroy attempted; one failure must not stop the sweep")
}

// TestOrphanDetector_MultipleRegionsPerProvider verifies the detector
// sweeps every region the spec mentions, not just one.
func TestOrphanDetector_MultipleRegionsPerProvider(t *testing.T) {
	scheme := testScheme(t)
	relay1 := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "fleet-a", UID: types.UID("a")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers:   []v1.RelayProviderSpec{{Provider: "aws", Region: "us-west-2"}},
		},
	}
	relay2 := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "fleet-b", UID: types.UID("b")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers:   []v1.RelayProviderSpec{{Provider: "aws", Region: "us-east-1"}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay1, relay2).Build()
	driver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"us-west-2": {{InstanceID: "i-west-orphan", State: VMStateRunning, OwnerUID: "deleted-x"}},
			"us-east-1": {{InstanceID: "i-east-orphan", State: VMStateRunning, OwnerUID: "deleted-y"}},
		},
	}

	d := &OrphanDetector{Client: c, Drivers: map[string]ProviderDriver{"aws": driver}}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	destroyed := driver.destroyedIDs()
	assert.ElementsMatch(t, []string{"i-west-orphan", "i-east-orphan"}, destroyed,
		"detector must sweep every region mentioned by any active CR's spec")
}

// TestOrphanDetector_OperatorRegionOverride pins the d.Regions escape
// hatch: an operator can supply extra regions to sweep beyond what
// any CR currently references.
func TestOrphanDetector_OperatorRegionOverride(t *testing.T) {
	scheme := testScheme(t)
	// No CRs; without override, no regions would be swept.
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	driver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"eu-west-1": {{InstanceID: "i-orphan", State: VMStateRunning, OwnerUID: "deleted-cr"}},
		},
	}

	d := &OrphanDetector{
		Client:  c,
		Drivers: map[string]ProviderDriver{"aws": driver},
		Regions: []string{"eu-west-1"},
	}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Equal(t, []string{"i-orphan"}, driver.destroyedIDs(),
		"operator-supplied region override must be swept even when no CR mentions it")
}

// TestOrphanDetector_NeedLeaderElection pins the leader-election
// guarantee: only the leader runs the sweep so multi-replica controllers
// don't race to destroy the same orphans.
func TestOrphanDetector_NeedLeaderElection(t *testing.T) {
	d := &OrphanDetector{}
	assert.True(t, d.NeedLeaderElection(),
		"OrphanDetector must require leader election — without it, "+
			"replicated controllers race to destroy the same orphans")
}

// TestOrphanDetector_Start_RunsAtStartupAndExitsOnContextCancel verifies
// the runnable contract: sweep runs once at startup, the goroutine ticks,
// and ctx cancellation cleanly terminates Start (no goroutine leak).
func TestOrphanDetector_Start_RunsAtStartupAndExitsOnContextCancel(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "fleet", UID: types.UID("u")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers:   []v1.RelayProviderSpec{{Provider: "aws", Region: "us-west-2"}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relay).Build()
	driver := &listingDriver{
		listByRegion: map[string][]VMInstance{
			"us-west-2": {{InstanceID: "i-orphan", State: VMStateRunning, OwnerUID: "deleted-cr"}},
		},
	}

	d := &OrphanDetector{
		Client:   c,
		Drivers:  map[string]ProviderDriver{"aws": driver},
		Interval: 10 * time.Millisecond, // tight tick so test is fast
	}

	ctx, cancel := context.WithCancel(logf.IntoContext(context.Background(), testr.New(t)))
	done := make(chan error, 1)
	go func() { done <- d.Start(ctx) }()

	// Wait for the startup sweep to run + at least one tick.
	require.Eventually(t, func() bool {
		return len(driver.destroyedIDs()) >= 1
	}, time.Second, 5*time.Millisecond,
		"Start must run sweep at startup")

	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Start did not return within 1s of context cancel — goroutine leak")
	}
}

// TestOrphanDetector_RaceAvoidance_NewCRAddedDuringSweep pins the
// sandwich pattern that protects against destroying VMs whose owner CR
// was created during the sweep itself. Scenario:
//
//  1. Pass 1: detector lists CRs to discover regions.
//     The cluster has 1 CR (UID="alpha"). Sweep records us-west-2 region.
//  2. Between pass 1 and pass 2, a NEW CR (UID="beta") is created.
//     Its reconciler immediately Provisions a VM tagged with "beta".
//  3. Pass 2: detector lists VMs. Sees both "alpha" and "beta" VMs.
//  4. Pass 3: detector lists CRs again. Now the list contains BOTH
//     "alpha" and "beta".
//  5. Detector classifies VMs: both UIDs in active set → no destroy.
//
// Without pass 3 (only pass 1's UIDs), "beta" would be misclassified
// as orphan and the VM destroyed.
//
// We simulate the new-CR-creation by hooking the fake client's List
// behavior: the first List call captures the current state; before the
// second List, we inject the new CR.
func TestOrphanDetector_RaceAvoidance_NewCRAddedDuringSweep(t *testing.T) {
	scheme := testScheme(t)

	relayAlpha := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", UID: types.UID("alpha-uid")},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers:   []v1.RelayProviderSpec{{Provider: "aws", Region: "us-west-2"}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(relayAlpha).Build()

	// Driver returns BOTH alpha's VM and beta's freshly-launched VM
	// (simulating that beta was provisioned between pass 1 and pass 2).
	driver := &raceListingDriver{
		listingDriver: listingDriver{
			listByRegion: map[string][]VMInstance{
				"us-west-2": {
					{InstanceID: "i-alpha", State: VMStateRunning, OwnerUID: "alpha-uid"},
					{InstanceID: "i-beta", State: VMStateRunning, OwnerUID: "beta-uid"},
				},
			},
		},
		// On the FIRST ListInstances call, simulate beta's CR being
		// created (the controller-runtime watch loop would normally do
		// this — we do it inline here).
		onFirstList: func() {
			relayBeta := &v1.InferenceRelay{
				ObjectMeta: metav1.ObjectMeta{Name: "beta", UID: types.UID("beta-uid")},
				Spec: v1.InferenceRelaySpec{
					UpstreamURL: "https://opencode.ai/zen/v1",
					Providers:   []v1.RelayProviderSpec{{Provider: "aws", Region: "us-west-2"}},
				},
			}
			_ = c.Create(context.Background(), relayBeta)
		},
	}

	d := &OrphanDetector{Client: c, Drivers: map[string]ProviderDriver{"aws": driver}}
	d.sweep(logf.IntoContext(context.Background(), testr.New(t)))

	assert.Empty(t, driver.destroyedIDs(),
		"VM whose owner CR was created during the sweep MUST NOT be "+
			"destroyed — the sandwich pattern (CR-list, VM-list, CR-list) "+
			"guarantees the late-binding CR is captured by pass 3")
}

// raceListingDriver wraps listingDriver and runs onFirstList exactly
// once (on the first ListInstances call) so tests can simulate state
// changes in the middle of a sweep.
type raceListingDriver struct {
	listingDriver
	onFirstList func()
	called      bool
}

func (r *raceListingDriver) ListInstances(ctx context.Context, region string) ([]VMInstance, error) {
	r.mu.Lock()
	if !r.called {
		r.called = true
		r.mu.Unlock()
		if r.onFirstList != nil {
			r.onFirstList()
		}
	} else {
		r.mu.Unlock()
	}
	return r.listingDriver.ListInstances(ctx, region)
}
