// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lenaxia/llmsafespace/pkg/credentials"
	"github.com/lib/pq"
)

// Compile-time interface check.
var _ credentials.Store = (*Service)(nil)

// Why pq.Array everywhere []string crosses the SQL boundary
// --------------------------------------------------------
// `model_allowlist` is `TEXT[]` in Postgres (migration 000006). The
// pgx-stdlib driver (database/sql interface) does not natively bind
// or scan a Go `[]string` to/from a Postgres array column. A bare
// scan produces:
//
//   sql: Scan error on column index 5, name "model_allowlist":
//   unsupported Scan, storing driver.Value type string into type *[]string
//
// `pq.Array` from github.com/lib/pq is the standard adapter. It
// implements driver.Valuer for binds and sql.Scanner for scans, and
// is compatible with both lib/pq and pgx-stdlib drivers (it formats
// to/from the textual `{a,b,c}` representation that both speak).
//
// Why we don't use the native pgx interface here
// ----------------------------------------------
// The rest of api/internal/services/database/ uses `database/sql` via
// pgx-stdlib (see database.go:12). Switching to the native pgx
// interface for one type would split the codebase and require a much
// bigger refactor. pq.Array is drop-in.

func (s *Service) CreateCredentialSet(ctx context.Context, name string, encrypted []byte, keyVersion int, modelAllowlist []string, assignedTo json.RawMessage, isDefault bool) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO credential_sets (name, providers_encrypted, key_version, model_allowlist, assigned_to, is_default)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		name, encrypted, keyVersion, pq.Array(modelAllowlist), assignedTo, isDefault,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert credential_set: %w", err)
	}
	return id, nil
}

func (s *Service) GetCredentialSet(ctx context.Context, id string) (*credentials.CredentialSetRow, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, is_default, providers_encrypted, key_version, model_allowlist, assigned_to, created_at, updated_at
		 FROM credential_sets WHERE id = $1`, id,
	)
	var r credentials.CredentialSetRow
	err := row.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, pq.Array(&r.ModelAllowlist), &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get credential_set: %w", err)
	}
	return &r, nil
}

func (s *Service) ListCredentialSets(ctx context.Context) ([]*credentials.CredentialSetRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, is_default, providers_encrypted, key_version, model_allowlist, assigned_to, created_at, updated_at
		 FROM credential_sets ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("list credential_sets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*credentials.CredentialSetRow
	for rows.Next() {
		var r credentials.CredentialSetRow
		if err := rows.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, pq.Array(&r.ModelAllowlist), &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan credential_set: %w", err)
		}
		result = append(result, &r)
	}
	return result, rows.Err()
}

func (s *Service) UpdateCredentialSet(ctx context.Context, id string, updates credentials.CredentialSetUpdates) error {
	// Build dynamic SET clause
	setClauses := []string{}
	args := []any{}
	argIdx := 1

	if updates.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *updates.Name)
		argIdx++
	}
	if updates.ProvidersEncrypted != nil {
		setClauses = append(setClauses, fmt.Sprintf("providers_encrypted = $%d", argIdx))
		args = append(args, *updates.ProvidersEncrypted)
		argIdx++
	}
	if updates.KeyVersion != nil {
		setClauses = append(setClauses, fmt.Sprintf("key_version = $%d", argIdx))
		args = append(args, *updates.KeyVersion)
		argIdx++
	}
	if updates.ModelAllowlist != nil {
		setClauses = append(setClauses, fmt.Sprintf("model_allowlist = $%d", argIdx))
		args = append(args, pq.Array(*updates.ModelAllowlist))
		argIdx++
	}
	if updates.AssignedTo != nil {
		setClauses = append(setClauses, fmt.Sprintf("assigned_to = $%d", argIdx))
		args = append(args, *updates.AssignedTo)
		argIdx++
	}
	if updates.IsDefault != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_default = $%d", argIdx))
		args = append(args, *updates.IsDefault)
		argIdx++
	}

	if len(setClauses) == 0 {
		return nil
	}

	// String-concatenation here builds the SET clause skeleton from a
	// fixed allow-list of column names; user-supplied values bind via
	// $N placeholders (see args). gosec G202 cannot prove this; the
	// concatenated text contains only literal column-name SQL, never
	// caller input.
	query := "UPDATE credential_sets SET "
	for i, clause := range setClauses {
		if i > 0 {
			query += ", "
		}
		query += clause
	}
	query += fmt.Sprintf(" WHERE id = $%d", argIdx) //nolint:gosec // G202: literal "WHERE id = $N" with placeholder bind
	args = append(args, id)

	result, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update credential_set: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("credential set %q not found", id)
	}
	return nil
}

func (s *Service) DeleteCredentialSet(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM credential_sets WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete credential_set: %w", err)
	}
	return nil
}

func (s *Service) SetDefault(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Clear existing default
	if _, err := tx.ExecContext(ctx, `UPDATE credential_sets SET is_default = false WHERE is_default = true`); err != nil {
		return fmt.Errorf("clear default: %w", err)
	}
	// Set new default
	result, err := tx.ExecContext(ctx, `UPDATE credential_sets SET is_default = true WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("set default: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("credential set %q not found", id)
	}
	return tx.Commit()
}

func (s *Service) GetDefault(ctx context.Context) (*credentials.CredentialSetRow, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, is_default, providers_encrypted, key_version, model_allowlist, assigned_to, created_at, updated_at
		 FROM credential_sets WHERE is_default = true`,
	)
	var r credentials.CredentialSetRow
	err := row.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, pq.Array(&r.ModelAllowlist), &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get default credential_set: %w", err)
	}
	return &r, nil
}

func (s *Service) ListByKeyVersionBelow(ctx context.Context, version int) ([]*credentials.CredentialSetRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, is_default, providers_encrypted, key_version, model_allowlist, assigned_to, created_at, updated_at
		 FROM credential_sets WHERE key_version < $1`, version,
	)
	if err != nil {
		return nil, fmt.Errorf("list credential_sets by key version: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*credentials.CredentialSetRow
	for rows.Next() {
		var r credentials.CredentialSetRow
		if err := rows.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, pq.Array(&r.ModelAllowlist), &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan credential_set: %w", err)
		}
		result = append(result, &r)
	}
	return result, rows.Err()
}

func (s *Service) UpdateEncrypted(ctx context.Context, id string, encrypted []byte, keyVersion int) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE credential_sets SET providers_encrypted = $1, key_version = $2 WHERE id = $3`,
		encrypted, keyVersion, id,
	)
	if err != nil {
		return fmt.Errorf("update encrypted: %w", err)
	}
	return nil
}
