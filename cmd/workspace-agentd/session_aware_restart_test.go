// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/agentd/secrets"
)

// US-44.2: Session-aware restart mechanism.
// US-44.3: Fix api-key restart bug.
// Worklog 371 C2/H1: deferred-restart goroutine must be cancellable (H1a),
// bounded by maxDefer (H1b), tracked (H1c), prune stale busy entries (C2a),
// and not immediately restart on a cold-start empty tracker when opencode
// is reachable (C2b).

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
// Session-aware restart decision (US-44.2 + worklog 371 C2/H1)
// ---------------------------------------------------------------------------

// TestSessionAwareRestartDecision_AllIdle_RestartsImmediately verifies
// that when no sessions are busy, the restart proceeds immediately (no
// deferral). This is the happy path — config change while idle.
func TestSessionAwareRestartDecision_AllIdle_RestartsImmediately(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")
	tracker.set("ses_2", "idle")

	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(context.Background(), proc, tracker, 5*time.Second, time.Hour, nil, nil)

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
	decided := makeSessionAwareRestartDecision(context.Background(), proc, tracker, 50*time.Millisecond, time.Hour, nil, nil)

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
	_ = makeSessionAwareRestartDecision(context.Background(), proc, tracker, 20*time.Millisecond, time.Hour, nil, nil)

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

// TestSessionAwareRestartDecision_NilProc_NoPanic verifies graceful
// degradation when proc is nil (test-only or misconfigured).
func TestSessionAwareRestartDecision_NilProc_NoPanic(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "idle")

	assert.NotPanics(t, func() {
		decided := makeSessionAwareRestartDecision(context.Background(), nil, tracker, 5*time.Second, time.Hour, nil, nil)
		assert.True(t, decided, "nil proc with idle sessions returns true (no-op)")
	})
}

// ---------------------------------------------------------------------------
// C2a: prune stale busy entries from the deferred-restart poll tick
// ---------------------------------------------------------------------------

// TestSessionAwareRestartDecision_C2a_PruneClearsStaleBusy verifies that
// when opencode dies mid-busy and the supervisor respawns it, the stale
// "busy" entry is pruned (via the liveSessions lister) and the deferred
// restart fires on the next poll tick instead of deferring forever.
//
// Scenario: session ses_stale is marked busy in the tracker. opencode dies
// and respawns; the live session list no longer includes ses_stale. The
// lister returns the new live IDs; prune removes ses_stale; hasAnyBusy
// returns false; the deferred restart fires.
func TestSessionAwareRestartDecision_C2a_PruneClearsStaleBusy(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_stale", "busy")

	// Lister reports that opencode now has only ses_alive (idle) — ses_stale
	// no longer exists (it died with the previous opencode process).
	lister := func(ctx context.Context) []string {
		return []string{"ses_alive"}
	}
	// Pre-seed the tracker so the first prune has something to remove.
	tracker.set("ses_alive", "idle")

	proc := &mockManagedProcess{}
	_ = makeSessionAwareRestartDecision(context.Background(), proc, tracker, 20*time.Millisecond, time.Hour, lister, nil)

	// ses_stale is pruned on the first poll tick → hasAnyBusy false → restart.
	require.Eventually(t, func() bool {
		return proc.restartCount() == 1
	}, 500*time.Millisecond, 10*time.Millisecond,
		"stale busy entry must be pruned so the deferred restart fires")
}

// ---------------------------------------------------------------------------
// C2b: cold-start empty tracker does NOT immediately restart if opencode
// is reachable with sessions (regression: would destroy in-flight work).
// ---------------------------------------------------------------------------

// TestSessionAwareRestartDecision_C2b_EmptyTracker_OpencodeAliveWithSessions_Defers
// is the core C2b regression test: an empty tracker (agentd restarted, SSE
// not yet reconnected) with opencode reachable and holding sessions must
// DEFER, not immediately restart. Pre-fix, this would immediately restart
// and destroy in-flight agentic work (Incident B regression).
func TestSessionAwareRestartDecision_C2b_EmptyTracker_OpencodeAliveWithSessions_Defers(t *testing.T) {
	tracker := newSessionStatusTracker() // empty — cold start

	lister := func(ctx context.Context) []string {
		return []string{"ses_inflight_1", "ses_inflight_2"} // opencode alive, sessions exist
	}

	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(context.Background(), proc, tracker, 50*time.Millisecond, 200*time.Millisecond, lister, nil)

	assert.False(t, decided,
		"empty tracker with opencode alive + sessions must DEFER (C2b) — immediate restart would destroy in-flight work")
	assert.Equal(t, 0, proc.restartCount(),
		"restart must NOT fire immediately on cold-start with live opencode")
}

// TestSessionAwareRestartDecision_C2b_EmptyTracker_OpencodeUnreachable_RestartsImmediately
// preserves the legitimate SSE-disconnect fallback: if opencode is genuinely
// unreachable (not just SSE-disconnected), there is nothing to lose — restart
// immediately so the credential applies.
func TestSessionAwareRestartDecision_C2b_EmptyTracker_OpencodeUnreachable_RestartsImmediately(t *testing.T) {
	tracker := newSessionStatusTracker() // empty — cold start

	lister := func(ctx context.Context) []string {
		return nil // opencode unreachable
	}

	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(context.Background(), proc, tracker, 5*time.Second, time.Hour, lister, nil)

	assert.True(t, decided,
		"empty tracker + unreachable opencode must restart immediately — nothing to lose")
	assert.Equal(t, 1, proc.restartCount())
}

// TestSessionAwareRestartDecision_C2b_EmptyTracker_OpencodeAliveNoSessions_RestartsImmediately
// verifies that an alive opencode with zero sessions restarts immediately —
// there is no in-flight work to protect.
func TestSessionAwareRestartDecision_C2b_EmptyTracker_OpencodeAliveNoSessions_RestartsImmediately(t *testing.T) {
	tracker := newSessionStatusTracker()

	lister := func(ctx context.Context) []string {
		return []string{} // opencode alive, zero sessions
	}

	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(context.Background(), proc, tracker, 5*time.Second, time.Hour, lister, nil)

	assert.True(t, decided,
		"alive opencode with zero sessions must restart immediately — nothing to lose")
	assert.Equal(t, 1, proc.restartCount())
}

// TestSessionAwareRestartDecision_NilTracker_NilLister_RestartsImmediately
// preserves the original no-lister fallback: nil tracker + nil lister →
// immediate restart (no way to probe opencode, assume safe).
func TestSessionAwareRestartDecision_NilTracker_NilLister_RestartsImmediately(t *testing.T) {
	proc := &mockManagedProcess{}
	decided := makeSessionAwareRestartDecision(context.Background(), proc, nil, 5*time.Second, time.Hour, nil, nil)

	assert.True(t, decided,
		"nil tracker with no lister must restart immediately — cannot probe opencode")
	assert.Equal(t, 1, proc.restartCount())
}

// ---------------------------------------------------------------------------
// H1a: deferred-restart goroutine is cancellable via context (shutdown)
// ---------------------------------------------------------------------------

// TestSessionAwareRestartDecision_H1a_ContextCancel_StopsGoroutine verifies
// that canceling the context (agentd shutdown) stops the deferred-restart
// goroutine. Pre-fix, the goroutine had no context and polled forever.
func TestSessionAwareRestartDecision_H1a_ContextCancel_StopsGoroutine(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "busy")

	ctx, cancel := context.WithCancel(context.Background())
	proc := &mockManagedProcess{}
	_ = makeSessionAwareRestartDecision(ctx, proc, tracker, 20*time.Millisecond, time.Hour, nil, nil)

	// Cancel the context (simulate shutdown) while session is still busy.
	cancel()

	// Give the goroutine time to observe the cancellation.
	time.Sleep(80 * time.Millisecond)

	// Now transition to idle — the restart must NOT fire because the
	// goroutine has exited.
	tracker.set("ses_1", "idle")
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, 0, proc.restartCount(),
		"canceled deferred-restart goroutine must not fire a restart after shutdown")
}

// ---------------------------------------------------------------------------
// H1b: maxDefer force-restarts a stuck-busy session
// ---------------------------------------------------------------------------

// TestSessionAwareRestartDecision_H1b_MaxDefer_ForceRestarts verifies that
// a session stuck busy (infinite loop, hung tool) eventually gets the
// credential applied via the maxDefer force-restart. Pre-fix, the goroutine
// deferred forever and the credential silently never applied.
func TestSessionAwareRestartDecision_H1b_MaxDefer_ForceRestarts(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_stuck", "busy") // never goes idle

	proc := &mockManagedProcess{}
	_ = makeSessionAwareRestartDecision(context.Background(), proc, tracker, 10*time.Millisecond, 80*time.Millisecond, nil, nil)

	// Session stays busy; maxDefer (80ms) must force the restart.
	require.Eventually(t, func() bool {
		return proc.restartCount() == 1
	}, 500*time.Millisecond, 10*time.Millisecond,
		"maxDefer must force-restart a stuck-busy session so the credential applies")
}

// ---------------------------------------------------------------------------
// H1c: deferred-restart goroutine is tracked by the WaitGroup
// ---------------------------------------------------------------------------

// TestSessionAwareRestartDecision_H1c_WaitGroupTracked verifies that the
// deferred-restart goroutine registers with the provided WaitGroup so
// shutdown can wait for it before proc.stop(). The WaitGroup must reach
// zero once the goroutine exits (here, via context cancellation).
func TestSessionAwareRestartDecision_H1c_WaitGroupTracked(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_1", "busy")

	bgWg := &sync.WaitGroup{}

	ctx, cancel := context.WithCancel(context.Background())
	proc := &mockManagedProcess{}
	_ = makeSessionAwareRestartDecision(ctx, proc, tracker, 20*time.Millisecond, time.Hour, nil, bgWg)

	// Goroutine is running (session busy). Cancel and Wait must return.
	cancel()

	waitDone := make(chan struct{})
	go func() {
		bgWg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
		// success — goroutine called Done()
	case <-time.After(time.Second):
		t.Fatal("bgWg.Wait() did not return after context cancel — goroutine not tracked")
	}
}

// ---------------------------------------------------------------------------
// mockManagedProcess — test double for *managedProcess
// ---------------------------------------------------------------------------

type mockManagedProcess struct {
	restarts atomic.Int32
	done     chan struct{}
}

func (m *mockManagedProcess) restart() {
	if m.done == nil {
		m.done = make(chan struct{})
	}
	m.restarts.Add(1)
}

func (m *mockManagedProcess) restartCount() int {
	return int(m.restarts.Load())
}
