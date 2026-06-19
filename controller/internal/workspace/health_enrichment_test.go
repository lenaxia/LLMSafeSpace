// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// === Sessions populated from statusz ===

func TestCheckAgentHealth_PopulatesSessions(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0", UptimeSeconds: 100,
		Sessions: []agentd.SessionInfo{
			{ID: "ses_1", Title: "Debug auth", Status: "idle"},
			{ID: "ses_2", Title: "Refactor proxy", Status: "busy"},
		},
		SessionsActive: 1,
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Len(t, ws.Status.Sessions, 2)
	assert.Equal(t, "ses_1", ws.Status.Sessions[0].ID)
	assert.Equal(t, "Debug auth", ws.Status.Sessions[0].Title)
	assert.Equal(t, "idle", ws.Status.Sessions[0].Status)
	assert.Equal(t, "ses_2", ws.Status.Sessions[1].ID)
	assert.Equal(t, "Refactor proxy", ws.Status.Sessions[1].Title)
	assert.Equal(t, "busy", ws.Status.Sessions[1].Status)
}

func TestCheckAgentHealth_EmptySessions_ClearsStatus(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions: []agentd.SessionInfo{},
	})
	defer server.Close()

	// Pre-populate sessions to verify they get cleared
	ws.Status.Sessions = []v1.AgentSessionStatus{{ID: "old", Title: "stale", Status: "idle"}}

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Nil(t, ws.Status.Sessions)
}

func TestCheckAgentHealth_NilSessions_ClearsStatus(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions: nil,
	})
	defer server.Close()

	ws.Status.Sessions = []v1.AgentSessionStatus{{ID: "old"}}

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Nil(t, ws.Status.Sessions)
}

// === Disk usage populated from statusz ===

func TestCheckAgentHealth_PopulatesDiskUsage(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Disk: &agentd.DiskUsage{UsedBytes: 500_000_000, TotalBytes: 10_000_000_000},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Equal(t, int64(500_000_000), ws.Status.DiskUsedBytes)
	assert.Equal(t, int64(10_000_000_000), ws.Status.DiskTotalBytes)
}

func TestCheckAgentHealth_NilDisk_ZeroesFields(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Disk: nil,
	})
	defer server.Close()

	// Pre-set to verify they don't get cleared (nil disk = no update)
	ws.Status.DiskUsedBytes = 123
	ws.Status.DiskTotalBytes = 456

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	// Nil disk means agentd couldn't read it — preserve previous values
	assert.Equal(t, int64(123), ws.Status.DiskUsedBytes)
	assert.Equal(t, int64(456), ws.Status.DiskTotalBytes)
}

// === Unhealthy agent does NOT populate sessions/disk ===

func TestCheckAgentHealth_Unhealthy_DoesNotPopulateSessions(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: false, Ready: false, Connected: []string{},
		Sessions: []agentd.SessionInfo{{ID: "ses_1", Title: "x", Status: "idle"}},
		Disk:     &agentd.DiskUsage{UsedBytes: 100, TotalBytes: 200},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	// Unhealthy path returns before populating metadata
	assert.Nil(t, ws.Status.Sessions)
	assert.Equal(t, int64(0), ws.Status.DiskUsedBytes)
}

// === Degraded agent (no providers) does NOT populate sessions/disk ===

func TestCheckAgentHealth_Degraded_DoesNotPopulateSessions(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: false, Connected: []string{},
		ProvidersConfigured: 1,
		Sessions:            []agentd.SessionInfo{{ID: "ses_1"}},
		Disk:                &agentd.DiskUsage{UsedBytes: 100, TotalBytes: 200},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	// Degraded path returns before populating metadata
	assert.Nil(t, ws.Status.Sessions)
	assert.Equal(t, int64(0), ws.Status.DiskUsedBytes)
}

// === Sessions with empty titles (omitempty) ===

func TestCheckAgentHealth_SessionsWithoutTitles(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions: []agentd.SessionInfo{
			{ID: "ses_new", Title: "", Status: "busy"},
		},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Len(t, ws.Status.Sessions, 1)
	assert.Equal(t, "ses_new", ws.Status.Sessions[0].ID)
	assert.Equal(t, "", ws.Status.Sessions[0].Title)
	assert.Equal(t, "busy", ws.Status.Sessions[0].Status)
}

func TestCheckAgentHealth_ThreadsContextUsed(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions: []agentd.SessionInfo{
			{ID: "ses_1", Title: "main session", Status: "idle", ContextUsed: 42000},
			{ID: "ses_2", Title: "other session", Status: "busy", ContextUsed: 99000},
		},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Len(t, ws.Status.Sessions, 2)
	assert.Equal(t, int64(42000), ws.Status.Sessions[0].ContextUsed, "ses_1 ContextUsed threaded to CRD")
	assert.Equal(t, int64(99000), ws.Status.Sessions[1].ContextUsed, "ses_2 ContextUsed threaded to CRD")
}

func TestCheckAgentHealth_ZeroContextUsed_NotSet(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions: []agentd.SessionInfo{
			{ID: "ses_new", Title: "fresh", Status: "idle"}, // ContextUsed not set (zero value)
		},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Len(t, ws.Status.Sessions, 1)
	assert.Equal(t, int64(0), ws.Status.Sessions[0].ContextUsed, "zero ContextUsed preserved as-is")
}

func TestCheckAgentHealth_SetsActiveSessions(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions: []agentd.SessionInfo{
			{ID: "ses_1", Status: "busy"},
			{ID: "ses_2", Status: "idle"},
			{ID: "ses_3", Status: "busy"},
		},
		SessionsActive: 2,
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Equal(t, int32(2), ws.Status.ActiveSessions)
}

// === Suspend/Terminate clears agent-reported fields ===

func TestSuspending_ClearsSessionsAndActiveSessions(t *testing.T) {
	r, ws, _ := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions:       []agentd.SessionInfo{{ID: "ses_1", Status: "busy"}},
		SessionsActive: 1,
		Disk:           &agentd.DiskUsage{UsedBytes: 500, TotalBytes: 1000},
	})

	// Simulate active workspace with populated fields
	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)
	assert.Len(t, ws.Status.Sessions, 1)
	assert.Equal(t, int32(1), ws.Status.ActiveSessions)
	assert.Equal(t, int64(500), ws.Status.DiskUsedBytes)

	// Now suspend
	now := metav1.Now()
	ws.Status.Phase = v1.WorkspacePhaseSuspended
	ws.Status.PodName = ""
	ws.Status.PodIP = ""
	ws.Status.Endpoint = ""
	ws.Status.SuspendedAt = &now
	ws.Status.Sessions = nil
	ws.Status.ActiveSessions = 0

	// Verify cleared
	assert.Nil(t, ws.Status.Sessions)
	assert.Equal(t, int32(0), ws.Status.ActiveSessions)
	// Disk stays (PVC persists during suspend)
	assert.Equal(t, int64(500), ws.Status.DiskUsedBytes)
}

func TestTerminating_ClearsAllAgentFields(t *testing.T) {
	r, ws, _ := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Sessions:       []agentd.SessionInfo{{ID: "ses_1", Status: "idle"}},
		SessionsActive: 0,
		Disk:           &agentd.DiskUsage{UsedBytes: 999, TotalBytes: 2000},
	})

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)
	assert.Equal(t, int64(999), ws.Status.DiskUsedBytes)

	// Simulate terminate (clears everything including disk since PVC is deleted)
	ws.Status.Phase = v1.WorkspacePhaseTerminated
	ws.Status.Sessions = nil
	ws.Status.ActiveSessions = 0
	ws.Status.DiskUsedBytes = 0
	ws.Status.DiskTotalBytes = 0

	assert.Nil(t, ws.Status.Sessions)
	assert.Equal(t, int32(0), ws.Status.ActiveSessions)
	assert.Equal(t, int64(0), ws.Status.DiskUsedBytes)
	assert.Equal(t, int64(0), ws.Status.DiskTotalBytes)
}

func TestCheckAgentHealth_ThreadsContextTotal(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Context: &agentd.ContextUsage{UsedTokens: 0, TotalTokens: 200000},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Equal(t, int64(0), ws.Status.ContextUsed, "top-level ContextUsed from statusz")
	assert.Equal(t, int64(200000), ws.Status.ContextTotal, "ContextTotal threaded to CRD")
}

func TestCheckAgentHealth_ContextTotal_ZeroPreserved(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Context: &agentd.ContextUsage{UsedTokens: 0, TotalTokens: 0},
	})
	defer server.Close()

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Equal(t, int64(0), ws.Status.ContextUsed)
	assert.Equal(t, int64(0), ws.Status.ContextTotal, "zero ContextTotal preserved")
}

func TestCheckAgentHealth_NilContext_PreservesOldValues(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.0.0",
		Context: nil,
	})
	defer server.Close()

	ws.Status.ContextUsed = 12345
	ws.Status.ContextTotal = 200000

	r.enrichAgentStatus(context.Background(), ws, 60*time.Second)

	assert.Equal(t, int64(12345), ws.Status.ContextUsed, "nil Context → old ContextUsed preserved")
	assert.Equal(t, int64(200000), ws.Status.ContextTotal, "nil Context → old ContextTotal preserved")
}
