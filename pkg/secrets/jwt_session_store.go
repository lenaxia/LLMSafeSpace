// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JWTSession is the in-memory shape of a jwt_sessions row.
//
// Layout matches migration 000045:
//
//	jwt_sessions(jti UUID PK, user_id TEXT FK, wrapped_dek BYTEA,
//	             kek_salt BYTEA, created_at TIMESTAMPTZ, expires_at TIMESTAMPTZ)
//
// WrappedDEK is the user's DEK encrypted under a KEK derived from
// (matched_jwt_signing_key || jti) via HKDF-SHA256 with the
// llmsafespaces-jwt-session-dek-kek info string. See Epic 56 design.
//
// JTI is the canonical UUID form that auth.go generates via
// uuid.New().String() and embeds in the JWT's "jti" claim.
type JWTSession struct {
	JTI        uuid.UUID
	UserID     string
	WrappedDEK []byte
	KEKSalt    []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// JWTSessionStore abstracts the durable jwt_sessions table for tests.
//
// All methods are best-effort writes from the caller's perspective:
// login's hot path tolerates write failure (Redis cache still works),
// the revocation paths tolerate delete failure (Redis revocation is
// authoritative), and the janitor tolerates failure (it retries on the
// next tick). The DAL itself returns the underlying error verbatim so
// callers can log it without changing the surface.
type JWTSessionStore interface {
	// GetJWTSession fetches the durable row for jti. Returns (nil, nil)
	// when no row exists (the rehydrate path uses this signal — pre-feature
	// JWTs, expired-and-pruned rows, soft-unlock backfill cases).
	GetJWTSession(ctx context.Context, jti uuid.UUID) (*JWTSession, error)
	// WriteJWTSession upserts the row. Used by login (initial write) and
	// soft-unlock (backfill / US-50.4 rewrite). ON CONFLICT (jti) DO UPDATE
	// because a soft-unlock re-issues a fresh kek_salt + wrapped_dek for an
	// existing jti.
	WriteJWTSession(ctx context.Context, session *JWTSession) error
	// DeleteJWTSession removes the row for a specific jti. Used by EvictDEK
	// (logout, cache miss handling, etc.) so the durable row does not
	// outlive its Redis counterpart.
	DeleteJWTSession(ctx context.Context, jti uuid.UUID) error
	// DeleteJWTSessionsForUser removes all rows for a user. Used by
	// RevokeAllUserSessions (password reset / explicit logout-everywhere).
	// Returns the number of rows deleted.
	DeleteJWTSessionsForUser(ctx context.Context, userID string) (int64, error)
	// DeleteExpiredJWTSessions prunes rows with expires_at < before. The
	// janitor goroutine calls this on a ticker. Returns the number of rows
	// deleted. Bounded by the idx_jwt_sessions_expires_at index for O(log N)
	// scan even at 1M rows.
	DeleteExpiredJWTSessions(ctx context.Context, before time.Time) (int64, error)
}

// PgJWTSessionStore is the production JWTSessionStore backed by Postgres.
type PgJWTSessionStore struct {
	pool *pgxpool.Pool
}

// NewPgJWTSessionStore creates a new PostgreSQL-backed JWTSessionStore.
func NewPgJWTSessionStore(pool *pgxpool.Pool) *PgJWTSessionStore {
	return &PgJWTSessionStore{pool: pool}
}

// GetJWTSession returns the durable row for jti, or (nil, nil) when none exists.
func (s *PgJWTSessionStore) GetJWTSession(ctx context.Context, jti uuid.UUID) (*JWTSession, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT jti, user_id, wrapped_dek, kek_salt, created_at, expires_at
		 FROM jwt_sessions WHERE jti = $1`, jti)

	var r JWTSession
	err := row.Scan(&r.JTI, &r.UserID, &r.WrappedDEK, &r.KEKSalt, &r.CreatedAt, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query jwt_sessions: %w", err)
	}
	return &r, nil
}

// WriteJWTSession upserts the row. The primary key is jti, so duplicate
// writes for the same jti (soft-unlock backfill, US-50.4 rewrite, or two
// near-simultaneous logins racing on the same uuid — astronomically
// unlikely but defended against) overwrite the previous wrapped_dek and
// kek_salt. user_id and created_at are preserved on conflict because
// they describe identity, not state; only the KEK material rotates.
func (s *PgJWTSessionStore) WriteJWTSession(ctx context.Context, session *JWTSession) error {
	if session == nil {
		return errors.New("write jwt_session: nil session")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO jwt_sessions (jti, user_id, wrapped_dek, kek_salt, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (jti) DO UPDATE SET
		   wrapped_dek = EXCLUDED.wrapped_dek,
		   kek_salt    = EXCLUDED.kek_salt,
		   expires_at  = EXCLUDED.expires_at`,
		session.JTI, session.UserID, session.WrappedDEK, session.KEKSalt, session.CreatedAt, session.ExpiresAt)
	if err != nil {
		return fmt.Errorf("upsert jwt_sessions: %w", err)
	}
	return nil
}

// DeleteJWTSession removes the row for jti. Idempotent: deleting a
// non-existent row is not an error (the DELETE returns rowsAffected=0).
func (s *PgJWTSessionStore) DeleteJWTSession(ctx context.Context, jti uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM jwt_sessions WHERE jti = $1`, jti); err != nil {
		return fmt.Errorf("delete jwt_sessions: %w", err)
	}
	return nil
}

// DeleteJWTSessionsForUser removes every row for userID. Used by
// RevokeAllUserSessions so a password-reset cascade leaves no durable
// DEK row behind. Returns the number of rows deleted so the caller
// can audit-log the magnitude.
func (s *PgJWTSessionStore) DeleteJWTSessionsForUser(ctx context.Context, userID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM jwt_sessions WHERE user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete jwt_sessions for user: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteExpiredJWTSessions prunes rows whose expires_at is strictly
// before the provided cutoff. The janitor passes time.Now(); tests
// pass a fixed clock for determinism. Returns the count for logging
// and monitoring (a sudden spike in deletes flags a clock skew or a
// large rotation event).
func (s *PgJWTSessionStore) DeleteExpiredJWTSessions(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM jwt_sessions WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("delete expired jwt_sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
