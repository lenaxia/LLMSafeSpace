// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package billing

import (
	"context"
	"errors"
	"fmt"

	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/billingportal/session"
	checkout "github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/customer"
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
	// Populated from instance settings / config; empty entries cause
	// CreateCheckoutSession to return an error so misconfiguration fails loudly.
	PlanPrices map[string]string
}

// StripeProvider implements CheckoutProvider against the live Stripe API.
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
	sess, err := session.New(params)
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
