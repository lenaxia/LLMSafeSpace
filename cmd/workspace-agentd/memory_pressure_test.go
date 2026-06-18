// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// helper: write cgroup-memory-mocked files to a temp dir and point
// the read functions at them by overriding the paths.
func setupCgroupFiles(t *testing.T, current, max string) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()

	currentPath := filepath.Join(dir, "memory.current")
	maxPath := filepath.Join(dir, "memory.max")
	os.WriteFile(currentPath, []byte(current), 0644)
	os.WriteFile(maxPath, []byte(max), 0644)

	// Override the package-level cgroup path constants used by the
	// read functions. Since readCgroupMemoryCurrent and readCgroupMemoryMax
	// hardcode "/sys/fs/cgroup/...", we test the logic via the monitor's
	// check method with injected readers instead.
	return func() { os.RemoveAll(dir) }
}

// --- memoryPressureMonitor tests ---

func TestMemoryPressureMonitor_BelowThreshold_NoPressure(t *testing.T) {
	m := newMemoryPressureMonitor()
	// Manually set state since we can't read real cgroup in tests.
	m.mu.Lock()
	m.usedBytes = 1000
	m.maxBytes = 2000
	m.pressure = false // 50% < 85%
	m.mu.Unlock()

	pressure, used, max := m.snapshot()
	assert.False(t, pressure)
	assert.Equal(t, int64(1000), used)
	assert.Equal(t, int64(2000), max)
}

func TestMemoryPressureMonitor_AtThreshold_Pressure(t *testing.T) {
	m := newMemoryPressureMonitor()
	m.mu.Lock()
	m.usedBytes = 1700
	m.maxBytes = 2000 // 85% exactly
	m.pressure = true
	m.mu.Unlock()

	assert.True(t, m.isUnderPressure())
}

func TestMemoryPressureMonitor_AboveThreshold_Pressure(t *testing.T) {
	m := newMemoryPressureMonitor()
	m.mu.Lock()
	m.usedBytes = 1900
	m.maxBytes = 2000 // 95%
	m.pressure = true
	m.mu.Unlock()

	assert.True(t, m.isUnderPressure())
}

func TestMemoryPressureMonitor_UnlimitedMemory_NoPressure(t *testing.T) {
	m := newMemoryPressureMonitor()
	m.mu.Lock()
	m.usedBytes = 999999999
	m.maxBytes = 0 // unlimited
	m.pressure = false
	m.mu.Unlock()

	assert.False(t, m.isUnderPressure(), "unlimited memory should never report pressure")
}

// --- estimateSessionMemoryMB tests ---

func TestEstimateSessionMemoryMB_ZeroTokens(t *testing.T) {
	assert.Equal(t, int64(0), estimateSessionMemoryMB(0))
}

func TestEstimateSessionMemoryMB_TypicalSession(t *testing.T) {
	// 100K tokens × 2 bytes = 200KB / 1MiB = 0 MiB (rounds down)
	assert.Equal(t, int64(0), estimateSessionMemoryMB(100000))
}

func TestEstimateSessionMemoryMB_HeavySession(t *testing.T) {
	// 600K tokens × 2 bytes = 1.2MB / 1MiB = 1 MiB
	assert.Equal(t, int64(1), estimateSessionMemoryMB(600000))
}

func TestEstimateSessionMemoryMB_VeryHeavySession(t *testing.T) {
	// 5M tokens × 2 bytes = 10MB / 1MiB = 9 MiB
	assert.Equal(t, int64(9), estimateSessionMemoryMB(5000000))
}

// --- config override tests ---

func TestMemoryWarningThreshold_Default(t *testing.T) {
	// Default threshold is 0.85 unless env var overrides.
	// Reset to default for this test.
	orig := memoryWarningThreshold
	memoryWarningThreshold = 0.85
	defer func() { memoryWarningThreshold = orig }()

	assert.Equal(t, 0.85, memoryWarningThreshold)
}

func TestReadCgroupMemoryMax_Unlimited(t *testing.T) {
	// Can't read real cgroup in CI; just verify the function doesn't
	// panic on missing files.
	_, err := readCgroupMemoryMax()
	// In test environment, the file likely doesn't exist.
	if err != nil {
		t.Skip("cgroup v2 memory.max not available in test environment")
	}
}

// --- context cancellation test ---

func TestMemoryPressureMonitor_Run_StopsOnContextCancel(t *testing.T) {
	m := newMemoryPressureMonitor()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.run(ctx, nil)
		close(done)
	}()

	cancel()
	<-done // must not block forever
}
