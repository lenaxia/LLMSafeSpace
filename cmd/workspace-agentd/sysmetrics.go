// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
)

var workspacePath = agentd.WorkspacePath

func getMemoryUsage() *agentd.MemoryUsage {
	// Read container memory from cgroup v2 (fallback to /proc/meminfo)
	memTotal := int64(0)
	memUsed := int64(0)

	// Try cgroup v2 first
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.current"); err == nil {
		_, _ = fmt.Sscanf(string(data), "%d", &memUsed)
	}
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		max := strings.TrimSpace(string(data))
		if max != "max" {
			_, _ = fmt.Sscanf(max, "%d", &memTotal)
		}
	}
	if memTotal == 0 {
		// Fallback to /proc/meminfo
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			var totalKB, availKB int64
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					_, _ = fmt.Sscanf(line, "MemTotal: %d kB", &totalKB)
				} else if strings.HasPrefix(line, "MemAvailable:") {
					_, _ = fmt.Sscanf(line, "MemAvailable: %d kB", &availKB)
				}
			}
			if totalKB > 0 {
				memTotal = totalKB * 1024
				if availKB > 0 {
					memUsed = (totalKB - availKB) * 1024
				}
			}
		}
	}

	if memTotal <= 0 {
		return nil
	}
	return &agentd.MemoryUsage{
		UsedBytes:  memUsed,
		TotalBytes: memTotal,
	}
}

func getDiskUsage() *agentd.DiskUsage {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(workspacePath, &stat); err != nil {
		return nil
	}
	// Statfs returns uint64 block counts; a disk large enough to overflow
	// int64 (>9 EiB) is implausible. Cast is safe in practice.
	total := int64(stat.Blocks) * int64(stat.Bsize) //nolint:gosec // G115: bounded by physical disk size
	free := int64(stat.Bfree) * int64(stat.Bsize)   //nolint:gosec // G115: same as above
	return &agentd.DiskUsage{
		UsedBytes:  total - free,
		TotalBytes: total,
	}
}

// getCPUUsage reads cumulative CPU from cgroup v2 cpu.stat.
// Covers entire pod cgroup (all processes). UsageMicros is monotonically
// increasing; callers compute delta for rate.
func getCPUUsage() *agentd.CPUUsage {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return nil
	}
	var usageMicros int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "usage_usec ") {
			_, _ = fmt.Sscanf(strings.TrimPrefix(line, "usage_usec "), "%d", &usageMicros)
			break
		}
	}
	if usageMicros == 0 {
		return nil
	}
	var limitMicrosPerSec int64
	if maxData, merr := os.ReadFile("/sys/fs/cgroup/cpu.max"); merr == nil {
		fields := strings.Fields(strings.TrimSpace(string(maxData)))
		if len(fields) == 2 && fields[0] != "max" {
			var quota, period int64
			if _, serr := fmt.Sscanf(fields[0], "%d", &quota); serr == nil {
				if _, serr = fmt.Sscanf(fields[1], "%d", &period); serr == nil && period > 0 {
					limitMicrosPerSec = quota * 1_000_000 / period
				}
			}
		}
	}
	return &agentd.CPUUsage{
		UsageMicros:       usageMicros,
		LimitMicrosPerSec: limitMicrosPerSec,
	}
}
