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

// SeedWorkspaceCredentials inserts credential bindings for a new workspace:
//  1. Admin auto-apply rules (target_type='all', 'user', or 'org' matching workspace owner).
//     org rules use userID as a placeholder until org membership is implemented.
//  2. All user-owned credentials belonging to userID.
//
// Idempotent — uses ON CONFLICT DO NOTHING throughout.
func (s *PgSecretStore) SeedWorkspaceCredentials(ctx context.Context, workspaceID, userID string) error {
	// Bind admin auto-apply rules (all target types including org — H-4 fix).
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		SELECT caa.credential_id, $1, 'auto', caa.within_priority
		FROM credential_auto_apply caa
		WHERE caa.target_type = 'all'
		   OR (caa.target_type = 'user' AND caa.target_id = $2)
		   OR (caa.target_type = 'org'  AND caa.target_id = $2)
		ON CONFLICT (credential_id, workspace_id) DO NOTHING
	`, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("seed workspace credentials (admin rules): %w", err)
	}

	// Bind all personal credentials owned by this user.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		SELECT pc.id, $1, 'auto', 10
		FROM provider_credentials pc
		WHERE pc.owner_type = 'user' AND pc.owner_id = $2
		ON CONFLICT (credential_id, workspace_id) DO NOTHING
	`, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("seed workspace credentials (user creds): %w", err)
	}

	return nil
}

// BindCredentialToAllUserWorkspaces binds a user credential to every workspace
// owned by userID. Called when a user creates a new personal credential so that
// the invariant "all credentials bound to all workspaces" is maintained. Idempotent.
func (s *PgSecretStore) BindCredentialToAllUserWorkspaces(ctx context.Context, credentialID, userID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		SELECT $1, w.id, 'auto', 10
		FROM workspaces w
		WHERE w.user_id = $2
		ON CONFLICT (credential_id, workspace_id) DO NOTHING
	`, credentialID, userID)
	if err != nil {
		return fmt.Errorf("bind credential to all user workspaces: %w", err)
	}
	return nil
}

// BackfillFreeTierBindings inserts workspace_credential_bindings for all
// existing workspaces that lack the free-tier opencode credential binding.
// Idempotent — uses ON CONFLICT DO NOTHING. Returns the number of rows inserted.
func (s *PgSecretStore) BackfillFreeTierBindings(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		SELECT pc.id, w.id, 'auto', 0
		FROM provider_credentials pc
		CROSS JOIN workspaces w
		WHERE pc.owner_type = 'admin' AND pc.owner_id = '_platform' AND pc.provider = 'opencode'
		  AND NOT EXISTS (
		    SELECT 1 FROM workspace_credential_bindings wcb
		    WHERE wcb.credential_id = pc.id AND wcb.workspace_id = w.id
		  )
		ON CONFLICT (credential_id, workspace_id) DO NOTHING
	`)
	if err != nil {
		return 0, fmt.Errorf("backfill free-tier bindings: %w", err)
	}
	return tag.RowsAffected(), nil
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
// Filters on both owner_type AND owner_id (L-4 fix: safer against future multi-admin).
func (s *PgSecretStore) GetAdminCredential(ctx context.Context, id string) (*AdminCredentialRow, error) {
	var r AdminCredentialRow
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, provider, ciphertext, key_version, model_allowlist, created_at, updated_at
		FROM provider_credentials WHERE id = $1 AND owner_type = 'admin' AND owner_id = '_platform'
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
// Omits updated_at from the SET clause so the DB trigger sets it to now(),
// then reads it back via RETURNING so the response timestamp is accurate (M-8 fix).
func (s *PgSecretStore) UpdateAdminCredential(ctx context.Context, row *AdminCredentialRow) error {
	return s.pool.QueryRow(ctx, `
		UPDATE provider_credentials
		SET name = $2, provider = $3, ciphertext = $4, key_version = $5, model_allowlist = $6
		WHERE id = $1 AND owner_type = 'admin'
		RETURNING updated_at
	`, row.ID, row.Name, row.Provider, row.Ciphertext, row.KeyVersion, row.ModelAllowlist).Scan(&row.UpdatedAt)
}

// DeleteAdminCredential deletes an admin credential by ID. FK cascades handle bindings.
// Returns pgx.ErrNoRows if no row was deleted so callers can distinguish 404 (L-1 fix).
func (s *PgSecretStore) DeleteAdminCredential(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM provider_credentials WHERE id = $1 AND owner_type = 'admin' AND owner_id = '_platform'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// CreateAutoApply inserts an auto-apply rule.
func (s *PgSecretStore) CreateAutoApply(ctx context.Context, credentialID, targetType string, targetID *string, priority int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO credential_auto_apply (credential_id, target_type, target_id, within_priority)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING
	`, credentialID, targetType, targetID, priority)
	return err
}

// DeleteAutoApply removes an auto-apply rule.
func (s *PgSecretStore) DeleteAutoApply(ctx context.Context, credentialID, targetType string, targetID *string) error {
	if targetID == nil {
		_, err := s.pool.Exec(ctx, `
			DELETE FROM credential_auto_apply
			WHERE credential_id = $1 AND target_type = $2 AND target_id IS NULL
		`, credentialID, targetType)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM credential_auto_apply
		WHERE credential_id = $1 AND target_type = $2 AND target_id = $3
	`, credentialID, targetType, *targetID)
	return err
}

// AutoApplyRule is a row from credential_auto_apply (exported for handler use).
type AutoApplyRule struct {
	CredentialID string
	TargetType   string
	TargetID     *string
	Priority     int
}

// ListAutoApply returns all auto-apply rules for a credential.
func (s *PgSecretStore) ListAutoApply(ctx context.Context, credentialID string) ([]AutoApplyRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT credential_id, target_type, target_id, within_priority
		FROM credential_auto_apply WHERE credential_id = $1
	`, credentialID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AutoApplyRule
	for rows.Next() {
		var r AutoApplyRule
		if err := rows.Scan(&r.CredentialID, &r.TargetType, &r.TargetID, &r.Priority); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UserCredentialRow is the DB row shape for user-owned provider credentials.
type UserCredentialRow struct {
	ID             string
	OwnerID        string
	Name           string
	Provider       string
	Ciphertext     []byte
	KeyVersion     int
	ModelAllowlist []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateUserCredential inserts a user-owned provider credential.
func (s *PgSecretStore) CreateUserCredential(ctx context.Context, row *UserCredentialRow) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO provider_credentials (id, owner_type, owner_id, name, provider, ciphertext, key_version, model_allowlist, created_at, updated_at)
		VALUES ($1, 'user', $2, $3, $4, $5, $6, $7, $8, $9)
	`, row.ID, row.OwnerID, row.Name, row.Provider, row.Ciphertext, row.KeyVersion, row.ModelAllowlist, row.CreatedAt, row.UpdatedAt)
	return err
}

// ListUserCredentials returns all credentials owned by a user.
func (s *PgSecretStore) ListUserCredentials(ctx context.Context, userID string) ([]*UserCredentialRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, name, provider, ciphertext, key_version, model_allowlist, created_at, updated_at
		FROM provider_credentials WHERE owner_type = 'user' AND owner_id = $1
		ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UserCredentialRow
	for rows.Next() {
		var r UserCredentialRow
		if err := rows.Scan(&r.ID, &r.OwnerID, &r.Name, &r.Provider, &r.Ciphertext, &r.KeyVersion, &r.ModelAllowlist, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// GetUserCredential returns a single user credential by ID, or nil if not found/not owned.
func (s *PgSecretStore) GetUserCredential(ctx context.Context, userID, id string) (*UserCredentialRow, error) {
	var r UserCredentialRow
	err := s.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, provider, ciphertext, key_version, model_allowlist, created_at, updated_at
		FROM provider_credentials WHERE id = $1 AND owner_type = 'user' AND owner_id = $2
	`, id, userID).Scan(&r.ID, &r.OwnerID, &r.Name, &r.Provider, &r.Ciphertext, &r.KeyVersion, &r.ModelAllowlist, &r.CreatedAt, &r.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteUserCredential deletes a user credential by ID.
func (s *PgSecretStore) DeleteUserCredential(ctx context.Context, userID, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM provider_credentials WHERE id = $1 AND owner_type = 'user' AND owner_id = $2`, id, userID)
	return err
}

// BindCredentialToWorkspace explicitly binds a credential to a workspace.
func (s *PgSecretStore) BindCredentialToWorkspace(ctx context.Context, credentialID, workspaceID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		VALUES ($1, $2, 'explicit', 0)
		ON CONFLICT (credential_id, workspace_id) DO UPDATE SET source_type = 'explicit'
	`, credentialID, workspaceID)
	return err
}

// UnbindCredentialFromWorkspace removes an EXPLICIT credential binding.
// Returns ErrAutoBindingProtected if the binding is auto-managed (H-1 fix).
func (s *PgSecretStore) UnbindCredentialFromWorkspace(ctx context.Context, credentialID, workspaceID string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM workspace_credential_bindings
		WHERE credential_id = $1 AND workspace_id = $2 AND source_type = 'explicit'
	`, credentialID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Distinguish "already gone" (idempotent OK) from "auto-binding" (protected).
		var sourceType string
		scanErr := s.pool.QueryRow(ctx, `
			SELECT source_type FROM workspace_credential_bindings
			WHERE credential_id = $1 AND workspace_id = $2
		`, credentialID, workspaceID).Scan(&sourceType)
		if scanErr == pgx.ErrNoRows {
			return nil // Already gone — idempotent.
		}
		if scanErr == nil && sourceType == "auto" {
			return ErrAutoBindingProtected
		}
	}
	return nil
}

// GetCredentialBindings returns workspace IDs the credential is bound to,
// scoped to workspaces owned by ownerID.
func (s *PgSecretStore) GetCredentialBindings(ctx context.Context, credentialID, ownerID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT wcb.workspace_id
		FROM workspace_credential_bindings wcb
		JOIN workspaces w ON w.id = wcb.workspace_id
		WHERE wcb.credential_id = $1
		  AND w.user_id = $2
		ORDER BY wcb.workspace_id
	`, credentialID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, rows.Err()
}

// GetCredentialBindingsWithSource returns workspace IDs and source type for bindings,
// scoped to workspaces owned by ownerID (M-1 fix: allows UI to distinguish auto vs explicit).
func (s *PgSecretStore) GetCredentialBindingsWithSource(ctx context.Context, credentialID, ownerID string) ([]CredentialBindingInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT wcb.workspace_id, wcb.source_type
		FROM workspace_credential_bindings wcb
		JOIN workspaces w ON w.id = wcb.workspace_id
		WHERE wcb.credential_id = $1
		  AND w.user_id = $2
		ORDER BY wcb.workspace_id
	`, credentialID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialBindingInfo
	for rows.Next() {
		var b CredentialBindingInfo
		if err := rows.Scan(&b.WorkspaceID, &b.SourceType); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if out == nil {
		out = []CredentialBindingInfo{}
	}
	return out, rows.Err()
}
