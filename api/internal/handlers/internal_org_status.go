// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// internalOrgStatusReader is the minimal store surface the internal org-status
// endpoint needs. *PgOrgStore satisfies it.
type internalOrgStatusReader interface {
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
}

// InternalOrgStatusHandler serves GET /api/v1/internal/orgs/:orgID/status —
// the cluster-internal endpoint the workspace controller polls (with a 30s
// cache) to drive org-suspension of workspaces (D20, US-43.19).
//
// This endpoint is intentionally NOT behind AuthMiddleware: the controller has
// no user identity. Defense-in-depth is provided by an optional shared-secret
// header (X-Internal-Token) read from LLMSAFESPACE_INTERNAL_TOKEN. When the env
// var is unset the endpoint is open — the cluster NetworkPolicy is the primary
// boundary (same opt-in model as /metrics). It MUST only ever return org
// status, never secrets.
type InternalOrgStatusHandler struct {
	store internalOrgStatusReader
}

// NewInternalOrgStatusHandler constructs the handler.
func NewInternalOrgStatusHandler(store internalOrgStatusReader) *InternalOrgStatusHandler {
	return &InternalOrgStatusHandler{store: store}
}

// GetOrgStatus handles GET /api/v1/internal/orgs/:orgID/status.
//
// Fail-safe: a missing org row (hard-deleted or never existed) returns
// {"status":"active"} so the controller does NOT suspend the workspace. Per
// D20, an unwarranted suspension (deleting a running pod) is more disruptive
// than leaving it active during an anomaly.
func (h *InternalOrgStatusHandler) GetOrgStatus(c *gin.Context) {
	if expected := os.Getenv("LLMSAFESPACE_INTERNAL_TOKEN"); expected != "" {
		if c.GetHeader("X-Internal-Token") != expected {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
	}

	orgID := c.Param("orgID")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "orgID required"})
		return
	}

	org, err := h.store.GetOrg(c.Request.Context(), orgID)
	if err != nil || org == nil {
		// Unknown / soft-deleted / lookup error → fail open (do not suspend).
		c.JSON(http.StatusOK, gin.H{"status": string(types.OrgStatusActive)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": string(org.Status)})
}
