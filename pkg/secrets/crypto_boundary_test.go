// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

func TestDeriveKEK_EmptyPassword(t *testing.T) {
	salt := make([]byte, 32)
	kek, err := DeriveKEK([]byte{}, salt, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK with empty password should succeed: %v", err)
	}
	if len(kek) != 32 {
		t.Errorf("KEK should still be 32 bytes, got %d", len(kek))
	}
}

func TestDeriveKEK_EmptySalt(t *testing.T) {
	kek, err := DeriveKEK([]byte("password"), []byte{}, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK with empty salt should succeed: %v", err)
	}
	if len(kek) != 32 {
		t.Errorf("KEK should be 32 bytes, got %d", len(kek))
	}
}

func TestDeriveKEK_EmptyInfo(t *testing.T) {
	salt := make([]byte, 32)
	kek, err := DeriveKEK([]byte("password"), salt, "")
	if err != nil {
		t.Fatalf("DeriveKEK with empty info should succeed: %v", err)
	}
	if len(kek) != 32 {
		t.Errorf("KEK should be 32 bytes, got %d", len(kek))
	}
}

func TestWrapDEK_InvalidKeySize(t *testing.T) {
	// AES requires 16, 24, or 32 byte keys
	shortKey := []byte("short")
	dek := make([]byte, 32)

	_, err := WrapDEK(shortKey, dek)
	if err == nil {
		t.Error("WrapDEK with invalid key size should fail")
	}
}

func TestUnwrapDEK_EmptyInput(t *testing.T) {
	kek := make([]byte, 32)
	_, err := UnwrapDEK(kek, []byte{})
	if err != ErrInvalidCiphertext {
		t.Errorf("Expected ErrInvalidCiphertext for empty input, got: %v", err)
	}
}

func TestUnwrapDEK_NilInput(t *testing.T) {
	kek := make([]byte, 32)
	_, err := UnwrapDEK(kek, nil)
	if err != ErrInvalidCiphertext {
		t.Errorf("Expected ErrInvalidCiphertext for nil input, got: %v", err)
	}
}

func TestEncryptSecret_InvalidKeySize(t *testing.T) {
	_, err := EncryptSecret([]byte("short"), []byte("plaintext"))
	if err == nil {
		t.Error("EncryptSecret with invalid key size should fail")
	}
}

func TestDecryptSecret_NilCiphertext(t *testing.T) {
	dek := make([]byte, 32)
	_, err := DecryptSecret(dek, nil)
	if err != ErrInvalidCiphertext {
		t.Errorf("Expected ErrInvalidCiphertext for nil, got: %v", err)
	}
}

func TestDecryptSecret_ExactlyNonceSize(t *testing.T) {
	dek := make([]byte, 32)
	block, _ := aes.NewCipher(dek)
	gcm, _ := cipher.NewGCM(block)
	// Input exactly nonce size — no ciphertext to decrypt
	nonce := make([]byte, gcm.NonceSize())
	_, err := DecryptSecret(dek, nonce)
	if err == nil {
		t.Error("Decrypting nonce-only input should fail")
	}
}

func TestEncryptDecryptSecret_BinaryData(t *testing.T) {
	dek, _ := GenerateDEK()
	// Binary data with null bytes
	plaintext := []byte{0x00, 0x01, 0xFF, 0xFE, 0x00, 0x00, 0xAB}

	ct, err := EncryptSecret(dek, plaintext)
	if err != nil {
		t.Fatalf("Encrypt binary data failed: %v", err)
	}
	pt, err := DecryptSecret(dek, ct)
	if err != nil {
		t.Fatalf("Decrypt binary data failed: %v", err)
	}
	if !bytes.Equal(plaintext, pt) {
		t.Error("Binary data round-trip failed")
	}
}

func TestEncryptDecryptSecret_UnicodeData(t *testing.T) {
	dek, _ := GenerateDEK()
	plaintext := []byte("密码是：🔑 très sécurisé 日本語テスト")

	ct, _ := EncryptSecret(dek, plaintext)
	pt, err := DecryptSecret(dek, ct)
	if err != nil {
		t.Fatalf("Decrypt unicode failed: %v", err)
	}
	if !bytes.Equal(plaintext, pt) {
		t.Error("Unicode round-trip failed")
	}
}

func TestWrapUnwrapDEK_16ByteKey(t *testing.T) {
	// AES-128
	kek := make([]byte, 16)
	copy(kek, []byte("1234567890123456"))
	dek, _ := GenerateDEK()

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK with 16-byte key failed: %v", err)
	}
	unwrapped, err := UnwrapDEK(kek, wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK with 16-byte key failed: %v", err)
	}
	if !bytes.Equal(dek, unwrapped) {
		t.Error("16-byte key round-trip failed")
	}
}

func TestWrapUnwrapDEK_24ByteKey(t *testing.T) {
	// AES-192
	kek := make([]byte, 24)
	copy(kek, []byte("123456789012345678901234"))
	dek, _ := GenerateDEK()

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK with 24-byte key failed: %v", err)
	}
	unwrapped, err := UnwrapDEK(kek, wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK with 24-byte key failed: %v", err)
	}
	if !bytes.Equal(dek, unwrapped) {
		t.Error("24-byte key round-trip failed")
	}
}
