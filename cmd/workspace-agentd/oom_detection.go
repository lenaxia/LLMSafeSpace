// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// OOMMarkerPath is the PVC-backed path where agentd writes a marker
// file when opencode is killed by the OOM killer. The file persists
// across pod restarts so the next boot can detect the prior OOM and
// surface it to the user. Written to /workspace (PVC subPath: workspace).
const OOMMarkerPath = "/workspace/.opencode-oom-marker"

// oomKillsCounter tracks OOM kills per workspace for ops dashboards.
// Registered once at package level (Prometheus default registry rejects
// duplicates).
var oomKillsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "workspace_oom_kills_total",
	Help: "Total number of opencode OOM kills (exit 137 / SIGKILL from OOM killer)",
}, []string{"workspace_id"})

// exitKind classifies how the opencode process exited.
type exitKind int

const (
	exitNormal exitKind = iota
	exitSigKill
	exitSigTerm
	exitCrash
)

// classifyExit determines the kind of process exit from the wait error
// and process state. Used by the supervisor to decide whether to write
// an OOM marker.
func classifyExit(waitErr error) exitKind {
	if waitErr == nil {
		return exitNormal
	}
	// Check if the process was killed by a signal.
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		ps := exitErr.Sys()
		if ws, ok := ps.(syscall.WaitStatus); ok {
			if ws.Signaled() {
				switch syscall.Signal(ws.Signal()) {
				case syscall.SIGKILL:
					return exitSigKill
				case syscall.SIGTERM:
					return exitSigTerm
				}
				return exitCrash
			}
		}
	}
	return exitCrash
}

// isOOMExit returns true if the exit kind indicates a potential OOM kill.
// SIGKILL is the signal the Linux OOM killer sends; no other common
// path produces SIGKILL for a well-behaved process.
func isOOMExit(kind exitKind) bool {
	return kind == exitSigKill
}

// writeOOMMarker writes a JSON marker file recording the OOM event.
// The marker is read on next boot to surface the OOM to the user.
func writeOOMMarker(path, memoryLimit string) error {
	marker := map[string]interface{}{
		"reason":      "oom",
		"exitCode":    137,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"memoryLimit": memoryLimit,
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("marshal OOM marker: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create marker dir %s: %w", dir, err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write OOM marker %s: %w", path, err)
	}
	return nil
}

// handleOOMExit is called from the supervisor's crash path when OOM is
// detected. It writes the marker file, increments the Prometheus counters
// (OOM kills + restarts), and logs the event.
func handleOOMExit(workspaceID, markerPath string) {
	if workspaceID == "" {
		workspaceID = "unknown"
	}
	log.Warn("opencode was killed by OOM killer (SIGKILL/exit 137)",
		zap.String("workspace_id", workspaceID))

	if err := writeOOMMarker(markerPath, getMemoryLimit()); err != nil {
		log.Error("failed to write OOM marker file", zap.Error(err),
			zap.String("path", markerPath))
	}

	oomKillsCounter.WithLabelValues(workspaceID).Inc()
	pkgOpsMetrics.RecordRestart(workspaceID, "oom")
}

// workspaceIDFromEnv reads the WORKSPACE_ID environment variable set by
// the controller's pod builder. Returns empty string if not set (tests).
func workspaceIDFromEnv() string {
	return os.Getenv("WORKSPACE_ID")
}

// getMemoryLimit reads the cgroup v2 memory limit for logging in the
// OOM marker. Returns "unknown" if the limit cannot be read.
func getMemoryLimit() string {
	data, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return "unknown"
	}
	limit := strings.TrimSpace(string(data))
	if limit == "" || limit == "max" {
		return "unlimited"
	}
	// Convert bytes to human-readable (e.g. "2Gi")
	bytes, err := strconv.ParseInt(limit, 10, 64)
	if err != nil {
		return limit
	}
	return formatBytes(bytes)
}

func formatBytes(bytes int64) string {
	const gi = 1024 * 1024 * 1024
	const mi = 1024 * 1024
	if bytes >= gi {
		return fmt.Sprintf("%dGi", bytes/gi)
	}
	if bytes >= mi {
		return fmt.Sprintf("%dMi", bytes/mi)
	}
	return fmt.Sprintf("%d", bytes)
}

// readCgroupMemoryCurrent reads the current memory usage from cgroup v2.
// Returns an error if the file is not available (non-Linux, cgroup v1).
func readCgroupMemoryCurrent() (int64, error) {
	data, err := os.ReadFile("/sys/fs/cgroup/memory.current")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}
