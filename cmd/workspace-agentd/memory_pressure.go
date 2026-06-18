// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// memoryWarningThreshold is the fraction of the cgroup memory limit at
// which agentd emits a pressure warning. 85% per user requirement
// (US-44.5). Overridable via MEMORY_WARNING_THRESHOLD env var.
var memoryWarningThreshold = 0.85

// memoryCheckInterval is how often agentd polls cgroup memory.
// 60s per US-44.5. Overridable via MEMORY_CHECK_INTERVAL_MS env var.
var memoryCheckInterval = 60 * time.Second

// memoryPressureMonitor periodically reads cgroup v2 memory usage and
// tracks whether the workspace is in a memory-pressure state (>85% of
// limit). The statusz endpoint reads the current state to surface it
// to the controller, which sets the WorkspaceConditionMemoryPressure
// condition.
type memoryPressureMonitor struct {
	mu        sync.RWMutex
	pressure  bool  // true when usage > threshold
	usedBytes int64 // last sampled usage
	maxBytes  int64 // cgroup limit (0 if unlimited/unreadable)
}

func newMemoryPressureMonitor() *memoryPressureMonitor {
	return &memoryPressureMonitor{}
}

// isUnderPressure returns the current pressure state.
func (m *memoryPressureMonitor) isUnderPressure() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pressure
}

// snapshot returns the current memory stats for statusz enrichment.
func (m *memoryPressureMonitor) snapshot() (pressure bool, usedBytes, maxBytes int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pressure, m.usedBytes, m.maxBytes
}

// check reads cgroup v2 memory and updates the pressure state.
// Returns true if the state changed (used for logging).
func (m *memoryPressureMonitor) check() bool {
	used, err := readCgroupMemoryCurrent()
	if err != nil {
		return false
	}
	max, err := readCgroupMemoryMax()
	if err != nil || max <= 0 {
		return false
	}

	ratio := float64(used) / float64(max)
	newPressure := ratio >= memoryWarningThreshold

	m.mu.Lock()
	changed := m.pressure != newPressure
	m.pressure = newPressure
	m.usedBytes = used
	m.maxBytes = max
	m.mu.Unlock()

	return changed
}

// run starts the periodic memory check loop. Blocks until ctx is done.
func (m *memoryPressureMonitor) run(ctx context.Context, logger *zap.Logger) {
	// Load config from env (allows override without rebuild).
	if v := os.Getenv("MEMORY_WARNING_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 1 {
			memoryWarningThreshold = f
		}
	}
	if v := os.Getenv("MEMORY_CHECK_INTERVAL_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 1000 {
			memoryCheckInterval = time.Duration(ms) * time.Millisecond
		}
	}

	ticker := time.NewTicker(memoryCheckInterval)
	defer ticker.Stop()

	// Do an immediate check on startup so statusz has data right away.
	m.check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.check() {
				_, used, max := m.snapshot()
				pct := float64(used) / float64(max) * 100
				logger.Warn("Memory pressure state changed",
					zap.Bool("pressure", m.isUnderPressure()),
					zap.Int64("used_bytes", used),
					zap.Int64("max_bytes", max),
					zap.Float64("percent", pct),
					zap.Float64("threshold", memoryWarningThreshold))
			}
		}
	}
}

// readCgroupMemoryMax reads the memory limit from cgroup v2.
func readCgroupMemoryMax() (int64, error) {
	data, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	if s == "max" {
		return 0, nil // unlimited
	}
	return strconv.ParseInt(s, 10, 64)
}

// estimateSessionMemoryMB computes an approximate memory estimate for a
// session based on its context token count (US-44.6).
// Formula: (contextTokens × 2 bytes) / 1MiB.
// The 2 bytes/token factor is a rough approximation of the in-memory
// representation overhead for tokenized context. This is NOT a precise
// measurement — it gives users a relative signal to identify heavy sessions.
func estimateSessionMemoryMB(contextTokens int64) int64 {
	const bytesPerToken = 2
	const bytesPerMiB = 1024 * 1024
	return (contextTokens * bytesPerToken) / bytesPerMiB
}
