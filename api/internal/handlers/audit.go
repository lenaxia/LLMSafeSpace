// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// auditStore is the data-access surface for org-scoped audit reads.
type auditStore interface {
	ListOrgAudit(ctx context.Context, orgID string, limit, offset int) ([]*types.AuditEntry, *types.PaginationMetadata, error)
	ListAllAudit(ctx context.Context, filters types.AuditFilters) ([]*types.AuditEntry, *types.PaginationMetadata, error)
}

// AuditHandler handles GET /api/v1/orgs/:id/audit (admin-only).
type AuditHandler struct {
	store auditStore
}

// NewAuditHandler constructs the handler.
func NewAuditHandler(store auditStore) *AuditHandler {
	return &AuditHandler{store: store}
}

// List handles GET /api/v1/orgs/:id/audit.
func (h *AuditHandler) List(c *gin.Context) {
	orgID := c.Param("id")

	limit := 50
	offset := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	entries, pagination, err := h.store.ListOrgAudit(c.Request.Context(), orgID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit entries"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":      entries,
		"pagination": pagination,
	})
}

// ListCrossOrg handles GET /api/v1/admin/audit (platform-admin only). It reads
// the org_id / actor_id / domain / limit / offset query params, constructs an
// AuditFilters, and returns all matching audit entries across every org.
func (h *AuditHandler) ListCrossOrg(c *gin.Context) {
	var filters types.AuditFilters
	if v := c.Query("org_id"); v != "" {
		filters.OrgID = &v
	}
	if v := c.Query("actor_id"); v != "" {
		filters.ActorID = &v
	}
	if v := c.Query("domain"); v != "" {
		filters.Domain = &v
	}
	filters.Limit = 100
	filters.Offset = 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filters.Limit = n
			if filters.Limit > 500 {
				filters.Limit = 500
			}
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filters.Offset = n
		}
	}

	entries, pagination, err := h.store.ListAllAudit(c.Request.Context(), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit entries"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":      entries,
		"pagination": pagination,
	})
}
