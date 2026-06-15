// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	stripe "github.com/stripe/stripe-go/v76"
	portal "github.com/stripe/stripe-go/v76/billingportal/session"
	checkout "github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/customer"
	"github.com/stripe/stripe-go/v76/usagerecord"
	"github.com/stripe/stripe-go/v76/webhook"
)

// CheckoutProvider creates Stripe Checkout and Customer Portal sessions and
// verifies webhook signatures. It is intentionally separate from
// BillingProvider (usage reporting) so callers depend only on what they need.
type CheckoutProvider interface {
	CreateCustomer(ctx context.Context, email, name string) (string, error)
	CreateCheckoutSession(ctx context.Context, customerID, planID, successURL, cancelURL string) (string, error)
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error)
	ConstructWebhookEvent(payload []byte, signature string) (stripe.Event, error)
}

// StripeConfig holds the credentials and price mapping for StripeProvider.
type StripeConfig struct {
	SecretKey     string
	WebhookSecret string
	// PlanPrices maps an internal plan id (e.g. "team") to a Stripe Price id.
	PlanPrices map[string]string
	// Meters maps a usage event type (e.g. "llm_tokens", "compute_seconds")
	// to a Stripe subscription item id for Metered Billing.
	Meters map[string]string
}

// StripeProvider implements CheckoutProvider and BillingProvider against the
// live Stripe API.
type StripeProvider struct {
	cfg StripeConfig
}

// NewStripeProvider sets the package-level stripe.Key and returns a provider.
// Returns an error if SecretKey is empty — callers should use
// NoopCheckoutProvider in that case.
func NewStripeProvider(cfg StripeConfig) (*StripeProvider, error) {
	if cfg.SecretKey == "" {
		return nil, errors.New("stripe secret key is required")
	}
	stripe.Key = cfg.SecretKey
	return &StripeProvider{cfg: cfg}, nil
}

func (s *StripeProvider) CreateCustomer(_ context.Context, email, name string) (string, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Name:  stripe.String(name),
	}
	c, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("create stripe customer: %w", err)
	}
	return c.ID, nil
}

func (s *StripeProvider) CreateCheckoutSession(ctx context.Context, customerID, planID, successURL, cancelURL string) (string, error) {
	priceID, ok := s.cfg.PlanPrices[planID]
	if !ok || priceID == "" {
		return "", fmt.Errorf("no stripe price configured for plan %q", planID)
	}
	params := &stripe.CheckoutSessionParams{
		Customer:   stripe.String(customerID),
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
	}
	params.Context = ctx
	sess, err := checkout.New(params)
	if err != nil {
		return "", fmt.Errorf("create stripe checkout session: %w", err)
	}
	return sess.URL, nil
}

func (s *StripeProvider) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}
	params.Context = ctx
	sess, err := portal.New(params)
	if err != nil {
		return "", fmt.Errorf("create stripe portal session: %w", err)
	}
	return sess.URL, nil
}

func (s *StripeProvider) ConstructWebhookEvent(payload []byte, signature string) (stripe.Event, error) {
	const maxPayloadBytes = 1 << 20
	if len(payload) > maxPayloadBytes {
		return stripe.Event{}, errors.New("webhook payload exceeds 1MB limit")
	}
	if s.cfg.WebhookSecret == "" {
		return stripe.Event{}, errors.New("stripe webhook secret not configured")
	}
	ev, err := webhook.ConstructEvent(payload, signature, s.cfg.WebhookSecret)
	if err != nil {
		return stripe.Event{}, fmt.Errorf("verify stripe signature: %w", err)
	}
	return ev, nil
}

// ReportUsage reports usage events to Stripe Metered Billing. Each event maps
// to a Stripe usage record on the subscription item for the corresponding
// meter (llm_tokens or compute_seconds).
func (s *StripeProvider) ReportUsage(ctx context.Context, events []UsageExportEvent) ([]int64, error) {
	ids := make([]int64, len(events))
	for i, event := range events {
		meterID, ok := s.cfg.Meters[event.EventType]
		if !ok || meterID == "" {
			ids[i] = 0
			continue
		}
		params := &stripe.UsageRecordParams{
			SubscriptionItem: stripe.String(meterID),
			Quantity:         stripe.Int64(event.Quantity),
			Timestamp:        stripe.Int64(parseUnixTimestamp(event.Timestamp)),
		}
		params.Context = ctx
		if event.IdempotencyKey != "" {
			params.SetIdempotencyKey(event.IdempotencyKey)
		}
		if _, err := usagerecord.New(params); err != nil {
			return ids[:i], fmt.Errorf("report usage for %s: %w", event.EventType, err)
		}
		ids[i] = 1
	}
	return ids, nil
}

// SuspendCustomer marks the Stripe customer as suspended (preserves data).
func (s *StripeProvider) SuspendCustomer(_ context.Context, externalID string) error {
	params := &stripe.CustomerParams{}
	params.AddMetadata("suspended", "true")
	if _, err := customer.Update(externalID, params); err != nil {
		return fmt.Errorf("mark customer %s suspended: %w", externalID, err)
	}
	return nil
}

func parseUnixTimestamp(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now().Unix()
	}
	return t.Unix()
}

// NoopCheckoutProvider is the dev/test CheckoutProvider. It never calls Stripe
// and returns deterministic placeholder values so the request path can be
// exercised without network access.
type NoopCheckoutProvider struct{}

func (n *NoopCheckoutProvider) CreateCustomer(_ context.Context, _, name string) (string, error) {
	return "cus_noop_" + name, nil
}

func (n *NoopCheckoutProvider) CreateCheckoutSession(_ context.Context, _, planID, _, _ string) (string, error) {
	return "https://checkout.stripe.com/noop#" + planID, nil
}

func (n *NoopCheckoutProvider) CreatePortalSession(_ context.Context, _, _ string) (string, error) {
	return "https://billing.stripe.com/noop", nil
}

func (n *NoopCheckoutProvider) ConstructWebhookEvent(_ []byte, _ string) (stripe.Event, error) {
	return stripe.Event{}, errors.New("noop checkout provider cannot verify webhooks")
}
