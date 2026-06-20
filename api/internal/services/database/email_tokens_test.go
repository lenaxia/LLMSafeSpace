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

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

func newMockTokenDB(t *testing.T) (*PgEmailTokenStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	store := NewPgEmailTokenStore(db)
	cleanup := func() {
		db.Close()
	}
	return store, mock, cleanup
}

func TestPgEmailTokenStore_Create_Success(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	tok := &types.EmailToken{
		ID:        "tok-1",
		UserID:    "user-1",
		Kind:      "password_reset",
		TokenHash: "hash123",
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	mock.ExpectExec("INSERT INTO email_tokens").
		WithArgs(tok.ID, tok.UserID, tok.Kind, tok.TokenHash, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.CreateEmailToken(context.Background(), tok)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgEmailTokenStore_Create_DBError(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	mock.ExpectExec("INSERT INTO email_tokens").
		WillReturnError(errors.New("connection refused"))

	err := store.CreateEmailToken(context.Background(), &types.EmailToken{
		ID: "tok-1", UserID: "u1", Kind: "password_reset",
		TokenHash: "h", ExpiresAt: time.Now(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create email token")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPgEmailTokenStore_GetByHash_Success(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	expires := time.Now().Add(10 * time.Minute)
	rows := sqlmock.NewRows([]string{"id", "user_id", "kind", "token_hash", "expires_at", "consumed_at"}).
		AddRow("tok-1", "user-1", "password_reset", "hash123", expires, nil)

	mock.ExpectQuery("SELECT.*FROM email_tokens WHERE token_hash").
		WithArgs("hash123").
		WillReturnRows(rows)

	tok, err := store.GetEmailTokenByHash(context.Background(), "hash123")
	require.NoError(t, err)
	require.NotNil(t, tok)
	assert.Equal(t, "tok-1", tok.ID)
	assert.Equal(t, "password_reset", tok.Kind)
	assert.Nil(t, tok.ConsumedAt, "unconsumed token must have nil ConsumedAt")
}

func TestPgEmailTokenStore_GetByHash_Consumed(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	consumed := time.Now().Add(-5 * time.Minute)
	expires := time.Now().Add(10 * time.Minute)
	rows := sqlmock.NewRows([]string{"id", "user_id", "kind", "token_hash", "expires_at", "consumed_at"}).
		AddRow("tok-2", "user-1", "password_reset", "hash456", expires, consumed)

	mock.ExpectQuery("SELECT.*FROM email_tokens WHERE token_hash").
		WithArgs("hash456").
		WillReturnRows(rows)

	tok, err := store.GetEmailTokenByHash(context.Background(), "hash456")
	require.NoError(t, err)
	require.NotNil(t, tok)
	require.NotNil(t, tok.ConsumedAt, "consumed token must have non-nil ConsumedAt")
	assert.WithinDuration(t, consumed, *tok.ConsumedAt, time.Second)
}

func TestPgEmailTokenStore_GetByHash_NotFound(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT.*FROM email_tokens WHERE token_hash").
		WithArgs("nonexistent").
		WillReturnError(sql.ErrNoRows)

	tok, err := store.GetEmailTokenByHash(context.Background(), "nonexistent")
	require.NoError(t, err, "sql.ErrNoRows must return nil, nil — not an error")
	assert.Nil(t, tok)
}

func TestPgEmailTokenStore_GetByHash_DBError(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT.*FROM email_tokens WHERE token_hash").
		WillReturnError(errors.New("connection refused"))

	tok, err := store.GetEmailTokenByHash(context.Background(), "any")
	require.Error(t, err)
	assert.Nil(t, tok)
	assert.Contains(t, err.Error(), "get email token by hash")
}

func TestPgEmailTokenStore_Consume_Success(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	mock.ExpectExec("UPDATE email_tokens SET consumed_at").
		WithArgs("tok-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.ConsumeEmailToken(context.Background(), "tok-1")
	require.NoError(t, err)
}

func TestPgEmailTokenStore_Consume_AlreadyConsumed(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	// UPDATE ... WHERE consumed_at IS NULL → 0 rows affected (already consumed)
	mock.ExpectExec("UPDATE email_tokens SET consumed_at").
		WithArgs("tok-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.ConsumeEmailToken(context.Background(), "tok-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenAlreadyConsumed)
}

func TestPgEmailTokenStore_Consume_DBError(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	mock.ExpectExec("UPDATE email_tokens SET consumed_at").
		WithArgs("tok-1").
		WillReturnError(errors.New("connection refused"))

	err := store.ConsumeEmailToken(context.Background(), "tok-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consume email token")
}

// TestPgEmailTokenStore_CRUD_RoundTrip is a serial integration-style test
// using sqlmock that exercises the full lifecycle: create → get → consume → get (consumed).
func TestPgEmailTokenStore_CRUD_RoundTrip(t *testing.T) {
	store, mock, cleanup := newMockTokenDB(t)
	defer cleanup()

	tok := &types.EmailToken{
		ID:        "round-trip-1",
		UserID:    "user-rt",
		Kind:      "email_verify",
		TokenHash: "rt-hash",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	// Create
	mock.ExpectExec("INSERT INTO email_tokens").
		WithArgs(tok.ID, tok.UserID, tok.Kind, tok.TokenHash, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.CreateEmailToken(context.Background(), tok))

	// Get (unconsumed)
	rows := sqlmock.NewRows([]string{"id", "user_id", "kind", "token_hash", "expires_at", "consumed_at"}).
		AddRow(tok.ID, tok.UserID, tok.Kind, tok.TokenHash, tok.ExpiresAt, nil)
	mock.ExpectQuery("SELECT.*FROM email_tokens WHERE token_hash").
		WithArgs("rt-hash").
		WillReturnRows(rows)
	got, err := store.GetEmailTokenByHash(context.Background(), "rt-hash")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.ConsumedAt)

	// Consume
	mock.ExpectExec("UPDATE email_tokens SET consumed_at").
		WithArgs(tok.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.ConsumeEmailToken(context.Background(), tok.ID))

	// Get again (consumed)
	consumed := time.Now()
	rows2 := sqlmock.NewRows([]string{"id", "user_id", "kind", "token_hash", "expires_at", "consumed_at"}).
		AddRow(tok.ID, tok.UserID, tok.Kind, tok.TokenHash, tok.ExpiresAt, consumed)
	mock.ExpectQuery("SELECT.*FROM email_tokens WHERE token_hash").
		WithArgs("rt-hash").
		WillReturnRows(rows2)
	got2, err := store.GetEmailTokenByHash(context.Background(), "rt-hash")
	require.NoError(t, err)
	require.NotNil(t, got2)
	require.NotNil(t, got2.ConsumedAt, "after consume, GetByHash must show ConsumedAt")

	assert.NoError(t, mock.ExpectationsWereMet())
}
