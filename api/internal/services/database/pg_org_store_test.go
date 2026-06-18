// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

func TestPgOrgStore_GetUserIDByEmail_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT id FROM users WHERE email = \$1`).
		WithArgs("owner@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("user-123"))

	id, err := store.GetUserIDByEmail(context.Background(), "owner@example.com")
	require.NoError(t, err)
	assert.Equal(t, "user-123", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserIDByEmail_NotFound_ReturnsEmptyNoError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT id FROM users WHERE email = \$1`).
		WithArgs("nobody@example.com").
		WillReturnError(sql.ErrNoRows)

	id, err := store.GetUserIDByEmail(context.Background(), "nobody@example.com")
	require.NoError(t, err, "not-found must return (\"\", nil), not an error")
	assert.Equal(t, "", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserIDByEmail_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT id FROM users WHERE email = \$1`).
		WithArgs("boom@example.com").
		WillReturnError(errors.New("connection refused"))

	id, err := store.GetUserIDByEmail(context.Background(), "boom@example.com")
	require.Error(t, err)
	assert.Equal(t, "", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserOrgID_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT org_id FROM org_memberships WHERE user_id = \$1`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id"}).AddRow("org-abc"))

	orgID, err := store.GetUserOrgID(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, "org-abc", orgID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserOrgID_NotFound_ReturnsEmptyNoError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT org_id FROM org_memberships WHERE user_id = \$1`).
		WithArgs("user-no-org").
		WillReturnError(sql.ErrNoRows)

	orgID, err := store.GetUserOrgID(context.Background(), "user-no-org")
	require.NoError(t, err, "not-found must return (\"\", nil), not an error")
	assert.Equal(t, "", orgID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOrgStore_GetUserOrgID_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectQuery(`SELECT org_id FROM org_memberships WHERE user_id = \$1`).
		WithArgs("user-boom").
		WillReturnError(errors.New("connection refused"))

	orgID, err := store.GetUserOrgID(context.Background(), "user-boom")
	require.Error(t, err)
	assert.Equal(t, "", orgID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSoftDeleteOrg_F6_DoesNotNullWorkspaceOrgID verifies F6: SoftDeleteOrg
// must NOT null workspaces.org_id. It only sets organizations.deleted_at. The
// workspaces keep their org_id and become frozen via IsOrgMember's deleted_at
// check.
func TestSoftDeleteOrg_F6_DoesNotNullWorkspaceOrgID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	// Only the organizations UPDATE — NO workspaces UPDATE.
	mock.ExpectExec(`UPDATE organizations SET deleted_at = NOW\(\) WHERE id = \$1`).
		WithArgs("org-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Explicitly assert NO "UPDATE workspaces" query is expected.
	// (sqlmock fails if an unexpected query is executed.)

	require.NoError(t, store.SoftDeleteOrg(context.Background(), "org-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListWorkspaces_S1_FiltersFrozenWorkspaces verifies S1: the query includes
// the membership condition that filters out frozen workspaces. The sqlmock
// regex anchors the full WHERE clause including the org_id/EXISTS condition.
func TestListWorkspaces_S1_IncludesMembershipFilter(t *testing.T) {
	// This test uses the database.Service (not PgOrgStore) since ListWorkspaces
	// lives there. We verify the SQL contains the frozen-filter condition.
	svc, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// COUNT query with the membership condition
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM workspaces w.*org_id IS NULL.*EXISTS`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	metas, _, err := svc.ListWorkspaces(context.Background(), "user-1", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, metas)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateOrgWithAdmin_MigratesOwnerPersonalWorkspaces verifies M1/D4: the
// create transaction includes the same workspace migration as AcceptInvitationTx.
func TestCreateOrgWithAdmin_MigratesOwnerPersonalWorkspaces(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO organizations`).
		WithArgs("org-1", "Acme", "acme", "admin-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "created_by", "created_at", "updated_at", "status", "plan_id", "subscription_status"}).
			AddRow("org-1", "Acme", "acme", "admin-1", time.Now(), time.Now(), "pending_activation", "free", "inactive"))
	// INSERT membership (CreateOrgWithAdmin hardcodes 'admin' as a literal)
	mock.ExpectExec(`INSERT INTO org_memberships`).
		WithArgs("org-1", "owner-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// UPDATE workspaces — the migration (M1/D4). Anchor the full WHERE clause.
	mock.ExpectExec(`UPDATE workspaces SET org_id = .* WHERE user_id = .* AND org_id IS NULL AND deleted_at IS NULL`).
		WithArgs("owner-1", "org-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	org := &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme", CreatedBy: "admin-1"}
	_, err = store.CreateOrgWithAdmin(context.Background(), org, "owner-1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAcceptInvitationTx_MigratesPersonalWorkspaces verifies D4: the accept
// transaction includes an UPDATE that migrates the user's personal workspaces
// to the org. Uses sqlmock to assert the UPDATE statement is executed inside
// the transaction.
func TestAcceptInvitationTx_MigratesPersonalWorkspaces(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := NewPgOrgStore(db)

	mock.ExpectBegin()
	// SELECT invitation FOR UPDATE — returns the org_id.
	mock.ExpectQuery(`SELECT org_id, accepted_at, declined_at`).
		WithArgs("inv-1").
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "accepted_at", "declined_at"}).
			AddRow("org-1", nil, nil))
	// INSERT membership
	mock.ExpectExec(`INSERT INTO org_memberships`).
		WithArgs("org-1", "user-1", "member").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// UPDATE workspaces — the migration (D4). Anchor the full WHERE clause so
	// a regression that drops deleted_at/org_id/user_id scoping is caught.
	mock.ExpectExec(`UPDATE workspaces SET org_id = .* WHERE user_id = .* AND org_id IS NULL AND deleted_at IS NULL`).
		WithArgs("user-1", "org-1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	// UPDATE invitation accepted
	mock.ExpectExec(`UPDATE org_invitations SET accepted_at`).
		WithArgs("inv-1", "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	member, alreadyTaken, err := store.AcceptInvitationTx(context.Background(), "inv-1", "user-1", "member")
	require.NoError(t, err)
	assert.False(t, alreadyTaken)
	require.NotNil(t, member)
	assert.Equal(t, "org-1", member.OrgID)
	assert.Equal(t, "user-1", member.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}
