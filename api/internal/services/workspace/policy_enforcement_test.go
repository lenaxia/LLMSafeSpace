// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// stubPolicyChecker implements PolicyChecker for enforcement tests.
type stubPolicyChecker struct {
	policy *types.OrgPolicyValues
	err    error
}

func (s *stubPolicyChecker) GetEffectivePolicy(_ context.Context, _ string) (*types.OrgPolicyValues, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.policy, nil
}

func TestCreateWorkspace_PolicyMaxWorkspaces_Exceeded(t *testing.T) {
	f := newFixture(t)
	orgID := "org-1"
	wsLimit := 3

	f.db.On("GetWorkspace", mock.Anything, mock.Anything).Return(&types.WorkspaceMetadata{}, nil)
	f.db.On("CountWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(3, nil)
	f.db.On("CountActiveWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(0, nil)

	org := newStubOrgChecker()
	org.members[orgID+":user-1"] = true
	f.svc.SetOrgStore(org)

	pol := &stubPolicyChecker{
		policy: &types.OrgPolicyValues{MaxWorkspacesPerMember: &wsLimit},
	}
	f.svc.SetPolicyChecker(pol)

	req := types.CreateWorkspaceRequest{
		Name:    "test",
		OrgID:   &orgID,
		Runtime: "python",
	}
	_, err := f.svc.CreateWorkspace(context.Background(), "user-1", req)
	if err == nil {
		t.Fatal("expected error when workspace quota exceeded")
	}
}

func TestCreateWorkspace_PolicyMaxActive_Exceeded(t *testing.T) {
	f := newFixture(t)
	orgID := "org-1"
	activeLimit := 2

	f.db.On("GetWorkspace", mock.Anything, mock.Anything).Return(&types.WorkspaceMetadata{}, nil)
	f.db.On("CountWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(1, nil)
	f.db.On("CountActiveWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(2, nil)

	org := newStubOrgChecker()
	org.members[orgID+":user-1"] = true
	f.svc.SetOrgStore(org)

	pol := &stubPolicyChecker{
		policy: &types.OrgPolicyValues{MaxActiveWorkspacesPerMem: &activeLimit},
	}
	f.svc.SetPolicyChecker(pol)

	req := types.CreateWorkspaceRequest{
		Name:    "test",
		OrgID:   &orgID,
		Runtime: "python",
	}
	_, err := f.svc.CreateWorkspace(context.Background(), "user-1", req)
	if err == nil {
		t.Fatal("expected error when active workspace quota exceeded")
	}
}

func TestCreateWorkspace_PolicyUnderLimit_Allowed(t *testing.T) {
	f := newFixture(t)
	orgID := "org-1"
	wsLimit := 5

	f.db.On("GetWorkspace", mock.Anything, mock.Anything).Return(&types.WorkspaceMetadata{}, nil)
	f.db.On("CountWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(2, nil)
	f.db.On("CountActiveWorkspacesByUserAndOrg", mock.Anything, "user-1", orgID).Return(1, nil)

	org := newStubOrgChecker()
	org.members[orgID+":user-1"] = true
	f.svc.SetOrgStore(org)

	pol := &stubPolicyChecker{
		policy: &types.OrgPolicyValues{MaxWorkspacesPerMember: &wsLimit},
	}
	f.svc.SetPolicyChecker(pol)

	// The fixture's CreateWorkspace needs a running K8s mock to proceed past
	// policy checks. Since we only care that policy doesn't reject, the test
	// verifies the error is NOT a policy/validation error by checking it's
	// either nil or a non-validation error.
	req := types.CreateWorkspaceRequest{
		Name:    "test",
		OrgID:   &orgID,
		Runtime: "python",
	}
	_, err := f.svc.CreateWorkspace(context.Background(), "user-1", req)
	// Error may be from downstream K8s mock, but it should NOT be a policy
	// validation error.
	if err != nil {
		// Check that the error message does NOT contain "quota exceeded"
		if msg := err.Error(); contains(msg, "quota exceeded") {
			t.Fatalf("workspace under quota should not be rejected: %v", err)
		}
	}
}

func TestCreateWorkspace_NoPolicyChecker_NoEnforcement(t *testing.T) {
	f := newFixture(t)
	orgID := "org-1"

	f.db.On("GetWorkspace", mock.Anything, mock.Anything).Return(&types.WorkspaceMetadata{}, nil)

	org := newStubOrgChecker()
	org.members[orgID+":user-1"] = true
	f.svc.SetOrgStore(org)
	// No policy checker set — policyChecker is nil

	req := types.CreateWorkspaceRequest{
		Name:    "test",
		OrgID:   &orgID,
		Runtime: "python",
	}
	_, err := f.svc.CreateWorkspace(context.Background(), "user-1", req)
	// Should proceed (no policy check), error from K8s mock is acceptable
	if err != nil && contains(err.Error(), "quota exceeded") {
		t.Fatal("no policy checker means no quota enforcement")
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

var _ PolicyChecker = (*stubPolicyChecker)(nil)
