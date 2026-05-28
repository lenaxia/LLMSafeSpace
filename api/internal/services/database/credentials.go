package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lenaxia/llmsafespace/pkg/credentials"
)

// Compile-time interface check.
var _ credentials.Store = (*Service)(nil)

func (s *Service) CreateCredentialSet(ctx context.Context, name string, encrypted []byte, keyVersion int, modelAllowlist []string, assignedTo json.RawMessage, isDefault bool) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO credential_sets (name, providers_encrypted, key_version, model_allowlist, assigned_to, is_default)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		name, encrypted, keyVersion, modelAllowlist, assignedTo, isDefault,
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
	err := row.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, &r.ModelAllowlist, &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt)
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
	defer rows.Close()

	var result []*credentials.CredentialSetRow
	for rows.Next() {
		var r credentials.CredentialSetRow
		if err := rows.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, &r.ModelAllowlist, &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt); err != nil {
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
		args = append(args, *updates.ModelAllowlist)
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

	query := "UPDATE credential_sets SET "
	for i, clause := range setClauses {
		if i > 0 {
			query += ", "
		}
		query += clause
	}
	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
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
	defer tx.Rollback()

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
	err := row.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, &r.ModelAllowlist, &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt)
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
	defer rows.Close()

	var result []*credentials.CredentialSetRow
	for rows.Next() {
		var r credentials.CredentialSetRow
		if err := rows.Scan(&r.ID, &r.Name, &r.IsDefault, &r.ProvidersEncrypted, &r.KeyVersion, &r.ModelAllowlist, &r.AssignedTo, &r.CreatedAt, &r.UpdatedAt); err != nil {
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

func (s *Service) CountWorkspacesUsingCredentialSet(ctx context.Context, credSetID string) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workspaces WHERE credential_set_id = $1`, credSetID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count workspaces using credential set: %w", err)
	}
	return count, nil
}
