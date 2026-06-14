// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package email

import (
	"context"
	"fmt"
	"os"
)

// NoopProvider logs emails to stderr. Used in development and tests so the
// invitation flow can be exercised without an AWS account.
type NoopProvider struct{}

func (n *NoopProvider) Send(_ context.Context, msg Message) error {
	fmt.Fprintf(os.Stderr, "[email:noop] To=%s Subject=%s\n%s\n", msg.To, msg.Subject, msg.TextBody)
	return nil
}
