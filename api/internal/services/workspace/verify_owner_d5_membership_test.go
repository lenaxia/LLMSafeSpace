// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// TestVerifyOwner_D5_CreatorLeftOrg_Denied verifies D5: a workspace creator who
// has left the org (offboarded) loses access to their org-attributed workspace.
// The creator match alone is no longer sufficient for org workspaces.
func TestVerifyOwner_D5_CreatorLeftOrg_Denied(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orgID := "org-1"

	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(&types.WorkspaceMetadata{
		ID:     "ws-1",
		UserID: "user-1",
		OrgID:  &orgID,
	}, nil)

	org := newStubOrgChecker()
	// user-1 is the creator but is NO LONGER a member of org-1 (offboarded)
	org.members[orgID+":user-1"] = false
	org.admins[orgID+":user-1"] = false
	f.svc.SetOrgStore(org)

	err := f.svc.verifyOwner(ctx, "user-1", "ws-1")
	if err == nil {
		t.Fatal("expected forbidden error when creator left the org")
	}
	if !isForbidden(err) {
		t.Errorf("expected a forbidden error, got %v", err)
	}
}

// TestVerifyOwner_D5_CreatorStillMember_Allowed verifies D5: a workspace
// creator who is still a member of the org retains access.
func TestVerifyOwner_D5_CreatorStillMember_Allowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orgID := "org-1"

	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(&types.WorkspaceMetadata{
		ID:     "ws-1",
		UserID: "user-1",
		OrgID:  &orgID,
	}, nil)

	org := newStubOrgChecker()
	org.members[orgID+":user-1"] = true // still a member
	f.svc.SetOrgStore(org)

	err := f.svc.verifyOwner(ctx, "user-1", "ws-1")
	if err != nil {
		t.Errorf("creator who is still a member should have access, got %v", err)
	}
}

// TestVerifyOwner_D5_PersonalWorkspace_CreatorAlwaysAllowed verifies D5:
// personal workspaces (no org_id) are unaffected — creator always has access.
func TestVerifyOwner_D5_PersonalWorkspace_CreatorAlwaysAllowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(&types.WorkspaceMetadata{
		ID:     "ws-1",
		UserID: "user-1",
		OrgID:  nil, // personal workspace
	}, nil)

	org := newStubOrgChecker()
	f.svc.SetOrgStore(org)

	err := f.svc.verifyOwner(ctx, "user-1", "ws-1")
	if err != nil {
		t.Errorf("personal workspace creator should always have access, got %v", err)
	}
}
