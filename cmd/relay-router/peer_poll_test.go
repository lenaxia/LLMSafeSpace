// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPollPeerConfig_RemovesRelaysWhenFileMissing pins the orphan-cleanup
// fix from worklog 0467 Action Item 3. When the peer ConfigMap is deleted
// (e.g. InferenceRelay CR deleted), the file becomes missing on the
// relay-router pod's volume mount. The poller must treat a missing or
// empty file as "no peers" and clear the in-memory fleet — otherwise
// stale relays linger in metrics and fleet selection until pod restart.
func TestPollPeerConfig_RemovesRelaysWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	// Seed with one relay
	err := os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600)
	require.NoError(t, err)

	fleet := newRelayFleet(3, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run one poll cycle synchronously by invoking the ticker logic via
	// a tight interval and waiting briefly.
	go pollPeerConfig(ctx, path, 20*time.Millisecond, fleet)
	time.Sleep(60 * time.Millisecond)

	// Verify the relay was loaded
	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1, "initial seed must populate the fleet")
	assert.Equal(t, "i-aaa", statuses[0].ID)

	// Delete the file (simulates ConfigMap deletion)
	require.NoError(t, os.Remove(path))

	// Wait long enough for the poller to observe the missing file
	time.Sleep(60 * time.Millisecond)

	// Fleet must now be empty
	statuses = fleet.HealthyRelays()
	assert.Empty(t, statuses,
		"poller must clear the fleet when the peer config file is removed; "+
			"otherwise orphaned relays persist forever (worklog 0467)")
}

// TestPollPeerConfig_RemovesRelaysWhenFileEmpty verifies the same behavior
// for an empty file (a configmap can be patched to empty data).
func TestPollPeerConfig_RemovesRelaysWhenFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	// Seed with one relay
	err := os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600)
	require.NoError(t, err)

	fleet := newRelayFleet(3, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pollPeerConfig(ctx, path, 20*time.Millisecond, fleet)
	time.Sleep(60 * time.Millisecond)
	require.Len(t, fleet.HealthyRelays(), 1)

	// Truncate to empty — must clear the fleet
	require.NoError(t, os.WriteFile(path, []byte(""), 0o600))
	time.Sleep(60 * time.Millisecond)

	assert.Empty(t, fleet.HealthyRelays(),
		"poller must clear the fleet when the peer config file is empty")
}

// TestPollPeerConfig_RemovesRelaysWhenEmptyRelaysList verifies the
// JSON-empty-list path (controller writes {"relays":[]} when no providers).
func TestPollPeerConfig_RemovesRelaysWhenEmptyRelaysList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	// Seed with one relay
	err := os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600)
	require.NoError(t, err)

	fleet := newRelayFleet(3, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pollPeerConfig(ctx, path, 20*time.Millisecond, fleet)
	time.Sleep(60 * time.Millisecond)
	require.Len(t, fleet.HealthyRelays(), 1)

	// Replace with empty list — must clear the fleet
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[]}`), 0o600))
	time.Sleep(60 * time.Millisecond)

	assert.Empty(t, fleet.HealthyRelays(),
		"poller must clear the fleet when the peer list is explicitly empty")
}

// TestPollPeerConfig_PreservesFleetOnParseError verifies a corrupt file
// does NOT trigger removal — the previous valid peer list must stick
// around so a transient ConfigMap glitch doesn't drain all traffic.
func TestPollPeerConfig_PreservesFleetOnParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	err := os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600)
	require.NoError(t, err)

	fleet := newRelayFleet(3, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pollPeerConfig(ctx, path, 20*time.Millisecond, fleet)
	time.Sleep(60 * time.Millisecond)
	require.Len(t, fleet.HealthyRelays(), 1)

	// Corrupt the file
	require.NoError(t, os.WriteFile(path, []byte(`{garbage`), 0o600))
	time.Sleep(60 * time.Millisecond)

	// Fleet must NOT be cleared on parse errors — corrupt data is
	// transient and the previous valid state is safer to keep.
	assert.Len(t, fleet.HealthyRelays(), 1,
		"poller must NOT clear the fleet on parse errors — keep last known good")
}
