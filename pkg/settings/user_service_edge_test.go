// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"testing"
)

func TestUserService_Set_IntBoundary(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	// fontSize: min=10, max=24
	if err := svc.Set(context.Background(), "u1", "fontSize", 10); err != nil {
		t.Errorf("unexpected error for min: %v", err)
	}
	if err := svc.Set(context.Background(), "u1", "fontSize", 24); err != nil {
		t.Errorf("unexpected error for max: %v", err)
	}
	if err := svc.Set(context.Background(), "u1", "fontSize", 9); err == nil {
		t.Error("expected error below min")
	}
	if err := svc.Set(context.Background(), "u1", "fontSize", 25); err == nil {
		t.Error("expected error above max")
	}
}

func TestUserService_Set_AllEnumValues(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	for _, theme := range []string{"light", "dark", "system"} {
		if err := svc.Set(context.Background(), "u1", "theme", theme); err != nil {
			t.Errorf("unexpected error for theme %q: %v", theme, err)
		}
	}
}

func TestUserService_GetAll_ReturnsAllKeys(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	all, err := svc.GetAll(context.Background(), "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, def := range UserSettings() {
		if _, exists := all[def.Key]; !exists {
			t.Errorf("GetAll missing key %q", def.Key)
		}
	}
}

func TestUserService_Set_PreferredModel_EmptyString(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	// preferredModel has no pattern, empty string is valid
	if err := svc.Set(context.Background(), "u1", "preferredModel", ""); err != nil {
		t.Errorf("unexpected error for empty preferredModel: %v", err)
	}
}

func TestUserService_Set_FontSize_Boundaries(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	// fontSize: min=10, max=24
	if err := svc.Set(context.Background(), "u1", "fontSize", 10); err != nil {
		t.Errorf("unexpected error for min: %v", err)
	}
	if err := svc.Set(context.Background(), "u1", "fontSize", 24); err != nil {
		t.Errorf("unexpected error for max: %v", err)
	}
	if err := svc.Set(context.Background(), "u1", "fontSize", 9); err == nil {
		t.Error("expected error below min")
	}
	if err := svc.Set(context.Background(), "u1", "fontSize", 25); err == nil {
		t.Error("expected error above max")
	}
}

func TestUserService_MultipleUsers_Independent(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	_ = svc.Set(context.Background(), "alice", "fontSize", 18)
	_ = svc.Set(context.Background(), "bob", "fontSize", 12)

	aliceVal, _ := svc.GetInt(context.Background(), "alice", "fontSize")
	bobVal, _ := svc.GetInt(context.Background(), "bob", "fontSize")

	if aliceVal != 18 {
		t.Errorf("alice expected 18, got %d", aliceVal)
	}
	if bobVal != 12 {
		t.Errorf("bob expected 12, got %d", bobVal)
	}
}

func TestUserService_GetBool_TypeMismatch(t *testing.T) {
	store := newMockUserStore()
	store.setForUser("u1", "fontSize", 14) // int, not bool
	svc := newTestUserService(store)

	_, err := svc.GetBool(context.Background(), "u1", "fontSize")
	if err == nil {
		t.Error("expected type mismatch error")
	}
}

func TestUserService_GetInt_TypeMismatch(t *testing.T) {
	store := newMockUserStore()
	store.setForUser("u1", "theme", "dark") // string, not int
	svc := newTestUserService(store)

	_, err := svc.GetInt(context.Background(), "u1", "theme")
	if err == nil {
		t.Error("expected type mismatch error")
	}
}

func TestUserService_Set_WrongType_BoolAsString(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	err := svc.Set(context.Background(), "u1", "compactMode", "true")
	if err == nil {
		t.Error("expected error for string on bool setting")
	}
}

func TestUserService_Set_WrongType_IntAsString(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	err := svc.Set(context.Background(), "u1", "fontSize", "14")
	if err == nil {
		t.Error("expected error for string on int setting")
	}
}
