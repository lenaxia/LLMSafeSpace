// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// mockSeedStore implements SeedStore for testing.
type mockSeedStore struct {
	mu        sync.Mutex
	data      map[string]json.RawMessage
	getErr    error
	insertErr error
}

func newMockSeedStore() *mockSeedStore {
	return &mockSeedStore{data: make(map[string]json.RawMessage)}
}

func (m *mockSeedStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	cp := make(map[string]json.RawMessage, len(m.data))
	for k, v := range m.data {
		cp[k] = v
	}
	return cp, nil
}

func (m *mockSeedStore) InsertInstanceSettingIfMissing(_ context.Context, key string, value json.RawMessage) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.insertErr != nil {
		return false, m.insertErr
	}
	if _, exists := m.data[key]; exists {
		return false, nil
	}
	m.data[key] = value
	return true, nil
}

func TestSeed_FreshDB_InsertsAllDefaults(t *testing.T) {
	store := newMockSeedStore()
	result, err := Seed(context.Background(), store, &mockLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCount := len(InstanceSettings())
	if result.Inserted != expectedCount {
		t.Errorf("expected %d inserted, got %d", expectedCount, result.Inserted)
	}
	if result.Skipped != 0 {
		t.Errorf("expected 0 skipped, got %d", result.Skipped)
	}
	if len(result.Orphaned) != 0 {
		t.Errorf("expected 0 orphaned, got %v", result.Orphaned)
	}
}

func TestSeed_ExistingValues_NotOverwritten(t *testing.T) {
	store := newMockSeedStore()
	// Pre-set a value
	store.data["auth.registrationEnabled"] = json.RawMessage(`false`)

	result, err := Seed(context.Background(), store, &mockLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have skipped the pre-existing key
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.Skipped)
	}
	if result.Inserted != len(InstanceSettings())-1 {
		t.Errorf("expected %d inserted, got %d", len(InstanceSettings())-1, result.Inserted)
	}

	// Verify the pre-existing value was NOT overwritten
	val := string(store.data["auth.registrationEnabled"])
	if val != "false" {
		t.Errorf("expected pre-existing value preserved, got %s", val)
	}
}

func TestSeed_OrphanedKeys_Detected(t *testing.T) {
	store := newMockSeedStore()
	// Add a key that's not in the schema
	store.data["deprecated.oldSetting"] = json.RawMessage(`"stale"`)

	result, err := Seed(context.Background(), store, &mockLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Orphaned) != 1 || result.Orphaned[0] != "deprecated.oldSetting" {
		t.Errorf("expected orphaned [deprecated.oldSetting], got %v", result.Orphaned)
	}
}

func TestSeed_Idempotent(t *testing.T) {
	store := newMockSeedStore()

	// Run seed twice
	result1, err := Seed(context.Background(), store, &mockLogger{})
	if err != nil {
		t.Fatalf("first seed failed: %v", err)
	}
	result2, err := Seed(context.Background(), store, &mockLogger{})
	if err != nil {
		t.Fatalf("second seed failed: %v", err)
	}

	// Second run should skip all
	if result2.Inserted != 0 {
		t.Errorf("second seed should insert 0, got %d", result2.Inserted)
	}
	if result2.Skipped != result1.Inserted {
		t.Errorf("second seed should skip %d, got %d", result1.Inserted, result2.Skipped)
	}
}

func TestSeed_DBReadError(t *testing.T) {
	store := newMockSeedStore()
	store.getErr = fmt.Errorf("connection refused")

	_, err := Seed(context.Background(), store, &mockLogger{})
	if err == nil {
		t.Error("expected error when DB read fails")
	}
}

func TestSeed_DBInsertError(t *testing.T) {
	store := newMockSeedStore()
	store.insertErr = fmt.Errorf("write failed")

	_, err := Seed(context.Background(), store, &mockLogger{})
	if err == nil {
		t.Error("expected error when DB insert fails")
	}
}
