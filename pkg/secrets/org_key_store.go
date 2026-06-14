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
	GetOrgKeyMember(ctx context.Context, orgID, userID string) (*OrgKeyMemberRecord, error)
	UpsertOrgKeyMember(ctx context.Context, record *OrgKeyMemberRecord) error
	DeleteOrgKeyMember(ctx context.Context, orgID, userID string) error
	ListOrgKeyMembers(ctx context.Context, orgID string) ([]*OrgKeyMemberRecord, error)
	DeleteAllOrgKeyMembers(ctx context.Context, orgID string) error
	GetOrgKeyMembersForUser(ctx context.Context, userID string) ([]*OrgKeyMemberRecord, error)
	GetUserSalt(ctx context.Context, userID string) ([]byte, error)
	// BeginTx starts a new pgx transaction. Used by RotateOrgDEK to wrap
	// re-encryption + key member updates atomically.
	BeginTx(ctx context.Context) (pgx.Tx, error)
	// UpsertOrgKeyMemberTx is like UpsertOrgKeyMember but within a transaction.
	UpsertOrgKeyMemberTx(ctx context.Context, tx pgx.Tx, record *OrgKeyMemberRecord) error
	// DeleteAllOrgKeyMembersTx is like DeleteAllOrgKeyMembers but within a transaction.
	DeleteAllOrgKeyMembersTx(ctx context.Context, tx pgx.Tx, orgID string) error
	// SetPendingKeyWrapForOtherAdminsTx sets pending_key_wrap=true for all admins
	// in orgID except excludeUserID, within tx.
	SetPendingKeyWrapForOtherAdminsTx(ctx context.Context, tx pgx.Tx, orgID, excludeUserID string) error
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

func (s *PgOrgKeyStore) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return tx, nil
}

func (s *PgOrgKeyStore) UpsertOrgKeyMemberTx(ctx context.Context, tx pgx.Tx, record *OrgKeyMemberRecord) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO org_key_members (org_id, user_id, wrapped_dek, key_version)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (org_id, user_id) DO UPDATE
		   SET wrapped_dek = EXCLUDED.wrapped_dek,
		       key_version = EXCLUDED.key_version,
		       updated_at  = now()`,
		record.OrgID, record.UserID, record.WrappedDEK, record.KeyVersion)
	if err != nil {
		return fmt.Errorf("upsert org_key_members (tx): %w", err)
	}
	return nil
}

func (s *PgOrgKeyStore) DeleteAllOrgKeyMembersTx(ctx context.Context, tx pgx.Tx, orgID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM org_key_members WHERE org_id = $1`, orgID)
	if err != nil {
		return fmt.Errorf("delete all org_key_members (tx): %w", err)
	}
	return nil
}

func (s *PgOrgKeyStore) SetPendingKeyWrapForOtherAdminsTx(ctx context.Context, tx pgx.Tx, orgID, excludeUserID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE org_memberships
		SET pending_key_wrap = true
		WHERE org_id = $1 AND user_id != $2 AND role = 'admin'
	`, orgID, excludeUserID)
	if err != nil {
		return fmt.Errorf("set pending_key_wrap for other admins (tx): %w", err)
	}
	return nil
}
