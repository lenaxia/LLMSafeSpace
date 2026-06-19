// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/billing"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// orgPlanReader is the minimal store surface FeatureGuard needs. Concretely
// satisfied by *database.PgOrgStore.GetOrg. Kept separate from
// orgMemberChecker (Interface Segregation) because feature gating has a
// different reason to change than membership checks.
type orgPlanReader interface {
	GetOrg(ctx context.Context, orgID string) (*types.Organization, error)
}

// FeatureGuard returns Gin middleware that denies the request when the
// org identified by ":id" is on a plan that does not include the named
// feature. Feature names map 1:1 to the cases in billing.IsFeatureAllowed
// ("policies", "audit", "sso", "custom_credentials").
//
// FeatureGuard MUST run after OrgAdminGuard or OrgMemberGuard so that the
// caller has already been authenticated; it does not perform its own
// membership check. It performs a GetOrg lookup to read the plan; for
// admin-only routes the extra query is acceptable (low request volume).
//
// Unknown feature names are allowed (fail-open) to preserve forward
// compatibility with new features added in billing.IsFeatureAllowed.
func FeatureGuard(reader orgPlanReader, feature string) gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID := c.Param("id")
		if orgID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "org id required"})
			return
		}

		org, err := reader.GetOrg(c.Request.Context(), orgID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to load organization"})
			return
		}
		if org == nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "organization not found"})
			return
		}

		if !billing.IsFeatureAllowed(org.PlanID, feature) {
			c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{
				"error":   "feature not included in current plan",
				"feature": feature,
				"planId":  org.PlanID,
				"hint":    "upgrade your subscription to access this feature",
			})
			return
		}
		c.Next()
	}
}
