// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// --- check() tests with injected readers (core pressure detection) ---

func TestCheck_BelowThreshold_NoPressure(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 800, nil }, // 40% of 2000
		readMax:     func() (int64, error) { return 2000, nil },
	}
	m.check()
	assert.False(t, m.isUnderPressure())
}

func TestCheck_AboveThreshold_PressureDetected(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 1800, nil }, // 90% of 2000
		readMax:     func() (int64, error) { return 2000, nil },
	}
	changed := m.check()
	assert.True(t, changed, "state should change from false to true")
	assert.True(t, m.isUnderPressure())
}

func TestCheck_ExactlyAtThreshold_PressureDetected(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 1700, nil }, // 85% of 2000
		readMax:     func() (int64, error) { return 2000, nil },
	}
	m.check()
	assert.True(t, m.isUnderPressure(), "85% exactly should trigger pressure")
}

func TestCheck_DropsBelowThreshold_ClearsPressure(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 1800, nil },
		readMax:     func() (int64, error) { return 2000, nil },
	}
	m.check() // sets pressure=true
	assert.True(t, m.isUnderPressure())

	// Swap reader to simulate memory drop
	m.readCurrent = func() (int64, error) { return 1000, nil } // 50%
	changed := m.check()
	assert.True(t, changed, "state should change from true to false")
	assert.False(t, m.isUnderPressure())
}

func TestCheck_StablePressure_NoChangeReported(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 1800, nil },
		readMax:     func() (int64, error) { return 2000, nil },
	}
	m.check() // first check: false→true, changed=true
	changed := m.check()
	assert.False(t, changed, "second check at same state should report no change")
}

func TestCheck_ReadCurrentError_NoChange(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 0, errors.New("cgroup unreadable") },
		readMax:     func() (int64, error) { return 2000, nil },
	}
	changed := m.check()
	assert.False(t, changed)
	assert.False(t, m.isUnderPressure())
}

func TestCheck_ReadMaxError_NoChange(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 1000, nil },
		readMax:     func() (int64, error) { return 0, errors.New("cgroup unreadable") },
	}
	changed := m.check()
	assert.False(t, changed)
}

func TestCheck_UnlimitedMemory_NoPressure(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 999999999, nil },
		readMax:     func() (int64, error) { return 0, nil }, // "max" = unlimited
	}
	m.check()
	assert.False(t, m.isUnderPressure(), "unlimited memory should never report pressure")
}

// --- snapshot tests ---

func TestSnapshot_ReturnsLatestValues(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 1700, nil },
		readMax:     func() (int64, error) { return 2000, nil },
	}
	m.check()

	pressure, used, max := m.snapshot()
	assert.True(t, pressure)
	assert.Equal(t, int64(1700), used)
	assert.Equal(t, int64(2000), max)
}

// --- estimateSessionMemoryMB tests ---

func TestEstimateSessionMemoryMB_ZeroTokens(t *testing.T) {
	assert.Equal(t, int64(0), estimateSessionMemoryMB(0))
}

func TestEstimateSessionMemoryMB_TypicalSession(t *testing.T) {
	assert.Equal(t, int64(0), estimateSessionMemoryMB(100000))
}

func TestEstimateSessionMemoryMB_HeavySession(t *testing.T) {
	assert.Equal(t, int64(1), estimateSessionMemoryMB(600000))
}

func TestEstimateSessionMemoryMB_VeryHeavySession(t *testing.T) {
	assert.Equal(t, int64(9), estimateSessionMemoryMB(5000000))
}

// --- config tests ---

func TestMemoryWarningThreshold_Default(t *testing.T) {
	orig := memoryWarningThreshold
	memoryWarningThreshold = 0.85
	defer func() { memoryWarningThreshold = orig }()
	assert.Equal(t, 0.85, memoryWarningThreshold)
}

// --- run context cancellation ---

func TestMemoryPressureMonitor_Run_StopsOnContextCancel(t *testing.T) {
	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 0, errors.New("skip") },
		readMax:     func() (int64, error) { return 0, errors.New("skip") },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.run(ctx, nil)
		close(done)
	}()
	cancel()
	<-done
}

// ---------------------------------------------------------------------------
// H4 (worklog 371): one-shot cgroup-v2-unavailable warning.
// ---------------------------------------------------------------------------

// withObservedLog swaps the package-level log for an observer capturing at
// Warn level and returns the observer + a restore func. No test in this
// package calls t.Parallel(), so swapping the global is safe within a single
// test as long as restore runs via defer.
func withObservedLog(t *testing.T) (*observer.ObservedLogs, func()) {
	t.Helper()
	core, recorded := observer.New(zapcore.WarnLevel)
	prev := log
	log = zap.New(core)
	return recorded, func() { log = prev }
}

// TestCheck_H4_CgroupReadFailure_WarnsExactlyOnce verifies the H4 fix: when
// readCurrent fails (typical on cgroup v1 hosts), the monitor emits a Warn
// exactly once (sync.Once), not on every 60s poll tick. The second failing
// check() must NOT emit a second warning. This is the observable behavior that
// makes the silent-degradation failure mode diagnosable.
func TestCheck_H4_CgroupReadFailure_WarnsExactlyOnce(t *testing.T) {
	recorded, restore := withObservedLog(t)
	defer restore()

	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 0, errors.New("cgroup v2 memory.current: no such file") },
		readMax:     func() (int64, error) { return 2000, nil },
	}

	m.check()
	m.check() // second call must be suppressed by warnOnce

	warnings := recorded.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1,
		"exactly one Warn must be emitted across two failing check() calls (sync.Once)")
	assert.Contains(t, warnings[0].Message, "memory.current unreadable",
		"the warning must name the unreadable cgroup file so operators can diagnose")
}

// TestCheck_H4_UnlimitedMemoryMax_WarnsExactlyOnce verifies the second H4 error
// branch: readMax returning (0, nil) (unlimited memory) emits the one-shot
// warning with the "zero/unlimited" reason. This branch is distinct from the
// readCurrent-failure branch and has its own reason string.
func TestCheck_H4_UnlimitedMemoryMax_WarnsExactlyOnce(t *testing.T) {
	recorded, restore := withObservedLog(t)
	defer restore()

	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 1000, nil },
		readMax:     func() (int64, error) { return 0, nil }, // unlimited → no pressure threshold
	}

	m.check()
	m.check()

	warnings := recorded.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warnings, 1,
		"exactly one Warn for the unlimited-memory.max branch (sync.Once)")
	assert.Contains(t, warnings[0].Message, "memory.max",
		"the warning must name memory.max so operators can diagnose")
}

// TestCheck_H4_SuccessfulRead_DoesNotWarn verifies the positive case: when
// cgroup reads succeed, no warning is emitted. This guards against a regression
// that warns unconditionally.
func TestCheck_H4_SuccessfulRead_DoesNotWarn(t *testing.T) {
	recorded, restore := withObservedLog(t)
	defer restore()

	m := &memoryPressureMonitor{
		readCurrent: func() (int64, error) { return 800, nil },
		readMax:     func() (int64, error) { return 2000, nil },
	}
	m.check()

	assert.Empty(t, recorded.All(),
		"no warning when cgroup reads succeed")
}
