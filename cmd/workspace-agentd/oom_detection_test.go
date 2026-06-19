// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// US-44.4: OOM Detection & User Notification.
//
// When opencode is killed by the OOM killer (SIGKILL / exit 137), the
// managedProcess supervisor must:
// 1. Detect the OOM kill signal
// 2. Write the unified restart-reason marker (reason="oom") to the PVC so
//    the next boot can surface it
// 3. Increment Prometheus counters (oom_kills + restarts) for ops dashboards
// 4. Log the event
//
// opencode is third-party and cannot be modified — all detection and
// marker-writing happens in agentd's supervisor loop.
//
// Worklog 371 H5: the separate OOM-specific marker (writeOOMMarker +
// OOMMarkerPath) was removed — it had zero read-side consumers and its
// exitCode/memoryLimit fields were dead data. The restart-reason marker
// (reason="oom") is the single persistent surface; workspace_oom_kills_total
// and workspace_restarts_total are the metric surfaces.

// ---------------------------------------------------------------------------
// OOM exit code detection
// ---------------------------------------------------------------------------

func TestIsOOMExit_SigKill_ReturnsTrue(t *testing.T) {
	assert.True(t, isOOMExit(exitSigKill),
		"SIGKILL (the OOM killer's weapon) must be detected as OOM")
}

func TestIsOOMExit_SigTerm_ReturnsFalse(t *testing.T) {
	assert.False(t, isOOMExit(exitSigTerm),
		"SIGTERM is graceful termination, not OOM")
}

func TestIsOOMExit_NormalExit_ReturnsFalse(t *testing.T) {
	assert.False(t, isOOMExit(exitNormal),
		"normal exit (exit code 0) is not OOM")
}

func TestIsOOMExit_CrashExit_ReturnsFalse(t *testing.T) {
	assert.False(t, isOOMExit(exitCrash),
		"non-SIGKILL crash (e.g. segfault exit code 139) is not OOM")
}

// ---------------------------------------------------------------------------
// handleOOMExit writes the unified restart-reason marker (US-44.7) and
// records the OOM in Prometheus counters. H5 removed the separate OOM
// marker — only the restart-reason marker remains.
// ---------------------------------------------------------------------------

func TestHandleOOMExit_WritesRestartReasonMarker(t *testing.T) {
	dir := t.TempDir()
	restartPath := filepath.Join(dir, ".opencode-restart-reason")

	handleOOMExit("ws-123", restartPath)

	// Restart-reason marker is written with reason="oom".
	restartData, err := os.ReadFile(restartPath)
	require.NoError(t, err, "restart-reason marker must be written")
	var r restartReason
	require.NoError(t, json.Unmarshal(restartData, &r))
	assert.Equal(t, "oom", r.Reason)
	assert.NotEmpty(t, r.Timestamp)
	assert.Empty(t, r.SecretNames, "oom reason has no secret names")
}

func TestHandleOOMExit_DoesNotWriteOOMSpecificMarker(t *testing.T) {
	// H5: the separate OOM marker (OOMMarkerPath) no longer exists.
	// handleOOMExit must not create it.
	dir := t.TempDir()
	restartPath := filepath.Join(dir, ".opencode-restart-reason")
	oomPath := filepath.Join(dir, ".opencode-oom-marker")

	handleOOMExit("ws-h5", restartPath)

	require.FileExists(t, restartPath, "restart-reason marker written")
	_, err := os.Stat(oomPath)
	assert.True(t, os.IsNotExist(err),
		"the dead OOM-specific marker must NOT be written (H5 removed it)")
}

func TestHandleOOMExit_EmptyWorkspaceID_DefaultsToUnknown(t *testing.T) {
	dir := t.TempDir()
	restartPath := filepath.Join(dir, ".opencode-restart-reason")

	// Must not panic; counter increment uses the defaulted label.
	assert.NotPanics(t, func() { handleOOMExit("", restartPath) })

	require.FileExists(t, restartPath, "restart-reason marker written even with empty workspace ID")
}
