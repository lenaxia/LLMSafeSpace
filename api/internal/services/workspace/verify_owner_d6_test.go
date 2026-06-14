// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// stubOrgChecker is a minimal OrgMembershipChecker for D6 access tests.
type stubOrgChecker struct {
	members map[string]bool
	admins  map[string]bool
	err     error
}

func newStubOrgChecker() *stubOrgChecker {
	return &stubOrgChecker{members: map[string]bool{}, admins: map[string]bool{}}
}

func (s *stubOrgChecker) IsOrgMember(_ context.Context, orgID, userID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.members[orgID+":"+userID], nil
}

func (s *stubOrgChecker) IsOrgAdmin(_ context.Context, orgID, userID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.admins[orgID+":"+userID], nil
}

// TestVerifyOwner_D6_NonAdminMemberDenied verifies the D6 breaking change: a
// plain org member (not admin) cannot access an org workspace they did not
// create. Pre-D6 this returned nil (access granted via IsOrgMember).
func TestVerifyOwner_D6_NonAdminMemberDenied(t *testing.T) {
	f := newFixture(t)
	orgID := "org-1"
	wsID := "ws-1"
	creatorID := "creator"
	memberID := "member"

	f.db.On("GetWorkspace", mock.Anything, wsID).Return(&types.WorkspaceMetadata{
		ID: wsID, UserID: creatorID, OrgID: &orgID,
	}, nil)

	org := newStubOrgChecker()
	org.members[orgID+":"+memberID] = true
	org.admins[orgID+":"+memberID] = false
	f.svc.SetOrgStore(org)

	err := f.svc.verifyOwner(context.Background(), memberID, wsID)
	if !isForbidden(err) {
		t.Fatalf("D6: non-admin member must be DENIED access to another member's org workspace; got err=%v", err)
	}
}

// TestVerifyOwner_D6_OrgAdminAllowed verifies that an org admin can still
// access any org workspace (the IsOrgAdmin path).
func TestVerifyOwner_D6_OrgAdminAllowed(t *testing.T) {
	f := newFixture(t)
	orgID := "org-1"
	wsID := "ws-1"
	creatorID := "creator"
	adminID := "admin"

	f.db.On("GetWorkspace", mock.Anything, wsID).Return(&types.WorkspaceMetadata{
		ID: wsID, UserID: creatorID, OrgID: &orgID,
	}, nil)

	org := newStubOrgChecker()
	org.admins[orgID+":"+adminID] = true
	f.svc.SetOrgStore(org)

	if err := f.svc.verifyOwner(context.Background(), adminID, wsID); err != nil {
		t.Fatalf("D6: org admin must be allowed access; got err=%v", err)
	}
}

// TestVerifyOwner_D6_CreatorAlwaysAllowed verifies the workspace creator keeps
// access regardless of org role.
func TestVerifyOwner_D6_CreatorAlwaysAllowed(t *testing.T) {
	f := newFixture(t)
	orgID := "org-1"
	wsID := "ws-1"
	creatorID := "creator"

	f.db.On("GetWorkspace", mock.Anything, wsID).Return(&types.WorkspaceMetadata{
		ID: wsID, UserID: creatorID, OrgID: &orgID,
	}, nil)

	f.svc.SetOrgStore(newStubOrgChecker())

	if err := f.svc.verifyOwner(context.Background(), creatorID, wsID); err != nil {
		t.Fatalf("creator must always be allowed; got err=%v", err)
	}
}

// TestVerifyOwner_PersonalWorkspace_NonOwnerDenied verifies that a personal
// workspace (no org_id) is only accessible by its owner.
func TestVerifyOwner_PersonalWorkspace_NonOwnerDenied(t *testing.T) {
	f := newFixture(t)
	wsID := "ws-1"
	ownerID := "owner"
	otherID := "other"

	f.db.On("GetWorkspace", mock.Anything, wsID).Return(&types.WorkspaceMetadata{
		ID: wsID, UserID: ownerID, OrgID: nil,
	}, nil)

	err := f.svc.verifyOwner(context.Background(), otherID, wsID)
	if !isForbidden(err) {
		t.Fatalf("non-owner must be denied for a personal workspace; got err=%v", err)
	}
}

// isForbidden returns true if err is an APIError with the forbidden type.
func isForbidden(err error) bool {
	var apiErr *apierrors.APIError
	if asAPIError(err, &apiErr) {
		return apiErr.StatusCode() == 403
	}
	return false
}

func asAPIError(err error, target any) bool {
	if err == nil {
		return false
	}
	a, ok := target.(**apierrors.APIError)
	if !ok {
		return false
	}
	if ae, ok := err.(*apierrors.APIError); ok {
		*a = ae
		return true
	}
	return false
}

// Compile-time assertion that stubOrgChecker satisfies OrgMembershipChecker.
var _ OrgMembershipChecker = (*stubOrgChecker)(nil)
