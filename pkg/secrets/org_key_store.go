// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OrgKeyMemberRecord is one row in org_key_members.
type OrgKeyMemberRecord struct {
	OrgID      string
	UserID     string
	WrappedDEK []byte
	KeyVersion int
}

// OrgKeyStore abstracts DB operations for org key material.
type OrgKeyStore interface {
	// GetOrgKeyMember returns the wrapped DEK for (orgID, userID). Returns nil, nil if not found.
	GetOrgKeyMember(ctx context.Context, orgID, userID string) (*OrgKeyMemberRecord, error)
	// UpsertOrgKeyMember inserts or updates a wrapped DEK row for (orgID, userID).
	// The org_memberships row for (orgID, userID) must already exist (composite FK).
	UpsertOrgKeyMember(ctx context.Context, record *OrgKeyMemberRecord) error
	// DeleteOrgKeyMember removes the wrapped DEK for (orgID, userID).
	DeleteOrgKeyMember(ctx context.Context, orgID, userID string) error
	// ListOrgKeyMembers returns all wrapped DEK rows for an org (used by RotateOrgDEK).
	ListOrgKeyMembers(ctx context.Context, orgID string) ([]*OrgKeyMemberRecord, error)
	// DeleteAllOrgKeyMembers deletes every org_key_members row for an org in one operation.
	// Used by RotateOrgDEK to atomically clear all admin key rows before inserting the
	// rotating admin's new row.
	DeleteAllOrgKeyMembers(ctx context.Context, orgID string) error
	// GetOrgKeyMembersForUser returns all org_key_members rows for a given user across
	// all orgs. Used by the login path to batch-fetch all org DEK records in one query.
	// Returns empty slice (not error) if user has no rows.
	GetOrgKeyMembersForUser(ctx context.Context, userID string) ([]*OrgKeyMemberRecord, error)
	// GetUserSalt returns the salt from user_keys for a given userID.
	// Needed to derive KEK during key handshake and rotation without requiring the caller
	// to reach into the user key service.
	GetUserSalt(ctx context.Context, userID string) ([]byte, error)
}

// PgOrgKeyStore implements OrgKeyStore using PostgreSQL.
type PgOrgKeyStore struct {
	pool *pgxpool.Pool
}

// NewPgOrgKeyStore creates a new PostgreSQL-backed org key store.
func NewPgOrgKeyStore(pool *pgxpool.Pool) *PgOrgKeyStore {
	return &PgOrgKeyStore{pool: pool}
}

func (s *PgOrgKeyStore) GetOrgKeyMember(ctx context.Context, orgID, userID string) (*OrgKeyMemberRecord, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT org_id, user_id, wrapped_dek, key_version
		 FROM org_key_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID)

	var r OrgKeyMemberRecord
	err := row.Scan(&r.OrgID, &r.UserID, &r.WrappedDEK, &r.KeyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query org_key_members: %w", err)
	}
	return &r, nil
}

func (s *PgOrgKeyStore) UpsertOrgKeyMember(ctx context.Context, record *OrgKeyMemberRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO org_key_members (org_id, user_id, wrapped_dek, key_version)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (org_id, user_id) DO UPDATE
		   SET wrapped_dek = EXCLUDED.wrapped_dek,
		       key_version = EXCLUDED.key_version,
		       updated_at  = now()`,
		record.OrgID, record.UserID, record.WrappedDEK, record.KeyVersion)
	if err != nil {
		return fmt.Errorf("upsert org_key_members: %w", err)
	}
	return nil
}

func (s *PgOrgKeyStore) DeleteOrgKeyMember(ctx context.Context, orgID, userID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM org_key_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID)
	if err != nil {
		return fmt.Errorf("delete org_key_members: %w", err)
	}
	return nil
}

func (s *PgOrgKeyStore) ListOrgKeyMembers(ctx context.Context, orgID string) ([]*OrgKeyMemberRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT org_id, user_id, wrapped_dek, key_version
		 FROM org_key_members WHERE org_id = $1`,
		orgID)
	if err != nil {
		return nil, fmt.Errorf("list org_key_members: %w", err)
	}
	defer rows.Close()

	var out []*OrgKeyMemberRecord
	for rows.Next() {
		var r OrgKeyMemberRecord
		if err := rows.Scan(&r.OrgID, &r.UserID, &r.WrappedDEK, &r.KeyVersion); err != nil {
			return nil, fmt.Errorf("scan org_key_members row: %w", err)
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *PgOrgKeyStore) DeleteAllOrgKeyMembers(ctx context.Context, orgID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM org_key_members WHERE org_id = $1`,
		orgID)
	if err != nil {
		return fmt.Errorf("delete all org_key_members: %w", err)
	}
	return nil
}

func (s *PgOrgKeyStore) GetOrgKeyMembersForUser(ctx context.Context, userID string) ([]*OrgKeyMemberRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT org_id, user_id, wrapped_dek, key_version
		 FROM org_key_members WHERE user_id = $1`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("query org_key_members for user: %w", err)
	}
	defer rows.Close()

	var out []*OrgKeyMemberRecord
	for rows.Next() {
		var r OrgKeyMemberRecord
		if err := rows.Scan(&r.OrgID, &r.UserID, &r.WrappedDEK, &r.KeyVersion); err != nil {
			return nil, fmt.Errorf("scan org_key_members row: %w", err)
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *PgOrgKeyStore) GetUserSalt(ctx context.Context, userID string) ([]byte, error) {
	var salt []byte
	err := s.pool.QueryRow(ctx,
		`SELECT salt FROM user_keys WHERE user_id = $1`,
		userID).Scan(&salt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserKeysMissing
	}
	if err != nil {
		return nil, fmt.Errorf("query user salt: %w", err)
	}
	return salt, nil
}
