// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/billing"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// OrgBilling creates Checkout/Portal sessions for org subscription management
// (plan upgrades, billing portal access). Customer creation was removed with
// the self-service org-creation flow (design 0031 D1).
type OrgBilling interface {
	CreateCheckoutSession(ctx context.Context, customerID, planID, successURL, cancelURL string) (string, error)
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error)
}

// stripeBilling adapts a billing.CheckoutProvider to the OrgBilling interface.
type stripeBilling struct {
	provider billing.CheckoutProvider
}

func (s stripeBilling) CreateCheckoutSession(ctx context.Context, customerID, planID, successURL, cancelURL string) (string, error) {
	return s.provider.CreateCheckoutSession(ctx, customerID, planID, successURL, cancelURL)
}
func (s stripeBilling) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	return s.provider.CreatePortalSession(ctx, customerID, returnURL)
}

// NewOrgBilling adapts a billing.CheckoutProvider into an OrgBilling.
func NewOrgBilling(p billing.CheckoutProvider) OrgBilling {
	return stripeBilling{provider: p}
}

func isPlatformAdmin(c *gin.Context) bool {
	r, _ := c.Get("userRole")
	role, _ := r.(string)
	return strings.EqualFold(role, "admin")
}

// resolveCustomerID returns the Stripe customer id linked to the org, if any.
// This is a read against billing_accounts; the org-creation flow no longer
// creates a Stripe customer (self-service provisioning was removed in design
// 0031 Story 2). Customers may still be linked via the future billing portal.
func (h *OrgsHandler) resolveCustomerID(ctx context.Context, org *types.Organization) (string, error) {
	return h.orgStore.GetStripeCustomerID(ctx, org.ID)
}

// Checkout handles POST /api/v1/orgs/:id/billing/checkout. Creates a Stripe
// Checkout Session for the requested plan and returns its hosted URL.
func (h *OrgsHandler) Checkout(c *gin.Context) {
	orgID := c.Param("id")
	ctx := c.Request.Context()

	if h.billing == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "billing is not configured"})
		return
	}

	org, err := h.orgStore.GetOrg(ctx, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get organization"})
		return
	}
	if org == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	var req struct {
		PlanID types.OrgPlan `json:"planId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	customerID, err := h.resolveCustomerID(ctx, org)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve billing customer"})
		return
	}
	if customerID == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "no billing customer linked to this organization"})
		return
	}

	url, err := h.billing.CreateCheckoutSession(ctx, customerID, string(req.PlanID), h.successURL, h.cancelURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create checkout session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
}

// Portal handles POST /api/v1/orgs/:id/billing/portal. Creates a Stripe Customer
// Portal Session and returns its URL.
func (h *OrgsHandler) Portal(c *gin.Context) {
	orgID := c.Param("id")
	ctx := c.Request.Context()

	if h.billing == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "billing is not configured"})
		return
	}

	customerID, err := h.resolveCustomerID(ctx, &types.Organization{ID: orgID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve billing customer"})
		return
	}
	if customerID == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "no billing customer linked to this organization"})
		return
	}

	url, err := h.billing.CreatePortalSession(ctx, customerID, h.portalURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create portal session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
}
