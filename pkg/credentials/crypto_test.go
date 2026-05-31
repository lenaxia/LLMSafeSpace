// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package credentials

import (
	"crypto/rand"
	"testing"
)

func testKeySet() *EncryptionKeySet {
	key := make([]byte, 32)
	rand.Read(key)
	return &EncryptionKeySet{
		Keys: []EncryptionKey{{Version: 1, Key: key}},
	}
}

func testMultiKeySet() *EncryptionKeySet {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)
	return &EncryptionKeySet{
		Keys: []EncryptionKey{
			{Version: 1, Key: key1},
			{Version: 2, Key: key2},
		},
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	ks := testKeySet()
	plaintext := []byte(`{"openai":{"apiKey":"sk-test123"}}`)
	aad := []byte("cred-set-id-1")

	encrypted, version, err := Encrypt(ks, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if version != 1 {
		t.Errorf("expected version 1, got %d", version)
	}
	if len(encrypted) <= len(plaintext) {
		t.Error("encrypted should be longer than plaintext (nonce + tag + version byte)")
	}

	decrypted, err := Decrypt(ks, encrypted, aad)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("round-trip failed: got %q", decrypted)
	}
}

func TestEncryptDecrypt_DifferentAAD_Fails(t *testing.T) {
	ks := testKeySet()
	plaintext := []byte("secret")
	aad := []byte("correct-id")

	encrypted, _, err := Encrypt(ks, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	_, err = Decrypt(ks, encrypted, []byte("wrong-id"))
	if err == nil {
		t.Error("expected decryption to fail with wrong AAD")
	}
}

func TestEncryptDecrypt_MultiKey_UsesActiveKey(t *testing.T) {
	ks := testMultiKeySet()
	plaintext := []byte("secret data")
	aad := []byte("id")

	encrypted, version, err := Encrypt(ks, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	// Active key is version 2 (highest)
	if version != 2 {
		t.Errorf("expected version 2 (active), got %d", version)
	}

	// Should decrypt with the correct key
	decrypted, err := Decrypt(ks, encrypted, aad)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("round-trip failed")
	}
}

func TestEncryptDecrypt_OldKeyStillDecrypts(t *testing.T) {
	key1 := make([]byte, 32)
	rand.Read(key1)
	ks1 := &EncryptionKeySet{Keys: []EncryptionKey{{Version: 1, Key: key1}}}

	plaintext := []byte("old secret")
	aad := []byte("id")

	// Encrypt with key v1
	encrypted, _, err := Encrypt(ks1, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	// Add a new key v2 — old data should still decrypt
	key2 := make([]byte, 32)
	rand.Read(key2)
	ks2 := &EncryptionKeySet{Keys: []EncryptionKey{
		{Version: 1, Key: key1},
		{Version: 2, Key: key2},
	}}

	decrypted, err := Decrypt(ks2, encrypted, aad)
	if err != nil {
		t.Fatalf("decrypt with old key failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("round-trip failed")
	}
}

func TestDecrypt_UnknownKeyVersion_Fails(t *testing.T) {
	ks := testKeySet()
	// Fabricate encrypted data with version 99
	fake := []byte{99, 0, 0, 0, 0}
	_, err := Decrypt(ks, fake, nil)
	if err == nil {
		t.Error("expected error for unknown key version")
	}
}

func TestDecrypt_EmptyData_Fails(t *testing.T) {
	ks := testKeySet()
	_, err := Decrypt(ks, []byte{}, nil)
	if err == nil {
		t.Error("expected error for empty data")
	}
}

func TestDecrypt_TruncatedData_Fails(t *testing.T) {
	ks := testKeySet()
	plaintext := []byte("test")
	aad := []byte("id")

	encrypted, _, _ := Encrypt(ks, plaintext, aad)
	// Truncate the ciphertext
	_, err := Decrypt(ks, encrypted[:5], aad)
	if err == nil {
		t.Error("expected error for truncated data")
	}
}

func TestEncrypt_EmptyKeySet_Fails(t *testing.T) {
	ks := &EncryptionKeySet{Keys: []EncryptionKey{}}
	_, _, err := Encrypt(ks, []byte("test"), nil)
	if err == nil {
		t.Error("expected error for empty key set")
	}
}

func TestEncrypt_WrongKeySize_Fails(t *testing.T) {
	ks := &EncryptionKeySet{Keys: []EncryptionKey{{Version: 1, Key: []byte("short")}}}
	_, _, err := Encrypt(ks, []byte("test"), nil)
	if err == nil {
		t.Error("expected error for wrong key size")
	}
}

func TestActiveKey_ReturnsHighestVersion(t *testing.T) {
	ks := testMultiKeySet()
	active, err := ks.ActiveKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active.Version != 2 {
		t.Errorf("expected version 2, got %d", active.Version)
	}
}

func TestProviderConfig_MarshalUnmarshal(t *testing.T) {
	config := ProviderConfig{
		"openai":    {APIKey: "sk-test", BaseURL: "https://api.openai.com/v1"},
		"anthropic": {APIKey: "sk-ant-test"},
	}

	data, err := MarshalProviders(config)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	result, err := UnmarshalProviders(data)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if result["openai"].APIKey != "sk-test" {
		t.Errorf("expected sk-test, got %q", result["openai"].APIKey)
	}
	if result["anthropic"].APIKey != "sk-ant-test" {
		t.Errorf("expected sk-ant-test, got %q", result["anthropic"].APIKey)
	}
}

func TestEncrypt_ProducesUniqueOutput(t *testing.T) {
	ks := testKeySet()
	plaintext := []byte("same input")
	aad := []byte("id")

	enc1, _, _ := Encrypt(ks, plaintext, aad)
	enc2, _, _ := Encrypt(ks, plaintext, aad)

	// Due to random nonce, same plaintext should produce different ciphertext
	if string(enc1) == string(enc2) {
		t.Error("expected different ciphertext for same plaintext (random nonce)")
	}
}
