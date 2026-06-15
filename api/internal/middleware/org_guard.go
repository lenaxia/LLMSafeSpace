// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// orgMemberChecker is the minimal interface required by the org guard middlewares.
type orgMemberChecker interface {
	IsOrgMember(ctx context.Context, orgID, userID string) (bool, error)
	IsOrgAdmin(ctx context.Context, orgID, userID string) (bool, error)
}

// OrgMemberGuard returns Gin middleware that verifies the caller is a member
// of the org identified by the ":id" path parameter.
// Returns 403 for unauthorized callers (including members of soft-deleted orgs).
func OrgMemberGuard(store orgMemberChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("userID")
		uid, _ := userID.(string)
		if uid == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		orgID := c.Param("id")
		if orgID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "org id required"})
			return
		}

		ok, err := store.IsOrgMember(c.Request.Context(), orgID, uid)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to check org membership"})
			return
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this organization"})
			return
		}

		c.Next()
	}
}

// OrgAdminGuard returns Gin middleware that verifies the caller is an admin
// (role='admin') of the org identified by ":id". Returns 403 for non-admins
// and members of soft-deleted orgs.
func OrgAdminGuard(store orgMemberChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("userID")
		uid, _ := userID.(string)
		if uid == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		orgID := c.Param("id")
		if orgID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "org id required"})
			return
		}

		ok, err := store.IsOrgAdmin(c.Request.Context(), orgID, uid)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to check org admin status"})
			return
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}

		c.Next()
	}
}
