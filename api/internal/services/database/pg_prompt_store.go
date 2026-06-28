// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// GetPlatformSetting retrieves a single platform-wide setting by key.
// Returns nil (not an error) when the key does not exist.
func (s *PgOrgStore) GetPlatformSetting(ctx context.Context, key types.PlatformSettingKey) (*types.PlatformSetting, error) {
	query := `
        SELECT key, value, COALESCE(updated_by, ''), updated_at
        FROM platform_settings
        WHERE key = $1
    `
	var setting types.PlatformSetting
	err := s.db.QueryRowContext(ctx, query, string(key)).Scan(
		&setting.Key,
		&setting.Value,
		&setting.UpdatedBy,
		&setting.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get platform setting %s: %w", key, err)
	}
	return &setting, nil
}

// SetPlatformSetting upserts a platform-wide setting.
func (s *PgOrgStore) SetPlatformSetting(ctx context.Context, key types.PlatformSettingKey, value json.RawMessage, updatedBy string) error {
	query := `
        INSERT INTO platform_settings (key, value, updated_by, updated_at)
        VALUES ($1, $2, $3, now())
        ON CONFLICT (key) DO UPDATE SET
            value = EXCLUDED.value,
            updated_by = EXCLUDED.updated_by,
            updated_at = now()
    `
	_, err := s.db.ExecContext(ctx, query, string(key), []byte(value), nullableString(updatedBy))
	if err != nil {
		return fmt.Errorf("set platform setting %s: %w", key, err)
	}
	return nil
}

// GetWorkspacePrompt retrieves the user-level prompt override for a workspace.
// Returns nil (not an error) when no override exists.
func (s *PgOrgStore) GetWorkspacePrompt(ctx context.Context, workspaceID string) (*types.WorkspacePrompt, error) {
	query := `
        SELECT workspace_id, prompt, COALESCE(agent_role_id::text, ''), COALESCE(updated_by, ''), updated_at
        FROM workspace_prompts
        WHERE workspace_id = $1
    `
	var wp types.WorkspacePrompt
	var roleIDStr string
	err := s.db.QueryRowContext(ctx, query, workspaceID).Scan(
		&wp.WorkspaceID,
		&wp.Prompt,
		&roleIDStr,
		&wp.UpdatedBy,
		&wp.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get workspace prompt %s: %w", workspaceID, err)
	}
	if roleIDStr != "" {
		wp.AgentRoleID = &roleIDStr
	}
	return &wp, nil
}

// SetWorkspacePrompt upserts the user-level prompt override for a workspace.
func (s *PgOrgStore) SetWorkspacePrompt(ctx context.Context, workspaceID string, prompt string, updatedBy string) error {
	query := `
        INSERT INTO workspace_prompts (workspace_id, prompt, updated_by, updated_at)
        VALUES ($1, $2, $3, now())
        ON CONFLICT (workspace_id) DO UPDATE SET
            prompt = EXCLUDED.prompt,
            updated_by = EXCLUDED.updated_by,
            updated_at = now()
    `
	_, err := s.db.ExecContext(ctx, query, workspaceID, prompt, nullableString(updatedBy))
	if err != nil {
		return fmt.Errorf("set workspace prompt %s: %w", workspaceID, err)
	}
	return nil
}

// DeleteWorkspacePrompt removes the user-level prompt override.
func (s *PgOrgStore) DeleteWorkspacePrompt(ctx context.Context, workspaceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workspace_prompts WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return fmt.Errorf("delete workspace prompt %s: %w", workspaceID, err)
	}
	return nil
}

// GetWorkspaceOrgID returns the org_id for a workspace, or "" if the
// workspace has no org (standalone user).
func (s *PgOrgStore) GetWorkspaceOrgID(ctx context.Context, workspaceID string) (string, error) {
	var orgID *string
	err := s.db.QueryRowContext(ctx, `SELECT org_id FROM workspaces WHERE id = $1`, workspaceID).Scan(&orgID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("get workspace org_id %s: %w", workspaceID, err)
	}
	if orgID == nil {
		return "", nil
	}
	return *orgID, nil
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
