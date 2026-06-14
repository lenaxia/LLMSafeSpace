// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ParsePeerConfig tests
// ---------------------------------------------------------------------------

func TestParsePeerConfig_Valid(t *testing.T) {
	data := []byte(`{
		"relays": [
			{"id": "oci-1", "wgIP": "10.42.42.2", "provider": "oci", "state": "healthy"},
			{"id": "gcp-1", "wgIP": "10.42.42.3", "provider": "gcp", "state": "healthy"}
		]
	}`)

	cfg, err := ParsePeerConfig(data)
	require.NoError(t, err)
	require.Len(t, cfg.Relays, 2)
	assert.Equal(t, "oci-1", cfg.Relays[0].ID)
	assert.Equal(t, "10.42.42.2", cfg.Relays[0].WgIP)
	assert.Equal(t, "oci", cfg.Relays[0].Provider)
	assert.Equal(t, "gcp-1", cfg.Relays[1].ID)
}

func TestParsePeerConfig_Empty(t *testing.T) {
	cfg, err := ParsePeerConfig([]byte(`{"relays": []}`))
	require.NoError(t, err)
	assert.Empty(t, cfg.Relays)
}

func TestParsePeerConfig_InvalidJSON(t *testing.T) {
	_, err := ParsePeerConfig([]byte(`{invalid`))
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// UpdatePeers tests
// ---------------------------------------------------------------------------

func TestRelayFleet_UpdatePeers_AddsNew(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.Equal(t, "oci-1", statuses[0].ID)
}

func TestRelayFleet_UpdatePeers_PreservesHealth(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordHealthCheck("oci-1", false)
	f.RecordHealthCheck("oci-1", false)
	f.RecordHealthCheck("oci-1", false)

	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Healthy, "health state should survive peer update")
}

func TestRelayFleet_UpdatePeers_RemovesStale(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1, "gcp-1 should be removed")
	assert.Equal(t, "oci-1", statuses[0].ID)
}

func TestRelayFleet_UpdatePeers_UpdatesState(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "draining"},
	})

	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.Equal(t, "draining", statuses[0].State)
}

// ---------------------------------------------------------------------------
// SelectRelay tests
// ---------------------------------------------------------------------------

func TestSelectRelay_OCIPrimaryWhenBothHealthy(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	for i := 0; i < 100; i++ {
		id, _, ok := f.SelectRelay()
		require.True(t, ok)
		assert.Equal(t, "oci-1", id, "OCI should receive 100%% of traffic when both healthy (iteration %d)", i)
	}
}

func TestSelectRelay_GCPFailoverWhenOCIUnhealthy(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	for i := 0; i < 3; i++ {
		f.RecordHealthCheck("oci-1", false)
	}

	id, _, ok := f.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "gcp-1", id, "GCP should receive traffic when OCI is unhealthy")
}

func TestSelectRelay_GCPFailoverWhenOCIDraining(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "draining"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	id, _, ok := f.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "gcp-1", id)
}

func TestSelectRelay_NoHealthyRelays(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	for i := 0; i < 3; i++ {
		f.RecordHealthCheck("oci-1", false)
		f.RecordHealthCheck("gcp-1", false)
	}

	_, _, ok := f.SelectRelay()
	assert.False(t, ok)
}

func TestSelectRelay_EmptyFleet(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	_, _, ok := f.SelectRelay()
	assert.False(t, ok)
}

func TestSelectRelay_429DrainingExcludesRelay(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	f.Mark429Draining("oci-1", "storm")

	for i := 0; i < 50; i++ {
		id, _, ok := f.SelectRelay()
		require.True(t, ok)
		assert.Equal(t, "gcp-1", id, "429-draining OCI should be excluded")
	}
}

func TestSelectRelay_ReturnsWgIP(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	id, wgIP, ok := f.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "oci-1", id)
	assert.Equal(t, "10.42.42.2", wgIP)
}

// ---------------------------------------------------------------------------
// Health check recording tests
// ---------------------------------------------------------------------------

func TestRecordHealthCheck_SuccessClearsFailures(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordHealthCheck("oci-1", false)
	f.RecordHealthCheck("oci-1", false)
	f.RecordHealthCheck("oci-1", true)

	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Healthy, "success should reset failure count")
}

func TestRecordHealthCheck_UnknownRelay(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.RecordHealthCheck("nonexistent", true)
}

func TestRecordHealthCheck_ThreeFailuresMarksUnhealthy(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordHealthCheck("oci-1", false)
	statuses := f.HealthyRelays()
	assert.True(t, statuses[0].Healthy, "1 failure should not mark unhealthy")

	f.RecordHealthCheck("oci-1", false)
	statuses = f.HealthyRelays()
	assert.True(t, statuses[0].Healthy, "2 failures should not mark unhealthy")

	f.RecordHealthCheck("oci-1", false)
	statuses = f.HealthyRelays()
	assert.False(t, statuses[0].Healthy, "3 failures should mark unhealthy")
}

// ---------------------------------------------------------------------------
// Request recording + 429 detection tests
// ---------------------------------------------------------------------------

func TestRecordRequest_RecordsStatusCodes(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordRequest("oci-1", 200)
	f.RecordRequest("oci-1", 200)
	f.RecordRequest("oci-1", 429)

	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.Equal(t, int64(3), statuses[0].TotalRequests)
	assert.Equal(t, int64(1), statuses[0].Requests429)
}

func TestRecordRequest_UnknownRelay(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.RecordRequest("nonexistent", 200)
}

func TestRelay429Rate_EmptyFleet(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	rate, probes := f.Relay429Rate("nonexistent")
	assert.Equal(t, 0.0, rate)
	assert.Equal(t, 0, probes)
}

func TestRelay429Rate_RecordsRate(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordRequest("oci-1", 200)
	f.RecordRequest("oci-1", 429)
	f.RecordRequest("oci-1", 429)

	rate, _ := f.Relay429Rate("oci-1")
	assert.Greater(t, rate, 0.0)
}

func TestRelay429Rate_WindowPruning(t *testing.T) {
	f := newRelayFleet(3, 50*time.Millisecond)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordRequest("oci-1", 429)
	f.RecordRequest("oci-1", 429)

	time.Sleep(60 * time.Millisecond)

	rate, _ := f.Relay429Rate("oci-1")
	assert.Equal(t, 0.0, rate, "429s outside window should be pruned")
}

// ---------------------------------------------------------------------------
// Stream tracking tests
// ---------------------------------------------------------------------------

func TestRecordStreamStartEnd(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordStreamStart("oci-1")
	f.RecordStreamStart("oci-1")
	assert.Equal(t, int64(2), f.ActiveStreams("oci-1"))

	f.RecordStreamEnd("oci-1")
	assert.Equal(t, int64(1), f.ActiveStreams("oci-1"))

	f.RecordStreamEnd("oci-1")
	assert.Equal(t, int64(0), f.ActiveStreams("oci-1"))
}

func TestRecordStreamEnd_NeverGoesNegative(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordStreamEnd("oci-1")
	assert.Equal(t, int64(0), f.ActiveStreams("oci-1"))
}

func TestRecordEgress(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordEgress("oci-1", 1024)
	f.RecordEgress("oci-1", 512)

	statuses := f.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.Equal(t, int64(1536), statuses[0].EgressBytes)
}

// ---------------------------------------------------------------------------
// 429 state management tests
// ---------------------------------------------------------------------------

func TestMark429Draining(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	f.Mark429Draining("oci-1", "storm-detected")

	statuses := f.HealthyRelays()
	for _, s := range statuses {
		if s.ID == "oci-1" {
			assert.True(t, s.Draining429)
		}
	}
}

func TestMark429Suspect_Clear429State(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.Mark429Suspect("oci-1")
	f.Mark429Suspect("oci-1")
	f.Clear429State("oci-1")

	_, probes := f.Relay429Rate("oci-1")
	assert.Equal(t, 0, probes, "consecutive probes should be cleared")
}

// ---------------------------------------------------------------------------
// HasHealthyRelay tests
// ---------------------------------------------------------------------------

func TestHasHealthyRelay_True(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})
	assert.True(t, f.HasHealthyRelay())
}

func TestHasHealthyRelay_FalseAllUnhealthy(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	for i := 0; i < 3; i++ {
		f.RecordHealthCheck("oci-1", false)
	}

	assert.False(t, f.HasHealthyRelay())
}

func TestHasHealthyRelay_FalseAllDraining(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "draining"},
	})

	assert.False(t, f.HasHealthyRelay())
}

// ---------------------------------------------------------------------------
// GetWgIP tests
// ---------------------------------------------------------------------------

func TestGetWgIP_ExistingRelay(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})
	assert.Equal(t, "10.42.42.2", f.GetWgIP("oci-1"))
}

func TestGetWgIP_NonexistentRelay(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	assert.Equal(t, "", f.GetWgIP("nonexistent"))
}

// ---------------------------------------------------------------------------
// relayWeight tests
// ---------------------------------------------------------------------------

func TestRelayWeight_OCIPrimary(t *testing.T) {
	assert.Equal(t, 100.0, relayWeight("oci", "healthy", "healthy"))
}

func TestRelayWeight_GCPFailover(t *testing.T) {
	assert.Equal(t, 1.0, relayWeight("gcp", "healthy", "healthy"))
}

func TestRelayWeight_UnhealthyIsZero(t *testing.T) {
	assert.Equal(t, 0.0, relayWeight("oci", "healthy", "unhealthy"))
	assert.Equal(t, 0.0, relayWeight("gcp", "healthy", "unhealthy"))
}

func TestRelayWeight_SuspectReduced(t *testing.T) {
	assert.Equal(t, 10.0, relayWeight("oci", "suspect", "healthy"))
	assert.Equal(t, 0.1, relayWeight("gcp", "suspect", "healthy"))
}

// ---------------------------------------------------------------------------
// Concurrency test
// ---------------------------------------------------------------------------

func TestRelayFleet_ConcurrentAccess(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	done := make(chan struct{})

	go func() {
		for i := 0; i < 500; i++ {
			f.SelectRelay()
			f.RecordRequest("oci-1", 200)
			f.RecordHealthCheck("oci-1", true)
		}
		close(done)
	}()

	for i := 0; i < 500; i++ {
		f.HealthyRelays()
		f.HasHealthyRelay()
	}

	<-done
}

// ---------------------------------------------------------------------------
// PeerConfig JSON round-trip test
// ---------------------------------------------------------------------------

func TestPeerConfig_JSONRoundTrip(t *testing.T) {
	original := PeerConfig{
		Relays: []PeerEntry{
			{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
			{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "draining"},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	parsed, err := ParsePeerConfig(data)
	require.NoError(t, err)
	require.Len(t, parsed.Relays, 2)
	assert.Equal(t, "oci-1", parsed.Relays[0].ID)
	assert.Equal(t, "gcp-1", parsed.Relays[1].ID)
	assert.Equal(t, "draining", parsed.Relays[1].State)
}

// ---------------------------------------------------------------------------
// Regression tests for review findings
// ---------------------------------------------------------------------------

// TestClear429State_RecoversDrainingRelay verifies that a relay marked
// as 429-draining becomes eligible for selection again after Clear429State.
// This is the regression test for the critical review finding: relays marked
// 429-draining could never recover because Clear429State didn't reset
// markedDraining.
func TestClear429State_RecoversDrainingRelay(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	f.Mark429Draining("oci-1", "storm")

	id, _, ok := f.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "gcp-1", id, "429-draining OCI should be excluded")

	f.Clear429State("oci-1")

	for i := 0; i < 50; i++ {
		id, _, ok := f.SelectRelay()
		require.True(t, ok)
		assert.Equal(t, "oci-1", id, "recovered OCI should receive 100%% traffic (iteration %d)", i)
	}
}

// TestClear429State_ResetsAllFields verifies all 429 state fields are cleared.
func TestClear429State_ResetsAllFields(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordRequest("oci-1", 429)
	f.Mark429Suspect("oci-1")
	f.Mark429Suspect("oci-1")
	f.Mark429Draining("oci-1", "storm")

	statuses := f.HealthyRelays()
	require.True(t, statuses[0].Draining429)

	f.Clear429State("oci-1")

	statuses = f.HealthyRelays()
	assert.False(t, statuses[0].Draining429, "markedDraining should be cleared")

	rate, probes := f.Relay429Rate("oci-1")
	assert.Equal(t, 0.0, rate, "429 window should be cleared")
	assert.Equal(t, 0, probes, "consecutiveProbes should be cleared")
}

// TestConfigurableHealthThreshold verifies the unhealthy threshold is
// configurable and not hardcoded to 3.
func TestConfigurableHealthThreshold(t *testing.T) {
	f := newRelayFleet(5, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	f.RecordHealthCheck("oci-1", false)
	f.RecordHealthCheck("oci-1", false)
	f.RecordHealthCheck("oci-1", false)

	statuses := f.HealthyRelays()
	assert.True(t, statuses[0].Healthy, "3 failures should NOT mark unhealthy when threshold is 5")

	f.RecordHealthCheck("oci-1", false)
	f.RecordHealthCheck("oci-1", false)

	statuses = f.HealthyRelays()
	assert.False(t, statuses[0].Healthy, "5 failures should mark unhealthy when threshold is 5")
}

// TestConsecutiveProbeDraining verifies that 3 consecutive probe failures
// mark a relay as draining (Tier 2 consecutive-probe path via detector.checkStorm).
func TestConsecutiveProbeDraining(t *testing.T) {
	f := newRelayFleet(3, 5*time.Minute)
	f.UpdatePeers([]PeerEntry{
		{ID: "oci-1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "gcp-1", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	det := newDetector429(f, 0.99, 8080)

	f.Mark429Suspect("oci-1")
	det.checkStorm("oci-1")
	id, _, ok := f.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "oci-1", id, "1 probe should not drain")

	f.Mark429Suspect("oci-1")
	det.checkStorm("oci-1")
	id, _, ok = f.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "oci-1", id, "2 probes should not drain")

	f.Mark429Suspect("oci-1")
	det.checkStorm("oci-1")
	id, _, ok = f.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "gcp-1", id, "3 consecutive probes should drain OCI")

	statuses := f.HealthyRelays()
	for _, s := range statuses {
		if s.ID == "oci-1" {
			assert.True(t, s.Draining429, "OCI should be marked 429-draining")
		}
	}
}
