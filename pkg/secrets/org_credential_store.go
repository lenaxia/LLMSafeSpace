// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
)

// OrgAutoApplyStore is the DB interface for org-scoped auto-apply and
// workspace-binding operations (the credential CRUD itself is served by the
// owner-parameterized CredentialStore methods on PgSecretStore).
type OrgAutoApplyStore interface {
	BindCredentialToAllOrgWorkspaces(ctx context.Context, credentialID, orgID string) error
	CreateOrgAutoApply(ctx context.Context, credentialID, orgID string, withinPriority int) error
	ListOrgAutoApply(ctx context.Context, orgID string) ([]*AutoApplyRule, error)
	DeleteOrgAutoApply(ctx context.Context, credentialID, orgID string) error
}

// PgSecretStore implements OrgAutoApplyStore. The org-scoped CRUD methods
// (Create/List/Get/Update/Delete) are the owner-parameterized CredentialStore
// methods in pg_credential_store.go.

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
