// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// TestCreateWorkspace_OrgMember_AutoAttributed verifies D4: when a user is in
// an org and does NOT supply OrgID, the workspace is automatically attributed to
// the user's org. The user cannot create personal workspaces while in an org.
func TestCreateWorkspace_OrgMember_AutoAttributed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orgID := "org-1"

	f.ws.On("Create", mock.Anything, mock.Anything).Return(crdWorkspace("ws-1", "default", "user-1", "1Gi"), nil)

	var capturedMeta *types.WorkspaceMetadata
	f.db.On("CreateWorkspace", ctx, mock.MatchedBy(func(m *types.WorkspaceMetadata) bool {
		capturedMeta = m
		return true
	})).Return(nil)
	f.db.On("CountWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(0, nil)
	f.db.On("CountActiveWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(0, nil)

	org := newStubOrgChecker()
	org.members[orgID+":user-1"] = true
	org.userOrgID["user-1"] = orgID
	f.svc.SetOrgStore(org)

	// Request WITHOUT OrgID — should be auto-attributed.
	req := types.CreateWorkspaceRequest{
		Name:        "my workspace",
		Runtime:     "python",
		StorageSize: "1Gi",
	}
	_, err := f.svc.CreateWorkspace(ctx, "user-1", req)
	assert.NoError(t, err)

	if capturedMeta == nil {
		t.Fatal("CreateWorkspace was not called on the db mock")
	}
	if capturedMeta.OrgID == nil || *capturedMeta.OrgID != orgID {
		t.Errorf("expected OrgID=%q (auto-attributed), got %v", orgID, capturedMeta.OrgID)
	}
}

// TestCreateWorkspace_NonOrgUser_OrgIDStaysNil verifies D4: a non-org user
// creates a workspace → OrgID stays nil (personal workspace).
func TestCreateWorkspace_NonOrgUser_OrgIDStaysNil(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything, mock.Anything).Return(crdWorkspace("ws-1", "default", "user-1", "1Gi"), nil)

	var capturedMeta *types.WorkspaceMetadata
	f.db.On("CreateWorkspace", ctx, mock.MatchedBy(func(m *types.WorkspaceMetadata) bool {
		capturedMeta = m
		return true
	})).Return(nil)

	org := newStubOrgChecker()
	org.userOrgID["user-1"] = "" // not in any org
	f.svc.SetOrgStore(org)

	req := types.CreateWorkspaceRequest{
		Name:        "personal workspace",
		Runtime:     "python",
		StorageSize: "1Gi",
	}
	_, err := f.svc.CreateWorkspace(ctx, "user-1", req)
	assert.NoError(t, err)

	if capturedMeta == nil {
		t.Fatal("CreateWorkspace was not called on the db mock")
	}
	if capturedMeta.OrgID != nil {
		t.Errorf("non-org user workspace must have nil OrgID, got %v", capturedMeta.OrgID)
	}
}

// TestCreateWorkspace_GetUserOrgIDError_ProceedsAsPersonal verifies D4: when
// GetUserOrgID fails (DB error), CreateWorkspace is non-fatal — it proceeds as
// a personal workspace (OrgID nil) with a warning log. This prevents a DB
// hiccup from blocking workspace creation entirely.
func TestCreateWorkspace_GetUserOrgIDError_ProceedsAsPersonal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ws.On("Create", mock.Anything, mock.Anything).Return(crdWorkspace("ws-1", "default", "user-1", "1Gi"), nil)

	var capturedMeta *types.WorkspaceMetadata
	f.db.On("CreateWorkspace", ctx, mock.MatchedBy(func(m *types.WorkspaceMetadata) bool {
		capturedMeta = m
		return true
	})).Return(nil)

	org := newStubOrgChecker()
	org.err = errors.New("db down") // GetUserOrgID will error
	f.svc.SetOrgStore(org)

	req := types.CreateWorkspaceRequest{
		Name:        "fallback workspace",
		Runtime:     "python",
		StorageSize: "1Gi",
	}
	_, err := f.svc.CreateWorkspace(ctx, "user-1", req)
	assert.NoError(t, err, "CreateWorkspace must succeed even if GetUserOrgID errors")

	if capturedMeta == nil {
		t.Fatal("CreateWorkspace was not called on the db mock")
	}
	if capturedMeta.OrgID != nil {
		t.Errorf("on GetUserOrgID error, workspace must be personal (OrgID nil), got %v", capturedMeta.OrgID)
	}
}
