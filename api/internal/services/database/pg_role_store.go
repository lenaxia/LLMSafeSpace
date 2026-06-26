// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// GetAgentRole retrieves a single agent role by ID.
func (s *PgOrgStore) GetAgentRole(ctx context.Context, roleID string) (*types.AgentRole, error) {
	query := `
        SELECT id, scope, COALESCE(org_id::text, ''), name, slug, description,
               COALESCE(extends::text, ''), is_default, config, created_at, updated_at
        FROM agent_roles WHERE id = $1
    `
	return scanAgentRole(s.db.QueryRowContext(ctx, query, roleID))
}

// ListAgentRoles lists roles by scope and optional org_id.
func (s *PgOrgStore) ListAgentRoles(ctx context.Context, scope string, orgID string) ([]*types.AgentRole, error) {
	var rows *sql.Rows
	var err error
	if scope == "org" && orgID != "" {
		query := `
            SELECT id, scope, COALESCE(org_id::text, ''), name, slug, description,
                   COALESCE(extends::text, ''), is_default, config, created_at, updated_at
            FROM agent_roles WHERE scope = $1 AND org_id = $2
            ORDER BY name
        `
		rows, err = s.db.QueryContext(ctx, query, scope, orgID)
	} else {
		query := `
            SELECT id, scope, COALESCE(org_id::text, ''), name, slug, description,
                   COALESCE(extends::text, ''), is_default, config, created_at, updated_at
            FROM agent_roles WHERE scope = $1
            ORDER BY name
        `
		rows, err = s.db.QueryContext(ctx, query, scope)
	}
	if err != nil {
		return nil, fmt.Errorf("list agent roles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var roles []*types.AgentRole
	for rows.Next() {
		role, err := scanAgentRoleRows(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if roles == nil {
		roles = []*types.AgentRole{}
	}
	return roles, nil
}

// CreateAgentRole inserts a new agent role.
func (s *PgOrgStore) CreateAgentRole(ctx context.Context, role *types.AgentRole, configJSON []byte) (*types.AgentRole, error) {
	query := `
        INSERT INTO agent_roles (scope, org_id, name, slug, description, extends, is_default, config)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        RETURNING id, scope, COALESCE(org_id::text, ''), name, slug, description,
                  COALESCE(extends::text, ''), is_default, config, created_at, updated_at
    `
	var orgID interface{}
	if role.OrgID != nil && *role.OrgID != "" {
		orgID = *role.OrgID
	}
	var extends interface{}
	if role.Extends != nil && *role.Extends != "" {
		extends = *role.Extends
	}
	return scanAgentRole(s.db.QueryRowContext(ctx, query,
		role.Scope, orgID, role.Name, role.Slug, role.Description,
		extends, role.IsDefault, configJSON,
	))
}

// UpdateAgentRole updates an agent role.
func (s *PgOrgStore) UpdateAgentRole(ctx context.Context, roleID string, role *types.AgentRole, configJSON []byte) (*types.AgentRole, error) {
	query := `
        UPDATE agent_roles SET
            name = $2, slug = $3, description = $4, extends = $5,
            is_default = $6, config = $7, updated_at = now()
        WHERE id = $1
        RETURNING id, scope, COALESCE(org_id::text, ''), name, slug, description,
                  COALESCE(extends::text, ''), is_default, config, created_at, updated_at
    `
	var extends interface{}
	if role.Extends != nil && *role.Extends != "" {
		extends = *role.Extends
	}
	roleResult, err := scanAgentRole(s.db.QueryRowContext(ctx, query,
		roleID, role.Name, role.Slug, role.Description,
		extends, role.IsDefault, configJSON,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return roleResult, nil
}

// DeleteAgentRole deletes a role. Caller must check dependents first.
func (s *PgOrgStore) DeleteAgentRole(ctx context.Context, roleID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_roles WHERE id = $1`, roleID)
	if err != nil {
		return fmt.Errorf("delete agent role %s: %w", roleID, err)
	}
	return nil
}

// GetRoleDependents returns roles that extend the given role.
func (s *PgOrgStore) GetRoleDependents(ctx context.Context, roleID string) ([]*types.AgentRole, error) {
	query := `
        SELECT id, scope, COALESCE(org_id::text, ''), name, slug, description,
               COALESCE(extends::text, ''), is_default, config, created_at, updated_at
        FROM agent_roles WHERE extends = $1
        ORDER BY name
    `
	rows, err := s.db.QueryContext(ctx, query, roleID)
	if err != nil {
		return nil, fmt.Errorf("get role dependents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var roles []*types.AgentRole
	for rows.Next() {
		role, err := scanAgentRoleRows(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if roles == nil {
		roles = []*types.AgentRole{}
	}
	return roles, nil
}

// HasRoleWorkspaceUsage checks if any workspace references this role.
func (s *PgOrgStore) HasRoleWorkspaceUsage(ctx context.Context, roleID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM workspace_prompts WHERE agent_role_id = $1`, roleID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check role workspace usage: %w", err)
	}
	return count > 0, nil
}

// SetOrgDefaultRole atomically sets one role as default and clears all others.
func (s *PgOrgStore) SetOrgDefaultRole(ctx context.Context, orgID, roleID string) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE agent_roles
        SET is_default = (id = $1), updated_at = now()
        WHERE org_id = $2 AND scope = 'org'
    `, roleID, orgID)
	if err != nil {
		return fmt.Errorf("set org default role: %w", err)
	}
	return nil
}

// GetWorkspaceAgentRole retrieves the role assigned to a workspace.
func (s *PgOrgStore) GetWorkspaceAgentRole(ctx context.Context, workspaceID string) (*types.AgentRole, error) {
	query := `
        SELECT ar.id, ar.scope, COALESCE(ar.org_id::text, ''), ar.name, ar.slug,
               ar.description, COALESCE(ar.extends::text, ''), ar.is_default,
               ar.config, ar.created_at, ar.updated_at
        FROM workspace_prompts wp
        JOIN agent_roles ar ON ar.id = wp.agent_role_id
        WHERE wp.workspace_id = $1
    `
	role, err := scanAgentRole(s.db.QueryRowContext(ctx, query, workspaceID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return role, nil
}

// SetWorkspaceAgentRole sets the role for a workspace.
func (s *PgOrgStore) SetWorkspaceAgentRole(ctx context.Context, workspaceID, roleID, userID string) error {
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO workspace_prompts (workspace_id, agent_role_id, updated_by, updated_at)
        VALUES ($1, $2, $3, now())
        ON CONFLICT (workspace_id) DO UPDATE SET
            agent_role_id = EXCLUDED.agent_role_id,
            updated_by = EXCLUDED.updated_by,
            updated_at = now()
    `, workspaceID, roleID, nullableString(userID))
	if err != nil {
		return fmt.Errorf("set workspace agent role: %w", err)
	}
	return nil
}

// --- Scanning helpers ---

func scanAgentRole(row *sql.Row) (*types.AgentRole, error) {
	var r types.AgentRole
	var orgID, extends string
	var configJSON []byte
	err := row.Scan(&r.ID, &r.Scope, &orgID, &r.Name, &r.Slug, &r.Description,
		&extends, &r.IsDefault, &configJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if orgID != "" {
		r.OrgID = &orgID
	}
	if extends != "" {
		r.Extends = &extends
	}
	cfg, err := types.UnmarshalRoleConfig(configJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal role config: %w", err)
	}
	r.Config = *cfg
	return &r, nil
}

func scanAgentRoleRows(rows *sql.Rows) (*types.AgentRole, error) {
	var r types.AgentRole
	var orgID, extends string
	var configJSON []byte
	err := rows.Scan(&r.ID, &r.Scope, &orgID, &r.Name, &r.Slug, &r.Description,
		&extends, &r.IsDefault, &configJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if orgID != "" {
		r.OrgID = &orgID
	}
	if extends != "" {
		r.Extends = &extends
	}
	cfg, err := types.UnmarshalRoleConfig(configJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal role config: %w", err)
	}
	r.Config = *cfg
	return &r, nil
}

// ClearWorkspaceAgentRole removes the role assignment from a workspace
// (sets agent_role_id = NULL). Used by the "use platform default" action.
func (s *PgOrgStore) ClearWorkspaceAgentRole(ctx context.Context, workspaceID, userID string) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE workspace_prompts SET agent_role_id = NULL, updated_by = $2, updated_at = now()
        WHERE workspace_id = $1
    `, workspaceID, nullableString(userID))
	if err != nil {
		return fmt.Errorf("clear workspace agent role: %w", err)
	}
	return nil
}
