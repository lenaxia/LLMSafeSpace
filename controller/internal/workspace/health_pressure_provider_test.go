// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// findCondition returns a pointer to the matching condition or nil.
func findCondition(ws *v1.Workspace, t v1.WorkspaceConditionType) *v1.WorkspaceCondition {
	for i := range ws.Status.Conditions {
		if ws.Status.Conditions[i].Type == t {
			return &ws.Status.Conditions[i]
		}
	}
	return nil
}

func TestEnrichAgentStatus_DiskPressure_Above95Percent_SetsCondition(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.2.27",
		Disk: &agentd.DiskUsage{UsedBytes: 980 * 1024 * 1024, TotalBytes: 1024 * 1024 * 1024},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionDiskPressure)
	require.NotNil(t, c, "DiskPressure condition must be set when disk >95%% full")
	assert.Equal(t, "True", c.Status)
	assert.Equal(t, v1.ReasonDiskPressure, c.Reason)
	assert.Contains(t, c.Message, "% full", "message should report the percentage")
}

func TestEnrichAgentStatus_DiskPressure_At100Percent_SetsCondition(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Disk:                &agentd.DiskUsage{UsedBytes: 1024 * 1024 * 1024, TotalBytes: 1024 * 1024 * 1024},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionDiskPressure)
	require.NotNil(t, c)
	assert.Equal(t, "True", c.Status)
	assert.Equal(t, v1.ReasonDiskPressure, c.Reason)
}

func TestEnrichAgentStatus_DiskPressure_Below95Percent_ClearsCondition(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Disk:                &agentd.DiskUsage{UsedBytes: 500 * 1024 * 1024, TotalBytes: 1024 * 1024 * 1024},
	})
	defer server.Close()

	r.setCondition(ws, v1.WorkspaceConditionDiskPressure, "True", v1.ReasonDiskPressure, "pre-existing")
	require.NotNil(t, findCondition(ws, v1.WorkspaceConditionDiskPressure))

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Nil(t, findCondition(ws, v1.WorkspaceConditionDiskPressure),
		"DiskPressure must auto-clear when usage drops below 95%%")
}

func TestEnrichAgentStatus_DiskPressure_ZeroTotal_NoConditionNoPanic(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Disk:                &agentd.DiskUsage{UsedBytes: 0, TotalBytes: 0},
	})
	defer server.Close()

	assert.NotPanics(t, func() {
		r.enrichAgentStatus(context.Background(), ws, 60*time.Second)
	})
	assert.Nil(t, findCondition(ws, v1.WorkspaceConditionDiskPressure),
		"no DiskPressure when TotalBytes=0 (divide-by-zero guard)")
}

func TestEnrichAgentStatus_DiskPressure_NilDisk_NoCondition(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, Disk: nil,
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)
	assert.Nil(t, findCondition(ws, v1.WorkspaceConditionDiskPressure))
}

// TestEnrichAgentStatus_DiskPressure_PreExistingPersistsAcrossNilDisk
// documents the intent that a pre-existing DiskPressure condition is NOT
// auto-cleared when status.Disk becomes nil (we lack current data to clear
// it). The condition only auto-clears when we observe a fresh disk reading
// below 95%.
func TestEnrichAgentStatus_DiskPressure_PreExistingPersistsAcrossNilDisk(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, Disk: nil,
	})
	defer server.Close()

	// Pre-set DiskPressure=True (set in a prior cycle when disk was observed >95%).
	r.setCondition(ws, v1.WorkspaceConditionDiskPressure, "True", v1.ReasonDiskPressure, "prior")

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionDiskPressure)
	require.NotNil(t, c, "pre-existing DiskPressure must persist when current disk reading is nil")
	assert.Equal(t, "True", c.Status, "condition value must not change without fresh data")
}

func TestEnrichAgentStatus_DiskPressure_DoesNotRestartPod(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Disk:                &agentd.DiskUsage{UsedBytes: 1024 * 1024 * 1024, TotalBytes: 1024 * 1024 * 1024},
	})
	defer server.Close()

	origPhase := ws.Status.Phase
	origRestarts := ws.Status.RestartCount
	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Equal(t, origPhase, ws.Status.Phase, "degraded detection must NOT restart the pod (per US-24.17 design)")
	assert.Equal(t, origRestarts, ws.Status.RestartCount)
}

func TestEnrichAgentStatus_ProviderReady_TrueWhenConnected(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.2.27",
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionProviderReady)
	require.NotNil(t, c, "ProviderReady must be set when providers are connected")
	assert.Equal(t, "True", c.Status)
	assert.Equal(t, v1.ReasonProvidersReady, c.Reason)
}

func TestEnrichAgentStatus_ProviderReady_FalseWhenDegraded(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: false, Connected: []string{},
		ProvidersConfigured: 1,
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionProviderReady)
	require.NotNil(t, c, "ProviderReady must be set to False when no providers are connected")
	assert.Equal(t, "False", c.Status)
	assert.Equal(t, v1.ReasonProvidersNotConnected, c.Reason)
}

func TestEnrichAgentStatus_ProviderReady_MessageContainsConnectedList(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"openai", "anthropic"},
		ProvidersConfigured: 2,
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionProviderReady)
	require.NotNil(t, c)
	assert.Contains(t, c.Message, "openai")
	assert.Contains(t, c.Message, "anthropic")
}

// --- US-44.5 MemoryPressure condition tests ---

func TestEnrichAgentStatus_MemoryPressure_True_SetsCondition(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Memory:              &agentd.MemoryUsage{UsedBytes: 1800 * 1024 * 1024, TotalBytes: 2048 * 1024 * 1024},
		MemoryPressure:      true,
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionMemoryPressure)
	require.NotNil(t, c, "MemoryPressure condition must be set when agentd reports pressure")
	assert.Equal(t, "True", c.Status)
	assert.Equal(t, v1.ReasonMemoryPressure, c.Reason)
	assert.Contains(t, c.Message, "memory usage high")
}

func TestEnrichAgentStatus_MemoryPressure_False_ClearsCondition(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Memory:              &agentd.MemoryUsage{UsedBytes: 500 * 1024 * 1024, TotalBytes: 2048 * 1024 * 1024},
		MemoryPressure:      false,
	})
	defer server.Close()

	// Pre-set the condition so we can verify it clears.
	ws.Status.Conditions = append(ws.Status.Conditions, v1.WorkspaceCondition{
		Type: v1.WorkspaceConditionMemoryPressure, Status: "True",
		Reason: v1.ReasonMemoryPressure, Message: "old",
	})

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	c := findCondition(ws, v1.WorkspaceConditionMemoryPressure)
	assert.Nil(t, c, "MemoryPressure condition must be cleared when agentd reports no pressure")
}

func TestEnrichAgentStatus_MemoryPressure_DoesNotRestartPod(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Memory:              &agentd.MemoryUsage{UsedBytes: 2048 * 1024 * 1024, TotalBytes: 2048 * 1024 * 1024},
		MemoryPressure:      true,
	})
	defer server.Close()

	origPhase := ws.Status.Phase
	origRestarts := ws.Status.RestartCount
	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Equal(t, origPhase, ws.Status.Phase, "memory pressure must NOT restart the pod")
	assert.Equal(t, origRestarts, ws.Status.RestartCount)
}

// --- US-44.6 EstimatedMemoryMB pass-through test ---

func TestEnrichAgentStatus_SessionMemoryMB_PassedToCRD(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1,
		Sessions: []agentd.SessionInfo{
			{ID: "ses-1", Status: "idle", ContextUsed: 100000, EstimatedMemoryMB: 5},
			{ID: "ses-2", Status: "busy", ContextUsed: 500000, EstimatedMemoryMB: 12},
		},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	require.Len(t, ws.Status.Sessions, 2)
	assert.Equal(t, int64(5), ws.Status.Sessions[0].EstimatedMemoryMB, "session 1 estimated memory must pass through")
	assert.Equal(t, int64(12), ws.Status.Sessions[1].EstimatedMemoryMB, "session 2 estimated memory must pass through")
}
