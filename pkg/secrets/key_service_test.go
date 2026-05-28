package secrets

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- In-memory mocks ---

type mockKeyStore struct {
	mu      sync.Mutex
	records map[string]*UserKeyRecord
}

func newMockKeyStore() *mockKeyStore {
	return &mockKeyStore{records: make(map[string]*UserKeyRecord)}
}

func (m *mockKeyStore) GetUserKey(_ context.Context, userID string) (*UserKeyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[userID]
	if !ok {
		return nil, nil
	}
	// Return a copy
	cp := *r
	return &cp, nil
}

func (m *mockKeyStore) CreateUserKey(_ context.Context, record *UserKeyRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.records[record.UserID]; exists {
		return errors.New("user key already exists")
	}
	cp := *record
	m.records[record.UserID] = &cp
	return nil
}

func (m *mockKeyStore) UpdateWrappedDEK(_ context.Context, userID string, wrappedDEK []byte, salt []byte, keyVersion int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[userID]
	if !ok {
		return errors.New("user key not found")
	}
	r.WrappedDEK = wrappedDEK
	r.Salt = salt
	r.KeyVersion = keyVersion
	return nil
}

func (m *mockKeyStore) UpdateWrappedDEKRecovery(_ context.Context, userID string, wrappedDEKRecovery []byte, recoverySalt []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[userID]
	if !ok {
		return errors.New("user key not found")
	}
	r.WrappedDEKRecovery = wrappedDEKRecovery
	r.RecoverySalt = recoverySalt
	return nil
}

type mockDEKCache struct {
	mu    sync.Mutex
	store map[string][]byte
}

func newMockDEKCache() *mockDEKCache {
	return &mockDEKCache{store: make(map[string][]byte)}
}

func (m *mockDEKCache) CacheDEK(_ context.Context, sessionID string, dek []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(dek))
	copy(cp, dek)
	m.store[sessionID] = cp
	return nil
}

func (m *mockDEKCache) GetDEK(_ context.Context, sessionID string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dek, ok := m.store[sessionID]
	if !ok {
		return nil, nil
	}
	return dek, nil
}

func (m *mockDEKCache) EvictDEK(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, sessionID)
	return nil
}

// --- Tests ---

func TestKeyService_InitializeUserKeys(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	recoveryHex, err := svc.InitializeUserKeys(ctx, "user-1", []byte("password123"))
	if err != nil {
		t.Fatalf("InitializeUserKeys failed: %v", err)
	}

	// Recovery key should be valid hex, 16 bytes = 32 hex chars
	recoveryBytes, err := hex.DecodeString(recoveryHex)
	if err != nil {
		t.Fatalf("Recovery key is not valid hex: %v", err)
	}
	if len(recoveryBytes) != 16 {
		t.Errorf("Recovery key should be 16 bytes, got %d", len(recoveryBytes))
	}

	// Verify record was stored
	record, err := store.GetUserKey(ctx, "user-1")
	if err != nil {
		t.Fatalf("GetUserKey failed: %v", err)
	}
	if record == nil {
		t.Fatal("Expected user key record to exist")
	}
	if record.KeyVersion != 1 {
		t.Errorf("Expected key version 1, got %d", record.KeyVersion)
	}
	if len(record.WrappedDEK) == 0 {
		t.Error("WrappedDEK should not be empty")
	}
	if len(record.WrappedDEKRecovery) == 0 {
		t.Error("WrappedDEKRecovery should not be empty")
	}
	if len(record.Salt) != 32 {
		t.Errorf("Salt should be 32 bytes, got %d", len(record.Salt))
	}
	if len(record.RecoverySalt) != 32 {
		t.Errorf("RecoverySalt should be 32 bytes, got %d", len(record.RecoverySalt))
	}
}

func TestKeyService_InitializeUserKeys_DuplicateUser(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	_, err := svc.InitializeUserKeys(ctx, "user-1", []byte("password"))
	if err != nil {
		t.Fatalf("First init failed: %v", err)
	}

	_, err = svc.InitializeUserKeys(ctx, "user-1", []byte("password"))
	if err == nil {
		t.Error("Second init for same user should fail")
	}
}

func TestKeyService_UnlockDEK(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	password := []byte("my-password")
	_, err := svc.InitializeUserKeys(ctx, "user-1", password)
	if err != nil {
		t.Fatalf("InitializeUserKeys failed: %v", err)
	}

	// Unlock (simulate login)
	err = svc.UnlockDEK(ctx, "user-1", password, "session-abc", 24*time.Hour)
	if err != nil {
		t.Fatalf("UnlockDEK failed: %v", err)
	}

	// DEK should be cached
	dek, err := cache.GetDEK(ctx, "session-abc")
	if err != nil {
		t.Fatalf("GetDEK failed: %v", err)
	}
	if dek == nil {
		t.Error("DEK should be cached after unlock")
	}
	if len(dek) != 32 {
		t.Errorf("Cached DEK should be 32 bytes, got %d", len(dek))
	}
}

func TestKeyService_UnlockDEK_WrongPassword(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	_, _ = svc.InitializeUserKeys(ctx, "user-1", []byte("correct-password"))

	err := svc.UnlockDEK(ctx, "user-1", []byte("wrong-password"), "session-1", time.Hour)
	if err == nil {
		t.Error("UnlockDEK with wrong password should fail")
	}
}

func TestKeyService_UnlockDEK_NoKeys(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	// User without keys (legacy user) — should succeed silently
	err := svc.UnlockDEK(ctx, "user-no-keys", []byte("password"), "session-1", time.Hour)
	if err != nil {
		t.Errorf("UnlockDEK for user without keys should succeed silently, got: %v", err)
	}
}

func TestKeyService_EvictDEK(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	password := []byte("password")
	_, _ = svc.InitializeUserKeys(ctx, "user-1", password)
	_ = svc.UnlockDEK(ctx, "user-1", password, "session-1", time.Hour)

	// Verify cached
	if !svc.DEKAvailable(ctx, "session-1") {
		t.Fatal("DEK should be available before eviction")
	}

	// Evict
	err := svc.EvictDEK(ctx, "session-1")
	if err != nil {
		t.Fatalf("EvictDEK failed: %v", err)
	}

	// Verify evicted
	if svc.DEKAvailable(ctx, "session-1") {
		t.Error("DEK should not be available after eviction")
	}
}

func TestKeyService_GetDEK(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	password := []byte("password")
	_, _ = svc.InitializeUserKeys(ctx, "user-1", password)
	_ = svc.UnlockDEK(ctx, "user-1", password, "session-1", time.Hour)

	dek, err := svc.GetDEK(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetDEK failed: %v", err)
	}
	if len(dek) != 32 {
		t.Errorf("DEK should be 32 bytes, got %d", len(dek))
	}
}

func TestKeyService_GetDEK_NotCached(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	_, err := svc.GetDEK(ctx, "nonexistent-session")
	if err == nil {
		t.Error("GetDEK for nonexistent session should fail")
	}
}

func TestKeyService_ChangePassword(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	oldPassword := []byte("old-password")
	newPassword := []byte("new-password")

	_, _ = svc.InitializeUserKeys(ctx, "user-1", oldPassword)

	// Unlock with old password to get DEK
	_ = svc.UnlockDEK(ctx, "user-1", oldPassword, "session-before", time.Hour)
	dekBefore, _ := svc.GetDEK(ctx, "session-before")

	// Change password
	err := svc.ChangePassword(ctx, "user-1", oldPassword, newPassword)
	if err != nil {
		t.Fatalf("ChangePassword failed: %v", err)
	}

	// Old password should no longer work
	err = svc.UnlockDEK(ctx, "user-1", oldPassword, "session-old", time.Hour)
	if err == nil {
		t.Error("Old password should no longer unlock DEK")
	}

	// New password should work
	err = svc.UnlockDEK(ctx, "user-1", newPassword, "session-new", time.Hour)
	if err != nil {
		t.Fatalf("New password should unlock DEK: %v", err)
	}

	// DEK should be the same (password change doesn't rotate DEK)
	dekAfter, _ := svc.GetDEK(ctx, "session-new")
	if len(dekBefore) != len(dekAfter) {
		t.Error("DEK length changed after password change")
	}
	for i := range dekBefore {
		if dekBefore[i] != dekAfter[i] {
			t.Error("DEK changed after password change — should remain the same")
			break
		}
	}
}

func TestKeyService_ChangePassword_WrongOldPassword(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	_, _ = svc.InitializeUserKeys(ctx, "user-1", []byte("correct"))

	err := svc.ChangePassword(ctx, "user-1", []byte("wrong"), []byte("new"))
	if err == nil {
		t.Error("ChangePassword with wrong old password should fail")
	}
}

func TestKeyService_ResetWithRecoveryKey(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	password := []byte("original-password")
	recoveryHex, _ := svc.InitializeUserKeys(ctx, "user-1", password)

	// Unlock to get original DEK
	_ = svc.UnlockDEK(ctx, "user-1", password, "session-orig", time.Hour)
	originalDEK, _ := svc.GetDEK(ctx, "session-orig")

	// Reset with recovery key
	newPassword := []byte("new-password-after-reset")
	newRecoveryHex, err := svc.ResetWithRecoveryKey(ctx, "user-1", recoveryHex, newPassword)
	if err != nil {
		t.Fatalf("ResetWithRecoveryKey failed: %v", err)
	}

	// New recovery key should be different
	if newRecoveryHex == recoveryHex {
		t.Error("New recovery key should differ from old one")
	}

	// New password should work
	err = svc.UnlockDEK(ctx, "user-1", newPassword, "session-reset", time.Hour)
	if err != nil {
		t.Fatalf("New password after reset should work: %v", err)
	}

	// DEK should be unchanged
	resetDEK, _ := svc.GetDEK(ctx, "session-reset")
	for i := range originalDEK {
		if originalDEK[i] != resetDEK[i] {
			t.Error("DEK should be unchanged after recovery reset")
			break
		}
	}

	// Old recovery key should no longer work
	_, err = svc.ResetWithRecoveryKey(ctx, "user-1", recoveryHex, []byte("another"))
	if err == nil {
		t.Error("Old recovery key should no longer work after reset")
	}
}

func TestKeyService_ResetWithRecoveryKey_InvalidKey(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	_, _ = svc.InitializeUserKeys(ctx, "user-1", []byte("password"))

	_, err := svc.ResetWithRecoveryKey(ctx, "user-1", "deadbeefdeadbeefdeadbeefdeadbeef", []byte("new"))
	if err == nil {
		t.Error("Invalid recovery key should fail")
	}
}

func TestKeyService_ResetWithRecoveryKey_InvalidHex(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	_, _ = svc.InitializeUserKeys(ctx, "user-1", []byte("password"))

	_, err := svc.ResetWithRecoveryKey(ctx, "user-1", "not-valid-hex!", []byte("new"))
	if err == nil {
		t.Error("Invalid hex recovery key should fail")
	}
}

func TestKeyService_HasKeys(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	has, err := svc.HasKeys(ctx, "user-1")
	if err != nil {
		t.Fatalf("HasKeys failed: %v", err)
	}
	if has {
		t.Error("User without keys should return false")
	}

	_, _ = svc.InitializeUserKeys(ctx, "user-1", []byte("password"))

	has, err = svc.HasKeys(ctx, "user-1")
	if err != nil {
		t.Fatalf("HasKeys failed: %v", err)
	}
	if !has {
		t.Error("User with keys should return true")
	}
}

func TestKeyService_DEKAvailable(t *testing.T) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	ctx := context.Background()

	if svc.DEKAvailable(ctx, "no-session") {
		t.Error("DEK should not be available for nonexistent session")
	}

	_, _ = svc.InitializeUserKeys(ctx, "user-1", []byte("pw"))
	_ = svc.UnlockDEK(ctx, "user-1", []byte("pw"), "sess-1", time.Hour)

	if !svc.DEKAvailable(ctx, "sess-1") {
		t.Error("DEK should be available after unlock")
	}
}
