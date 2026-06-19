// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespaces/pkg/types"
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
// no user identity. The PRIMARY boundary is a mandatory shared-secret header
// (X-Internal-Token) read from LLMSAFESPACES_INTERNAL_TOKEN (F5, US-43.19).
// When the env var is unset the endpoint FAILS CLOSED with 403: serving it
// unauthenticated would let any pod that can route to the API enumerate which
// orgs are suspended. The chart sets the token on BOTH the API and the
// controller so a single mounted Secret configures both sides. The comparison
// is constant-time to avoid a timing leak of the shared secret. It MUST only
// ever return org status, never secrets.
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
	expected := os.Getenv("LLMSAFESPACES_INTERNAL_TOKEN")
	if expected == "" {
		// F5: fail closed. No shared secret configured → refuse rather than
		// serve unauthenticated. The controller cannot legitimately call this
		// endpoint without the token either, so 403 here means the chart has
		// not wired LLMSAFESPACES_INTERNAL_TOKEN (a deployment misconfiguration),
		// not a legitimate caller being blocked.
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "internal endpoint not configured"})
		return
	}
	// Constant-time compare to avoid leaking the shared secret via timing.
	if subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Internal-Token")), []byte(expected)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
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
