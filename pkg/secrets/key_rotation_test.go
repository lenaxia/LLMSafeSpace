package secrets

import (
	"context"
	"testing"
	"time"
)

// newRotationTestService constructs a KeyService wired to an empty
// SecretStore. RotateKeyWithPassword refuses to run without a store
// (Bug 9 fix in worklog 0094) so every rotation test must provide one;
// these tests don't care about secrets, only about the key lifecycle.
func newRotationTestService() (*KeyService, *mockKeyStore, *mockDEKCache) {
	store := newMockKeyStore()
	cache := newMockDEKCache()
	svc := NewKeyService(store, cache)
	svc.SetSecretStore(newMockSecretStore())
	return svc, store, cache
}

func TestKeyService_RotateKeyWithPassword(t *testing.T) {
	svc, store, _ := newRotationTestService()
	ctx := context.Background()

	password := []byte("my-password")
	_, _ = svc.InitializeUserKeys(ctx, "user-1", password)
	_ = svc.UnlockDEK(ctx, "user-1", password, "sess-1", time.Hour)

	// Get DEK before rotation
	dekBefore, _ := svc.GetDEK(ctx, "sess-1")

	// Rotate
	newVersion, err := svc.RotateKeyWithPassword(ctx, "user-1", password, "sess-1", time.Hour)
	if err != nil {
		t.Fatalf("RotateKeyWithPassword failed: %v", err)
	}
	if newVersion != 2 {
		t.Errorf("Expected version 2, got %d", newVersion)
	}

	// DEK should be different after rotation
	dekAfter, _ := svc.GetDEK(ctx, "sess-1")
	if bytesEq(dekBefore, dekAfter) {
		t.Error("DEK should change after rotation")
	}

	// Verify key version in store
	record, _ := store.GetUserKey(ctx, "user-1")
	if record.KeyVersion != 2 {
		t.Errorf("Store key version should be 2, got %d", record.KeyVersion)
	}
}

func TestKeyService_RotateKeyWithPassword_WrongPassword(t *testing.T) {
	svc, _, _ := newRotationTestService()
	ctx := context.Background()

	_, _ = svc.InitializeUserKeys(ctx, "user-1", []byte("correct"))
	_ = svc.UnlockDEK(ctx, "user-1", []byte("correct"), "sess-1", time.Hour)

	_, err := svc.RotateKeyWithPassword(ctx, "user-1", []byte("wrong"), "sess-1", time.Hour)
	if err == nil {
		t.Error("RotateKeyWithPassword with wrong password should fail")
	}
}

func TestKeyService_RotateKeyWithPassword_MultipleRotations(t *testing.T) {
	svc, store, _ := newRotationTestService()
	ctx := context.Background()

	password := []byte("pw")
	_, _ = svc.InitializeUserKeys(ctx, "user-1", password)
	_ = svc.UnlockDEK(ctx, "user-1", password, "sess-1", time.Hour)

	// Rotate 3 times
	for i := 0; i < 3; i++ {
		v, err := svc.RotateKeyWithPassword(ctx, "user-1", password, "sess-1", time.Hour)
		if err != nil {
			t.Fatalf("Rotation %d failed: %v", i+1, err)
		}
		if v != i+2 {
			t.Errorf("Rotation %d: expected version %d, got %d", i+1, i+2, v)
		}
	}

	record, _ := store.GetUserKey(ctx, "user-1")
	if record.KeyVersion != 4 {
		t.Errorf("After 3 rotations, version should be 4, got %d", record.KeyVersion)
	}
}

func TestKeyService_RotateKeyWithPassword_LoginAfterRotation(t *testing.T) {
	svc, _, _ := newRotationTestService()
	ctx := context.Background()

	password := []byte("pw")
	_, _ = svc.InitializeUserKeys(ctx, "user-1", password)
	_ = svc.UnlockDEK(ctx, "user-1", password, "sess-1", time.Hour)

	// Rotate
	svc.RotateKeyWithPassword(ctx, "user-1", password, "sess-1", time.Hour)

	// Evict and re-login (simulates new session)
	svc.EvictDEK(ctx, "sess-1")

	// Should be able to unlock with same password (KEK unchanged, just wraps new DEK)
	err := svc.UnlockDEK(ctx, "user-1", password, "sess-2", time.Hour)
	if err != nil {
		t.Fatalf("UnlockDEK after rotation failed: %v", err)
	}

	dek, _ := svc.GetDEK(ctx, "sess-2")
	if dek == nil || len(dek) != 32 {
		t.Error("Should have valid DEK after re-login post-rotation")
	}
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
