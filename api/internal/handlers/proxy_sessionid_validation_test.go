// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

// Closes F1.1.2 (Epic 17 Phase 1) and RT-2.16 (Phase 2): the proxy
// handlers that accept `:sessionId` from the URL concatenate it into
// the upstream URL path without validation. A user with
// `sessionId = "../../../v1/admin"` would address an arbitrary
// upstream endpoint.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestG6_F112_ValidSessionIDsAreAccepted(t *testing.T) {
	for _, valid := range []string{
		"abc-123",
		"550e8400-e29b-41d4-a716-446655440000", // UUID v4
		"sess_alphanumeric123",
		"a", // 1-char minimum
	} {
		t.Run(valid, func(t *testing.T) {
			assert.NoError(t, validateSessionID(valid),
				"valid sessionId %q must pass", valid)
		})
	}
}

func TestG6_F112_RejectsTraversalSessionIDs(t *testing.T) {
	for _, payload := range []string{
		"../../../v1/admin",
		"..%2F..%2Fadmin",
		"sess/with/slash",
		"sess?query=evil",
		"sess#fragment",
		"sess..other",
		"",                        // empty
		"sess with space",         // whitespace
		"sess\nnewline",           // newline
		"sess\x00null",            // NUL
		"sess'quote",              // shell metacharacter
		"sess$(whoami)",           // shell substitution
		string(make([]byte, 130)), // overlong (130 chars; cap is 128)
	} {
		t.Run(payload, func(t *testing.T) {
			assert.Error(t, validateSessionID(payload),
				"adversarial sessionId %q must be rejected", payload)
		})
	}
}
