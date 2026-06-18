// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// PendingOrgCleanup describes a pending_activation org eligible for the cleanup
// cron. StripeCustomerID lets the cron verify checkout state with Stripe before
// deleting.
type PendingOrgCleanup struct {
	OrgID            string
	Slug             string
	CreatedAt        time.Time
	StripeCustomerID string
}

// OrgStore is the data-access interface for organizations and their memberships.
type OrgStore interface {
	CreateOrgWithAdmin(ctx context.Context, org *types.Organization, adminUserID string) (*types.Organization, error)
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
	GetOrgBySlug(ctx context.Context, slug string) (*types.Organization, error)
	ListOrgsForUser(ctx context.Context, userID string) ([]*types.OrgResponse, error)
	UpdateOrg(ctx context.Context, orgID string, req types.UpdateOrgRequest) (*types.Organization, error)
	SoftDeleteOrg(ctx context.Context, orgID string) error

	AddOrgMember(ctx context.Context, orgID, userID string, role types.OrgRole) error
	GetOrgMember(ctx context.Context, orgID, userID string) (*types.OrgMember, error)
	ListOrgMembers(ctx context.Context, orgID string) ([]*types.OrgMember, error)
	UpdateOrgMemberRole(ctx context.Context, orgID, userID string, role types.OrgRole) error
	RemoveOrgMember(ctx context.Context, orgID, userID string) error
	RemoveOrgAdminIfNotLast(ctx context.Context, orgID, targetUserID string) (bool, error)
	DemoteOrgAdminIfNotLast(ctx context.Context, orgID, targetUserID string) (bool, error)
	IsOrgMember(ctx context.Context, orgID, userID string) (bool, error)
	IsOrgAdmin(ctx context.Context, orgID, userID string) (bool, error)
	ListOrgWorkspaces(ctx context.Context, orgID string, limit, offset int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error)
	// GetUserIDByEmail resolves an owner email to a user ID for admin-driven org
	// creation (design 0031 D1). Returns ("", nil) when no user matches. This is
	// a single targeted lookup, never a search/list endpoint, to prevent account
	// enumeration. Users are hard-deleted (no deleted_at column), so no soft-delete
	// filter is needed.
	GetUserIDByEmail(ctx context.Context, email string) (string, error)
	// GetUserEmail resolves a user ID to their email (inverse of GetUserIDByEmail).
	// Used by invitation acceptance to verify email binding.
	GetUserEmail(ctx context.Context, userID string) (string, error)
	// GetUserOrgID returns the user's single org ID (or "" if not in any org).
	// With single-org enforcement (D8), a user belongs to at most one org. Used
	// by invitation acceptance (S3 cross-org check) and workspace auto-attribution
	// (D4). Returns ("", nil) on no membership. S7 in 0034.
	GetUserOrgID(ctx context.Context, userID string) (string, error)

	// US-43.1: Stripe lifecycle. UpdateOrgStatus sets the operational status
	// (active/suspended) and/or subscription_status and/or plan_id. A nil/empty
	// argument leaves the column unchanged.
	UpdateOrgStatus(ctx context.Context, orgID string, status *types.OrgStatus, subStatus *types.OrgSubscriptionStatus, planID *types.OrgPlan) error
	// GetOrgIDByStripeCustomer resolves a Stripe customer ID to the owning org's
	// ID via billing_accounts. Returns ("", nil) when no row matches.
	GetOrgIDByStripeCustomer(ctx context.Context, stripeCustomerID string) (string, error)
	// GetStripeCustomerID resolves an org to its Stripe customer id via
	// billing_accounts. Returns ("", nil) when no billing account exists.
	GetStripeCustomerID(ctx context.Context, orgID string) (string, error)
	// --- US-43.2: Invitations ---
	CreateInvitation(ctx context.Context, inv *types.OrgInvitation) error
	ListPendingInvitations(ctx context.Context, orgID string) ([]*types.OrgInvitation, error)
	GetInvitationByTokenHash(ctx context.Context, tokenHash string) (*types.OrgInvitation, error)
	GetInvitationByID(ctx context.Context, invID string) (*types.OrgInvitation, error)
	// AcceptInvitation performs the accept flow atomically under FOR UPDATE:
	// locks the invitation row, re-checks it is still pending, inserts the
	// membership, and marks the invitation accepted. Returns
	// (membership, alreadyAccepted, error).
	AcceptInvitationTx(ctx context.Context, invID, userID string, role types.OrgRole) (*types.OrgMember, bool, error)
	DeclineInvitation(ctx context.Context, invID string) error
	DeleteInvitation(ctx context.Context, invID string) error
	CountInvitationsLastHour(ctx context.Context, orgID string) (int, error)

	// --- US-43.7: Policies ---
	GetOrgPolicies(ctx context.Context, orgID string) ([]*types.OrgPolicy, error)
	SetOrgPolicy(ctx context.Context, orgID string, key types.OrgPolicyKey, value json.RawMessage, updatedBy string) error
	DeleteOrgPolicy(ctx context.Context, orgID string, key types.OrgPolicyKey) error

	// --- US-43.13: Org-scoped audit log ---
	LogOrgEvent(ctx context.Context, orgID, actorID, action, targetID string, metadata map[string]any) error
	ListOrgAudit(ctx context.Context, orgID string, limit, offset int) ([]*types.AuditEntry, *types.PaginationMetadata, error)
}

// PgOrgStore implements OrgStore using database/sql.
type PgOrgStore struct {
	db *sql.DB
}

// NewPgOrgStore creates a new PgOrgStore.
func NewPgOrgStore(db *sql.DB) *PgOrgStore {
	return &PgOrgStore{db: db}
}

func (s *PgOrgStore) CreateOrgWithAdmin(ctx context.Context, org *types.Organization, adminUserID string) (*types.Organization, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRowContext(ctx,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, NOW(), NOW())
		 RETURNING id, name, slug, created_by, created_at, updated_at, status, plan_id, subscription_status`,
		org.ID, org.Name, org.Slug, org.CreatedBy,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt,
		&org.Status, &org.PlanID, &org.SubscriptionStatus)
	if err != nil {
		return nil, fmt.Errorf("insert organization: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO org_memberships (org_id, user_id, role, created_at)
		 VALUES ($1, $2, 'admin', NOW())`,
		org.ID, adminUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("insert org membership: %w", err)
	}

	// D4: migrate the owner's existing personal workspaces to the org (same as
	// AcceptInvitationTx). Keeps the two "join the org" paths consistent.
	if _, err := tx.ExecContext(ctx,
		`UPDATE workspaces SET org_id = $2, updated_at = NOW()
		 WHERE user_id = $1 AND org_id IS NULL AND deleted_at IS NULL`,
		adminUserID, org.ID,
	); err != nil {
		return nil, fmt.Errorf("migrate owner personal workspaces to org: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return org, nil
}

func (s *PgOrgStore) GetOrg(ctx context.Context, orgID string) (*types.Organization, error) {
	var org types.Organization
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, created_by, created_at, updated_at, status, plan_id, subscription_status
		 FROM organizations WHERE id = $1 AND deleted_at IS NULL`,
		orgID,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt,
		&org.Status, &org.PlanID, &org.SubscriptionStatus)
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
		`SELECT id, name, slug, created_by, created_at, updated_at, status, plan_id, subscription_status
		 FROM organizations WHERE LOWER(slug) = LOWER($1) AND deleted_at IS NULL`,
		slug,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt,
		&org.Status, &org.PlanID, &org.SubscriptionStatus)
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
		        o.status, o.plan_id, o.subscription_status,
		        m.role,
		        COUNT(m2.user_id) AS member_count
		 FROM organizations o
		 JOIN org_memberships m ON m.org_id = o.id AND m.user_id = $1
		 JOIN org_memberships m2 ON m2.org_id = o.id
		 WHERE o.deleted_at IS NULL
		 GROUP BY o.id, o.name, o.slug, o.created_by, o.created_at, o.updated_at,
		          o.status, o.plan_id, o.subscription_status, m.role
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
			&r.Status, &r.PlanID, &r.SubscriptionStatus,
			&r.UserRole,
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
		     slug      = CASE WHEN $3 != '' THEN LOWER($3) ELSE slug END,
		     updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL
		 RETURNING id, name, slug, created_by, created_at, updated_at, status, plan_id, subscription_status`,
		orgID, req.Name, req.Slug,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt,
		&org.Status, &org.PlanID, &org.SubscriptionStatus)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update organization: %w", err)
	}
	return &org, nil
}

func (s *PgOrgStore) SoftDeleteOrg(ctx context.Context, orgID string) error {
	// F6: workspaces keep their org_id after org deletion. The org is
	// soft-deleted (deleted_at set), so IsOrgMember returns false for all
	// members → workspaces become frozen (no access by anyone). This prevents
	// former members from walking away with org workspaces as personal ones.
	_, err := s.db.ExecContext(ctx,
		`UPDATE organizations SET deleted_at = NOW() WHERE id = $1`, orgID,
	)
	if err != nil {
		return fmt.Errorf("soft delete organization: %w", err)
	}
	return nil
}

func (s *PgOrgStore) AddOrgMember(ctx context.Context, orgID, userID string, role types.OrgRole) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO org_memberships (org_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, NOW())`,
		orgID, userID, role,
	)
	if err != nil {
		return fmt.Errorf("add org member: %w", err)
	}
	return nil
}

func (s *PgOrgStore) GetOrgMember(ctx context.Context, orgID, userID string) (*types.OrgMember, error) {
	var m types.OrgMember
	err := s.db.QueryRowContext(ctx,
		`SELECT m.org_id, m.user_id, u.username, u.email, m.role, m.created_at
		 FROM org_memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.org_id = $1 AND m.user_id = $2`,
		orgID, userID,
	).Scan(&m.OrgID, &m.UserID, &m.Username, &m.Email, &m.Role, &m.CreatedAt)
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
		`SELECT m.org_id, m.user_id, u.username, u.email, m.role, m.created_at
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
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Username, &m.Email, &m.Role, &m.CreatedAt); err != nil {
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

	adminRows, err := tx.QueryContext(ctx,
		`SELECT user_id FROM org_memberships WHERE org_id = $1 AND role = 'admin' FOR UPDATE`,
		orgID,
	)
	if err != nil {
		return false, fmt.Errorf("lock admin rows: %w", err)
	}
	adminCount := 0
	for adminRows.Next() {
		adminCount++
	}
	closeErr := adminRows.Close() //nolint:sqlclosecheck // must close before next tx query; defer would close too late
	if err := adminRows.Err(); err != nil {
		return false, fmt.Errorf("iterate admin rows: %w", err)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close admin rows: %w", closeErr)
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

	adminRows, err := tx.QueryContext(ctx,
		`SELECT user_id FROM org_memberships WHERE org_id = $1 AND role = 'admin' FOR UPDATE`,
		orgID,
	)
	if err != nil {
		return false, fmt.Errorf("lock admin rows: %w", err)
	}
	adminCount := 0
	for adminRows.Next() {
		adminCount++
	}
	closeErr := adminRows.Close() //nolint:sqlclosecheck // must close before next tx query; defer would close too late
	if err := adminRows.Err(); err != nil {
		return false, fmt.Errorf("iterate admin rows: %w", err)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close admin rows: %w", closeErr)
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

	committed = true
	return true, tx.Commit()
}

func (s *PgOrgStore) IsOrgMember(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM org_memberships m
		   JOIN organizations o ON o.id = m.org_id
		   WHERE m.org_id = $1 AND m.user_id = $2 AND o.deleted_at IS NULL
		     AND o.status != 'suspended'
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
		     AND m.role = 'admin'
		     AND o.deleted_at IS NULL
		     AND o.status != 'suspended'
		 )`,
		orgID, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check org admin: %w", err)
	}
	return exists, nil
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

func (s *PgOrgStore) GetUserIDByEmail(ctx context.Context, email string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = $1`,
		email,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get user id by email: %w", err)
	}
	return id, nil
}

func (s *PgOrgStore) GetUserEmail(ctx context.Context, userID string) (string, error) {
	var email string
	err := s.db.QueryRowContext(ctx,
		`SELECT email FROM users WHERE id = $1`,
		userID,
	).Scan(&email)
	if err != nil {
		return "", fmt.Errorf("get user email: %w", err)
	}
	return email, nil
}

func (s *PgOrgStore) GetUserOrgID(ctx context.Context, userID string) (string, error) {
	var orgID string
	err := s.db.QueryRowContext(ctx,
		`SELECT org_id FROM org_memberships WHERE user_id = $1`,
		userID,
	).Scan(&orgID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get user org id: %w", err)
	}
	return orgID, nil
}

func (s *PgOrgStore) UpdateOrgStatus(ctx context.Context, orgID string, status *types.OrgStatus, subStatus *types.OrgSubscriptionStatus, planID *types.OrgPlan) error {
	if status == nil && subStatus == nil && planID == nil {
		return nil
	}

	setParts := []string{"updated_at = NOW()"}
	args := []interface{}{orgID}
	argIdx := 2
	if status != nil {
		setParts = append(setParts, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, string(*status))
		argIdx++
	}
	if subStatus != nil {
		setParts = append(setParts, fmt.Sprintf("subscription_status = $%d", argIdx))
		args = append(args, string(*subStatus))
		argIdx++
	}
	if planID != nil {
		setParts = append(setParts, fmt.Sprintf("plan_id = $%d", argIdx))
		args = append(args, string(*planID))
	}

	query := fmt.Sprintf( //nolint:gosec // setParts contains only literal column assignments, no user input
		`UPDATE organizations SET %s WHERE id = $1 AND deleted_at IS NULL`,
		strings.Join(setParts, ", "),
	)
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update org status: %w", err)
	}
	return nil
}

func (s *PgOrgStore) GetOrgIDByStripeCustomer(ctx context.Context, stripeCustomerID string) (string, error) {
	var orgID string
	err := s.db.QueryRowContext(ctx,
		`SELECT owner_id FROM billing_accounts
		 WHERE external_customer_id = $1 AND provider = 'stripe' AND owner_type = 'org'`,
		stripeCustomerID,
	).Scan(&orgID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup org by stripe customer: %w", err)
	}
	return orgID, nil
}

func (s *PgOrgStore) GetStripeCustomerID(ctx context.Context, orgID string) (string, error) {
	var customerID string
	err := s.db.QueryRowContext(ctx,
		`SELECT external_customer_id FROM billing_accounts
		 WHERE owner_id = $1 AND owner_type = 'org' AND provider = 'stripe'`,
		orgID,
	).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup stripe customer for org: %w", err)
	}
	return customerID, nil
}

func (s *PgOrgStore) RecordStripeEvent(ctx context.Context, eventID, eventType string) (bool, error) {
	var inserted bool
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO stripe_events (event_id, event_type) VALUES ($1, $2)
		 ON CONFLICT (event_id) DO NOTHING
		 RETURNING TRUE`,
		eventID, eventType,
	).Scan(&inserted)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("record stripe event: %w", err)
	}
	return inserted, nil
}

func (s *PgOrgStore) DeleteStripeEvent(ctx context.Context, eventID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM stripe_events WHERE event_id = $1`,
		eventID,
	)
	if err != nil {
		return fmt.Errorf("delete stripe event: %w", err)
	}
	return nil
}

func (s *PgOrgStore) ListPendingOrgsOlderThan(ctx context.Context, maxAge time.Duration) ([]PendingOrgCleanup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT o.id, o.slug, o.created_at,
		        COALESCE(b.external_customer_id, '') AS customer_id
		 FROM organizations o
		 LEFT JOIN billing_accounts b
		   ON b.owner_id = o.id AND b.owner_type = 'org' AND b.provider = 'stripe'
		 WHERE o.status = 'pending_activation'
		   AND o.deleted_at IS NULL
		   AND o.created_at < NOW() - make_interval(secs => $1)`,
		int(maxAge.Seconds()),
	)
	if err != nil {
		return nil, fmt.Errorf("list pending orgs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PendingOrgCleanup
	for rows.Next() {
		var p PendingOrgCleanup
		if err := rows.Scan(&p.OrgID, &p.Slug, &p.CreatedAt, &p.StripeCustomerID); err != nil {
			return nil, fmt.Errorf("scan pending org: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending orgs: %w", err)
	}
	return out, nil
}

func (s *PgOrgStore) HardDeleteOrg(ctx context.Context, orgID string) error {
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
		`UPDATE workspaces SET org_id = NULL WHERE org_id = $1`, orgID,
	); err != nil {
		return fmt.Errorf("null workspace org_id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM provider_credentials WHERE owner_type = 'org' AND owner_id = $1`, orgID,
	); err != nil {
		return fmt.Errorf("delete org provider credentials: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM billing_accounts WHERE owner_id = $1 AND owner_type = 'org'`, orgID,
	); err != nil {
		return fmt.Errorf("delete org billing accounts: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM organizations WHERE id = $1`, orgID,
	); err != nil {
		return fmt.Errorf("delete organization: %w", err)
	}

	committed = true
	return tx.Commit()
}

func (s *PgOrgStore) SetBillingAccountSubscription(ctx context.Context, ownerID, ownerType, provider, subscriptionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE billing_accounts
		 SET external_subscription_id = $4, status = 'active', updated_at = NOW()
		 WHERE owner_id = $1 AND owner_type = $2 AND provider = $3`,
		ownerID, ownerType, provider, subscriptionID,
	)
	if err != nil {
		return fmt.Errorf("set billing account subscription: %w", err)
	}
	return nil
}

// --- US-43.2: Invitation implementations ---

func (s *PgOrgStore) CreateInvitation(ctx context.Context, inv *types.OrgInvitation) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO org_invitations (id, org_id, email, role, invited_by, token_hash, expires_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())`,
		inv.ID, inv.OrgID, inv.Email, inv.Role, inv.InvitedBy, inv.TokenHash, inv.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create invitation: %w", err)
	}
	return nil
}

func (s *PgOrgStore) ListPendingInvitations(ctx context.Context, orgID string) ([]*types.OrgInvitation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, email, role, invited_by, expires_at, bounce_type, bounced_at, created_at
		 FROM org_invitations
		 WHERE org_id = $1 AND accepted_at IS NULL AND declined_at IS NULL
		 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending invitations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*types.OrgInvitation
	for rows.Next() {
		var inv types.OrgInvitation
		var bounceType sql.NullString
		var bouncedAt sql.NullTime
		if err := rows.Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedBy,
			&inv.ExpiresAt, &bounceType, &bouncedAt, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		inv.BounceType = bounceType.String
		if bouncedAt.Valid {
			inv.BouncedAt = &bouncedAt.Time
		}
		out = append(out, &inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invitations: %w", err)
	}
	if out == nil {
		out = []*types.OrgInvitation{}
	}
	return out, nil
}

func (s *PgOrgStore) GetInvitationByTokenHash(ctx context.Context, tokenHash string) (*types.OrgInvitation, error) {
	return s.scanInvitation(ctx,
		`SELECT id, org_id, email, role, invited_by, expires_at, accepted_at, accepted_by,
		        declined_at, bounce_type, bounced_at, created_at
		 FROM org_invitations WHERE token_hash = $1`, tokenHash)
}

func (s *PgOrgStore) GetInvitationByID(ctx context.Context, invID string) (*types.OrgInvitation, error) {
	return s.scanInvitation(ctx,
		`SELECT id, org_id, email, role, invited_by, expires_at, accepted_at, accepted_by,
		        declined_at, bounce_type, bounced_at, created_at
		 FROM org_invitations WHERE id = $1`, invID)
}

func (s *PgOrgStore) scanInvitation(ctx context.Context, query string, args ...interface{}) (*types.OrgInvitation, error) {
	var inv types.OrgInvitation
	var acceptedAt, declinedAt, bouncedAt sql.NullTime
	var acceptedBy sql.NullString
	var bounceType sql.NullString
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedBy, &inv.ExpiresAt,
		&acceptedAt, &acceptedBy, &declinedAt, &bounceType, &bouncedAt, &inv.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan invitation row: %w", err)
	}
	if acceptedAt.Valid {
		inv.AcceptedAt = &acceptedAt.Time
	}
	if declinedAt.Valid {
		inv.DeclinedAt = &declinedAt.Time
	}
	if bouncedAt.Valid {
		inv.BouncedAt = &bouncedAt.Time
	}
	inv.BounceType = bounceType.String
	return &inv, nil
}

func (s *PgOrgStore) AcceptInvitationTx(ctx context.Context, invID, userID string, role types.OrgRole) (*types.OrgMember, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var orgID string
	var acceptedAt, declinedAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT org_id, accepted_at, declined_at
		 FROM org_invitations
		 WHERE id = $1 AND expires_at > NOW()
		 FOR UPDATE`,
		invID,
	).Scan(&orgID, &acceptedAt, &declinedAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock invitation: %w", err)
	}
	if acceptedAt.Valid || declinedAt.Valid {
		return nil, true, nil
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO org_memberships (org_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (org_id, user_id) DO NOTHING`,
		orgID, userID, role,
	); err != nil {
		return nil, false, fmt.Errorf("insert membership: %w", err)
	}

	// D4: migrate the user's personal workspaces to the org. Single atomic
	// UPDATE inside the accept transaction — if anything else fails, the
	// migration rolls back too. Only non-deleted workspaces are migrated.
	if _, err := tx.ExecContext(ctx,
		`UPDATE workspaces SET org_id = $2, updated_at = NOW()
		 WHERE user_id = $1 AND org_id IS NULL AND deleted_at IS NULL`,
		userID, orgID,
	); err != nil {
		return nil, false, fmt.Errorf("migrate personal workspaces to org: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE org_invitations SET accepted_at = NOW(), accepted_by = $2 WHERE id = $1`,
		invID, userID,
	); err != nil {
		return nil, false, fmt.Errorf("mark invitation accepted: %w", err)
	}

	committed = true
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit: %w", err)
	}

	return &types.OrgMember{
		OrgID:  orgID,
		UserID: userID,
		Role:   role,
	}, false, nil
}

func (s *PgOrgStore) DeclineInvitation(ctx context.Context, invID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE org_invitations SET declined_at = NOW() WHERE id = $1
		 AND accepted_at IS NULL AND declined_at IS NULL`,
		invID,
	)
	if err != nil {
		return fmt.Errorf("decline invitation: %w", err)
	}
	return nil
}

func (s *PgOrgStore) DeleteInvitation(ctx context.Context, invID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM org_invitations WHERE id = $1 AND accepted_at IS NULL AND declined_at IS NULL`,
		invID,
	)
	if err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	return nil
}

func (s *PgOrgStore) CountInvitationsLastHour(ctx context.Context, orgID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM org_invitations
		 WHERE org_id = $1 AND created_at > NOW() - interval '1 hour'`,
		orgID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count invitations last hour: %w", err)
	}
	return count, nil
}

// --- US-43.7: Policy implementations ---

func (s *PgOrgStore) GetOrgPolicies(ctx context.Context, orgID string) ([]*types.OrgPolicy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value, updated_by, updated_at
		 FROM org_policies WHERE org_id = $1`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("get org policies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*types.OrgPolicy
	for rows.Next() {
		var p types.OrgPolicy
		var updatedBy sql.NullString
		if err := rows.Scan(&p.Key, &p.Value, &updatedBy, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan policy: %w", err)
		}
		p.OrgID = orgID
		p.UpdatedBy = updatedBy.String
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate policies: %w", err)
	}
	if out == nil {
		out = []*types.OrgPolicy{}
	}
	return out, nil
}

func (s *PgOrgStore) SetOrgPolicy(ctx context.Context, orgID string, key types.OrgPolicyKey, value json.RawMessage, updatedBy string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO org_policies (org_id, key, value, updated_by, updated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (org_id, key) DO UPDATE
		   SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		orgID, string(key), []byte(value), updatedBy,
	)
	if err != nil {
		return fmt.Errorf("set org policy: %w", err)
	}
	return nil
}

func (s *PgOrgStore) DeleteOrgPolicy(ctx context.Context, orgID string, key types.OrgPolicyKey) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM org_policies WHERE org_id = $1 AND key = $2`,
		orgID, string(key),
	)
	if err != nil {
		return fmt.Errorf("delete org policy: %w", err)
	}
	return nil
}

// --- US-43.13: Audit log implementations ---

func (s *PgOrgStore) LogOrgEvent(ctx context.Context, orgID, actorID, action, targetID string, metadata map[string]any) error {
	var metaBytes []byte
	if metadata != nil {
		var err error
		metaBytes, err = json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal audit metadata: %w", err)
		}
	} else {
		metaBytes = []byte(`{}`)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (actor_id, domain, action, target_id, org_id, metadata, created_at)
		 VALUES ($1, 'org', $2, NULLIF($3, ''), $4, $5, NOW())`,
		actorID, action, targetID, orgID, metaBytes,
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func (s *PgOrgStore) ListOrgAudit(ctx context.Context, orgID string, limit, offset int) ([]*types.AuditEntry, *types.PaginationMetadata, error) {
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE org_id = $1`,
		orgID,
	).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("count org audit entries: %w", err)
	}

	pagination := &types.PaginationMetadata{
		Total: total, Start: offset, End: offset + limit, Limit: limit, Offset: offset,
	}
	if pagination.End > total {
		pagination.End = total
	}
	if total == 0 {
		return []*types.AuditEntry{}, pagination, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, actor_id, domain, action, COALESCE(target_id, ''), org_id::text, metadata, created_at
		 FROM audit_log WHERE org_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("list org audit: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []*types.AuditEntry
	for rows.Next() {
		var e types.AuditEntry
		var metaBytes []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Domain, &e.Action, &e.TargetID, &e.OrgID, &metaBytes, &e.CreatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan audit entry: %w", err)
		}
		if len(metaBytes) > 0 && string(metaBytes) != "{}" {
			if err := json.Unmarshal(metaBytes, &e.Metadata); err != nil {
				return nil, nil, fmt.Errorf("unmarshal audit metadata: %w", err)
			}
		}
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	if entries == nil {
		entries = []*types.AuditEntry{}
	}
	return entries, pagination, nil
}
