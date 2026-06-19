// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestInstanceService_GetInt_Float64FromJSON(t *testing.T) {
	// JSON unmarshals numbers as float64; verify the service handles this
	store := newMockStore()
	raw, _ := json.Marshal(float64(42))
	store.mu.Lock()
	store.data["auth.lockoutAttempts"] = raw
	store.mu.Unlock()
	svc := newTestService(store)

	val, err := svc.GetInt(context.Background(), "auth.lockoutAttempts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
}

func TestInstanceService_GetStrings_EmptySlice(t *testing.T) {
	store := newMockStore()
	store.set("workspace.defaultNetworkAccess.egressDomains", []string{})
	svc := newTestService(store)

	val, err := svc.GetStrings(context.Background(), "workspace.defaultNetworkAccess.egressDomains")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(val) != 0 {
		t.Errorf("expected empty slice, got %v", val)
	}
}

func TestInstanceService_GetStrings_Default(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	val, err := svc.GetStrings(context.Background(), "workspace.defaultNetworkAccess.egressDomains")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default is []string{}
	if val == nil {
		t.Error("expected non-nil empty slice from default")
	}
}

func TestInstanceService_Set_BoolTrue(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	if err := svc.Set(context.Background(), "auth.registrationEnabled", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, _ := svc.GetBool(context.Background(), "auth.registrationEnabled")
	if val != true {
		t.Errorf("expected true, got %v", val)
	}
}

func TestInstanceService_Set_BoolFalse(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	if err := svc.Set(context.Background(), "auth.registrationEnabled", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, _ := svc.GetBool(context.Background(), "auth.registrationEnabled")
	if val != false {
		t.Errorf("expected false, got %v", val)
	}
}

func TestInstanceService_Set_StringWithPattern(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	if err := svc.Set(context.Background(), "workspace.defaultStorageSize", "5Gi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, _ := svc.GetString(context.Background(), "workspace.defaultStorageSize")
	if val != "5Gi" {
		t.Errorf("expected 5Gi, got %q", val)
	}
}

func TestInstanceService_Set_StringPatternReject(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	err := svc.Set(context.Background(), "workspace.defaultStorageSize", "5TB")
	if err == nil {
		t.Error("expected validation error for invalid storage pattern")
	}
}

// Regression: production failure 2026-06-18.
// Admin saved workspace.defaultResources.memory = "8gi" (lowercase
// unit) via the admin UI. Without normalization, the lowercase value
// reached the database and broke every workspace creation when the
// validating webhook rejected it. With Normalize() running before
// Validate() in InstanceService.Set, "8gi" gets canonicalized to
// "8Gi" and stored as "8Gi". This pins the end-to-end fix.
func TestInstanceService_Set_NormalizesMemoryQuantity(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)
	ctx := context.Background()

	// The exact production input.
	if err := svc.Set(ctx, "workspace.defaultResources.memory", "8gi"); err != nil {
		t.Fatalf("Set(\"8gi\") errored — Normalize must canonicalize before Validate: %v", err)
	}

	// Stored value must be the canonical form, not the input.
	stored, err := svc.GetString(ctx, "workspace.defaultResources.memory")
	if err != nil {
		t.Fatalf("GetString errored: %v", err)
	}
	if stored != "8Gi" {
		t.Errorf("stored value = %q; want %q (canonical Kubernetes Quantity form)", stored, "8Gi")
	}
}

func TestInstanceService_Set_NormalizesMemoryUnitVariants(t *testing.T) {
	// Exhaustive matrix of normalizations the user might type.
	cases := map[string]string{
		"8gi":   "8Gi",
		"8GB":   "8Gi",
		"8 Gi":  "8Gi",
		"  4gi": "4Gi",
		"512mi": "512Mi",
	}
	for in, want := range cases {
		store := newMockStore()
		svc := newTestService(store)
		ctx := context.Background()

		if err := svc.Set(ctx, "workspace.defaultResources.memory", in); err != nil {
			t.Errorf("Set(%q) errored: %v", in, err)
			continue
		}
		got, err := svc.GetString(ctx, "workspace.defaultResources.memory")
		if err != nil {
			t.Errorf("GetString errored after Set(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Set(%q) → stored %q; want %q", in, got, want)
		}
	}
}

func TestInstanceService_Set_StillRejectsGarbage(t *testing.T) {
	// Normalize must NOT silently fix unambiguous-bad inputs into
	// something valid. "banana" stays "banana" through the normalizer
	// and gets rejected by Validate's pattern check.
	store := newMockStore()
	svc := newTestService(store)
	ctx := context.Background()

	rejected := []string{"banana", "8 G", "8.5Gi", "-1Gi", ""}
	for _, v := range rejected {
		if err := svc.Set(ctx, "workspace.defaultResources.memory", v); err == nil {
			t.Errorf("Set(%q) should have errored — Normalize must not silently fix unambiguous-bad input", v)
		}
	}
}

func TestInstanceService_Set_EnumValid(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	for _, strategy := range []string{"token_bucket", "fixed_window", "sliding_window"} {
		if err := svc.Set(context.Background(), "rateLimiting.strategy", strategy); err != nil {
			t.Errorf("unexpected error for strategy %q: %v", strategy, err)
		}
	}
}

func TestInstanceService_Set_IntBoundary(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	// Min boundary (1)
	if err := svc.Set(context.Background(), "auth.lockoutAttempts", 1); err != nil {
		t.Errorf("unexpected error for min boundary: %v", err)
	}
	// Max boundary (100)
	if err := svc.Set(context.Background(), "auth.lockoutAttempts", 100); err != nil {
		t.Errorf("unexpected error for max boundary: %v", err)
	}
	// Below min
	if err := svc.Set(context.Background(), "auth.lockoutAttempts", 0); err == nil {
		t.Error("expected error for below min")
	}
	// Above max
	if err := svc.Set(context.Background(), "auth.lockoutAttempts", 101); err == nil {
		t.Error("expected error for above max")
	}
}

func TestInstanceService_Set_StringsValid(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	domains := []string{"api.openai.com", "github.com"}
	if err := svc.Set(context.Background(), "workspace.defaultNetworkAccess.egressDomains", domains); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, _ := svc.GetStrings(context.Background(), "workspace.defaultNetworkAccess.egressDomains")
	if len(val) != 2 || val[0] != "api.openai.com" {
		t.Errorf("unexpected value: %v", val)
	}
}

func TestInstanceService_ConcurrentReadsAndWrites(t *testing.T) {
	store := newMockStore()
	store.set("auth.lockoutAttempts", 5)
	svc := newTestService(store)

	var wg sync.WaitGroup
	// 10 concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = svc.GetInt(context.Background(), "auth.lockoutAttempts")
			}
		}()
	}
	// 3 concurrent writers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_ = svc.Set(context.Background(), "auth.lockoutAttempts", (n*5)+j+1)
			}
		}(i)
	}
	wg.Wait()

	// Just verify no panics/races occurred and we can still read
	val, err := svc.GetInt(context.Background(), "auth.lockoutAttempts")
	if err != nil {
		t.Fatalf("unexpected error after concurrent access: %v", err)
	}
	if val < 1 || val > 100 {
		t.Errorf("value out of expected range: %d", val)
	}
}

func TestInstanceService_GetAll_IncludesAllSchemaKeys(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	all, err := svc.GetAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, def := range InstanceSettings() {
		if _, exists := all[def.Key]; !exists {
			t.Errorf("GetAll missing key %q", def.Key)
		}
	}
}

func TestInstanceService_Set_OverwritesPreviousValue(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	_ = svc.Set(context.Background(), "auth.lockoutAttempts", 5)
	_ = svc.Set(context.Background(), "auth.lockoutAttempts", 10)

	val, _ := svc.GetInt(context.Background(), "auth.lockoutAttempts")
	if val != 10 {
		t.Errorf("expected 10 after overwrite, got %d", val)
	}
}

func TestInstanceService_GetString_EmptyStringDefault(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	// workspace.defaultStorageClass defaults to ""
	val, err := svc.GetString(context.Background(), "workspace.defaultStorageClass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string default, got %q", val)
	}
}
