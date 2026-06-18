// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	// UPDATE workspaces — the migration (D4). Assert the statement exists.
	mock.ExpectExec(`UPDATE workspaces SET org_id`).
		WithArgs("user-1", "org-1").
		WillReturnResult(sqlmock.NewResult(0, 3)) // 3 personal workspaces migrated
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
