// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// === GetWorkspaceStatus — sessions and disk fields ===

func TestGetWorkspaceStatus_IncludesSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Status.Sessions = []v1.AgentSessionStatus{
		{ID: "ses_a", Title: "Auth refactor", Status: "idle"},
		{ID: "ses_b", Title: "Fix proxy", Status: "busy"},
	}
	f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Len(t, result.Sessions, 2)
	assert.Equal(t, "ses_a", result.Sessions[0].ID)
	assert.Equal(t, "Auth refactor", result.Sessions[0].Title)
	assert.Equal(t, "idle", result.Sessions[0].Status)
	assert.Equal(t, "ses_b", result.Sessions[1].ID)
	assert.Equal(t, "busy", result.Sessions[1].Status)
}

func TestGetWorkspaceStatus_NoSessions_OmitsField(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Status.Sessions = nil
	f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Nil(t, result.Sessions)
}

func TestGetWorkspaceStatus_IncludesDiskUsage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Status.DiskUsedBytes = 1_073_741_824   // 1 GiB
	crd.Status.DiskTotalBytes = 10_737_418_240 // 10 GiB
	f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Equal(t, int64(1_073_741_824), result.DiskUsedBytes)
	assert.Equal(t, int64(10_737_418_240), result.DiskTotalBytes)
}

func TestGetWorkspaceStatus_ZeroDisk_OmitsField(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	// DiskUsedBytes and DiskTotalBytes default to 0
	f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Equal(t, int64(0), result.DiskUsedBytes)
	assert.Equal(t, int64(0), result.DiskTotalBytes)
}

func TestGetWorkspaceStatus_SessionsAndDiskTogether(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", ctx, "ws-1").Return(dbWorkspace("ws-1", "user1", "my-ws", "10Gi"), nil)
	crd := crdWorkspace("ws-1", "default", "user1", "10Gi")
	crd.Status.Phase = v1.WorkspacePhaseActive
	crd.Status.Sessions = []v1.AgentSessionStatus{
		{ID: "ses_1", Title: "Chat", Status: "idle"},
	}
	crd.Status.DiskUsedBytes = 500_000
	crd.Status.DiskTotalBytes = 1_000_000
	f.ws.On("Get", "ws-1", mock.Anything).Return(crd, nil)

	result, err := f.svc.GetWorkspaceStatus(ctx, "user1", "ws-1")

	assert.NoError(t, err)
	assert.Len(t, result.Sessions, 1)
	assert.Equal(t, "Chat", result.Sessions[0].Title)
	assert.Equal(t, int64(500_000), result.DiskUsedBytes)
	assert.Equal(t, int64(1_000_000), result.DiskTotalBytes)
}
