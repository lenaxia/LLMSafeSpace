package credentials

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
)

// --- Unhappy path tests for crypto ---

func TestEncrypt_NilPlaintext(t *testing.T) {
	ks := testKeySet()
	encrypted, _, err := Encrypt(ks, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error encrypting nil: %v", err)
	}
	// Should still decrypt to empty
	decrypted, err := Decrypt(ks, encrypted, nil)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if len(decrypted) != 0 {
		t.Errorf("expected empty decrypted, got %d bytes", len(decrypted))
	}
}

func TestEncrypt_LargePlaintext(t *testing.T) {
	ks := testKeySet()
	// 1MB of data
	plaintext := make([]byte, 1024*1024)
	rand.Read(plaintext)

	encrypted, _, err := Encrypt(ks, plaintext, []byte("large-test"))
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	decrypted, err := Decrypt(ks, encrypted, []byte("large-test"))
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if len(decrypted) != len(plaintext) {
		t.Errorf("size mismatch: %d vs %d", len(decrypted), len(plaintext))
	}
}

func TestDecrypt_CorruptedCiphertext_Fails(t *testing.T) {
	ks := testKeySet()
	plaintext := []byte("secret")
	encrypted, _, _ := Encrypt(ks, plaintext, []byte("id"))

	// Flip a byte in the ciphertext portion
	encrypted[len(encrypted)-1] ^= 0xFF

	_, err := Decrypt(ks, encrypted, []byte("id"))
	if err == nil {
		t.Error("expected error for corrupted ciphertext")
	}
}

func TestDecrypt_CorruptedNonce_Fails(t *testing.T) {
	ks := testKeySet()
	plaintext := []byte("secret")
	encrypted, _, _ := Encrypt(ks, plaintext, []byte("id"))

	// Flip a byte in the nonce (byte index 1 is first nonce byte)
	if len(encrypted) > 2 {
		encrypted[1] ^= 0xFF
	}

	_, err := Decrypt(ks, encrypted, []byte("id"))
	if err == nil {
		t.Error("expected error for corrupted nonce")
	}
}

func TestEncrypt_NilAAD_RoundTrips(t *testing.T) {
	ks := testKeySet()
	plaintext := []byte("no aad")

	encrypted, _, err := Encrypt(ks, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	decrypted, err := Decrypt(ks, encrypted, nil)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if string(decrypted) != "no aad" {
		t.Errorf("round-trip failed")
	}
}

func TestKeyByVersion_NotFound(t *testing.T) {
	ks := testKeySet()
	_, err := ks.KeyByVersion(99)
	if err == nil {
		t.Error("expected error for missing version")
	}
}

func TestActiveKey_EmptyKeySet(t *testing.T) {
	ks := &EncryptionKeySet{Keys: []EncryptionKey{}}
	_, err := ks.ActiveKey()
	if err == nil {
		t.Error("expected error for empty key set")
	}
}

// --- Concurrency tests ---

func TestEncryptDecrypt_ConcurrentAccess(t *testing.T) {
	ks := testKeySet()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			plaintext := []byte(fmt.Sprintf("secret-%d", n))
			aad := []byte(fmt.Sprintf("id-%d", n))

			encrypted, _, err := Encrypt(ks, plaintext, aad)
			if err != nil {
				t.Errorf("encrypt %d failed: %v", n, err)
				return
			}
			decrypted, err := Decrypt(ks, encrypted, aad)
			if err != nil {
				t.Errorf("decrypt %d failed: %v", n, err)
				return
			}
			if string(decrypted) != string(plaintext) {
				t.Errorf("round-trip %d failed", n)
			}
		}(i)
	}
	wg.Wait()
}

// --- Service unhappy paths ---

func TestCredService_Create_EmptyProviders(t *testing.T) {
	svc, _ := newTestCredService()

	cs, err := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:      "empty",
		Providers: ProviderConfig{},
	})
	// Empty providers is valid (no keys to encrypt, but the map is valid JSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cs.Providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(cs.Providers))
	}
}

func TestCredService_Get_NotFound(t *testing.T) {
	svc, _ := newTestCredService()

	_, err := svc.Get(context.Background(), "nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent credential set")
	}
}

func TestCredService_Delete_NotFound(t *testing.T) {
	svc, store := newTestCredService()
	store.refCount = 0

	err := svc.Delete(context.Background(), "nonexistent-id")
	// Should succeed (delete of non-existent is idempotent in mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCredService_Create_MultipleProviders(t *testing.T) {
	svc, _ := newTestCredService()

	cs, err := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name: "multi",
		Providers: ProviderConfig{
			"openai":    {APIKey: "sk-1", BaseURL: "https://api.openai.com/v1"},
			"anthropic": {APIKey: "sk-ant-1"},
			"deepseek":  {APIKey: "sk-ds-1", BaseURL: "https://api.deepseek.com"},
		},
		ModelAllowlist: []string{"gpt-4", "claude-3", "deepseek-v2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cs.Providers) != 3 {
		t.Errorf("expected 3 providers, got %d", len(cs.Providers))
	}
	if len(cs.ModelAllowlist) != 3 {
		t.Errorf("expected 3 models, got %d", len(cs.ModelAllowlist))
	}
}

func TestCredService_Create_AssignedToAll(t *testing.T) {
	svc, _ := newTestCredService()

	cs, err := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:       "all-users",
		Providers:  ProviderConfig{"x": {APIKey: "k"}},
		AssignedTo: "all",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.AssignedTo != "all" {
		t.Errorf("expected assignedTo=all, got %v", cs.AssignedTo)
	}
}

func TestCredService_Create_AssignedToSpecificUsers(t *testing.T) {
	svc, _ := newTestCredService()

	cs, err := svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:       "specific",
		Providers:  ProviderConfig{"x": {APIKey: "k"}},
		AssignedTo: []string{"user-1", "user-2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assigned, ok := cs.AssignedTo.([]string)
	if !ok {
		// JSON round-trip converts to []interface{}
		if arr, ok2 := cs.AssignedTo.([]interface{}); ok2 {
			if len(arr) != 2 {
				t.Errorf("expected 2 users, got %d", len(arr))
			}
		} else {
			t.Errorf("unexpected assignedTo type: %T", cs.AssignedTo)
		}
	} else if len(assigned) != 2 {
		t.Errorf("expected 2 users, got %d", len(assigned))
	}
}

func TestCredService_Create_NilAssignedTo_DefaultsToAll(t *testing.T) {
	svc, store := newTestCredService()

	svc.Create(context.Background(), CreateCredentialSetRequest{
		Name:       "default-assign",
		Providers:  ProviderConfig{"x": {APIKey: "k"}},
		AssignedTo: nil,
	})

	// Check the raw stored value
	for _, row := range store.sets {
		if row.Name == "default-assign" {
			if string(row.AssignedTo) != `"all"` {
				t.Errorf("expected \"all\" in DB, got %s", string(row.AssignedTo))
			}
		}
	}
}

func TestCredService_RotateKey_NoRows(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	ks := &EncryptionKeySet{Keys: []EncryptionKey{{Version: 1, Key: key}}}
	store := newMockCredStore()
	svc := NewService(store, ks, nil)

	result, err := svc.RotateEncryptionKey(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Rotated != 0 {
		t.Errorf("expected 0 rotated, got %d", result.Rotated)
	}
}

// --- Integration test: full lifecycle ---

func TestCredService_FullLifecycle(t *testing.T) {
	svc, store := newTestCredService()
	store.refCount = 0
	ctx := context.Background()

	// Create
	cs, err := svc.Create(ctx, CreateCredentialSetRequest{
		Name:           "lifecycle-test",
		Providers:      ProviderConfig{"openai": {APIKey: "sk-live"}},
		ModelAllowlist: []string{"gpt-4"},
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Get
	got, err := svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Name != "lifecycle-test" {
		t.Errorf("name mismatch")
	}
	if !got.IsDefault {
		t.Error("expected isDefault=true")
	}

	// List
	list, _ := svc.List(ctx)
	if len(list) != 1 {
		t.Errorf("expected 1 in list, got %d", len(list))
	}

	// Create second and set as default
	cs2, _ := svc.Create(ctx, CreateCredentialSetRequest{
		Name:      "second",
		Providers: ProviderConfig{"anthropic": {APIKey: "sk-ant"}},
	})
	svc.SetDefault(ctx, cs2.ID)

	// Verify first is no longer default
	if store.sets[cs.ID].IsDefault {
		t.Error("first should no longer be default")
	}

	// Delete first (no references)
	if err := svc.Delete(ctx, cs.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Verify deleted
	_, err = svc.Get(ctx, cs.ID)
	if err == nil {
		t.Error("expected not found after delete")
	}
}
