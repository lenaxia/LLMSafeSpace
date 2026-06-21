// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// healthStateLocked tests pin the state-machine transitions discovered in
// worklog 0467: a relay with peer.State="provisioning" must transition to
// healthy once active probes confirm reachability, otherwise the controller
// never observes Healthy=true and the CR never leaves the "provisioning"
// state. The fix adds a consecutiveSuccesses counter and healthyThr threshold.
//
// Drain precedence is preserved: peer.State="draining" wins over any number
// of successful probes (controller-driven graceful shutdown must work).

// TestHealthStateLocked_ProvisioningToHealthyOnSuccesses pins the primary
// fix path: a freshly-provisioned relay (peer.State="provisioning") that
// passes healthyThr consecutive probes is reported as healthy.
func TestHealthStateLocked_ProvisioningToHealthyOnSuccesses(t *testing.T) {
	f := newRelayFleetWithThresholds(3, 2, 5*time.Minute) // unhealthyThr=3, healthyThr=2
	f.UpdatePeers([]PeerEntry{
		{ID: "i-prov", Endpoint: "1.2.3.4:8080", Provider: "aws", State: "provisioning"},
	})

	// 1 success — not yet healthy (need 2)
	f.RecordHealthCheck("i-prov", true)
	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Healthy,
		"after 1 success (threshold=2), router must not yet report healthy")

	// 2 successes — threshold reached, must report healthy
	f.RecordHealthCheck("i-prov", true)
	statuses = f.HealthyRelays()
	assert.True(t, statuses[0].Healthy,
		"after 2 successes (threshold=2), router must report healthy regardless of peer.State")
}

// TestHealthStateLocked_DrainPrecedenceOverSuccesses ensures the controller
// can still drain a relay even after it has accumulated successful probes.
// The reviewer (PR #330) flagged this as a robustness concern.
func TestHealthStateLocked_DrainPrecedenceOverSuccesses(t *testing.T) {
	f := newRelayFleetWithThresholds(3, 2, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "i-aws", Endpoint: "1.2.3.4:8080", Provider: "aws", State: "healthy"},
	})

	// Accumulate many successful probes
	for i := 0; i < 10; i++ {
		f.RecordHealthCheck("i-aws", true)
	}
	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Healthy)

	// Controller writes drain to ConfigMap
	f.UpdatePeers([]PeerEntry{
		{ID: "i-aws", Endpoint: "1.2.3.4:8080", Provider: "aws", State: "draining"},
	})

	// Even with prior successes, drain must take precedence
	statuses = f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Healthy,
		"controller-driven drain must take precedence over accumulated success counter")

	// Drain relay must also be excluded from eligible selection
	_, _, _, ok := f.SelectRelay()
	assert.False(t, ok, "draining relay must not be eligible for selection")
}

// TestHealthStateLocked_FailureThresholdStillUnhealthy verifies the existing
// unhealthy path is unchanged: enough consecutive failures override anything.
func TestHealthStateLocked_FailureThresholdStillUnhealthy(t *testing.T) {
	f := newRelayFleetWithThresholds(3, 2, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "i-aws", Endpoint: "1.2.3.4:8080", Provider: "aws", State: "healthy"},
	})

	// 2 successes — healthy
	f.RecordHealthCheck("i-aws", true)
	f.RecordHealthCheck("i-aws", true)
	assert.True(t, f.HealthyRelays()[0].Healthy)

	// 3 consecutive failures — must flip to unhealthy
	f.RecordHealthCheck("i-aws", false)
	f.RecordHealthCheck("i-aws", false)
	f.RecordHealthCheck("i-aws", false)
	assert.False(t, f.HealthyRelays()[0].Healthy,
		"after unhealthyThr (3) failures, router must report unhealthy")
}

// TestHealthStateLocked_FailureResetsSuccessCounter verifies the success
// counter is reset on any failure — a single failure must drop the relay
// out of the "healthy by success threshold" path so it has to re-prove
// itself.
func TestHealthStateLocked_FailureResetsSuccessCounter(t *testing.T) {
	f := newRelayFleetWithThresholds(3, 2, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "i-aws", Endpoint: "1.2.3.4:8080", Provider: "aws", State: "provisioning"},
	})

	// 2 successes — healthy
	f.RecordHealthCheck("i-aws", true)
	f.RecordHealthCheck("i-aws", true)
	assert.True(t, f.HealthyRelays()[0].Healthy)

	// 1 failure — drops back to peer.State (provisioning), reports unhealthy
	f.RecordHealthCheck("i-aws", false)
	assert.False(t, f.HealthyRelays()[0].Healthy,
		"a single failure must reset the success counter and revert to peer.State")

	// 1 success after the failure — not enough yet (need 2 again)
	f.RecordHealthCheck("i-aws", true)
	assert.False(t, f.HealthyRelays()[0].Healthy,
		"after a failure interrupted the success run, must accumulate threshold successes again")

	// 2nd success — threshold met again
	f.RecordHealthCheck("i-aws", true)
	assert.True(t, f.HealthyRelays()[0].Healthy)
}

// TestHealthStateLocked_SuccessResetsFailureCounter is the symmetric guarantee:
// a single success after some failures must reset consecutiveFailures so the
// relay can re-enter the healthy path.
func TestHealthStateLocked_SuccessResetsFailureCounter(t *testing.T) {
	f := newRelayFleetWithThresholds(3, 2, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "i-aws", Endpoint: "1.2.3.4:8080", Provider: "aws", State: "provisioning"},
	})

	// 2 failures — under threshold but consecutive failure count is 2
	f.RecordHealthCheck("i-aws", false)
	f.RecordHealthCheck("i-aws", false)

	// Single success — must reset failure count
	f.RecordHealthCheck("i-aws", true)

	// Now 2 more failures should not be enough to flip unhealthy
	// (counter was reset; need 3 from this point)
	f.RecordHealthCheck("i-aws", false)
	f.RecordHealthCheck("i-aws", false)
	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Healthy,
		"only 2 consecutive failures (after reset) — not unhealthy yet, but not healthy either (peer.State=provisioning)")

	// One more failure — now 3 consecutive — flips unhealthy
	f.RecordHealthCheck("i-aws", false)
	assert.False(t, f.HealthyRelays()[0].Healthy)
}

// TestRelayStateProvisioningConstant verifies the constant exists and
// matches the controller's value (v1.RelayStateProvisioning = "provisioning").
// Without this, the router treats "provisioning" as a magic string with no
// recognized meaning.
func TestRelayStateProvisioningConstant(t *testing.T) {
	assert.Equal(t, "provisioning", relayStateProvisioning,
		"router must define relayStateProvisioning matching the controller's "+
			"v1.RelayStateProvisioning value (worklog 0467 action item 2)")
}

// TestNewRelayFleetWithThresholds_DefaultsBackcompat verifies the
// existing newRelayFleet (which only takes unhealthyThr) still works by
// using a sensible default healthyThr. This preserves backward-compat
// for the dozens of existing tests without requiring updates to all of them.
func TestNewRelayFleetWithThresholds_DefaultsBackcompat(t *testing.T) {
	// Old constructor — must continue working with reasonable default
	f := newRelayFleet(3, 5*time.Minute)
	assert.Equal(t, 3, f.unhealthyThr)
	assert.Greater(t, f.healthyThr, 0,
		"newRelayFleet must default healthyThr to a positive value so the "+
			"new state-machine path is reachable without explicit config")
}
