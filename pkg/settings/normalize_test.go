// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import "testing"

// Tests for value normalization. The bug that motivated this:
// an admin saved workspace.defaultResources.memory = "8gi" through the
// admin UI. The setting had no pattern, so the lowercase value passed
// through validation, into the database, and finally into the
// Workspace CRD spec — where the validating webhook rejected it with
// the cryptic message "memory \"8gi\" does not match ^[0-9]+(Ki|Mi|Gi)$".
// User-visible: every workspace creation broke for every user.
//
// The fix has two layers: (1) Pattern in the schema rejects garbage
// at save time (covered in schema_test.go), and (2) normalization
// canonicalizes near-misses (lowercase units, GB→Gi, whitespace) so
// the user gets auto-correct for honest typos.
//
// These tests pin the normalization contract: what gets corrected
// silently, what gets passed through unchanged, and what falls
// through to the validation rejection path.

func TestNormalize_Memory_LowercaseUnit(t *testing.T) {
	idx := InstanceSettingIndex()
	def := idx["workspace.defaultResources.memory"]

	cases := map[string]string{
		"8gi":   "8Gi",
		"8mi":   "8Mi",
		"8ki":   "8Ki",
		"512mi": "512Mi",
		"1gi":   "1Gi",
	}
	for in, want := range cases {
		got := Normalize(def, in)
		if got != want {
			t.Errorf("Normalize(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNormalize_Memory_WhitespaceAndCaseSplit(t *testing.T) {
	idx := InstanceSettingIndex()
	def := idx["workspace.defaultResources.memory"]

	cases := map[string]string{
		"  8Gi  ": "8Gi",
		"8 Gi":    "8Gi",
		"8 GB":    "8Gi", // GB → Gi (most users mean binary-power-of-2)
		"8gB":     "8Gi",
		"8 gi":    "8Gi",
		"8\tGi":   "8Gi",
	}
	for in, want := range cases {
		got := Normalize(def, in)
		if got != want {
			t.Errorf("Normalize(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNormalize_Memory_AlreadyCanonical(t *testing.T) {
	// Idempotence: canonical inputs pass through unchanged.
	idx := InstanceSettingIndex()
	def := idx["workspace.defaultResources.memory"]

	canonical := []string{"512Mi", "1Gi", "8Gi", "16Gi", "1024Ki"}
	for _, v := range canonical {
		got := Normalize(def, v)
		if got != v {
			t.Errorf("Normalize(%q) = %q; want unchanged", v, got)
		}
	}
}

func TestNormalize_Memory_AmbiguousFallsThrough(t *testing.T) {
	// Unambiguous near-misses are normalized; everything else passes
	// through unchanged so the Pattern validator can reject it. The
	// normalizer's job is "auto-correct typos", not "guess wildly".
	idx := InstanceSettingIndex()
	def := idx["workspace.defaultResources.memory"]

	passthrough := []string{
		"banana",    // not a quantity
		"",          // empty
		"-1Gi",      // negative
		"8.5Gi",     // fractional
		"8gigabyte", // word, not a recognized unit token
		"8 G",       // bare G is ambiguous (could be Giga or Gibi)
	}
	for _, v := range passthrough {
		got := Normalize(def, v)
		// Normalize never errors; ambiguous inputs must pass through
		// unchanged so Validate sees the original and rejects via
		// Pattern.
		if got != v {
			t.Errorf("Normalize(%q) silently transformed to %q; "+
				"normalizer should pass ambiguous inputs through "+
				"so Pattern validation rejects them", v, got)
		}
	}
}

func TestNormalize_CPU_SuffixCase(t *testing.T) {
	idx := InstanceSettingIndex()
	def := idx["workspace.defaultResources.cpu"]

	cases := map[string]string{
		"500M":   "500m", // capital M millicores → lowercase
		" 500m ": "500m",
		"1000M":  "1000m",
		"500 m":  "500m",
	}
	for in, want := range cases {
		got := Normalize(def, in)
		if got != want {
			t.Errorf("Normalize(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNormalize_StorageSize_LowercaseUnit(t *testing.T) {
	idx := InstanceSettingIndex()
	def := idx["workspace.defaultStorageSize"]

	cases := map[string]string{
		"15gi":   "15Gi",
		"15GB":   "15Gi",
		" 15Gi ": "15Gi",
		"15 mi":  "15Mi",
	}
	for in, want := range cases {
		got := Normalize(def, in)
		if got != want {
			t.Errorf("Normalize(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNormalize_NonResourceSettings_PassThrough(t *testing.T) {
	// Settings that aren't quantity-shaped (instance.name, MOTD,
	// defaultImage) should pass through untouched. Normalization is
	// resource-specific.
	idx := InstanceSettingIndex()

	def := idx["instance.name"]
	got := Normalize(def, "  Mixed Case   With Spaces  ")
	if got != "  Mixed Case   With Spaces  " {
		t.Errorf("Normalize(instance.name) trimmed/touched a free-form string; got %q", got)
	}
}

func TestNormalize_PreservesNonStringTypes(t *testing.T) {
	// Bool, int, enum, strings — all pass through unchanged. The
	// signature uses `any` so the existing call sites don't have to
	// type-switch.
	idx := InstanceSettingIndex()

	tests := []struct {
		key   string
		value any
	}{
		{"auth.registrationEnabled", true},
		{"workspace.maxActiveWorkspacesPerUser", 10},
		{"workspace.defaultSecurityLevel", "high"},
	}
	for _, tc := range tests {
		def := idx[tc.key]
		got := Normalize(def, tc.value)
		// any-to-any equality is reasonable here because all our test
		// values are comparable scalar types.
		if got != tc.value {
			t.Errorf("Normalize(%s) = %v; want %v unchanged", tc.key, got, tc.value)
		}
	}
}

func TestNormalize_ThenValidate_FixesTheBug(t *testing.T) {
	// End-to-end pin: the exact reported failure mode. An admin types
	// "8gi" in the UI. The Set path runs Normalize then Validate. The
	// normalized value passes validation, gets stored as "8Gi", and
	// the workspace CRD it eventually reaches has the canonical
	// uppercase suffix the webhook accepts.
	idx := InstanceSettingIndex()
	def := idx["workspace.defaultResources.memory"]

	got := Normalize(def, "8gi")
	if got != "8Gi" {
		t.Fatalf("Normalize(\"8gi\") = %q; want %q", got, "8Gi")
	}
	if err := Validate(def, got); err != nil {
		t.Fatalf("normalized value %q failed Validate: %v", got, err)
	}
}
