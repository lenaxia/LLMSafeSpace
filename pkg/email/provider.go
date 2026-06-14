// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package email

import "context"

// EmailProvider sends transactional emails. Implementations: SESProvider
// (production), NoopProvider (dev/test).
type EmailProvider interface {
	Send(ctx context.Context, msg Message) error
}

// Message is a single outbound email.
type Message struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
}
