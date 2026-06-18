// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
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
// 2. Write a marker file to the PVC so the next boot can surface it
// 3. Increment a Prometheus counter for ops dashboards
// 4. Log the event
//
// opencode is third-party and cannot be modified — all detection and
// marker-writing happens in agentd's supervisor loop.

// ---------------------------------------------------------------------------
// OOM marker file
// ---------------------------------------------------------------------------

func TestWriteOOMMarker_CreatesFileWithTimestamp(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-oom-marker")

	err := writeOOMMarker(markerPath, "2Gi")
	require.NoError(t, err)

	data, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"reason":"oom"`)
	assert.Contains(t, string(data), `"memoryLimit":"2Gi"`)
	assert.Contains(t, string(data), `"exitCode":137`)
	assert.Contains(t, string(data), `"timestamp":"`)
}

func TestWriteOOMMarker_OverwritesExistingMarker(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, ".opencode-oom-marker")

	require.NoError(t, writeOOMMarker(markerPath, "2Gi"))
	require.NoError(t, writeOOMMarker(markerPath, "4Gi"))

	data, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"memoryLimit":"4Gi"`,
		"second write must overwrite the first")
}

func TestWriteOOMMarker_CreatesParentDirIfMissing(t *testing.T) {
	// writeOOMMarker calls MkdirAll on the parent — the marker dir may
	// not exist if init containers haven't run yet. This is a feature:
	// the supervisor must be able to write the marker even on first boot.
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "subdir", ".opencode-oom-marker")

	err := writeOOMMarker(markerPath, "2Gi")
	require.NoError(t, err, "MkdirAll must create missing parent dirs")

	_, err = os.Stat(markerPath)
	assert.NoError(t, err, "marker file must exist after writeOOMMarker")
}

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
