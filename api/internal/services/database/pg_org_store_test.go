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
