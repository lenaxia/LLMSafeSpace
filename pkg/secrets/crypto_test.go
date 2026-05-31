// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"testing"
)

func TestDeriveKEK_Deterministic(t *testing.T) {
	password := []byte("test-password-123")
	salt := []byte("0123456789abcdef0123456789abcdef") // 32 bytes

	kek1, err := DeriveKEK(password, salt, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}
	kek2, err := DeriveKEK(password, salt, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}

	if !bytes.Equal(kek1, kek2) {
		t.Error("DeriveKEK should be deterministic for same inputs")
	}
	if len(kek1) != 32 {
		t.Errorf("KEK should be 32 bytes, got %d", len(kek1))
	}
}

func TestDeriveKEK_DifferentSalts(t *testing.T) {
	password := []byte("test-password-123")
	salt1 := []byte("0123456789abcdef0123456789abcdef")
	salt2 := []byte("fedcba9876543210fedcba9876543210")

	kek1, err := DeriveKEK(password, salt1, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}
	kek2, err := DeriveKEK(password, salt2, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}

	if bytes.Equal(kek1, kek2) {
		t.Error("Different salts should produce different KEKs")
	}
}

func TestDeriveKEK_DifferentPasswords(t *testing.T) {
	salt := []byte("0123456789abcdef0123456789abcdef")

	kek1, err := DeriveKEK([]byte("password-1"), salt, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}
	kek2, err := DeriveKEK([]byte("password-2"), salt, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}

	if bytes.Equal(kek1, kek2) {
		t.Error("Different passwords should produce different KEKs")
	}
}

func TestDeriveKEK_DifferentInfo(t *testing.T) {
	password := []byte("test-password")
	salt := []byte("0123456789abcdef0123456789abcdef")

	kek1, err := DeriveKEK(password, salt, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}
	kek2, err := DeriveKEK(password, salt, recInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}

	if bytes.Equal(kek1, kek2) {
		t.Error("Different info strings should produce different KEKs")
	}
}

func TestGenerateDEK(t *testing.T) {
	dek, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK failed: %v", err)
	}
	if len(dek) != 32 {
		t.Errorf("DEK should be 32 bytes, got %d", len(dek))
	}

	// Two calls should produce different DEKs
	dek2, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK failed: %v", err)
	}
	if bytes.Equal(dek, dek2) {
		t.Error("Two GenerateDEK calls should produce different keys")
	}
}

func TestGenerateSalt(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt failed: %v", err)
	}
	if len(salt) != 32 {
		t.Errorf("Salt should be 32 bytes, got %d", len(salt))
	}
}

func TestGenerateRecoveryKey(t *testing.T) {
	key, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatalf("GenerateRecoveryKey failed: %v", err)
	}
	if len(key) != 16 {
		t.Errorf("Recovery key should be 16 bytes, got %d", len(key))
	}
}

func TestWrapUnwrapDEK_RoundTrip(t *testing.T) {
	kek := make([]byte, 32)
	copy(kek, []byte("12345678901234567890123456789012"))

	dek, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK failed: %v", err)
	}

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK failed: %v", err)
	}

	unwrapped, err := UnwrapDEK(kek, wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK failed: %v", err)
	}

	if !bytes.Equal(dek, unwrapped) {
		t.Error("Unwrapped DEK should match original")
	}
}

func TestWrapDEK_DifferentCiphertexts(t *testing.T) {
	kek := make([]byte, 32)
	copy(kek, []byte("12345678901234567890123456789012"))
	dek := make([]byte, 32)
	copy(dek, []byte("abcdefghijklmnopqrstuvwxyz012345"))

	wrapped1, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK failed: %v", err)
	}
	wrapped2, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK failed: %v", err)
	}

	if bytes.Equal(wrapped1, wrapped2) {
		t.Error("Two wraps of same DEK should produce different ciphertexts (random nonce)")
	}
}

func TestUnwrapDEK_WrongKey(t *testing.T) {
	kek1 := make([]byte, 32)
	copy(kek1, []byte("12345678901234567890123456789012"))
	kek2 := make([]byte, 32)
	copy(kek2, []byte("abcdefghijklmnopqrstuvwxyz012345"))

	dek, _ := GenerateDEK()
	wrapped, _ := WrapDEK(kek1, dek)

	_, err := UnwrapDEK(kek2, wrapped)
	if err == nil {
		t.Error("UnwrapDEK with wrong key should fail")
	}
	if err != ErrDecryptionFailed {
		t.Errorf("Expected ErrDecryptionFailed, got: %v", err)
	}
}

func TestUnwrapDEK_TamperedCiphertext(t *testing.T) {
	kek := make([]byte, 32)
	copy(kek, []byte("12345678901234567890123456789012"))
	dek, _ := GenerateDEK()
	wrapped, _ := WrapDEK(kek, dek)

	// Tamper with the last byte
	wrapped[len(wrapped)-1] ^= 0xFF

	_, err := UnwrapDEK(kek, wrapped)
	if err == nil {
		t.Error("UnwrapDEK with tampered ciphertext should fail")
	}
}

func TestUnwrapDEK_TooShort(t *testing.T) {
	kek := make([]byte, 32)
	copy(kek, []byte("12345678901234567890123456789012"))

	_, err := UnwrapDEK(kek, []byte("short"))
	if err != ErrInvalidCiphertext {
		t.Errorf("Expected ErrInvalidCiphertext for short input, got: %v", err)
	}
}

func TestEncryptDecryptSecret_RoundTrip(t *testing.T) {
	dek, _ := GenerateDEK()
	plaintext := []byte("my-secret-api-key-sk-1234567890")

	ciphertext, err := EncryptSecret(dek, plaintext)
	if err != nil {
		t.Fatalf("EncryptSecret failed: %v", err)
	}

	decrypted, err := DecryptSecret(dek, ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("Decrypted text should match original plaintext")
	}
}

func TestEncryptSecret_DifferentCiphertexts(t *testing.T) {
	dek, _ := GenerateDEK()
	plaintext := []byte("same-plaintext")

	ct1, _ := EncryptSecret(dek, plaintext)
	ct2, _ := EncryptSecret(dek, plaintext)

	if bytes.Equal(ct1, ct2) {
		t.Error("Two encryptions of same plaintext should differ (random nonce)")
	}
}

func TestDecryptSecret_WrongKey(t *testing.T) {
	dek1, _ := GenerateDEK()
	dek2, _ := GenerateDEK()
	plaintext := []byte("secret-data")

	ciphertext, _ := EncryptSecret(dek1, plaintext)

	_, err := DecryptSecret(dek2, ciphertext)
	if err == nil {
		t.Error("DecryptSecret with wrong key should fail")
	}
	if err != ErrDecryptionFailed {
		t.Errorf("Expected ErrDecryptionFailed, got: %v", err)
	}
}

func TestDecryptSecret_TamperedCiphertext(t *testing.T) {
	dek, _ := GenerateDEK()
	plaintext := []byte("secret-data")

	ciphertext, _ := EncryptSecret(dek, plaintext)
	ciphertext[len(ciphertext)-1] ^= 0xFF

	_, err := DecryptSecret(dek, ciphertext)
	if err == nil {
		t.Error("DecryptSecret with tampered ciphertext should fail")
	}
}

func TestDecryptSecret_TooShort(t *testing.T) {
	dek, _ := GenerateDEK()

	_, err := DecryptSecret(dek, []byte("x"))
	if err != ErrInvalidCiphertext {
		t.Errorf("Expected ErrInvalidCiphertext, got: %v", err)
	}
}

func TestEncryptDecryptSecret_EmptyPlaintext(t *testing.T) {
	dek, _ := GenerateDEK()
	plaintext := []byte("")

	ciphertext, err := EncryptSecret(dek, plaintext)
	if err != nil {
		t.Fatalf("EncryptSecret with empty plaintext failed: %v", err)
	}

	decrypted, err := DecryptSecret(dek, ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("Empty plaintext round-trip failed")
	}
}

func TestEncryptDecryptSecret_LargePlaintext(t *testing.T) {
	dek, _ := GenerateDEK()
	plaintext := make([]byte, 1024*1024) // 1MB
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	ciphertext, err := EncryptSecret(dek, plaintext)
	if err != nil {
		t.Fatalf("EncryptSecret with large plaintext failed: %v", err)
	}

	decrypted, err := DecryptSecret(dek, ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("Large plaintext round-trip failed")
	}
}

func TestFullKeyWrappingFlow(t *testing.T) {
	// Simulate account creation
	password := []byte("user-password-secure-123")
	salt, _ := GenerateSalt()
	dek, _ := GenerateDEK()

	// Derive KEK from password
	kek, err := DeriveKEK(password, salt, kekInfo)
	if err != nil {
		t.Fatalf("DeriveKEK failed: %v", err)
	}

	// Wrap DEK with KEK
	wrappedDEK, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK failed: %v", err)
	}

	// Simulate login: derive KEK again, unwrap DEK
	kek2, _ := DeriveKEK(password, salt, kekInfo)
	unwrappedDEK, err := UnwrapDEK(kek2, wrappedDEK)
	if err != nil {
		t.Fatalf("UnwrapDEK failed: %v", err)
	}

	if !bytes.Equal(dek, unwrappedDEK) {
		t.Error("Full flow: unwrapped DEK should match original")
	}

	// Encrypt a secret with the DEK
	secret := []byte("sk-anthropic-key-12345")
	ciphertext, _ := EncryptSecret(unwrappedDEK, secret)

	// Decrypt the secret
	decrypted, err := DecryptSecret(unwrappedDEK, ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret failed: %v", err)
	}
	if !bytes.Equal(secret, decrypted) {
		t.Error("Full flow: decrypted secret should match original")
	}
}

func TestPasswordChangeFlow(t *testing.T) {
	// Setup: create account with password1
	password1 := []byte("old-password")
	salt1, _ := GenerateSalt()
	dek, _ := GenerateDEK()

	kek1, _ := DeriveKEK(password1, salt1, kekInfo)
	wrappedDEK, _ := WrapDEK(kek1, dek)

	// Encrypt a secret
	secret := []byte("my-secret")
	ciphertext, _ := EncryptSecret(dek, secret)

	// Password change: unwrap with old, re-wrap with new
	unwrapped, _ := UnwrapDEK(kek1, wrappedDEK)

	password2 := []byte("new-password")
	salt2, _ := GenerateSalt()
	kek2, _ := DeriveKEK(password2, salt2, kekInfo)
	newWrappedDEK, _ := WrapDEK(kek2, unwrapped)

	// Login with new password
	kek2Again, _ := DeriveKEK(password2, salt2, kekInfo)
	dekAfterChange, err := UnwrapDEK(kek2Again, newWrappedDEK)
	if err != nil {
		t.Fatalf("UnwrapDEK after password change failed: %v", err)
	}

	// Secret should still be decryptable (DEK unchanged)
	decrypted, err := DecryptSecret(dekAfterChange, ciphertext)
	if err != nil {
		t.Fatalf("DecryptSecret after password change failed: %v", err)
	}
	if !bytes.Equal(secret, decrypted) {
		t.Error("Password change should not affect secret decryption")
	}
}

func TestRecoveryKeyFlow(t *testing.T) {
	// Setup
	password := []byte("original-password")
	salt, _ := GenerateSalt()
	recoverySalt, _ := GenerateSalt()
	dek, _ := GenerateDEK()
	recoveryKey, _ := GenerateRecoveryKey()

	// Wrap DEK with password KEK
	kek, _ := DeriveKEK(password, salt, kekInfo)
	_, _ = WrapDEK(kek, dek)

	// Wrap DEK with recovery KEK
	recoveryKEK, _ := DeriveKEK(recoveryKey, recoverySalt, recInfo)
	wrappedDEKRecovery, _ := WrapDEK(recoveryKEK, dek)

	// Simulate password reset with recovery key
	recoveryKEK2, _ := DeriveKEK(recoveryKey, recoverySalt, recInfo)
	recoveredDEK, err := UnwrapDEK(recoveryKEK2, wrappedDEKRecovery)
	if err != nil {
		t.Fatalf("Recovery unwrap failed: %v", err)
	}

	if !bytes.Equal(dek, recoveredDEK) {
		t.Error("Recovered DEK should match original")
	}

	// Re-wrap with new password
	newPassword := []byte("new-password-after-reset")
	newSalt, _ := GenerateSalt()
	newKEK, _ := DeriveKEK(newPassword, newSalt, kekInfo)
	newWrappedDEK, _ := WrapDEK(newKEK, recoveredDEK)

	// Verify new password works
	newKEK2, _ := DeriveKEK(newPassword, newSalt, kekInfo)
	finalDEK, err := UnwrapDEK(newKEK2, newWrappedDEK)
	if err != nil {
		t.Fatalf("UnwrapDEK with new password after recovery failed: %v", err)
	}
	if !bytes.Equal(dek, finalDEK) {
		t.Error("DEK after recovery + re-wrap should match original")
	}
}
