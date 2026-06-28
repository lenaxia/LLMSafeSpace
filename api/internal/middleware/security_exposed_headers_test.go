// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDefaultSecurityConfig_ExposedHeaders_IncludesPaginationHeaders pins
// the CORS Access-Control-Expose-Headers contract for response headers
// the SPA reads from cross-origin API responses.
//
// Background: in cross-origin Helm deployments (api.ingress enabled,
// frontend at a different origin), the browser silently strips any
// response header NOT named in Access-Control-Expose-Headers. Forgetting
// to list a header here means JavaScript sees `headers.get('Foo') === null`
// even when the server set it — a class of bug that is invisible in
// single-origin tests and dev environments.
//
// X-Next-Cursor in particular is the message-history pagination header
// (#440). If it's stripped, `nextCursor` is undefined, `hasNextPage` is
// false, the "Load earlier messages" button never renders, and users with
// long sessions cannot scroll to the start of the conversation.
//
// This test enumerates every header the server actively sets that the
// frontend needs to read. Adding a new exposed header here is cheaper
// than fielding "the button doesn't appear in production" tickets.
func TestDefaultSecurityConfig_ExposedHeaders_IncludesPaginationHeaders(t *testing.T) {
	cfg := DefaultSecurityConfig()
	exposed := make(map[string]bool, len(cfg.ExposedHeaders))
	for _, h := range cfg.ExposedHeaders {
		exposed[h] = true
	}

	required := []string{
		// Pagination — message history (#440).
		"X-Next-Cursor",
		// Observability — every response carries this for distributed tracing.
		"X-Request-ID",
		// Rate limiting — the SPA reads these to show "X of Y remaining".
		"X-RateLimit-Limit",
		"X-RateLimit-Remaining",
		"X-RateLimit-Reset",
	}
	for _, h := range required {
		assert.True(t, exposed[h],
			"DefaultSecurityConfig().ExposedHeaders must include %q so CORS browsers can read it; current=%v",
			h, cfg.ExposedHeaders)
	}
}
