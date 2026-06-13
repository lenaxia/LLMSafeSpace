// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// OrgStore is the data-access interface for organizations and their memberships.
type OrgStore interface {
	CreateOrgWithAdmin(ctx context.Context, org *types.Organization, adminUserID string, adminWrappedDEK []byte) (*types.Organization, error)
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
	GetOrgBySlug(ctx context.Context, slug string) (*types.Organization, error)
	ListOrgsForUser(ctx context.Context, userID string) ([]*types.OrgResponse, error)
	UpdateOrg(ctx context.Context, orgID string, req types.UpdateOrgRequest) (*types.Organization, error)
	SoftDeleteOrg(ctx context.Context, orgID string) error
	OrgHasActiveWorkspaces(ctx context.Context, orgID string) (bool, error)

	AddOrgMember(ctx context.Context, orgID, userID string, role types.OrgRole, pendingKeyWrap bool) error
	GetOrgMember(ctx context.Context, orgID, userID string) (*types.OrgMember, error)
	ListOrgMembers(ctx context.Context, orgID string) ([]*types.OrgMember, error)
	UpdateOrgMemberRole(ctx context.Context, orgID, userID string, role types.OrgRole) error
	SetPendingKeyWrap(ctx context.Context, orgID, userID string, pending bool) error
	RemoveOrgMember(ctx context.Context, orgID, userID string) error
	RemoveOrgAdminIfNotLast(ctx context.Context, orgID, targetUserID string) (bool, error)
	DemoteOrgAdminIfNotLast(ctx context.Context, orgID, targetUserID string) (bool, error)
	CountOrgAdmins(ctx context.Context, orgID string) (int, error)
	IsOrgMember(ctx context.Context, orgID, userID string) (bool, error)
	IsOrgAdmin(ctx context.Context, orgID, userID string) (bool, error)
	SetPendingKeyWrapForOtherAdmins(ctx context.Context, orgID, excludeUserID string) error
	ListOrgWorkspaces(ctx context.Context, orgID string, limit, offset int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error)
	DeleteOrgKeyMember(ctx context.Context, orgID, userID string) error
	GetUserSalt(ctx context.Context, userID string) ([]byte, error)
}

// PgOrgStore implements OrgStore using database/sql.
type PgOrgStore struct {
	db *sql.DB
}

// NewPgOrgStore creates a new PgOrgStore.
func NewPgOrgStore(db *sql.DB) *PgOrgStore {
	return &PgOrgStore{db: db}
}

func (s *PgOrgStore) CreateOrgWithAdmin(ctx context.Context, org *types.Organization, adminUserID string, adminWrappedDEK []byte) (*types.Organization, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRowContext(ctx,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, NOW(), NOW())
		 RETURNING id, name, slug, created_by, created_at, updated_at`,
		org.ID, org.Name, org.Slug, org.CreatedBy,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert organization: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO org_memberships (org_id, user_id, role, pending_key_wrap, created_at)
		 VALUES ($1, $2, 'admin', false, NOW())`,
		org.ID, adminUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("insert org membership: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO org_key_members (org_id, user_id, wrapped_dek, key_version, created_at, updated_at)
		 VALUES ($1, $2, $3, 1, NOW(), NOW())`,
		org.ID, adminUserID, adminWrappedDEK,
	)
	if err != nil {
		return nil, fmt.Errorf("insert org key member: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return org, nil
}

func (s *PgOrgStore) GetOrg(ctx context.Context, orgID string) (*types.Organization, error) {
	var org types.Organization
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, created_by, created_at, updated_at
		 FROM organizations WHERE id = $1 AND deleted_at IS NULL`,
		orgID,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}
	return &org, nil
}

func (s *PgOrgStore) GetOrgBySlug(ctx context.Context, slug string) (*types.Organization, error) {
	var org types.Organization
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, created_by, created_at, updated_at
		 FROM organizations WHERE slug = $1 AND deleted_at IS NULL`,
		slug,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization by slug: %w", err)
	}
	return &org, nil
}

func (s *PgOrgStore) ListOrgsForUser(ctx context.Context, userID string) ([]*types.OrgResponse, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT o.id, o.name, o.slug, o.created_by, o.created_at, o.updated_at,
		        m.role, m.pending_key_wrap,
		        COUNT(m2.user_id) AS member_count
		 FROM organizations o
		 JOIN org_memberships m ON m.org_id = o.id AND m.user_id = $1
		 JOIN org_memberships m2 ON m2.org_id = o.id
		 WHERE o.deleted_at IS NULL
		 GROUP BY o.id, o.name, o.slug, o.created_by, o.created_at, o.updated_at, m.role, m.pending_key_wrap
		 ORDER BY o.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list orgs for user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var orgs []*types.OrgResponse
	for rows.Next() {
		var r types.OrgResponse
		if err := rows.Scan(
			&r.ID, &r.Name, &r.Slug, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt,
			&r.UserRole, &r.UserPendingKeyWrap,
			&r.MemberCount,
		); err != nil {
			return nil, fmt.Errorf("scan org row: %w", err)
		}
		orgs = append(orgs, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate org rows: %w", err)
	}
	if orgs == nil {
		orgs = []*types.OrgResponse{}
	}
	return orgs, nil
}

func (s *PgOrgStore) UpdateOrg(ctx context.Context, orgID string, req types.UpdateOrgRequest) (*types.Organization, error) {
	var org types.Organization
	err := s.db.QueryRowContext(ctx,
		`UPDATE organizations
		 SET name      = CASE WHEN $2 != '' THEN $2 ELSE name END,
		     slug      = CASE WHEN $3 != '' THEN $3 ELSE slug END,
		     updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL
		 RETURNING id, name, slug, created_by, created_at, updated_at`,
		orgID, req.Name, req.Slug,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update organization: %w", err)
	}
	return &org, nil
}

func (s *PgOrgStore) SoftDeleteOrg(ctx context.Context, orgID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE workspaces SET org_id = NULL WHERE org_id = $1`, orgID,
	); err != nil {
		return fmt.Errorf("null workspace org_id: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE organizations SET deleted_at = NOW() WHERE id = $1`, orgID,
	); err != nil {
		return fmt.Errorf("soft delete organization: %w", err)
	}

	return tx.Commit()
}

func (s *PgOrgStore) OrgHasActiveWorkspaces(ctx context.Context, orgID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workspaces WHERE org_id = $1 AND deleted_at IS NULL`,
		orgID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count active workspaces: %w", err)
	}
	return count > 0, nil
}

func (s *PgOrgStore) AddOrgMember(ctx context.Context, orgID, userID string, role types.OrgRole, pendingKeyWrap bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO org_memberships (org_id, user_id, role, pending_key_wrap, created_at)
		 VALUES ($1, $2, $3, $4, NOW())`,
		orgID, userID, role, pendingKeyWrap,
	)
	if err != nil {
		return fmt.Errorf("add org member: %w", err)
	}
	return nil
}

func (s *PgOrgStore) GetOrgMember(ctx context.Context, orgID, userID string) (*types.OrgMember, error) {
	var m types.OrgMember
	err := s.db.QueryRowContext(ctx,
		`SELECT m.org_id, m.user_id, u.username, u.email, m.role, m.pending_key_wrap, m.created_at
		 FROM org_memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.org_id = $1 AND m.user_id = $2`,
		orgID, userID,
	).Scan(&m.OrgID, &m.UserID, &m.Username, &m.Email, &m.Role, &m.PendingKeyWrap, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get org member: %w", err)
	}
	return &m, nil
}

func (s *PgOrgStore) ListOrgMembers(ctx context.Context, orgID string) ([]*types.OrgMember, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.org_id, m.user_id, u.username, u.email, m.role, m.pending_key_wrap, m.created_at
		 FROM org_memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.org_id = $1
		 ORDER BY m.created_at ASC`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("list org members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var members []*types.OrgMember
	for rows.Next() {
		var m types.OrgMember
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Username, &m.Email, &m.Role, &m.PendingKeyWrap, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan org member: %w", err)
		}
		members = append(members, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate org member rows: %w", err)
	}
	if members == nil {
		members = []*types.OrgMember{}
	}
	return members, nil
}

func (s *PgOrgStore) UpdateOrgMemberRole(ctx context.Context, orgID, userID string, role types.OrgRole) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE org_memberships SET role = $3 WHERE org_id = $1 AND user_id = $2`,
		orgID, userID, role,
	)
	if err != nil {
		return fmt.Errorf("update org member role: %w", err)
	}
	return nil
}

func (s *PgOrgStore) SetPendingKeyWrap(ctx context.Context, orgID, userID string, pending bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE org_memberships SET pending_key_wrap = $3 WHERE org_id = $1 AND user_id = $2`,
		orgID, userID, pending,
	)
	if err != nil {
		return fmt.Errorf("set pending key wrap: %w", err)
	}
	return nil
}

func (s *PgOrgStore) RemoveOrgMember(ctx context.Context, orgID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM org_key_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID,
	); err != nil {
		return fmt.Errorf("delete org key member: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM org_memberships WHERE org_id = $1 AND user_id = $2`,
		orgID, userID,
	); err != nil {
		return fmt.Errorf("delete org membership: %w", err)
	}

	committed = true
	return tx.Commit()
}

func (s *PgOrgStore) RemoveOrgAdminIfNotLast(ctx context.Context, orgID, targetUserID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var adminCount int
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM org_memberships WHERE org_id = $1 AND role = 'admin' FOR UPDATE`,
		orgID,
	).Scan(&adminCount)
	if err != nil {
		return false, fmt.Errorf("count org admins: %w", err)
	}

	var targetRole string
	err = tx.QueryRowContext(ctx,
		`SELECT role FROM org_memberships WHERE org_id = $1 AND user_id = $2`,
		orgID, targetUserID,
	).Scan(&targetRole)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get target member role: %w", err)
	}

	if targetRole == "admin" && adminCount <= 1 {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM org_key_members WHERE org_id = $1 AND user_id = $2`,
		orgID, targetUserID,
	); err != nil {
		return false, fmt.Errorf("delete org key member: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM org_memberships WHERE org_id = $1 AND user_id = $2`,
		orgID, targetUserID,
	); err != nil {
		return false, fmt.Errorf("delete org membership: %w", err)
	}

	committed = true
	return true, tx.Commit()
}

func (s *PgOrgStore) DemoteOrgAdminIfNotLast(ctx context.Context, orgID, targetUserID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var adminCount int
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM org_memberships WHERE org_id = $1 AND role = 'admin' FOR UPDATE`,
		orgID,
	).Scan(&adminCount)
	if err != nil {
		return false, fmt.Errorf("count org admins: %w", err)
	}

	if adminCount <= 1 {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE org_memberships SET role = 'member' WHERE org_id = $1 AND user_id = $2`,
		orgID, targetUserID,
	); err != nil {
		return false, fmt.Errorf("demote org admin: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM org_key_members WHERE org_id = $1 AND user_id = $2`,
		orgID, targetUserID,
	); err != nil {
		return false, fmt.Errorf("delete org key member: %w", err)
	}

	committed = true
	return true, tx.Commit()
}

func (s *PgOrgStore) CountOrgAdmins(ctx context.Context, orgID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM org_memberships WHERE org_id = $1 AND role = 'admin'`,
		orgID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count org admins: %w", err)
	}
	return count, nil
}

func (s *PgOrgStore) IsOrgMember(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM org_memberships m
		   JOIN organizations o ON o.id = m.org_id
		   WHERE m.org_id = $1 AND m.user_id = $2 AND o.deleted_at IS NULL
		 )`,
		orgID, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check org membership: %w", err)
	}
	return exists, nil
}

func (s *PgOrgStore) IsOrgAdmin(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM org_memberships m
		   JOIN organizations o ON o.id = m.org_id
		   WHERE m.org_id = $1 AND m.user_id = $2
		     AND m.role = 'admin' AND m.pending_key_wrap = false
		     AND o.deleted_at IS NULL
		 )`,
		orgID, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check org admin: %w", err)
	}
	return exists, nil
}

func (s *PgOrgStore) SetPendingKeyWrapForOtherAdmins(ctx context.Context, orgID, excludeUserID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE org_memberships
		 SET pending_key_wrap = true
		 WHERE org_id = $1 AND user_id != $2 AND role = 'admin'`,
		orgID, excludeUserID,
	)
	if err != nil {
		return fmt.Errorf("set pending key wrap for other admins: %w", err)
	}
	return nil
}

func (s *PgOrgStore) ListOrgWorkspaces(ctx context.Context, orgID string, limit, offset int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error) {
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workspaces WHERE org_id = $1 AND deleted_at IS NULL`,
		orgID,
	).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("count org workspaces: %w", err)
	}

	pagination := &types.PaginationMetadata{
		Total:  total,
		Start:  offset,
		End:    offset + limit,
		Limit:  limit,
		Offset: offset,
	}
	if pagination.End > total {
		pagination.End = total
	}

	if total == 0 {
		return []*types.WorkspaceMetadata{}, pagination, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.user_id, w.name, w.runtime, w.storage_size,
		        COALESCE(w.image_tag, '') AS image_tag,
		        COALESCE(w.agent_version, '') AS agent_version,
		        w.created_at, w.updated_at,
		        COALESCE(w.default_model, '') AS default_model,
		        COALESCE(s.pending_refresh, FALSE) AS agent_needs_refresh,
		        s.last_credential_changed_at AS credentials_pending_since,
		        w.org_id
		 FROM workspaces w
		 LEFT JOIN workspace_agent_state s ON s.workspace_id = w.id
		 WHERE w.org_id = $1 AND w.deleted_at IS NULL
		 ORDER BY w.created_at DESC
		 LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list org workspaces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	workspaces := make([]*types.WorkspaceMetadata, 0)
	for rows.Next() {
		var ws types.WorkspaceMetadata
		if err := rows.Scan(
			&ws.ID, &ws.UserID, &ws.Name, &ws.Runtime,
			&ws.StorageSize, &ws.ImageTag, &ws.AgentVersion,
			&ws.CreatedAt, &ws.UpdatedAt, &ws.DefaultModel,
			&ws.AgentNeedsRefresh, &ws.CredentialsPendingSince,
			&ws.OrgID,
		); err != nil {
			return nil, nil, fmt.Errorf("scan org workspace row: %w", err)
		}
		workspaces = append(workspaces, &ws)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate org workspace rows: %w", err)
	}

	return workspaces, pagination, nil
}

func (s *PgOrgStore) DeleteOrgKeyMember(ctx context.Context, orgID, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM org_key_members WHERE org_id = $1 AND user_id = $2`,
		orgID, userID,
	)
	if err != nil {
		return fmt.Errorf("delete org key member: %w", err)
	}
	return nil
}

func (s *PgOrgStore) GetUserSalt(ctx context.Context, userID string) ([]byte, error) {
	var salt []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT salt FROM user_keys WHERE user_id = $1`,
		userID,
	).Scan(&salt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user key record not found for user %s", userID)
	}
	if err != nil {
		return nil, fmt.Errorf("get user salt: %w", err)
	}
	return salt, nil
}
