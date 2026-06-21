// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive loadPeerConfig synchronously instead of starting a
// goroutine and racing time.Sleep windows. The previous goroutine-driven
// tests (worklog 0468) were flaky under CI race-detector when filesystem
// scheduling delayed the ticker tick past the assertion deadline. See
// worklog 0470.

// TestLoadPeerConfig_RemovesRelaysWhenFileMissing pins the orphan-cleanup
// fix from worklog 0467 Action Item 3. When the peer ConfigMap is deleted
// (e.g. InferenceRelay CR deleted), the file becomes missing on the
// relay-router pod's volume mount. loadPeerConfig must treat a missing
// file as "no peers" and clear the in-memory fleet.
func TestLoadPeerConfig_RemovesRelaysWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	// Seed the fleet with one relay (simulating a prior successful load).
	fleet := newRelayFleet(3, 5*time.Minute)
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600))
	loadPeerConfig(path, fleet)
	require.Len(t, fleet.HealthyRelays(), 1, "initial seed must populate the fleet")

	// Delete the file (simulates ConfigMap deletion / no peer file).
	require.NoError(t, os.Remove(path))
	loadPeerConfig(path, fleet)

	assert.Empty(t, fleet.HealthyRelays(),
		"loadPeerConfig must clear the fleet when the peer config file is missing; "+
			"otherwise orphaned relays persist forever (worklog 0467)")
}

// TestLoadPeerConfig_RemovesRelaysWhenFileEmpty verifies the same behavior
// for an empty file (a configmap can be patched to empty data).
func TestLoadPeerConfig_RemovesRelaysWhenFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	fleet := newRelayFleet(3, 5*time.Minute)
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600))
	loadPeerConfig(path, fleet)
	require.Len(t, fleet.HealthyRelays(), 1)

	// Truncate to empty — must clear the fleet.
	require.NoError(t, os.WriteFile(path, []byte(""), 0o600))
	loadPeerConfig(path, fleet)

	assert.Empty(t, fleet.HealthyRelays(),
		"loadPeerConfig must clear the fleet when the peer config file is empty")
}

// TestLoadPeerConfig_RemovesRelaysWhenWhitespaceOnly verifies the
// whitespace-only path (TrimSpace branch).
func TestLoadPeerConfig_RemovesRelaysWhenWhitespaceOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	fleet := newRelayFleet(3, 5*time.Minute)
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600))
	loadPeerConfig(path, fleet)
	require.Len(t, fleet.HealthyRelays(), 1)

	require.NoError(t, os.WriteFile(path, []byte("   \n  \t\n"), 0o600))
	loadPeerConfig(path, fleet)

	assert.Empty(t, fleet.HealthyRelays(),
		"loadPeerConfig must clear the fleet when the peer config file is whitespace-only")
}

// TestLoadPeerConfig_RemovesRelaysWhenEmptyRelaysList verifies the
// JSON-empty-list path (controller writes {"relays":[]} when no providers).
func TestLoadPeerConfig_RemovesRelaysWhenEmptyRelaysList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	fleet := newRelayFleet(3, 5*time.Minute)
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600))
	loadPeerConfig(path, fleet)
	require.Len(t, fleet.HealthyRelays(), 1)

	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[]}`), 0o600))
	loadPeerConfig(path, fleet)

	assert.Empty(t, fleet.HealthyRelays(),
		"loadPeerConfig must clear the fleet when the peer list is explicitly empty")
}

// TestLoadPeerConfig_PreservesFleetOnParseError verifies a corrupt file
// does NOT trigger removal — the previous valid peer list must stick
// around so a transient ConfigMap glitch doesn't drain all traffic.
func TestLoadPeerConfig_PreservesFleetOnParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	fleet := newRelayFleet(3, 5*time.Minute)
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`), 0o600))
	loadPeerConfig(path, fleet)
	require.Len(t, fleet.HealthyRelays(), 1)

	// Corrupt the file.
	require.NoError(t, os.WriteFile(path, []byte(`{garbage`), 0o600))
	loadPeerConfig(path, fleet)

	// Fleet must NOT be cleared on parse errors — corrupt data is
	// transient and the previous valid state is safer to keep.
	assert.Len(t, fleet.HealthyRelays(), 1,
		"loadPeerConfig must NOT clear the fleet on parse errors — keep last known good")
}

// TestLoadPeerConfig_AddsAndRemovesPeersBasedOnFile verifies the steady-
// state behavior: the in-memory fleet tracks the file content as it changes.
func TestLoadPeerConfig_AddsAndRemovesPeersBasedOnFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	fleet := newRelayFleet(3, 5*time.Minute)

	// Initial: 2 relays.
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[
		{"id":"a","endpoint":"1.1.1.1:8080","provider":"aws","state":"healthy","token":"t1"},
		{"id":"b","endpoint":"2.2.2.2:8080","provider":"aws","state":"healthy","token":"t2"}
	]}`), 0o600))
	loadPeerConfig(path, fleet)
	require.Len(t, fleet.HealthyRelays(), 2)

	// Drop relay a; add c.
	require.NoError(t, os.WriteFile(path, []byte(`{"relays":[
		{"id":"b","endpoint":"2.2.2.2:8080","provider":"aws","state":"healthy","token":"t2"},
		{"id":"c","endpoint":"3.3.3.3:8080","provider":"aws","state":"healthy","token":"t3"}
	]}`), 0o600))
	loadPeerConfig(path, fleet)

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 2)
	ids := []string{statuses[0].ID, statuses[1].ID}
	assert.Contains(t, ids, "b")
	assert.Contains(t, ids, "c")
	assert.NotContains(t, ids, "a", "relay a must be removed once it's no longer in the file")
}
