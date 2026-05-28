package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
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

	r.checkAgentHealth(context.Background(), ws)

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

	r.checkAgentHealth(context.Background(), ws)

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

	r.checkAgentHealth(context.Background(), ws)

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

	r.checkAgentHealth(context.Background(), ws)

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

	r.checkAgentHealth(context.Background(), ws)

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

	r.checkAgentHealth(context.Background(), ws)

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

	r.checkAgentHealth(context.Background(), ws)

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

	r.checkAgentHealth(context.Background(), ws)

	assert.Len(t, ws.Status.Sessions, 1)
	assert.Equal(t, "ses_new", ws.Status.Sessions[0].ID)
	assert.Equal(t, "", ws.Status.Sessions[0].Title)
	assert.Equal(t, "busy", ws.Status.Sessions[0].Status)
}
