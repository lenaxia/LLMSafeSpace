// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package utilities

import (
	"strings"
)

// maxLabelValueLength is the K8s limit for a label value (DNS-1123).
const maxLabelValueLength = 63

// SanitizeLabelValue produces a string guaranteed to satisfy Kubernetes'
// label-value validation rule:
//
//	(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?
//
// The rule allows: empty string, alphanumerics, '-', '_', '.', with the
// constraint that the first and last character must be alphanumeric and
// the total length is at most 63.
//
// API consumers commonly pass values like "python:3.11" or
// "ghcr.io/foo/bar:tag" (image references). Without sanitization, the
// API server rejects the CRD with HTTP 500 at creation time; this is
// exactly the bug surfaced by the worklog 0035 cluster validation:
//
//	"Workspace.llmsafespaces.dev is invalid: metadata.labels: Invalid value:
//	 \"python:3.11\": a valid label must be an empty string or consist of
//	 alphanumeric characters, '-', '_' or '.', and must start and end with
//	 an alphanumeric character"
//
// Strategy:
//  1. Replace every disallowed character with '_'. We do not try to
//     preserve information; the label is for grouping/filtering only and
//     users should consult the spec.runtime field for the canonical value.
//  2. Truncate to 63 chars.
//  3. Trim leading/trailing non-alphanumerics so the result satisfies the
//     start/end-with-alphanumeric rule.
//
// An input that is entirely disallowed characters (e.g. "::::") returns
// the empty string, which is itself a valid label value.
func SanitizeLabelValue(v string) string {
	if v == "" {
		return ""
	}

	// Step 1: replace every disallowed character with '_'.
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		if isAllowedLabelChar(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()

	// Step 2: truncate to 63 chars.
	if len(out) > maxLabelValueLength {
		out = out[:maxLabelValueLength]
	}

	// Step 3: trim leading/trailing non-alphanumerics. The rule requires
	// the first AND last character to be alphanumeric; '-', '_', '.' are
	// allowed in the middle but not at the boundaries.
	out = strings.TrimFunc(out, isNotAlphanumeric)

	return out
}

func isAllowedLabelChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-' || r == '_' || r == '.':
		return true
	}
	return false
}

func isNotAlphanumeric(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return false
	case r >= 'A' && r <= 'Z':
		return false
	case r >= '0' && r <= '9':
		return false
	}
	return true
}
