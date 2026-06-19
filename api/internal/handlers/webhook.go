// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/webhook"

	"github.com/lenaxia/llmsafespaces/pkg/billing"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// stripeEventStore is the data-access surface the webhook handler needs:
// idempotent event recording, customer→org resolution, and org-status writes.
// It is a caller-shaped interface (Rule 4) so the handler depends only on what
// it uses, not on the full OrgStore.
type stripeEventStore interface {
	RecordStripeEvent(ctx context.Context, eventID, eventType string) (bool, error)
	DeleteStripeEvent(ctx context.Context, eventID string) error
	GetOrgIDByStripeCustomer(ctx context.Context, stripeCustomerID string) (string, error)
	UpdateOrgStatus(ctx context.Context, orgID string, status *types.OrgStatus, subStatus *types.OrgSubscriptionStatus, planID *types.OrgPlan) error
	SetBillingAccountSubscription(ctx context.Context, ownerID, ownerType, provider, subscriptionID string) error
}

// StripeWebhookHandler receives and processes Stripe webhook deliveries. It
// verifies the HMAC signature, deduplicates via stripe_events, and dispatches by
// event type. It has no JWT auth — the signature is the credential.
type StripeWebhookHandler struct {
	provider billing.CheckoutProvider
	store    stripeEventStore
	logger   webHookLogger
}

type webHookLogger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, err error, args ...any)
}

// NewStripeWebhookHandler constructs the handler. provider must be able to
// verify webhook signatures (i.e. a *billing.StripeProvider, not the noop).
func NewStripeWebhookHandler(provider billing.CheckoutProvider, store stripeEventStore, logger webHookLogger) *StripeWebhookHandler {
	return &StripeWebhookHandler{provider: provider, store: store, logger: logger}
}

// HandleWebhook is POST /api/v1/webhooks/stripe.
func (h *StripeWebhookHandler) HandleWebhook(c *gin.Context) {
	if h == nil || h.provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stripe webhook processing not configured"})
		return
	}

	payload, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	event, err := h.provider.ConstructWebhookEvent(payload, c.GetHeader("Stripe-Signature"))
	if err != nil {
		if isSignatureError(err) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}
		h.logger.Error("stripe webhook parse failed", err, "eventID", "")
		c.JSON(http.StatusBadRequest, gin.H{"error": "malformed webhook"})
		return
	}

	// Claim the event via an idempotent insert. If the row already exists, the
	// event was fully processed on a prior delivery — return duplicate.
	inserted, err := h.store.RecordStripeEvent(c.Request.Context(), event.ID, string(event.Type))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record event"})
		return
	}
	if !inserted {
		c.JSON(http.StatusOK, gin.H{"status": "duplicate"})
		return
	}

	if err := h.dispatch(c.Request.Context(), event); err != nil {
		h.logger.Error("stripe webhook dispatch failed", err, "eventType", event.Type, "eventID", event.ID)
		// Release the claim so Stripe's retry can re-process the event. Without
		// this, a transient dispatch failure would permanently strand the event
		// (the retry would see the dedup row and skip dispatch).
		if delErr := h.store.DeleteStripeEvent(c.Request.Context(), event.ID); delErr != nil {
			h.logger.Error("stripe webhook: failed to release claim after dispatch failure", delErr, "eventID", event.ID)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "event processing failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// dispatch routes a verified, deduplicated event to the appropriate org-status
// transition. Unknown event types are logged and treated as success (Stripe
// retries on non-2xx; we only fail on events we understand and cannot process).
func (h *StripeWebhookHandler) dispatch(ctx context.Context, event stripe.Event) error {
	switch event.Type {
	case "checkout.session.completed":
		return h.onCheckoutCompleted(ctx, event)
	case "invoice.paid":
		return h.onInvoicePaid(ctx, event)
	case "invoice.payment_failed":
		return h.onPaymentFailed(ctx, event)
	case "customer.subscription.updated":
		return h.onSubscriptionUpdated(ctx, event)
	case "customer.subscription.deleted":
		return h.onSubscriptionDeleted(ctx, event)
	default:
		h.logger.Info("stripe webhook: unhandled event type", "eventType", event.Type, "eventID", event.ID)
		return nil
	}
}

// onCheckoutCompleted activates the org. The subscription's plan is mapped to an
// internal plan id; subscription_status becomes 'active' (or 'trialing' if Stripe
// reports a trial).
func (h *StripeWebhookHandler) onCheckoutCompleted(ctx context.Context, event stripe.Event) error {
	var obj struct {
		Customer     string `json:"customer"`
		Subscription string `json:"subscription"`
		Mode         string `json:"mode"`
	}
	if err := json.Unmarshal(event.Data.Raw, &obj); err != nil {
		return fmt.Errorf("unmarshal checkout.session.completed: %w", err)
	}

	orgID, err := h.store.GetOrgIDByStripeCustomer(ctx, obj.Customer)
	if err != nil {
		return fmt.Errorf("resolve org for customer %s: %w", obj.Customer, err)
	}
	if orgID == "" {
		h.logger.Warn("stripe webhook: checkout.completed for unknown customer", "customerID", obj.Customer, "eventID", event.ID)
		return nil
	}

	active := types.SubscriptionActive
	activeStatus := types.OrgStatusActive
	if err := h.store.UpdateOrgStatus(ctx, orgID, &activeStatus, &active, nil); err != nil {
		return fmt.Errorf("activate org %s: %w", orgID, err)
	}

	if obj.Subscription != "" {
		if err := h.store.SetBillingAccountSubscription(ctx, orgID, string(types.OwnerTypeOrg), "stripe", obj.Subscription); err != nil {
			return fmt.Errorf("persist subscription id for org %s: %w", orgID, err)
		}
	}
	return nil
}

// onInvoicePaid clears any past_due state. If the org was suspended due to
// non-payment, this reactivates it.
func (h *StripeWebhookHandler) onInvoicePaid(ctx context.Context, event stripe.Event) error {
	customerID, err := customerFromEvent(event)
	if err != nil {
		return fmt.Errorf("invoice.paid: %w", err)
	}
	orgID, err := h.store.GetOrgIDByStripeCustomer(ctx, customerID)
	if err != nil {
		return fmt.Errorf("resolve org for customer %s: %w", customerID, err)
	}
	if orgID == "" {
		return nil
	}
	active := types.SubscriptionActive
	activeStatus := types.OrgStatusActive
	return h.store.UpdateOrgStatus(ctx, orgID, &activeStatus, &active, nil)
}

// onPaymentFailed marks the subscription past_due. The org stays active during
// the Stripe Smart Retries grace period (D14).
func (h *StripeWebhookHandler) onPaymentFailed(ctx context.Context, event stripe.Event) error {
	customerID, err := customerFromEvent(event)
	if err != nil {
		return fmt.Errorf("invoice.payment_failed: %w", err)
	}
	orgID, err := h.store.GetOrgIDByStripeCustomer(ctx, customerID)
	if err != nil {
		return fmt.Errorf("resolve org for customer %s: %w", customerID, err)
	}
	if orgID == "" {
		return nil
	}
	pastDue := types.SubscriptionPastDue
	return h.store.UpdateOrgStatus(ctx, orgID, nil, &pastDue, nil)
}

// onSubscriptionUpdated reacts to Stripe's subscription lifecycle. When the
// status transitions to 'unpaid' (Smart Retries exhausted), the org is
// operationally suspended. Other statuses sync subscription_status only.
func (h *StripeWebhookHandler) onSubscriptionUpdated(ctx context.Context, event stripe.Event) error {
	var obj struct {
		Customer string `json:"customer"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(event.Data.Raw, &obj); err != nil {
		return fmt.Errorf("unmarshal customer.subscription.updated: %w", err)
	}
	orgID, err := h.store.GetOrgIDByStripeCustomer(ctx, obj.Customer)
	if err != nil {
		return fmt.Errorf("resolve org for customer %s: %w", obj.Customer, err)
	}
	if orgID == "" {
		return nil
	}

	switch obj.Status {
	case "unpaid":
		suspended := types.OrgStatusSuspended
		subStatus := types.SubscriptionUnpaid
		if err := h.store.UpdateOrgStatus(ctx, orgID, &suspended, &subStatus, nil); err != nil {
			return fmt.Errorf("suspend org %s: %w", orgID, err)
		}
	case "canceled":
		suspended := types.OrgStatusSuspended
		subStatus := types.SubscriptionCanceled
		if err := h.store.UpdateOrgStatus(ctx, orgID, &suspended, &subStatus, nil); err != nil {
			return fmt.Errorf("suspend org %s: %w", orgID, err)
		}
	case "past_due":
		pastDue := types.SubscriptionPastDue
		return h.store.UpdateOrgStatus(ctx, orgID, nil, &pastDue, nil)
	case "active", "trialing":
		sub := subscriptionStatusForStripe(obj.Status)
		activeStatus := types.OrgStatusActive
		return h.store.UpdateOrgStatus(ctx, orgID, &activeStatus, &sub, nil)
	}
	return nil
}

// onSubscriptionDeleted suspends the org (PVCs preserved per D20).
func (h *StripeWebhookHandler) onSubscriptionDeleted(ctx context.Context, event stripe.Event) error {
	customerID, err := customerFromEvent(event)
	if err != nil {
		return fmt.Errorf("customer.subscription.deleted: %w", err)
	}
	orgID, err := h.store.GetOrgIDByStripeCustomer(ctx, customerID)
	if err != nil {
		return fmt.Errorf("resolve org for customer %s: %w", customerID, err)
	}
	if orgID == "" {
		return nil
	}
	suspended := types.OrgStatusSuspended
	canceled := types.SubscriptionCanceled
	return h.store.UpdateOrgStatus(ctx, orgID, &suspended, &canceled, nil)
}

// customerFromEvent extracts the customer id from any event whose data object
// carries a top-level `customer` field (invoices and subscriptions do).
func customerFromEvent(event stripe.Event) (string, error) {
	var obj struct {
		Customer string `json:"customer"`
	}
	if err := json.Unmarshal(event.Data.Raw, &obj); err != nil {
		return "", fmt.Errorf("unmarshal %s: %w", event.Type, err)
	}
	if obj.Customer == "" {
		return "", fmt.Errorf("event %s missing customer field", event.Type)
	}
	return obj.Customer, nil
}

func subscriptionStatusForStripe(s string) types.OrgSubscriptionStatus {
	switch s {
	case "trialing":
		return types.SubscriptionTrialing
	case "past_due":
		return types.SubscriptionPastDue
	case "canceled":
		return types.SubscriptionCanceled
	case "unpaid":
		return types.SubscriptionUnpaid
	default:
		return types.SubscriptionActive
	}
}

// isSignatureError reports whether err is one of the stripe webhook signature
// verification failures (absent/invalid header, no matching signature, expired).
// Parse and API-version mismatches are NOT signature errors.
func isSignatureError(err error) bool {
	return errors.Is(err, webhook.ErrNotSigned) ||
		errors.Is(err, webhook.ErrInvalidHeader) ||
		errors.Is(err, webhook.ErrNoValidSignature) ||
		errors.Is(err, webhook.ErrTooOld)
}
