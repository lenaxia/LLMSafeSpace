// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package utilities

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/validation"
)

// TestSanitizeLabelValue_AllProducedValuesPassK8sValidation feeds a wide
// range of inputs through SanitizeLabelValue and asserts the output is
// a valid K8s label value. This is the contract the API server enforces
// at CRD-creation time; regressing it surfaces as
// "metadata.labels: Invalid value" 500 errors in production
// (worklog 0035 cluster validation).
func TestSanitizeLabelValue_AllProducedValuesPassK8sValidation(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"image-with-colon", "python:3.11"},
		{"image-with-tag-and-digest", "python:3.11@sha256:abcd1234"},
		{"slash-in-image", "ghcr.io/lenaxia/llmsafespace/base:dev"},
		{"plus-in-version", "node:18+experimental"},
		{"spaces", "Python 3.11 LTS"},
		{"unicode", "rust\u00e9"},
		{"too-long", strings.Repeat("a", 200)},
		{"leading-symbol", ":python:3.11"},
		{"trailing-symbol", "python:3.11:"},
		{"empty", ""},
		{"plain-alphanum", "python311"},
		{"with-underscore", "python_3_11"},
		{"with-hyphen", "python-3-11"},
		{"with-dot", "python.3.11"},
		{"only-symbols", "::::"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeLabelValue(tc.in)
			errs := validation.IsValidLabelValue(got)
			assert.Empty(t, errs,
				"SanitizeLabelValue(%q) = %q, must satisfy K8s label rules; got errors: %v",
				tc.in, got, errs)
		})
	}
}

func TestSanitizeLabelValue_PreservesValidInputs(t *testing.T) {
	// Already-valid inputs should pass through unchanged.
	cases := []string{
		"python311",
		"python-3-11",
		"python_3_11",
		"python.3.11",
		"abc123",
		"v1.0.0",
		"", // empty is a valid label value per K8s
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, in, SanitizeLabelValue(in))
		})
	}
}

func TestSanitizeLabelValue_KnownReplacements(t *testing.T) {
	// The exact mapping for known characters.
	cases := []struct {
		in  string
		out string
	}{
		{"python:3.11", "python_3.11"},
		{"a:b:c", "a_b_c"},
		{"a/b", "a_b"},
		{"a+b", "a_b"},
		{"a@b", "a_b"},
		{"a b", "a_b"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.out, SanitizeLabelValue(tc.in))
		})
	}
}

func TestSanitizeLabelValue_TruncatesTo63Chars(t *testing.T) {
	got := SanitizeLabelValue(strings.Repeat("a", 200))
	assert.LessOrEqual(t, len(got), 63)
	assert.Empty(t, validation.IsValidLabelValue(got))
}

func TestSanitizeLabelValue_TrimsLeadingAndTrailingNonAlphanum(t *testing.T) {
	// A leading/trailing '_' (post-replacement) makes the value invalid.
	assert.Equal(t, "python_3.11", SanitizeLabelValue(":python:3.11:"))
	assert.Equal(t, "abc", SanitizeLabelValue(".abc."))
	assert.Equal(t, "abc", SanitizeLabelValue("---abc---"))
}
