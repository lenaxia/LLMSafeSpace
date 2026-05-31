// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- Validation unhappy paths ---

func TestValidate_Int_NilMinMax(t *testing.T) {
	// No min/max constraints — any int is valid
	def := SettingDef{Key: "test", Type: TypeInt, Default: 0}
	if err := Validate(def, -999); err != nil {
		t.Errorf("expected no error without min/max, got %v", err)
	}
	if err := Validate(def, 999999); err != nil {
		t.Errorf("expected no error without min/max, got %v", err)
	}
}

func TestValidate_String_EmptyPattern_AcceptsAll(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeString, Default: "", Pattern: ""}
	if err := Validate(def, "anything at all!@#$%"); err != nil {
		t.Errorf("expected no error with empty pattern, got %v", err)
	}
}

func TestValidate_Enum_EmptySlice(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeEnum, Default: "", Enum: []string{}}
	err := Validate(def, "anything")
	if err == nil {
		t.Error("expected error when enum list is empty")
	}
}

func TestValidate_Bool_NilValue(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeBool, Default: true}
	err := Validate(def, nil)
	if err == nil {
		t.Error("expected error for nil on bool setting")
	}
}

func TestValidate_Int_NilValue(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeInt, Default: 5}
	err := Validate(def, nil)
	if err == nil {
		t.Error("expected error for nil on int setting")
	}
}

func TestValidate_String_NilValue(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeString, Default: ""}
	err := Validate(def, nil)
	if err == nil {
		t.Error("expected error for nil on string setting")
	}
}

func TestValidate_Strings_NilValue(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeStrings, Default: []string{}}
	err := Validate(def, nil)
	if err == nil {
		t.Error("expected error for nil on strings setting")
	}
}

func TestValidate_Int_Int64Value(t *testing.T) {
	def := SettingDef{Key: "test", Type: TypeInt, Default: 5, Min: intPtr(1), Max: intPtr(100)}
	if err := Validate(def, int64(50)); err != nil {
		t.Errorf("expected int64 to be accepted, got %v", err)
	}
}

// --- Instance service integration tests ---

func TestInstanceService_Integration_MultipleSetsThenGetAll(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	// Set multiple values
	_ = svc.Set(context.Background(), "auth.registrationEnabled", false)
	_ = svc.Set(context.Background(), "auth.lockoutAttempts", 10)
	_ = svc.Set(context.Background(), "rateLimiting.strategy", "sliding_window")
	_ = svc.Set(context.Background(), "workspace.defaultStorageSize", "5Gi")

	all, err := svc.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}

	if all["auth.registrationEnabled"] != false {
		t.Errorf("expected false, got %v", all["auth.registrationEnabled"])
	}
	// JSON numbers come back as float64
	if all["auth.lockoutAttempts"] != float64(10) {
		t.Errorf("expected 10, got %v (%T)", all["auth.lockoutAttempts"], all["auth.lockoutAttempts"])
	}
	if all["rateLimiting.strategy"] != "sliding_window" {
		t.Errorf("expected sliding_window, got %v", all["rateLimiting.strategy"])
	}
}

func TestInstanceService_Set_ThenGet_TypeConsistency(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)

	// Set bool, get bool
	_ = svc.Set(context.Background(), "auth.lockoutEnabled", true)
	b, _ := svc.GetBool(context.Background(), "auth.lockoutEnabled")
	if b != true {
		t.Error("bool round-trip failed")
	}

	// Set int, get int
	_ = svc.Set(context.Background(), "rateLimiting.defaultLimit", 500)
	n, _ := svc.GetInt(context.Background(), "rateLimiting.defaultLimit")
	if n != 500 {
		t.Errorf("int round-trip failed: got %d", n)
	}

	// Set string, get string
	_ = svc.Set(context.Background(), "workspace.defaultImage", "custom:v2")
	s, _ := svc.GetString(context.Background(), "workspace.defaultImage")
	if s != "custom:v2" {
		t.Errorf("string round-trip failed: got %q", s)
	}

	// Set strings, get strings
	_ = svc.Set(context.Background(), "workspace.defaultNetworkAccess.egressDomains", []string{"a.com", "b.com"})
	ss, _ := svc.GetStrings(context.Background(), "workspace.defaultNetworkAccess.egressDomains")
	if len(ss) != 2 || ss[0] != "a.com" {
		t.Errorf("strings round-trip failed: got %v", ss)
	}
}

func TestInstanceService_GetInt_OnBoolKey_TypeMismatch(t *testing.T) {
	store := newMockStore()
	store.set("auth.registrationEnabled", true)
	svc := newTestService(store)

	_, err := svc.GetInt(context.Background(), "auth.registrationEnabled")
	if err == nil {
		t.Error("expected type mismatch error getting int from bool key")
	}
}

func TestInstanceService_GetString_OnIntKey_TypeMismatch(t *testing.T) {
	store := newMockStore()
	store.set("auth.lockoutAttempts", 5)
	svc := newTestService(store)

	_, err := svc.GetString(context.Background(), "auth.lockoutAttempts")
	if err == nil {
		t.Error("expected type mismatch error getting string from int key")
	}
}

func TestInstanceService_GetStrings_OnStringKey_TypeMismatch(t *testing.T) {
	store := newMockStore()
	store.set("workspace.defaultImage", "img:latest")
	svc := newTestService(store)

	_, err := svc.GetStrings(context.Background(), "workspace.defaultImage")
	if err == nil {
		t.Error("expected type mismatch error getting strings from string key")
	}
}

// --- Concurrent write/read stress test ---

func TestInstanceService_StressTest_ConcurrentWritesAndReads(t *testing.T) {
	store := newMockStore()
	svc := newTestService(store)
	svc.ttl = 5 * time.Millisecond // Very short TTL to force cache refreshes

	ctx := context.Background()
	var wg sync.WaitGroup

	// 20 writers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			val := (n % 100) + 1 // 1-100
			_ = svc.Set(ctx, "auth.lockoutAttempts", val)
		}(i)
	}

	// 30 readers
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = svc.GetInt(ctx, "auth.lockoutAttempts")
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Should not panic, and final value should be valid
	val, err := svc.GetInt(ctx, "auth.lockoutAttempts")
	if err != nil {
		t.Fatalf("final read failed: %v", err)
	}
	if val < 1 || val > 100 {
		t.Errorf("final value out of range: %d", val)
	}
}

// --- Seed integration tests ---

func TestSeed_PartialExisting_OnlyMissingInserted(t *testing.T) {
	store := newMockSeedStore()
	// Pre-set 3 keys
	raw1, _ := json.Marshal(false)
	raw2, _ := json.Marshal(10)
	raw3, _ := json.Marshal("fixed_window")
	store.data["auth.registrationEnabled"] = raw1
	store.data["auth.lockoutAttempts"] = raw2
	store.data["rateLimiting.strategy"] = raw3

	result, err := Seed(context.Background(), store, &mockLogger{})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	expectedInserted := len(InstanceSettings()) - 3
	if result.Inserted != expectedInserted {
		t.Errorf("expected %d inserted, got %d", expectedInserted, result.Inserted)
	}
	if result.Skipped != 3 {
		t.Errorf("expected 3 skipped, got %d", result.Skipped)
	}
}

func TestSeed_MultipleOrphans(t *testing.T) {
	store := newMockSeedStore()
	store.data["old.removed.key1"] = json.RawMessage(`"stale1"`)
	store.data["old.removed.key2"] = json.RawMessage(`"stale2"`)
	store.data["old.removed.key3"] = json.RawMessage(`"stale3"`)

	result, err := Seed(context.Background(), store, &mockLogger{})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if len(result.Orphaned) != 3 {
		t.Errorf("expected 3 orphaned, got %d", len(result.Orphaned))
	}
}

// --- User service integration tests ---

func TestUserService_Integration_SetMultipleThenGetAll(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)
	ctx := context.Background()

	_ = svc.Set(ctx, "user-1", "theme", "dark")
	_ = svc.Set(ctx, "user-1", "fontSize", 18)
	_ = svc.Set(ctx, "user-1", "compactMode", true)

	all, err := svc.GetAll(ctx, "user-1")
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}

	if all["theme"] != "dark" {
		t.Errorf("expected dark, got %v", all["theme"])
	}
	if all["compactMode"] != true {
		t.Errorf("expected true, got %v", all["compactMode"])
	}
}

func TestUserService_Set_OverwritesPrevious(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)
	ctx := context.Background()

	_ = svc.Set(ctx, "u1", "theme", "light")
	_ = svc.Set(ctx, "u1", "theme", "dark")

	val, _ := svc.GetString(ctx, "u1", "theme")
	if val != "dark" {
		t.Errorf("expected dark after overwrite, got %q", val)
	}
}

func TestUserService_ConcurrentDifferentUsers(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-%d", n)
			_ = svc.Set(ctx, userID, "fontSize", (n%15)+10)
			_, _ = svc.GetInt(ctx, userID, "fontSize")
		}(i)
	}
	wg.Wait()
	// No panics = success
}

// --- Schema completeness tests ---

func TestInstanceSettings_AllEnumKeysHaveValues(t *testing.T) {
	for _, def := range InstanceSettings() {
		if def.Type == TypeEnum && len(def.Enum) == 0 {
			t.Errorf("enum setting %q has no enum values", def.Key)
		}
	}
}

func TestUserSettings_AllEnumKeysHaveValues(t *testing.T) {
	for _, def := range UserSettings() {
		if def.Type == TypeEnum && len(def.Enum) == 0 {
			t.Errorf("enum setting %q has no enum values", def.Key)
		}
	}
}

func TestInstanceSettings_IntKeysHaveMinMax(t *testing.T) {
	for _, def := range InstanceSettings() {
		if def.Type == TypeInt {
			if def.Min == nil && def.Max == nil {
				// Some int keys legitimately have no bounds (e.g. ttlDaysAfterSuspended min=0)
				// but all should have at least one bound
				// Actually check: ttlDaysAfterSuspended has Min=0, Max=365
				t.Errorf("int setting %q has no min or max", def.Key)
			}
		}
	}
}

func TestUserSettings_IntKeysHaveMinMax(t *testing.T) {
	for _, def := range UserSettings() {
		if def.Type == TypeInt {
			if def.Min == nil && def.Max == nil {
				t.Errorf("int setting %q has no min or max", def.Key)
			}
		}
	}
}

func TestAllSettings_DefaultTypeMatchesDeclaredType(t *testing.T) {
	for _, def := range AllSettings() {
		switch def.Type {
		case TypeBool:
			if _, ok := def.Default.(bool); !ok {
				t.Errorf("%q: default %v (%T) is not bool", def.Key, def.Default, def.Default)
			}
		case TypeInt:
			if _, ok := toInt(def.Default); !ok {
				t.Errorf("%q: default %v (%T) is not int", def.Key, def.Default, def.Default)
			}
		case TypeString, TypeEnum:
			if _, ok := def.Default.(string); !ok {
				t.Errorf("%q: default %v (%T) is not string", def.Key, def.Default, def.Default)
			}
		case TypeStrings:
			switch def.Default.(type) {
			case []string:
				// ok
			default:
				t.Errorf("%q: default %v (%T) is not []string", def.Key, def.Default, def.Default)
			}
		}
	}
}

func TestAllSettings_EnumDefaultIsInEnumList(t *testing.T) {
	for _, def := range AllSettings() {
		if def.Type == TypeEnum {
			defaultStr := def.Default.(string)
			found := false
			for _, v := range def.Enum {
				if v == defaultStr {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%q: default %q not in enum list %v", def.Key, defaultStr, def.Enum)
			}
		}
	}
}
