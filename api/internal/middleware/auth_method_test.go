// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import "testing"

// authMethodForToken classifies a credential into a low-cardinality method
// label for the auth_attempts_total metric. The dashboard's Auth Failure
// Ratio panel sums across methods, so misclassification is benign at the
// ratio level — but the per-method panel requires correct labels.
func TestAuthMethodForToken_Classifies(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"empty token returns missing", "", "missing"},
		{"lsp_-prefixed token returns apikey", "lsp_aaaaaaaaaaaaaaaa", "apikey"},
		// JWT (RFC 7519) is three base64url-encoded segments separated
		// by dots. utilities.IsAPIKey returns false for any string that
		// doesn't start with the configured prefix, so the JWT path
		// falls through to "jwt".
		{"JWT-shaped token returns jwt", "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1In0.s", "jwt"},
		// Edge: a non-JWT, non-prefixed credential still classifies as
		// "jwt". This is intentional fallback behavior — the metric
		// label is for operator dashboards, not security decisions, so
		// imprecise tokens collapse into a single bucket rather than
		// proliferating cardinality.
		{"unrecognized token shape falls back to jwt", "totally-not-a-token", "jwt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authMethodForToken(tc.token)
			if got != tc.want {
				t.Errorf("authMethodForToken(%q) = %q, want %q", tc.token, got, tc.want)
			}
		})
	}
}
