// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// fakeOrgBilling is a controllable OrgBilling for CreateOrg flow tests.
type fakeOrgBilling struct {
	customerID    string
	customerErr   error
	checkoutURL   string
	checkoutErr   error
	portalURL     string
	portalErr     error
	customerCalls int
	checkoutCalls int
}

func (f *fakeOrgBilling) CreateCustomer(_ context.Context, _, _ string) (string, error) {
	f.customerCalls++
	if f.customerErr != nil {
		return "", f.customerErr
	}
	if f.customerID != "" {
		return f.customerID, nil
	}
	return "cus_test_1", nil
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
	dekCache := newTestDEKCache()
	orgKeySvc := secrets.NewOrgKeyService(nil, dekCache)
	handler := NewOrgsHandler(store, orgKeySvc, dekCache, &mockOrgAuthService{userID: "admin-1"})
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

func TestCreateOrg_RegularUser_PendingActivationWithCheckoutURL(t *testing.T) {
	store := newMockOrgStore()
	store.salts["admin-1"] = make([]byte, 32)
	billing := &fakeOrgBilling{checkoutURL: "https://checkout.example.com/cs_1"}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Acme","slug":"ACME","password":"secretpass","planId":"team"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp types.CreateOrgResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != types.OrgStatusPendingActivation {
		t.Errorf("expected pending_activation, got %q", resp.Status)
	}
	if resp.CheckoutURL == "" {
		t.Errorf("expected non-empty checkout URL for regular user")
	}
	if resp.CheckoutURL != "https://checkout.example.com/cs_1" {
		t.Errorf("checkout URL: got %q", resp.CheckoutURL)
	}
	if billing.customerCalls != 1 {
		t.Errorf("expected 1 CreateCustomer call, got %d", billing.customerCalls)
	}
	if billing.checkoutCalls != 1 {
		t.Errorf("expected 1 CreateCheckoutSession call, got %d", billing.checkoutCalls)
	}
}

func TestCreateOrg_SlugLowercasedAndStored(t *testing.T) {
	store := newMockOrgStore()
	store.salts["admin-1"] = make([]byte, 32)
	billing := &fakeOrgBilling{}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Acme","slug":"AcMeCo","password":"secretpass"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	store.mu.Lock()
	var created *types.Organization
	for _, o := range store.orgs {
		created = o
	}
	store.mu.Unlock()
	if created == nil {
		t.Fatal("no org created")
	}
	if created.Slug != "acmeco" {
		t.Errorf("slug should be lowercased: got %q", created.Slug)
	}
	if !strings.EqualFold(created.Slug, "acmeco") {
		t.Errorf("slug mismatch")
	}
}

func TestCreateOrg_PlatformAdmin_ActiveEnterprise(t *testing.T) {
	store := newMockOrgStore()
	store.salts["admin-1"] = make([]byte, 32)
	billing := &fakeOrgBilling{}
	router := setupOrgTestRouterWithBilling(t, store, billing, true)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Enterprise Co","slug":"entco","password":"secretpass"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp types.CreateOrgResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != types.OrgStatusActive {
		t.Errorf("platform admin org must be active, got %q", resp.Status)
	}
	if resp.PlanID != types.PlanEnterprise {
		t.Errorf("platform admin org must be enterprise plan, got %q", resp.PlanID)
	}
	if resp.SubscriptionStatus != types.SubscriptionActive {
		t.Errorf("expected subscription active, got %q", resp.SubscriptionStatus)
	}
	if resp.CheckoutURL != "" {
		t.Errorf("platform admin org must NOT have a checkout URL, got %q", resp.CheckoutURL)
	}
	if billing.checkoutCalls != 0 {
		t.Errorf("platform admin must not trigger Stripe checkout, got %d calls", billing.checkoutCalls)
	}
}

func TestCreateOrg_NoBillingConfigured_StillCreatesPending(t *testing.T) {
	store := newMockOrgStore()
	store.salts["admin-1"] = make([]byte, 32)
	router := setupOrgTestRouterWithBilling(t, store, nil, false)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Dev Org","slug":"devorg","password":"secretpass"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 in dev mode without billing, got %d: %s", w.Code, w.Body.String())
	}
	var resp types.CreateOrgResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != types.OrgStatusPendingActivation {
		t.Errorf("expected pending_activation, got %q", resp.Status)
	}
	if resp.CheckoutURL != "" {
		t.Errorf("no checkout URL without billing config, got %q", resp.CheckoutURL)
	}
}

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

func TestCreateOrg_StripeCustomerCreationFails_LeavesPending(t *testing.T) {
	store := newMockOrgStore()
	store.salts["admin-1"] = make([]byte, 32)
	billing := &fakeOrgBilling{customerErr: errors.New("stripe down")}
	router := setupOrgTestRouterWithBilling(t, store, billing, false)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Acme","slug":"acme","password":"secretpass"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on customer creation failure, got %d: %s", w.Code, w.Body.String())
	}
	store.mu.Lock()
	var created *types.Organization
	for _, o := range store.orgs {
		created = o
	}
	store.mu.Unlock()
	if created != nil && created.Status != types.OrgStatusPendingActivation {
		t.Errorf("org should remain pending_activation after provisioning failure, got %q", created.Status)
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
