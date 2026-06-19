// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// fakeOrgBilling is a controllable OrgBilling for billing endpoint tests.
type fakeOrgBilling struct {
	checkoutURL   string
	checkoutErr   error
	portalURL     string
	portalErr     error
	checkoutCalls int
}

func (f *fakeOrgBilling) CreateCheckoutSession(_ context.Context, _, _, _, _ string) (string, error) {
	f.checkoutCalls++
	if f.checkoutErr != nil {
		return "", f.checkoutErr
	}
	if f.checkoutURL != "" {
		return f.checkoutURL, nil
	}
	return "https://checkout.stripe.com/c/pay/cs_test_1", nil
}

func (f *fakeOrgBilling) CreatePortalSession(_ context.Context, _, _ string) (string, error) {
	if f.portalErr != nil {
		return "", f.portalErr
	}
	if f.portalURL != "" {
		return f.portalURL, nil
	}
	return "https://billing.stripe.com/p/session/test", nil
}

func setupOrgTestRouterWithBilling(t *testing.T, store *mockOrgStore, billing OrgBilling, isPlatformAdmin bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewOrgsHandler(store, &mockOrgAuthService{userID: "admin-1"})
	if billing != nil {
		handler.SetBilling(billing, "https://app/success", "https://app/cancel", "https://app/portal")
	}
	router := gin.New()
	role := "user"
	if isPlatformAdmin {
		role = "admin"
	}
	router.Use(func(c *gin.Context) {
		c.Set("userID", "admin-1")
		c.Set("userRole", role)
		c.Next()
	})
	router.POST("/api/v1/orgs", handler.Create)
	router.POST("/api/v1/orgs/:id/billing/checkout", handler.Checkout)
	router.POST("/api/v1/orgs/:id/billing/portal", handler.Portal)
	return router
}

// The self-service org-creation flow was removed in design 0031 Story 2 (D1).
// POST /api/v1/orgs is now platform-admin only; see orgs_admin_create_test.go
// for the create-path coverage. The tests in this file cover the per-org
// billing endpoints (Checkout/Portal) that remain for future use.

func TestCheckout_RequiresPlanID(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Status: types.OrgStatusActive}
	store.billingAccounts["org-1"] = "cus_1"
	billing := &fakeOrgBilling{}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/billing/checkout", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing planId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCheckout_ReturnsURL(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Status: types.OrgStatusActive}
	store.billingAccounts["org-1"] = "cus_1"
	billing := &fakeOrgBilling{checkoutURL: "https://checkout.example.com/cs_upgrade"}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/billing/checkout", `{"planId":"business"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["url"] != "https://checkout.example.com/cs_upgrade" {
		t.Errorf("checkout url: got %q", resp["url"])
	}
}

func TestCheckout_NoCustomerLinked_Conflict(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Status: types.OrgStatusActive}
	billing := &fakeOrgBilling{}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/billing/checkout", `{"planId":"team"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for org without billing customer, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPortal_ReturnsURL(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Status: types.OrgStatusActive}
	store.billingAccounts["org-1"] = "cus_1"
	billing := &fakeOrgBilling{portalURL: "https://billing.stripe.com/p/portal_1"}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/billing/portal", ``)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["url"] != "https://billing.stripe.com/p/portal_1" {
		t.Errorf("portal url: got %q", resp["url"])
	}
}

func TestCheckout_NoBillingConfigured_503(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Status: types.OrgStatusActive}
	store.billingAccounts["org-1"] = "cus_1"
	router := setupOrgTestRouterWithBilling(t, store, nil, false)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/billing/checkout", `{"planId":"team"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when billing not configured, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPortal_NoCustomerLinked_Conflict(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	billing := &fakeOrgBilling{}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/billing/portal", ``)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for org without billing customer, got %d: %s", w.Code, w.Body.String())
	}
}
