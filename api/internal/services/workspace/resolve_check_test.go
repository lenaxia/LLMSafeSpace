// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apierrors "github.com/lenaxia/llmsafespaces/api/internal/errors"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// TestResolveWorkspace covers the pure DB-fetch half of the split verifyOwner.
// Empty / not-found → NotFound apierror; DB error → Internal apierror; success → meta.
func TestResolveWorkspace(t *testing.T) {
	t.Run("empty_id_returns_not_found", func(t *testing.T) {
		f := newFixture(t)
		// GetWorkspace returns (nil, nil) for empty id today — ResolveWorkspace
		// mirrors that as a NotFound so callers cannot use an empty id to bypass.
		f.db.On("GetWorkspace", mock.Anything, "").Return((*types.WorkspaceMetadata)(nil), nil)

		meta, err := f.svc.ResolveWorkspace(context.Background(), "")
		require.Error(t, err)
		assert.Nil(t, meta)
		ae, ok := err.(*apierrors.APIError)
		require.True(t, ok, "expected *APIError, got %T", err)
		assert.Equal(t, 404, ae.StatusCode())
	})

	t.Run("db_error_returns_internal", func(t *testing.T) {
		f := newFixture(t)
		dbErr := errors.New("connection refused")
		f.db.On("GetWorkspace", mock.Anything, "ws-1").Return((*types.WorkspaceMetadata)(nil), dbErr)

		meta, err := f.svc.ResolveWorkspace(context.Background(), "ws-1")
		require.Error(t, err)
		assert.Nil(t, meta)
		ae, ok := err.(*apierrors.APIError)
		require.True(t, ok, "expected *APIError, got %T", err)
		assert.Equal(t, 500, ae.StatusCode())
	})

	t.Run("nil_meta_returns_not_found", func(t *testing.T) {
		f := newFixture(t)
		f.db.On("GetWorkspace", mock.Anything, "ws-missing").Return((*types.WorkspaceMetadata)(nil), nil)

		meta, err := f.svc.ResolveWorkspace(context.Background(), "ws-missing")
		require.Error(t, err)
		assert.Nil(t, meta)
		ae, ok := err.(*apierrors.APIError)
		require.True(t, ok, "expected *APIError, got %T", err)
		assert.Equal(t, 404, ae.StatusCode())
	})

	t.Run("success_returns_meta", func(t *testing.T) {
		f := newFixture(t)
		expected := &types.WorkspaceMetadata{ID: "ws-1", UserID: "user-1"}
		f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(expected, nil)

		meta, err := f.svc.ResolveWorkspace(context.Background(), "ws-1")
		require.NoError(t, err)
		require.NotNil(t, meta)
		assert.Equal(t, "ws-1", meta.ID)
		assert.Equal(t, "user-1", meta.UserID)
	})
}

// TestCheckOwnership is a table-driven test mirroring verifyOwner's
// post-D5/D6 authorisation semantics, but operating on an already-resolved
// meta (no DB fetch).
func TestCheckOwnership(t *testing.T) {
	orgID := "org-1"

	tests := []struct {
		name     string
		userID   string
		meta     *types.WorkspaceMetadata
		org      func() *stubOrgChecker
		wantErr  bool
		wantCode int
	}{
		{
			name:   "creator_personal_workspace_allowed",
			userID: "user-1",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "user-1"},
			org:    func() *stubOrgChecker { return newStubOrgChecker() },
		},
		{
			name:   "creator_org_workspace_still_member_allowed",
			userID: "creator",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			org: func() *stubOrgChecker {
				o := newStubOrgChecker()
				o.members[orgID+":creator"] = true
				return o
			},
		},
		{
			name:   "creator_org_workspace_offboarded_forbidden",
			userID: "creator",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			org: func() *stubOrgChecker {
				o := newStubOrgChecker()
				o.members[orgID+":creator"] = false
				return o
			},
			wantErr:  true,
			wantCode: 403,
		},
		{
			name:   "non_creator_org_admin_allowed",
			userID: "admin",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			org: func() *stubOrgChecker {
				o := newStubOrgChecker()
				o.admins[orgID+":admin"] = true
				return o
			},
		},
		{
			name:   "non_creator_org_member_forbidden",
			userID: "member",
			meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID},
			org: func() *stubOrgChecker {
				o := newStubOrgChecker()
				o.members[orgID+":member"] = true
				return o
			},
			wantErr:  true,
			wantCode: 403,
		},
		{
			name:     "non_creator_personal_workspace_forbidden",
			userID:   "other",
			meta:     &types.WorkspaceMetadata{ID: "ws-1", UserID: "owner"},
			org:      func() *stubOrgChecker { return newStubOrgChecker() },
			wantErr:  true,
			wantCode: 403,
		},
		{
			name:     "nil_meta_forbidden_fail_closed",
			userID:   "user-1",
			meta:     nil,
			org:      func() *stubOrgChecker { return newStubOrgChecker() },
			wantErr:  true,
			wantCode: 403,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.svc.SetOrgStore(tc.org())

			err := f.svc.CheckOwnership(context.Background(), tc.userID, tc.meta)
			if tc.wantErr {
				require.Error(t, err)
				if tc.wantCode != 0 {
					ae, ok := err.(*apierrors.APIError)
					require.True(t, ok, "expected *APIError, got %T (%v)", err, err)
					assert.Equal(t, tc.wantCode, ae.StatusCode())
				}
			} else {
				require.NoError(t, err)
			}
		})
	}

	// Org-store failure should propagate as a wrapped error (not an *APIError),
	// matching verifyOwner's existing behavior.
	t.Run("org_membership_check_error_wrapped", func(t *testing.T) {
		f := newFixture(t)
		org := newStubOrgChecker()
		org.err = errors.New("org store down")
		f.svc.SetOrgStore(org)

		meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID}
		err := f.svc.CheckOwnership(context.Background(), "creator", meta)
		require.Error(t, err)
		_, ok := err.(*apierrors.APIError)
		assert.False(t, ok, "org-store error must NOT be an *APIError; matches verifyOwner semantics")
		assert.Contains(t, err.Error(), "org")
	})

	t.Run("org_admin_check_error_wrapped", func(t *testing.T) {
		f := newFixture(t)
		org := newStubOrgChecker()
		org.err = errors.New("org store down")
		f.svc.SetOrgStore(org)

		meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "creator", OrgID: &orgID}
		err := f.svc.CheckOwnership(context.Background(), "non-owner", meta)
		require.Error(t, err)
		_, ok := err.(*apierrors.APIError)
		assert.False(t, ok, "org-store error must NOT be an *APIError; matches verifyOwner semantics")
	})
}

// TestVerifyOwner_WrapperPreservesBehaviour is a regression guard: after the
// split, verifyOwner must still produce identical outcomes for existing callers.
func TestVerifyOwner_WrapperPreservesBehaviour(t *testing.T) {
	t.Run("creator_personal_still_allowed", func(t *testing.T) {
		f := newFixture(t)
		f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(&types.WorkspaceMetadata{
			ID: "ws-1", UserID: "user-1",
		}, nil)
		f.svc.SetOrgStore(newStubOrgChecker())
		assert.NoError(t, f.svc.verifyOwner(context.Background(), "user-1", "ws-1"))
	})

	t.Run("db_error_returns_internal", func(t *testing.T) {
		f := newFixture(t)
		f.db.On("GetWorkspace", mock.Anything, "ws-1").Return((*types.WorkspaceMetadata)(nil), errors.New("db down"))
		err := f.svc.verifyOwner(context.Background(), "user-1", "ws-1")
		require.Error(t, err)
		ae, ok := err.(*apierrors.APIError)
		require.True(t, ok)
		assert.Equal(t, 500, ae.StatusCode())
	})

	t.Run("not_found_returns_404", func(t *testing.T) {
		f := newFixture(t)
		f.db.On("GetWorkspace", mock.Anything, "ws-missing").Return((*types.WorkspaceMetadata)(nil), nil)
		err := f.svc.verifyOwner(context.Background(), "user-1", "ws-missing")
		require.Error(t, err)
		ae, ok := err.(*apierrors.APIError)
		require.True(t, ok)
		assert.Equal(t, 404, ae.StatusCode())
	})
}

// TestVerifyOwner_ShortCircuitsOnMiddlewareMeta covers design 0041 Story 2
// Deliverable 2: when WorkspaceAccessMiddleware has already resolved the
// metadata AND validated ownership for THIS workspace, verifyOwner must trust
// that decision and skip its own DB round-trip. This is what allows the
// middleware to be the single ownership gate without forcing the 11
// service-layer verifyOwner callers to drop their defense-in-depth check —
// the check stays, but becomes free on the HTTP path.
func TestVerifyOwner_ShortCircuitsOnMiddlewareMeta(t *testing.T) {
	meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "user-1"}

	t.Run("matching_meta_skips_db", func(t *testing.T) {
		f := newFixture(t)
		// No db expectation — any GetWorkspace call fails the test.
		ctx := context.WithValue(context.Background(), types.ContextKeyWorkspaceMeta, meta)

		err := f.svc.verifyOwner(ctx, "user-1", "ws-1")
		require.NoError(t, err)
		f.db.AssertNotCalled(t, "GetWorkspace", mock.Anything, mock.Anything)
	})

	t.Run("mismatched_meta_falls_through_to_full_check", func(t *testing.T) {
		f := newFixture(t)
		// Meta in context is for ws-other; verifyOwner is asked about ws-1.
		// The defensive guard must refuse to trust a meta meant for a different
		// workspace and fall through to the full ResolveWorkspace + CheckOwnership
		// path (which would otherwise let a caller forge ownership of any
		// workspace by stuffing an unrelated meta into context).
		otherMeta := &types.WorkspaceMetadata{ID: "ws-other", UserID: "user-1"}
		ctx := context.WithValue(context.Background(), types.ContextKeyWorkspaceMeta, otherMeta)

		f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(meta, nil)
		f.svc.SetOrgStore(newStubOrgChecker())

		err := f.svc.verifyOwner(ctx, "user-1", "ws-1")
		require.NoError(t, err)
		f.db.AssertCalled(t, "GetWorkspace", mock.Anything, "ws-1")
	})

	t.Run("nil_meta_in_context_falls_through", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.WithValue(context.Background(), types.ContextKeyWorkspaceMeta, (*types.WorkspaceMetadata)(nil))

		f.db.On("GetWorkspace", mock.Anything, "ws-1").Return(meta, nil)
		f.svc.SetOrgStore(newStubOrgChecker())

		err := f.svc.verifyOwner(ctx, "user-1", "ws-1")
		require.NoError(t, err)
		f.db.AssertCalled(t, "GetWorkspace", mock.Anything, "ws-1")
	})
}
