// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

package database

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/api/internal/testharness"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// These tests exercise PgEmailTokenStore against real PostgreSQL via the shared
// integration-test harness. The *sql.DB is provided by h.SQLDB() (the store is
// built on database/sql); the connection is closed by the harness.

// TestIntegration_EmailToken_CRUD exercises the full token lifecycle against
// real PostgreSQL: create → get → consume → get (consumed). Catches column
// mismatches, type errors, and constraint violations that sqlmock cannot.
func TestIntegration_EmailToken_CRUD(t *testing.T) {
	h := testharness.New(t)
	db := h.SQLDB()
	store := NewPgEmailTokenStore(db)
	ctx := h.NewContext()

	// Create a unique token for this test run
	tokenID := fmt.Sprintf("integ-crud-%d", time.Now().UnixNano())
	userID := fmt.Sprintf("integ-user-%d", time.Now().UnixNano())

	// Insert a user first (email_tokens has FK to users)
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, active, role, status, email_verified)
		 VALUES ($1, $2, $3, 'hash', true, 'user', 'active', false)
		 ON CONFLICT DO NOTHING`,
		userID, "integuser", userID+"@test.com")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	tok := &types.EmailToken{
		ID:        tokenID,
		UserID:    userID,
		Kind:      "password_reset",
		TokenHash: "integ-hash-" + tokenID,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}

	// Create
	err = store.CreateEmailToken(ctx, tok)
	require.NoError(t, err)

	// Get (unconsumed)
	got, err := store.GetEmailTokenByHash(ctx, tok.TokenHash)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, tokenID, got.ID)
	assert.Equal(t, "password_reset", got.Kind)
	assert.Nil(t, got.ConsumedAt, "fresh token must have nil ConsumedAt")

	// Consume
	err = store.ConsumeEmailToken(ctx, tokenID)
	require.NoError(t, err)

	// Get (consumed)
	got2, err := store.GetEmailTokenByHash(ctx, tok.TokenHash)
	require.NoError(t, err)
	require.NotNil(t, got2)
	require.NotNil(t, got2.ConsumedAt, "consumed token must have non-nil ConsumedAt")

	// Consume again (TOCTOU) — must return ErrTokenAlreadyConsumed
	err = store.ConsumeEmailToken(ctx, tokenID)
	require.ErrorIs(t, err, ErrTokenAlreadyConsumed, "double-consume must return sentinel")
}

// TestIntegration_EmailToken_KindConstraint verifies the CHECK constraint on
// the kind column rejects invalid values at the DB level.
func TestIntegration_EmailToken_KindConstraint(t *testing.T) {
	h := testharness.New(t)
	db := h.SQLDB()
	store := NewPgEmailTokenStore(db)
	ctx := h.NewContext()

	userID := fmt.Sprintf("integ-kind-%d", time.Now().UnixNano())
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, active, role, status, email_verified)
		 VALUES ($1, $2, $3, 'hash', true, 'user', 'active', false)
		 ON CONFLICT DO NOTHING`,
		userID, "kinduser", userID+"@test.com")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID) })

	// Invalid kind must be rejected by the CHECK constraint
	err = store.CreateEmailToken(ctx, &types.EmailToken{
		ID: "kind-test", UserID: userID, Kind: "invalid_kind",
		TokenHash: "kind-hash", ExpiresAt: time.Now().Add(1 * time.Minute),
	})
	require.Error(t, err, "CHECK constraint must reject invalid kind")
	assert.Contains(t, err.Error(), "create email token")
}

// TestIntegration_EmailToken_NotFound verifies GetEmailTokenByHash returns
// nil, nil for a nonexistent hash (not an error).
func TestIntegration_EmailToken_NotFound(t *testing.T) {
	h := testharness.New(t)
	store := NewPgEmailTokenStore(h.SQLDB())

	got, err := store.GetEmailTokenByHash(h.NewContext(), "nonexistent-hash-xyz")
	require.NoError(t, err, "not-found must return nil, nil — not an error")
	assert.Nil(t, got)
}

// TestIntegration_EmailToken_EmailVerify_Kind verifies the email_verify kind
// works through the same store (same table, same lifecycle).
func TestIntegration_EmailToken_EmailVerify_Kind(t *testing.T) {
	h := testharness.New(t)
	db := h.SQLDB()
	store := NewPgEmailTokenStore(db)
	ctx := h.NewContext()

	userID := fmt.Sprintf("integ-verify-%d", time.Now().UnixNano())
	tokenID := fmt.Sprintf("integ-verify-tok-%d", time.Now().UnixNano())
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, active, role, status, email_verified)
		 VALUES ($1, $2, $3, 'hash', true, 'user', 'active', false)
		 ON CONFLICT DO NOTHING`,
		userID, "verifyuser", userID+"@test.com")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID) })

	err = store.CreateEmailToken(ctx, &types.EmailToken{
		ID: tokenID, UserID: userID, Kind: "email_verify",
		TokenHash: "verify-hash-" + tokenID, ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	require.NoError(t, err)

	got, err := store.GetEmailTokenByHash(ctx, "verify-hash-"+tokenID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "email_verify", got.Kind)

	// Consume
	err = store.ConsumeEmailToken(ctx, tokenID)
	require.NoError(t, err)
}

// TestIntegration_EmailVerifiedColumn verifies the email_verified column
// exists on the users table and can be read/written. This catches the
// scenario where migration 000040 was applied but the column name or type
// doesn't match what the Go code expects.
func TestIntegration_EmailVerifiedColumn(t *testing.T) {
	h := testharness.New(t)
	db := h.SQLDB()
	ctx := h.NewContext()
	userID := fmt.Sprintf("integ-col-%d", time.Now().UnixNano())

	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, active, role, status, email_verified)
		 VALUES ($1, $2, $3, 'hash', true, 'user', 'active', false)`,
		userID, "coluser", userID+"@test.com")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID) })

	// Read back — email_verified must be false
	var verified bool
	err = db.QueryRowContext(ctx, "SELECT email_verified FROM users WHERE id = $1", userID).Scan(&verified)
	require.NoError(t, err)
	assert.False(t, verified, "newly inserted user must have email_verified=false")

	// Update to true
	verified = true
	_, err = db.ExecContext(ctx, "UPDATE users SET email_verified = $1 WHERE id = $2", verified, userID)
	require.NoError(t, err)

	// Read back — must be true
	err = db.QueryRowContext(ctx, "SELECT email_verified FROM users WHERE id = $1", userID).Scan(&verified)
	require.NoError(t, err)
	assert.True(t, verified, "after update, email_verified must be true")
}

// TestIntegration_EmailToken_CascadeDelete verifies the FK ON DELETE CASCADE
// works: deleting a user removes their email tokens.
func TestIntegration_EmailToken_CascadeDelete(t *testing.T) {
	h := testharness.New(t)
	db := h.SQLDB()
	store := NewPgEmailTokenStore(db)
	ctx := h.NewContext()

	userID := fmt.Sprintf("integ-cascade-%d", time.Now().UnixNano())
	tokenID := fmt.Sprintf("integ-cascade-tok-%d", time.Now().UnixNano())

	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, active, role, status, email_verified)
		 VALUES ($1, $2, $3, 'hash', true, 'user', 'active', false)`,
		userID, "cascuser", userID+"@test.com")
	require.NoError(t, err)

	err = store.CreateEmailToken(ctx, &types.EmailToken{
		ID: tokenID, UserID: userID, Kind: "password_reset",
		TokenHash: "cascade-hash", ExpiresAt: time.Now().Add(1 * time.Minute),
	})
	require.NoError(t, err)

	// Verify token exists
	got, err := store.GetEmailTokenByHash(ctx, "cascade-hash")
	require.NoError(t, err)
	require.NotNil(t, got)

	// Delete the user — token must cascade
	_, err = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
	require.NoError(t, err)

	// Token must be gone
	got2, err := store.GetEmailTokenByHash(ctx, "cascade-hash")
	require.NoError(t, err)
	assert.Nil(t, got2, "token must be cascade-deleted when user is deleted")
}
