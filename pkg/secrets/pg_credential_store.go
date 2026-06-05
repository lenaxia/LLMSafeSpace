// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetWorkspaceCredentials returns all credential bindings for a workspace,
// ordered by: (source_type='explicit') DESC, within_priority DESC, created_at ASC.
func (s *PgSecretStore) GetWorkspaceCredentials(ctx context.Context, workspaceID string) ([]CredentialBinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pc.id, pc.owner_type, pc.owner_id, pc.provider, pc.ciphertext,
		       pc.key_version, pc.model_allowlist, wcb.source_type, wcb.within_priority
		FROM workspace_credential_bindings wcb
		JOIN provider_credentials pc ON pc.id = wcb.credential_id
		WHERE wcb.workspace_id = $1
		ORDER BY (wcb.source_type = 'explicit') DESC, wcb.within_priority DESC, wcb.created_at ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query workspace credentials: %w", err)
	}
	defer rows.Close()

	var bindings []CredentialBinding
	for rows.Next() {
		var b CredentialBinding
		if err := rows.Scan(
			&b.ID, &b.OwnerType, &b.OwnerID, &b.Provider, &b.Ciphertext,
			&b.KeyVersion, &b.ModelAllowlist, &b.SourceType, &b.WithinPriority,
		); err != nil {
			return nil, fmt.Errorf("scan credential binding: %w", err)
		}
		bindings = append(bindings, b)
	}
	return bindings, rows.Err()
}

// UpsertFreeTierCredential atomically upserts the platform free-tier
// opencode credential and its auto-apply rule in a single transaction.
func (s *PgSecretStore) UpsertFreeTierCredential(ctx context.Context, ciphertext []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var credID string
	err = tx.QueryRow(ctx, `
		INSERT INTO provider_credentials (owner_type, owner_id, name, provider, ciphertext)
		VALUES ('admin', '_platform', 'opencode-free-tier', 'opencode', $1)
		ON CONFLICT (owner_type, owner_id, provider)
		DO UPDATE SET ciphertext = EXCLUDED.ciphertext, updated_at = now()
		RETURNING id
	`, ciphertext).Scan(&credID)
	if err != nil {
		return fmt.Errorf("upsert provider_credentials: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO credential_auto_apply (credential_id, target_type, within_priority)
		VALUES ($1, 'all', 0)
		ON CONFLICT DO NOTHING
	`, credID)
	if err != nil {
		return fmt.Errorf("upsert credential_auto_apply: %w", err)
	}

	return tx.Commit(ctx)
}

// SeedWorkspaceCredentials inserts auto-apply credential bindings for a
// workspace based on matching credential_auto_apply rules.
func (s *PgSecretStore) SeedWorkspaceCredentials(ctx context.Context, workspaceID, userID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		SELECT caa.credential_id, $1, 'auto', caa.within_priority
		FROM credential_auto_apply caa
		WHERE caa.target_type = 'all'
		   OR (caa.target_type = 'user' AND caa.target_id = $2)
		ON CONFLICT (credential_id, workspace_id) DO NOTHING
	`, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("seed workspace credentials: %w", err)
	}
	return nil
}

// HasUserProviderCredential returns true if the user owns a credential for the given provider.
func (s *PgSecretStore) HasUserProviderCredential(ctx context.Context, userID, provider string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM provider_credentials
			WHERE owner_type = 'user' AND owner_id = $1 AND provider = $2
		)
	`, userID, provider).Scan(&exists)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("check user provider credential: %w", err)
	}
	return exists, nil
}

// AdminCredentialRow mirrors the handler's AdminCredentialRow for DB operations.
// Defined here to avoid an import cycle (handlers → secrets → handlers).
type AdminCredentialRow struct {
	ID             string
	Name           string
	Provider       string
	Ciphertext     []byte
	KeyVersion     int
	ModelAllowlist []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateAdminCredential inserts a new admin-owned provider credential.
func (s *PgSecretStore) CreateAdminCredential(ctx context.Context, row *AdminCredentialRow) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO provider_credentials (id, owner_type, owner_id, name, provider, ciphertext, key_version, model_allowlist, created_at, updated_at)
		VALUES ($1, 'admin', '_platform', $2, $3, $4, $5, $6, $7, $8)
	`, row.ID, row.Name, row.Provider, row.Ciphertext, row.KeyVersion, row.ModelAllowlist, row.CreatedAt, row.UpdatedAt)
	return err
}

// ListAdminCredentials returns all admin-owned credentials.
func (s *PgSecretStore) ListAdminCredentials(ctx context.Context) ([]*AdminCredentialRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, provider, ciphertext, key_version, model_allowlist, created_at, updated_at
		FROM provider_credentials WHERE owner_type = 'admin' AND owner_id = '_platform'
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*AdminCredentialRow
	for rows.Next() {
		var r AdminCredentialRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Provider, &r.Ciphertext, &r.KeyVersion, &r.ModelAllowlist, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// GetAdminCredential returns a single admin credential by ID, or nil if not found.
func (s *PgSecretStore) GetAdminCredential(ctx context.Context, id string) (*AdminCredentialRow, error) {
	var r AdminCredentialRow
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, provider, ciphertext, key_version, model_allowlist, created_at, updated_at
		FROM provider_credentials WHERE id = $1 AND owner_type = 'admin'
	`, id).Scan(&r.ID, &r.Name, &r.Provider, &r.Ciphertext, &r.KeyVersion, &r.ModelAllowlist, &r.CreatedAt, &r.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateAdminCredential updates an existing admin credential.
func (s *PgSecretStore) UpdateAdminCredential(ctx context.Context, row *AdminCredentialRow) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE provider_credentials
		SET name = $2, provider = $3, ciphertext = $4, key_version = $5, model_allowlist = $6, updated_at = $7
		WHERE id = $1 AND owner_type = 'admin'
	`, row.ID, row.Name, row.Provider, row.Ciphertext, row.KeyVersion, row.ModelAllowlist, row.UpdatedAt)
	return err
}

// DeleteAdminCredential deletes an admin credential by ID. FK cascades handle bindings.
func (s *PgSecretStore) DeleteAdminCredential(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM provider_credentials WHERE id = $1 AND owner_type = 'admin'`, id)
	return err
}
