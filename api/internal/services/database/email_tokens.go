// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// PgEmailTokenStore implements the email-token CRUD against PostgreSQL.
type PgEmailTokenStore struct {
	db *sql.DB
}

func NewPgEmailTokenStore(db *sql.DB) *PgEmailTokenStore {
	return &PgEmailTokenStore{db: db}
}

func (s *PgEmailTokenStore) CreateEmailToken(ctx context.Context, t *types.EmailToken) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO email_tokens (id, user_id, kind, token_hash, expires_at, consumed_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, NULL, NOW())`,
		t.ID, t.UserID, t.Kind, t.TokenHash, t.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create email token: %w", err)
	}
	return nil
}

func (s *PgEmailTokenStore) GetEmailTokenByHash(ctx context.Context, hash string) (*types.EmailToken, error) {
	var t types.EmailToken
	var consumedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, kind, token_hash, expires_at, consumed_at FROM email_tokens WHERE token_hash = $1`,
		hash,
	).Scan(&t.ID, &t.UserID, &t.Kind, &t.TokenHash, &t.ExpiresAt, &consumedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get email token by hash: %w", err)
	}
	if consumedAt.Valid {
		t.ConsumedAt = &consumedAt.Time
	}
	return &t, nil
}

func (s *PgEmailTokenStore) ConsumeEmailToken(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE email_tokens SET consumed_at = NOW() WHERE id = $1 AND consumed_at IS NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("consume email token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume email token: rows check: %w", err)
	}
	if rows == 0 {
		return ErrTokenAlreadyConsumed
	}
	return nil
}

// ErrTokenAlreadyConsumed is returned when ConsumeEmailToken affects 0 rows
// (the token was consumed by a concurrent request between Get and Consume —
// TOCTOU race). The handler maps this to 410 Gone.
var ErrTokenAlreadyConsumed = fmt.Errorf("token already consumed")

// staleTime returns a time 24h in the past, for periodic cleanup of old
// consumed/expired tokens. Not yet wired (future cleanup job).
func staleTime() time.Time { return time.Now().Add(-24 * time.Hour) }
