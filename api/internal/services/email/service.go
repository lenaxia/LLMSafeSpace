// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package email provides the orchestration layer for outbound transactional
// email. It wraps a pkg/email.EmailProvider (SES or Noop) and centralises
// message construction so that email bodies and link shapes are defined in
// one place rather than scattered as inline fmt.Sprintf calls across handlers.
//
// The Service holds a resolved provider + baseURL. Handlers depend on the
// concrete *Service (the provider is the injectable seam, already an
// interface). baseURL is retained for the password-reset / email-verification
// flows (US-49.5/49.6) which build interstitial frontend links.
package email

import (
	"context"
	"errors"
	"strings"

	"github.com/lenaxia/llmsafespaces/pkg/email"
)

// ErrNotConfigured is returned when the Service has no provider (email is
// disabled). Callers use ProviderName() to distinguish noop from ses for UX;
// this sentinel guards against calling Send* on a nil-provider Service.
var ErrNotConfigured = errors.New("email is not configured")

// Service builds and sends transactional emails via the wrapped provider.
type Service struct {
	provider     email.EmailProvider
	baseURL      string
	providerName string // resolved label: "ses" | "noop"
}

// NewService constructs a Service. provider may be nil (email disabled); in
// that case Send* returns ErrNotConfigured. baseURL is the public origin used
// to build links in email bodies (consumed by US-49.5/49.6). providerName is
// the resolved provider label for UX ("ses", "noop"); empty or unknown values
// normalise to "noop".
func NewService(provider email.EmailProvider, baseURL, providerName string) *Service {
	return &Service{
		provider:     provider,
		baseURL:      baseURL,
		providerName: normaliseProviderName(providerName),
	}
}

// ProviderName returns the resolved provider label for UX ("ses" or "noop").
func (s *Service) ProviderName() string {
	return s.providerName
}

// SendTest sends a test email so an admin can verify SES wiring end-to-end.
func (s *Service) SendTest(ctx context.Context, to string) error {
	if s.provider == nil {
		return ErrNotConfigured
	}
	return s.provider.Send(ctx, email.Message{
		To:       to,
		Subject:  "LLMSafeSpaces test email",
		TextBody: "This is a test email from LLMSafeSpaces. If you received this, outbound email is working.",
		HTMLBody: "<p>This is a test email from LLMSafeSpaces. If you received this, outbound email is working.</p>",
	})
}

// normaliseProviderName maps the raw config value to the resolved label.
// Empty or unrecognised values become "noop" so the UX never shows a raw
// provider string the user can't interpret.
func normaliseProviderName(name string) string {
	switch strings.ToLower(name) {
	case "ses":
		return "ses"
	default:
		return "noop"
	}
}
