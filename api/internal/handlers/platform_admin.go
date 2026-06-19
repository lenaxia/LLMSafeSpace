// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// platformAdminOrgStore is the data-access surface for org-level suspension +
// audit + the last-admin deadlock check + the dashboard list. It is a strict
// subset of database.OrgStore so *PgOrgStore satisfies it without an adapter.
type platformAdminOrgStore interface {
	UpdateOrgStatus(ctx context.Context, orgID string, status *types.OrgStatus, subStatus *types.OrgSubscriptionStatus, planID *types.OrgPlan) error
	LogAuditEvent(ctx context.Context, domain, actorID, action, targetID string, orgID *string, metadata map[string]any) error
	// SuspendUserGuardedByLastAdmin performs the last-admin check and the
	// status update atomically (F7). The sole mutation path for user suspend.
	SuspendUserGuardedByLastAdmin(ctx context.Context, userID string, force bool) (*types.LastAdminOrg, error)
	ListAllOrgs(ctx context.Context, limit, offset int, statusFilter *string) ([]types.OrgSummary, *types.PaginationMetadata, error)
}

// platformAdminUserStore is the data-access surface for user-level suspension +
// the dashboard list. *database.Service satisfies it.
type platformAdminUserStore interface {
	SetUserStatus(ctx context.Context, userID string, status types.UserStatus) error
	ListAllUsers(ctx context.Context, limit, offset int, statusFilter *string) ([]types.UserListEntry, *types.PaginationMetadata, error)
}

// platformUserRevoker revokes a user's live JWTs/API keys the instant they are
// suspended (F4, US-43.19). *auth.Service satisfies it. When nil (e.g. minimal
// test setups) the handler skips the fast-path revocation; the per-request
// GetUser check (fail-closed, F3) remains the authoritative enforcement.
type platformUserRevoker interface {
	MarkUserSuspended(ctx context.Context, userID string) error
	ClearUserSuspended(ctx context.Context, userID string) error
}

// PlatformAdminHandler implements the platform-admin org/user suspension
// endpoints (D19, US-43.19). All routes are mounted behind AuthMiddleware +
// AdminGuard (users.role='admin'), so every method here runs in a
// platform-admin context only.
type PlatformAdminHandler struct {
	orgStore  platformAdminOrgStore
	userStore platformAdminUserStore
	authSvc   orgAuthService
	revoker   platformUserRevoker
	logger    policyLogger
}

// NewPlatformAdminHandler constructs the handler. revoker wires the F4 token
// revocation primitive (pass nil only in tests that do not exercise it). logger
// surfaces audit-write failures (audit is best-effort: a failed audit log never
// blocks the mutation, matching the existing policy/audit pattern).
func NewPlatformAdminHandler(orgs platformAdminOrgStore, users platformAdminUserStore, authSvc orgAuthService, revoker platformUserRevoker, logger policyLogger) *PlatformAdminHandler {
	return &PlatformAdminHandler{orgStore: orgs, userStore: users, authSvc: authSvc, revoker: revoker, logger: logger}
}

// SuspendOrg handles POST /api/v1/admin/orgs/:id/suspend.
//
// Sets organizations.status='suspended'. Per D20 the operational effect (pod
// termination) is applied asynchronously by the controller querying the
// org-status cache on its next reconcile cycle — this endpoint only flips the
// authoritative status. The audit event is org-scoped so org admins see it in
// their own audit log.
func (h *PlatformAdminHandler) SuspendOrg(c *gin.Context) {
	orgID := c.Param("id")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "org id required"})
		return
	}
	actorID := h.authSvc.GetUserID(c)
	ctx := c.Request.Context()

	if err := h.orgStore.UpdateOrgStatus(ctx, orgID, statusPtr(types.OrgStatusSuspended), nil, nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to suspend organization"})
		return
	}
	h.emitAudit(ctx, "org", "org.suspend", orgID, &orgID, actorID, nil)
	c.JSON(http.StatusOK, gin.H{"status": string(types.OrgStatusSuspended)})
}

// UnsuspendOrg handles POST /api/v1/admin/orgs/:id/unsuspend.
//
// Sets organizations.status='active'. Per D20 the controller does NOT
// auto-resume workspaces; members/admins must manually resume each one.
func (h *PlatformAdminHandler) UnsuspendOrg(c *gin.Context) {
	orgID := c.Param("id")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "org id required"})
		return
	}
	actorID := h.authSvc.GetUserID(c)
	ctx := c.Request.Context()

	if err := h.orgStore.UpdateOrgStatus(ctx, orgID, statusPtr(types.OrgStatusActive), nil, nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unsuspend organization"})
		return
	}
	h.emitAudit(ctx, "org", "org.unsuspend", orgID, &orgID, actorID, nil)
	c.JSON(http.StatusOK, gin.H{"status": string(types.OrgStatusActive)})
}

// SuspendUser handles POST /api/v1/admin/users/:id/suspend.
//
// Atomically refuses when the user is the sole active admin of any org (unless
// ?force=true), then sets users.status='suspended' and revokes the user's live
// tokens. The check + update run in one transaction (F7) so concurrent admin
// operations cannot orphan an org; the revocation marker (F4) makes the
// suspension take effect immediately, even during a DB blip.
func (h *PlatformAdminHandler) SuspendUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user id required"})
		return
	}
	actorID := h.authSvc.GetUserID(c)
	ctx := c.Request.Context()
	force := strings.ToLower(c.Query("force")) == "true"

	conflict, err := h.orgStore.SuspendUserGuardedByLastAdmin(ctx, userID, force)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to suspend user"})
		return
	}
	if conflict != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error": "cannot suspend last admin of org " + conflict.OrgName + " — promote another member first (use ?force=true to override)",
			"orgId": conflict.OrgID,
		})
		return
	}

	// F4: revoke live tokens immediately. Best-effort relative to the status
	// flip — the user is already suspended in the DB, so the per-request
	// GetUser gate (fail-closed, F3) enforces regardless. A Redis blip must not
	// roll back the admin action or surface a misleading 500.
	if h.revoker != nil {
		if rerr := h.revoker.MarkUserSuspended(ctx, userID); rerr != nil && h.logger != nil {
			h.logger.Warn("failed to revoke suspended user's tokens; DB status still enforces access",
				"userID", userID, "err", rerr)
		}
	}

	meta := map[string]any{}
	if force {
		meta["force"] = true
	}
	h.emitAudit(ctx, "admin", "user.suspend", userID, nil, actorID, meta)
	c.JSON(http.StatusOK, gin.H{"status": string(types.UserStatusSuspended)})
}

// UnsuspendUser handles POST /api/v1/admin/users/:id/unsuspend.
func (h *PlatformAdminHandler) UnsuspendUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user id required"})
		return
	}
	actorID := h.authSvc.GetUserID(c)
	ctx := c.Request.Context()

	if err := h.userStore.SetUserStatus(ctx, userID, types.UserStatusActive); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unsuspend user"})
		return
	}
	// F4: clear the revocation marker so the user's existing tokens work again
	// immediately rather than waiting for the marker TTL.
	if h.revoker != nil {
		if rerr := h.revoker.ClearUserSuspended(ctx, userID); rerr != nil && h.logger != nil {
			h.logger.Warn("failed to clear suspended user's revocation marker",
				"userID", userID, "err", rerr)
		}
	}
	h.emitAudit(ctx, "admin", "user.unsuspend", userID, nil, actorID, nil)
	c.JSON(http.StatusOK, gin.H{"status": string(types.UserStatusActive)})
}

// ListOrgs handles GET /api/v1/admin/orgs. Returns every organization with
// aggregated member + workspace counts for the platform-admin dashboard.
// Optional query params: limit (default 50, max 200), offset (>=0), status
// (one of pending_activation|active|suspended).
func (h *PlatformAdminHandler) ListOrgs(c *gin.Context) {
	limit, offset := parseAdminListPaging(c)
	var statusFilter *string
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		statusFilter = &v
	}

	orgs, pagination, err := h.orgStore.ListAllOrgs(c.Request.Context(), limit, offset, statusFilter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list organizations"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":      orgs,
		"pagination": pagination,
	})
}

// ListUsers handles GET /api/v1/admin/users. Returns every user (sans password
// hash) with their single org membership resolved. Optional query params: limit
// (default 50, max 200), offset (>=0), status (active|suspended).
func (h *PlatformAdminHandler) ListUsers(c *gin.Context) {
	limit, offset := parseAdminListPaging(c)
	var statusFilter *string
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		statusFilter = &v
	}

	users, pagination, err := h.userStore.ListAllUsers(c.Request.Context(), limit, offset, statusFilter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":      users,
		"pagination": pagination,
	})
}

// parseAdminListPaging extracts limit/offset for an admin list endpoint.
// limit defaults to 50 and is clamped to (0, 200]; negative/non-numeric values
// fall back to the default. offset defaults to 0 and clamps to >= 0.
func parseAdminListPaging(c *gin.Context) (limit, offset int) {
	limit = 50
	offset = 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 200 {
				limit = 200
			}
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

// emitAudit records an audit event and logs (never returns) a failure. Audit is
// best-effort: a broken audit_log must not roll back an already-committed
// status mutation. This mirrors the policy handler's behavior.
func (h *PlatformAdminHandler) emitAudit(ctx context.Context, domain, action, targetID string, orgID *string, actorID string, metadata map[string]any) {
	if err := h.orgStore.LogAuditEvent(ctx, domain, actorID, action, targetID, orgID, metadata); err != nil && h.logger != nil {
		h.logger.Warn("audit log emission failed", "action", action, "targetID", targetID, "error", err.Error())
	}
}

func statusPtr(s types.OrgStatus) *types.OrgStatus { return &s }
