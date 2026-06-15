// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/webhook"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// testLogger is shared with proxy_test.go (same package).

type fakeStripeEventStore struct {
	mu            sync.Mutex
	processed     map[string]string
	customerToOrg map[string]string
	statusUpdates []statusUpdateCall
	subUpdates    []subUpdateCall
	recordErr     error
	updateErr     error
}

type statusUpdateCall struct {
	orgID        string
	status       *types.OrgStatus
	subscription *types.OrgSubscriptionStatus
	plan         *types.OrgPlan
}

type subUpdateCall struct {
	ownerID        string
	ownerType      string
	provider       string
	subscriptionID string
}

func newFakeStripeEventStore() *fakeStripeEventStore {
	return &fakeStripeEventStore{
		processed:     make(map[string]string),
		customerToOrg: make(map[string]string),
	}
}

func (f *fakeStripeEventStore) RecordStripeEvent(_ context.Context, eventID, eventType string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recordErr != nil {
		return false, f.recordErr
	}
	if _, ok := f.processed[eventID]; ok {
		return false, nil
	}
	f.processed[eventID] = eventType
	return true, nil
}

func (f *fakeStripeEventStore) DeleteStripeEvent(_ context.Context, eventID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.processed, eventID)
	return nil
}

func (f *fakeStripeEventStore) SetBillingAccountSubscription(_ context.Context, ownerID, ownerType, provider, subscriptionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subUpdates = append(f.subUpdates, subUpdateCall{
		ownerID:        ownerID,
		ownerType:      ownerType,
		provider:       provider,
		subscriptionID: subscriptionID,
	})
	return nil
}

func (f *fakeStripeEventStore) GetOrgIDByStripeCustomer(_ context.Context, customerID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.customerToOrg[customerID], nil
}

func (f *fakeStripeEventStore) UpdateOrgStatus(_ context.Context, orgID string, status *types.OrgStatus, sub *types.OrgSubscriptionStatus, plan *types.OrgPlan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.statusUpdates = append(f.statusUpdates, statusUpdateCall{orgID: orgID, status: status, subscription: sub, plan: plan})
	return nil
}

// signingSecretProvider signs events with a real Stripe webhook secret so the
// HMAC verification path is exercised end-to-end.
type signingSecretProvider struct {
	secret string
}

func (s signingSecretProvider) CreateCustomer(_ context.Context, _, _ string) (string, error) {
	return "cus_test", nil
}
func (s signingSecretProvider) CreateCheckoutSession(_ context.Context, _, _, _, _ string) (string, error) {
	return "https://checkout.example.com", nil
}
func (s signingSecretProvider) CreatePortalSession(_ context.Context, _, _ string) (string, error) {
	return "https://portal.example.com", nil
}
func (s signingSecretProvider) ConstructWebhookEvent(payload []byte, signature string) (stripe.Event, error) {
	return webhook.ConstructEventWithOptions(payload, signature, s.secret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
}

func signEvent(t *testing.T, payload []byte, secret string) string {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  secret,
	})
	return signed.Header
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// buildStripeEvent builds a stripe.Event envelope with the given type and a
// data.object payload, serialized as standard Stripe webhook JSON.
func buildStripeEvent(t *testing.T, eventType, eventID string, dataObj any) []byte {
	t.Helper()
	env := map[string]any{
		"id":   eventID,
		"type": eventType,
		"data": map[string]any{"object": dataObj},
	}
	return mustMarshal(t, env)
}

func newWebhookTestRouter(t *testing.T, secret string, store *fakeStripeEventStore) (*gin.Engine, *StripeWebhookHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewStripeWebhookHandler(signingSecretProvider{secret: secret}, store, &testLogger{})
	r := gin.New()
	r.POST("/api/v1/webhooks/stripe", h.HandleWebhook)
	return r, h
}

func postWebhook(t *testing.T, r *gin.Engine, payload []byte, signature string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("Stripe-Signature", signature)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestWebhook_InvalidSignature_Rejected(t *testing.T) {
	store := newFakeStripeEventStore()
	r, _ := newWebhookTestRouter(t, "whsec_real", store)

	payload := buildStripeEvent(t, "checkout.session.completed", "evt_1", map[string]any{"customer": "cus_1"})
	w := postWebhook(t, r, payload, "t=1,v1=badsignature")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad signature, got %d body=%s", w.Code, w.Body.String())
	}
	if len(store.statusUpdates) != 0 {
		t.Fatalf("no status updates should occur on bad signature, got %d", len(store.statusUpdates))
	}
}

func TestWebhook_CheckoutCompleted_ActivatesOrg(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "checkout.session.completed", "evt_checkout_1", map[string]any{
		"customer":     "cus_org1",
		"subscription": "sub_1",
		"mode":         "subscription",
	})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(store.statusUpdates) != 1 {
		t.Fatalf("expected 1 status update, got %d", len(store.statusUpdates))
	}
	upd := store.statusUpdates[0]
	if upd.orgID != "org-uuid-1" {
		t.Errorf("expected orgID org-uuid-1, got %s", upd.orgID)
	}
	if upd.status == nil || *upd.status != types.OrgStatusActive {
		t.Errorf("expected status active, got %+v", upd.status)
	}
	if upd.subscription == nil || *upd.subscription != types.SubscriptionActive {
		t.Errorf("expected subscription active, got %+v", upd.subscription)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.subUpdates) != 1 {
		t.Fatalf("expected 1 subscription persistence call, got %d", len(store.subUpdates))
	}
	sub := store.subUpdates[0]
	if sub.ownerID != "org-uuid-1" {
		t.Errorf("expected ownerID org-uuid-1, got %s", sub.ownerID)
	}
	if sub.subscriptionID != "sub_1" {
		t.Errorf("expected subscriptionID sub_1, got %s", sub.subscriptionID)
	}
	if sub.provider != "stripe" {
		t.Errorf("expected provider stripe, got %s", sub.provider)
	}
}

func TestWebhook_DispatchFailure_ReleasesClaimForRetry(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	store.updateErr = errors.New("transient db outage")
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "checkout.session.completed", "evt_dispatch_fail", map[string]any{"customer": "cus_org1"})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on dispatch failure, got %d", w.Code)
	}
	store.mu.Lock()
	_, stillClaimed := store.processed["evt_dispatch_fail"]
	store.mu.Unlock()
	if stillClaimed {
		t.Fatal("dispatch failure must release the dedup claim so Stripe can retry; claim still held")
	}

	store.updateErr = nil
	w2 := postWebhook(t, r, payload, sig)
	if w2.Code != http.StatusOK {
		t.Fatalf("retry after released claim must succeed, got %d", w2.Code)
	}
	if len(store.statusUpdates) != 1 {
		t.Fatalf("retry must apply the update exactly once, got %d updates", len(store.statusUpdates))
	}
}

func TestWebhook_DuplicateEvent_Idempotent(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "checkout.session.completed", "evt_dup", map[string]any{"customer": "cus_org1"})
	sig := signEvent(t, payload, secret)

	w1 := postWebhook(t, r, payload, sig)
	if w1.Code != http.StatusOK {
		t.Fatalf("first delivery expected 200, got %d", w1.Code)
	}
	w2 := postWebhook(t, r, payload, sig)
	if w2.Code != http.StatusOK {
		t.Fatalf("duplicate delivery expected 200, got %d", w2.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &body)
	if body["status"] != "duplicate" {
		t.Fatalf("expected duplicate marker, got %s", w2.Body.String())
	}
	if len(store.statusUpdates) != 1 {
		t.Fatalf("duplicate must not reprocess; expected 1 update, got %d", len(store.statusUpdates))
	}
}

func TestWebhook_PaymentFailed_MarksPastDue_OrgStaysActive(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "invoice.payment_failed", "evt_pf", map[string]any{"customer": "cus_org1"})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(store.statusUpdates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.statusUpdates))
	}
	upd := store.statusUpdates[0]
	if upd.status != nil {
		t.Errorf("payment_failed must NOT change operational status, got %+v", upd.status)
	}
	if upd.subscription == nil || *upd.subscription != types.SubscriptionPastDue {
		t.Errorf("expected subscription past_due, got %+v", upd.subscription)
	}
}

func TestWebhook_SubscriptionUpdated_Unpaid_SuspendsOrg(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "customer.subscription.updated", "evt_unpaid", map[string]any{
		"customer": "cus_org1",
		"status":   "unpaid",
	})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	upd := store.statusUpdates[0]
	if upd.status == nil || *upd.status != types.OrgStatusSuspended {
		t.Errorf("expected operational status suspended, got %+v", upd.status)
	}
	if upd.subscription == nil || *upd.subscription != types.SubscriptionUnpaid {
		t.Errorf("expected subscription unpaid, got %+v", upd.subscription)
	}
}

func TestWebhook_SubscriptionUpdated_Active_RecoversOrg(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "customer.subscription.updated", "evt_sub_active", map[string]any{
		"customer": "cus_org1",
		"status":   "active",
	})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	upd := store.statusUpdates[0]
	if upd.status == nil || *upd.status != types.OrgStatusActive {
		t.Errorf("expected operational status active, got %+v", upd.status)
	}
	if upd.subscription == nil || *upd.subscription != types.SubscriptionActive {
		t.Errorf("expected subscription active, got %+v", upd.subscription)
	}
}

func TestWebhook_SubscriptionUpdated_Canceled_SuspendsOrg(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "customer.subscription.updated", "evt_sub_canceled", map[string]any{
		"customer": "cus_org1",
		"status":   "canceled",
	})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	upd := store.statusUpdates[0]
	if upd.status == nil || *upd.status != types.OrgStatusSuspended {
		t.Errorf("expected operational status suspended, got %+v", upd.status)
	}
	if upd.subscription == nil || *upd.subscription != types.SubscriptionCanceled {
		t.Errorf("expected subscription canceled, got %+v", upd.subscription)
	}
}

func TestWebhook_SubscriptionDeleted_SuspendsOrg(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "customer.subscription.deleted", "evt_deleted", map[string]any{"customer": "cus_org1"})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	upd := store.statusUpdates[0]
	if upd.status == nil || *upd.status != types.OrgStatusSuspended {
		t.Errorf("expected suspended, got %+v", upd.status)
	}
	if upd.subscription == nil || *upd.subscription != types.SubscriptionCanceled {
		t.Errorf("expected subscription canceled, got %+v", upd.subscription)
	}
}

func TestWebhook_InvoicePaid_ReactivatesSuspendedOrg(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "invoice.paid", "evt_paid", map[string]any{"customer": "cus_org1"})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	upd := store.statusUpdates[0]
	if upd.status == nil || *upd.status != types.OrgStatusActive {
		t.Errorf("expected operational status active on invoice.paid, got %+v", upd.status)
	}
	if upd.subscription == nil || *upd.subscription != types.SubscriptionActive {
		t.Errorf("expected subscription active, got %+v", upd.subscription)
	}
}

func TestWebhook_UnknownCustomer_NoStateMutation(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	r, _ := newWebhookTestRouter(t, secret, store)

	payload := buildStripeEvent(t, "checkout.session.completed", "evt_unknown", map[string]any{"customer": "cus_nobody"})
	sig := signEvent(t, payload, secret)
	w := postWebhook(t, r, payload, sig)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (graceful skip), got %d", w.Code)
	}
	if len(store.statusUpdates) != 0 {
		t.Fatalf("expected no status updates for unknown customer, got %d", len(store.statusUpdates))
	}
}

func TestWebhook_PaymentFailureToSuspensionToRecovery_Sequence(t *testing.T) {
	const secret = "whsec_test"
	store := newFakeStripeEventStore()
	store.customerToOrg["cus_org1"] = "org-uuid-1"
	r, _ := newWebhookTestRouter(t, secret, store)

	deliver := func(eventType, eventID string, dataObj any) {
		t.Helper()
		payload := buildStripeEvent(t, eventType, eventID, dataObj)
		sig := signEvent(t, payload, secret)
		w := postWebhook(t, r, payload, sig)
		if w.Code != http.StatusOK {
			t.Fatalf("deliver %s expected 200, got %d body=%s", eventType, w.Code, w.Body.String())
		}
	}

	deliver("invoice.payment_failed", "evt_seq_1", map[string]any{"customer": "cus_org1"})
	deliver("customer.subscription.updated", "evt_seq_2", map[string]any{"customer": "cus_org1", "status": "unpaid"})
	deliver("invoice.paid", "evt_seq_3", map[string]any{"customer": "cus_org1"})

	if len(store.statusUpdates) != 3 {
		t.Fatalf("expected 3 status updates across the sequence, got %d", len(store.statusUpdates))
	}
	pastDue := store.statusUpdates[0]
	if pastDue.subscription == nil || *pastDue.subscription != types.SubscriptionPastDue {
		t.Errorf("step 1: expected past_due, got %+v", pastDue.subscription)
	}
	suspended := store.statusUpdates[1]
	if suspended.status == nil || *suspended.status != types.OrgStatusSuspended {
		t.Errorf("step 2: expected suspended, got %+v", suspended.status)
	}
	recovered := store.statusUpdates[2]
	if recovered.status == nil || *recovered.status != types.OrgStatusActive {
		t.Errorf("step 3: expected active, got %+v", recovered.status)
	}
}
