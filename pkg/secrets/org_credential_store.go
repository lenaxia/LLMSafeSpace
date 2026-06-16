// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OrgCredentialMetadata is the list-view of an org credential (no ciphertext).
type OrgCredentialMetadata struct {
	ID                 string
	OrgID              string
	Name               string
	Provider           string
	ModelAllowlist     []string
	ModelContextLimits map[string]int // model_id → context window size in tokens
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// OrgCredentialRow is the full row including ciphertext (for update operations).
type OrgCredentialRow struct {
	OrgCredentialMetadata
	Ciphertext []byte
	KeyVersion int
}

// OrgCredentialStore is the DB interface for org-scoped credential operations.
type OrgCredentialStore interface {
	CreateOrgCredential(ctx context.Context, orgID, name, provider string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int) (string, error)
	ListOrgCredentials(ctx context.Context, orgID string) ([]*OrgCredentialRow, error)
	GetOrgCredential(ctx context.Context, orgID, credID string) (*OrgCredentialRow, error)
	UpdateOrgCredential(ctx context.Context, orgID, credID string, name *string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int, keyVersion int) error
	DeleteOrgCredential(ctx context.Context, orgID, credID string) error
	BindCredentialToAllOrgWorkspaces(ctx context.Context, credentialID, orgID string) error
	CreateOrgAutoApply(ctx context.Context, credentialID, orgID string, withinPriority int) error
	ListOrgAutoApply(ctx context.Context, orgID string) ([]*AutoApplyRule, error)
	DeleteOrgAutoApply(ctx context.Context, credentialID, orgID string) error
}

// PgSecretStore implements OrgCredentialStore. Methods are defined here alongside
// the other PgSecretStore credential methods.

func (s *PgSecretStore) CreateOrgCredential(ctx context.Context, orgID, name, provider string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int) (string, error) {
	if modelContextLimits == nil {
		modelContextLimits = map[string]int{}
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO provider_credentials (owner_type, owner_id, name, provider, ciphertext, key_version, model_allowlist, model_context_limits, created_at, updated_at)
		VALUES ('org', $1, $2, $3, $4, 1, COALESCE($5, '{}'::text[]), $6, now(), now())
		RETURNING id
	`, orgID, name, provider, ciphertext, modelAllowlist, modelContextLimits).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create org credential: %w", err)
	}
	return id, nil
}

func (s *PgSecretStore) ListOrgCredentials(ctx context.Context, orgID string) ([]*OrgCredentialRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, name, provider, model_allowlist, model_context_limits, created_at, updated_at, ciphertext, key_version
		FROM provider_credentials
		WHERE owner_type = 'org' AND owner_id = $1
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org credentials: %w", err)
	}
	defer rows.Close()

	var out []*OrgCredentialRow
	for rows.Next() {
		var r OrgCredentialRow
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Name, &r.Provider, &r.ModelAllowlist, &r.ModelContextLimits, &r.CreatedAt, &r.UpdatedAt, &r.Ciphertext, &r.KeyVersion); err != nil {
			return nil, fmt.Errorf("scan org credential: %w", err)
		}
		if r.ModelContextLimits == nil {
			r.ModelContextLimits = map[string]int{}
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *PgSecretStore) GetOrgCredential(ctx context.Context, orgID, credID string) (*OrgCredentialRow, error) {
	var r OrgCredentialRow
	err := s.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, provider, model_allowlist, model_context_limits, created_at, updated_at, ciphertext, key_version
		FROM provider_credentials
		WHERE owner_type = 'org' AND owner_id = $1 AND id = $2
	`, orgID, credID).Scan(
		&r.ID, &r.OrgID, &r.Name, &r.Provider, &r.ModelAllowlist, &r.ModelContextLimits,
		&r.CreatedAt, &r.UpdatedAt, &r.Ciphertext, &r.KeyVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get org credential: %w", err)
	}
	if r.ModelContextLimits == nil {
		r.ModelContextLimits = map[string]int{}
	}
	return &r, nil
}

func (s *PgSecretStore) UpdateOrgCredential(ctx context.Context, orgID, credID string, name *string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int, keyVersion int) error {
	// Do NOT normalize nil→{} here. A nil modelContextLimits means "don't change
	// this column" — COALESCE($6, model_context_limits) must receive a SQL NULL
	// to fall through to the existing value. An empty map {} is a valid "clear all
	// limits" value and must be written as-is.
	_, err := s.pool.Exec(ctx, `
		UPDATE provider_credentials
		SET name                 = COALESCE($3, name),
		    ciphertext           = CASE WHEN $4::bytea IS NOT NULL THEN $4 ELSE ciphertext END,
		    model_allowlist      = COALESCE($5, model_allowlist),
		    model_context_limits = COALESCE($6, model_context_limits),
		    key_version          = $7,
		    updated_at           = now()
		WHERE owner_type = 'org' AND owner_id = $1 AND id = $2
	`, orgID, credID, name, ciphertext, modelAllowlist, modelContextLimits, keyVersion)
	if err != nil {
		return fmt.Errorf("update org credential: %w", err)
	}
	return nil
}

func (s *PgSecretStore) DeleteOrgCredential(ctx context.Context, orgID, credID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM provider_credentials WHERE owner_type = 'org' AND owner_id = $1 AND id = $2
	`, orgID, credID)
	if err != nil {
		return fmt.Errorf("delete org credential: %w", err)
	}
	return nil
}

func (s *PgSecretStore) BindCredentialToAllOrgWorkspaces(ctx context.Context, credentialID, orgID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
		SELECT $1, w.id, 'auto', 5
		FROM workspaces w
		WHERE w.org_id = $2 AND w.deleted_at IS NULL
		ON CONFLICT (credential_id, workspace_id) DO NOTHING
	`, credentialID, orgID)
	if err != nil {
		return fmt.Errorf("bind org credential to workspaces: %w", err)
	}
	return nil
}

func (s *PgSecretStore) CreateOrgAutoApply(ctx context.Context, credentialID, orgID string, withinPriority int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO credential_auto_apply (credential_id, target_type, target_id, within_priority, created_at)
		VALUES ($1, 'org', $2, $3, now())
		ON CONFLICT DO NOTHING
	`, credentialID, orgID, withinPriority)
	if err != nil {
		return fmt.Errorf("create org auto-apply: %w", err)
	}
	return nil
}

func (s *PgSecretStore) ListOrgAutoApply(ctx context.Context, orgID string) ([]*AutoApplyRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT caa.credential_id, caa.target_type, caa.target_id, caa.within_priority
		FROM credential_auto_apply caa
		JOIN provider_credentials pc ON pc.id = caa.credential_id
		WHERE caa.target_type = 'org' AND caa.target_id = $1
		  AND pc.owner_type = 'org' AND pc.owner_id = $1
		ORDER BY caa.within_priority DESC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org auto-apply: %w", err)
	}
	defer rows.Close()

	var out []*AutoApplyRule
	for rows.Next() {
		var r AutoApplyRule
		if err := rows.Scan(&r.CredentialID, &r.TargetType, &r.TargetID, &r.Priority); err != nil {
			return nil, fmt.Errorf("scan org auto-apply: %w", err)
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *PgSecretStore) DeleteOrgAutoApply(ctx context.Context, credentialID, orgID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM credential_auto_apply WHERE credential_id = $1 AND target_type = 'org' AND target_id = $2
	`, credentialID, orgID)
	if err != nil {
		return fmt.Errorf("delete org auto-apply: %w", err)
	}
	return nil
}
