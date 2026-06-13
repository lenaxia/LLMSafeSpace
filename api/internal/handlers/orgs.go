// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// orgStore is the minimal OrgStore interface used by OrgsHandler.
type orgStore interface {
	CreateOrgWithAdmin(ctx context.Context, org *types.Organization, adminUserID string, adminWrappedDEK []byte) (*types.Organization, error)
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
	GetOrgBySlug(ctx context.Context, slug string) (*types.Organization, error)
	ListOrgsForUser(ctx context.Context, userID string) ([]*types.OrgResponse, error)
	UpdateOrg(ctx context.Context, orgID string, req types.UpdateOrgRequest) (*types.Organization, error)
	SoftDeleteOrg(ctx context.Context, orgID string) error
	OrgHasActiveWorkspaces(ctx context.Context, orgID string) (bool, error)
	IsOrgMember(ctx context.Context, orgID, userID string) (bool, error)
	GetOrgMember(ctx context.Context, orgID, userID string) (*types.OrgMember, error)
	ListOrgWorkspaces(ctx context.Context, orgID string, limit, offset int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error)
	GetUserSalt(ctx context.Context, userID string) ([]byte, error)
}

// orgAuthService is the minimal auth interface used by OrgsHandler.
type orgAuthService interface {
	GetUserID(c *gin.Context) string
}

// OrgsHandler handles org CRUD endpoints.
type OrgsHandler struct {
	orgStore   orgStore
	orgKeySvc  *secrets.OrgKeyService
	dekCache   secrets.DEKCache
	authSvc    orgAuthService
}

// NewOrgsHandler creates a new OrgsHandler.
func NewOrgsHandler(
	store orgStore,
	orgKeySvc *secrets.OrgKeyService,
	dekCache secrets.DEKCache,
	authSvc orgAuthService,
) *OrgsHandler {
	return &OrgsHandler{
		orgStore:  store,
		orgKeySvc: orgKeySvc,
		dekCache:  dekCache,
		authSvc:   authSvc,
	}
}

// Create handles POST /api/v1/orgs.
func (h *OrgsHandler) Create(c *gin.Context) {
	var req types.CreateOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx := c.Request.Context()

	existing, err := h.orgStore.GetOrgBySlug(ctx, req.Slug)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check slug uniqueness"})
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "slug already in use"})
		return
	}

	userID := h.authSvc.GetUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	orgDEK, err := secrets.GenerateDEK()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate org key"})
		return
	}
	defer zeroBytes(orgDEK)

	adminSalt, err := h.orgStore.GetUserSalt(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve admin key material"})
		return
	}

	adminKEK, err := secrets.DeriveKEK([]byte(req.Password), adminSalt, "llmsafespace-org-kek")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to derive admin key"})
		return
	}
	defer zeroBytes(adminKEK)

	wrappedDEK, err := secrets.WrapDEK(adminKEK, orgDEK)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to wrap org key"})
		return
	}

	newOrg := &types.Organization{
		ID:        uuid.New().String(),
		Name:      req.Name,
		Slug:      req.Slug,
		CreatedBy: userID,
	}

	created, err := h.orgStore.CreateOrgWithAdmin(ctx, newOrg, userID, wrappedDEK)
	if err != nil {
		if isDuplicateErr(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "slug already in use"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create organization"})
		return
	}

	_ = h.dekCache.CacheDEK(ctx, secrets.OrgCacheKey(created.ID), orgDEK, 24*time.Hour)

	c.JSON(http.StatusCreated, types.OrgResponse{
		Organization:       *created,
		UserRole:           types.OrgRoleAdmin,
		UserPendingKeyWrap: false,
		MemberCount:        1,
	})
}

// List handles GET /api/v1/orgs.
func (h *OrgsHandler) List(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	orgs, err := h.orgStore.ListOrgsForUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list organizations"})
		return
	}

	c.JSON(http.StatusOK, orgs)
}

// Get handles GET /api/v1/orgs/:id.
func (h *OrgsHandler) Get(c *gin.Context) {
	orgID := c.Param("id")
	userID := h.authSvc.GetUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	ctx := c.Request.Context()

	org, err := h.orgStore.GetOrg(ctx, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get organization"})
		return
	}
	if org == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	member, err := h.orgStore.GetOrgMember(ctx, orgID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get membership"})
		return
	}
	if member == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "not a member of this organization"})
		return
	}

	orgs, err := h.orgStore.ListOrgsForUser(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get org details"})
		return
	}

	for _, o := range orgs {
		if o.ID == orgID {
			c.JSON(http.StatusOK, o)
			return
		}
	}

	c.JSON(http.StatusOK, types.OrgResponse{
		Organization:       *org,
		UserRole:           member.Role,
		UserPendingKeyWrap: member.PendingKeyWrap,
		MemberCount:        0,
	})
}

// Update handles PUT /api/v1/orgs/:id.
func (h *OrgsHandler) Update(c *gin.Context) {
	orgID := c.Param("id")

	var req types.UpdateOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx := c.Request.Context()

	if req.Slug != "" {
		existing, err := h.orgStore.GetOrgBySlug(ctx, req.Slug)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check slug uniqueness"})
			return
		}
		if existing != nil && existing.ID != orgID {
			c.JSON(http.StatusConflict, gin.H{"error": "slug already in use"})
			return
		}
	}

	updated, err := h.orgStore.UpdateOrg(ctx, orgID, req)
	if err != nil {
		if isDuplicateErr(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "slug already in use"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization"})
		return
	}
	if updated == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	userID := h.authSvc.GetUserID(c)
	orgs, err := h.orgStore.ListOrgsForUser(ctx, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get org details"})
		return
	}
	for _, o := range orgs {
		if o.ID == orgID {
			c.JSON(http.StatusOK, o)
			return
		}
	}

	c.JSON(http.StatusOK, types.OrgResponse{Organization: *updated})
}

// Delete handles DELETE /api/v1/orgs/:id.
func (h *OrgsHandler) Delete(c *gin.Context) {
	orgID := c.Param("id")
	ctx := c.Request.Context()

	hasWS, err := h.orgStore.OrgHasActiveWorkspaces(ctx, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check active workspaces"})
		return
	}
	if hasWS {
		c.JSON(http.StatusConflict, gin.H{"error": "organization has active workspaces; remove them before deleting"})
		return
	}

	if err := h.orgStore.SoftDeleteOrg(ctx, orgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete organization"})
		return
	}

	c.Status(http.StatusNoContent)
}

// ListWorkspaces handles GET /api/v1/orgs/:id/workspaces.
func (h *OrgsHandler) ListWorkspaces(c *gin.Context) {
	orgID := c.Param("id")

	limit := 20
	offset := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	workspaces, pagination, err := h.orgStore.ListOrgWorkspaces(c.Request.Context(), orgID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list org workspaces"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":      workspaces,
		"pagination": pagination,
	})
}

// zeroBytes overwrites a byte slice with zeros to clear sensitive data from memory.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// IsOrgMember satisfies the middleware.orgMemberChecker interface by delegating to orgStore.
func (h *OrgsHandler) IsOrgMember(ctx context.Context, orgID, userID string) (bool, error) {
	return h.orgStore.IsOrgMember(ctx, orgID, userID)
}

// IsOrgAdmin satisfies the middleware.orgMemberChecker interface by delegating to orgStore.
func (h *OrgsHandler) IsOrgAdmin(ctx context.Context, orgID, userID string) (bool, error) {
	return h.orgStore.IsOrgAdmin(ctx, orgID, userID)
}
