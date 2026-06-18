// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/agentd/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// US-44.2: Session-aware restart mechanism.
// US-44.3: Fix api-key restart bug.
//
// When env-secret or api-key credentials change, opencode must be
// restarted (Node.js process.env is immutable after startup). The old
// code restarted IMMEDIATELY regardless of active sessions — destroying
// in-flight work (Incident B, 2026-06-16). The fix defers the restart
// until all sessions are idle, with no forced timeout.

// ---------------------------------------------------------------------------
// sessionStatusTracker.hasAnyBusy / listBusy (US-44.2 prerequisite)
// ---------------------------------------------------------------------------

func TestSessionStatusTracker_HasAnyBusy_Empty_ReturnsFalse(t *testing.T) {
	tracker := newSessionStatusTracker()
	assert.False(t, tracker.hasAnyBusy(),
		"empty tracker must return false — no sessions tracked means no busy sessions")
}

func TestSessionStatusTracker_HasAnyBusy_AllIdle_ReturnsFalse(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")
	tracker.set("ses_2", "idle")
	assert.False(t, tracker.hasAnyBusy())
}

func TestSessionStatusTracker_HasAnyBusy_OneBusy_ReturnsTrue(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")
	tracker.set("ses_2", "busy")
	assert.True(t, tracker.hasAnyBusy())
}

func TestSessionStatusTracker_ListBusy_NoBusy_ReturnsEmpty(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")
	busy := tracker.listBusy()
	assert.Empty(t, busy)
}

func TestSessionStatusTracker_ListBusy_Mixed_ReturnsOnlyBusy(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")
	tracker.set("ses_2", "busy")
	tracker.set("ses_3", "busy")
	tracker.set("ses_4", "idle")
	busy := tracker.listBusy()
	assert.ElementsMatch(t, []string{"ses_2", "ses_3"}, busy)
}

func TestSessionStatusTracker_HasAnyBusy_ConcurrentSafe(t *testing.T) {
	tracker := newSessionStatusTracker()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			tracker.set("ses_1", "busy")
			tracker.set("ses_1", "idle")
			_ = tracker.hasAnyBusy()
			_ = tracker.listBusy()
		}
	}()
	<-done
}

// ---------------------------------------------------------------------------
// shouldRestart includes api-key (US-44.3)
// ---------------------------------------------------------------------------

func TestShouldRestart_APIKey_ReturnsTrue(t *testing.T) {
	batch := []secrets.Secret{
		{Type: "api-key", Name: "my-api-key", Plaintext: "secret"},
	}
	assert.True(t, shouldRestart(batch),
		"api-key must trigger restart (US-44.3: was missing, latent bug)")
}

func TestShouldRestart_APIKeyMixedWithEnvSecret_ReturnsTrue(t *testing.T) {
	batch := []secrets.Secret{
		{Type: "api-key", Name: "k1", Plaintext: "v1"},
		{Type: "env-secret", Name: "e1", Metadata: map[string]string{"var_name": "VAR"}, Plaintext: "v"},
	}
	assert.True(t, shouldRestart(batch),
		"mixed api-key + env-secret must trigger restart")
}

func TestShouldRestart_APIKeyMixedWithSSHKey_ReturnsTrue(t *testing.T) {
	batch := []secrets.Secret{
		{Type: "ssh-key", Name: "k", Metadata: map[string]string{"key_type": "ed25519"}, Plaintext: "key"},
		{Type: "api-key", Name: "my-api-key", Plaintext: "secret"},
	}
	assert.True(t, shouldRestart(batch),
		"ssh-key + api-key must trigger restart (api-key requires it)")
}

// ---------------------------------------------------------------------------
// Session-aware restart decision (US-44.2)
// ---------------------------------------------------------------------------

// TestSessionAwareRestartDecision_AllIdle_RestartsImmediately verifies
// that when no sessions are busy, the restart proceeds immediately (no
// deferral). This is the happy path — config change while idle.
func TestSessionAwareRestartDecision_AllIdle_RestartsImmediately(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")
	tracker.set("ses_2", "idle")

	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(proc, tracker, 5*time.Second)

	assert.True(t, decided,
		"all sessions idle — restart must proceed immediately")
	assert.Equal(t, 1, proc.restartCount(),
		"restart must be called exactly once")
}

// TestSessionAwareRestartDecision_SessionsBusy_DefersRestart verifies
// the core fix for Incident B: when sessions are busy, the restart is
// deferred. The function returns false (not restarted yet) and spawns a
// background goroutine that will restart when sessions go idle.
func TestSessionAwareRestartDecision_SessionsBusy_DefersRestart(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "busy")
	tracker.set("ses_2", "idle")

	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(proc, tracker, 50*time.Millisecond)

	assert.False(t, decided,
		"sessions busy — restart must be deferred, not immediate")
	assert.Equal(t, 0, proc.restartCount(),
		"restart must NOT be called while sessions are busy")
}

// TestSessionAwareRestartDecision_DeferredRestart_AppliesWhenIdle
// verifies that the deferred restart actually fires once sessions
// transition to idle.
func TestSessionAwareRestartDecision_DeferredRestart_AppliesWhenIdle(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "busy")

	proc := &mockManagedProcess{}
	_ = makeSessionAwareRestartDecision(proc, tracker, 20*time.Millisecond)

	// Session is still busy — restart should NOT fire yet.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, proc.restartCount(),
		"restart must not fire while session is busy")

	// Session transitions to idle — restart should fire within poll interval.
	tracker.set("ses_1", "idle")

	require.Eventually(t, func() bool {
		return proc.restartCount() == 1
	}, 500*time.Millisecond, 10*time.Millisecond,
		"deferred restart must fire once sessions become idle")
}

// TestSessionAwareRestartDecision_EmptyTracker_RestartsImmediately
// verifies the SSE-disconnect fallback: if the tracker has no data (map
// empty, SSE disconnected), treat as "all idle" and restart immediately
// with a logged warning.
func TestSessionAwareRestartDecision_EmptyTracker_RestartsImmediately(t *testing.T) {
	tracker := newSessionStatusTracker()

	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(proc, tracker, 5*time.Second)

	assert.True(t, decided,
		"empty tracker (SSE disconnected) must restart immediately — no data to defer on")
	assert.Equal(t, 1, proc.restartCount())
}

// TestSessionAwareRestartDecision_NilProc_NoPanic verifies graceful
// degradation when proc is nil (test-only or misconfigured).
func TestSessionAwareRestartDecision_NilProc_NoPanic(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")

	assert.NotPanics(t, func() {
		decided := makeSessionAwareRestartDecision(nil, tracker, 5*time.Second)
		assert.True(t, decided, "nil proc with idle sessions returns true (no-op)")
	})
}

// TestSessionAwareRestartDecision_NilTracker_RestartsImmediately
// verifies that a nil tracker is treated as "all idle" — same fallback
// as empty tracker.
func TestSessionAwareRestartDecision_NilTracker_RestartsImmediately(t *testing.T) {
	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(proc, nil, 5*time.Second)

	assert.True(t, decided,
		"nil tracker must restart immediately — cannot check session state")
	assert.Equal(t, 1, proc.restartCount())
}

// ---------------------------------------------------------------------------
// mockManagedProcess — test double for *managedProcess
// ---------------------------------------------------------------------------

type mockManagedProcess struct {
	restarts int32
	done     chan struct{}
}

func (m *mockManagedProcess) restart() {
	if m.done == nil {
		m.done = make(chan struct{})
	}
	m.restarts++
}

func (m *mockManagedProcess) restartCount() int {
	return int(m.restarts)
}
