// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	dekSize  = 32 // 256-bit DEK
	saltSize = 32 // 256-bit salt
	kekInfo  = "llmsafespace-kek"
	recInfo  = "llmsafespace-recovery"
)

var (
	ErrDecryptionFailed  = errors.New("decryption failed: ciphertext tampered or wrong key")
	ErrInvalidCiphertext = errors.New("ciphertext too short")
)

// DeriveKEK derives a Key Encryption Key from a password and salt using HKDF-SHA256.
func DeriveKEK(password []byte, salt []byte, info string) ([]byte, error) {
	hkdfReader := hkdf.New(sha256.New, password, salt, []byte(info))
	kek := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, kek); err != nil {
		return nil, err
	}
	return kek, nil
}

// GenerateDEK generates a random 256-bit Data Encryption Key.
func GenerateDEK() ([]byte, error) {
	dek := make([]byte, dekSize)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}
	return dek, nil
}

// GenerateSalt generates a random salt.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// GenerateRecoveryKey generates a 128-bit recovery key.
func GenerateRecoveryKey() ([]byte, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// WrapDEK encrypts a DEK with a KEK using AES-256-GCM.
// Returns nonce || ciphertext (nonce is prepended).
func WrapDEK(kek, dek []byte) ([]byte, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, dek, nil), nil
}

// UnwrapDEK decrypts a wrapped DEK using a KEK.
// Expects nonce || ciphertext format.
func UnwrapDEK(kek, wrappedDEK []byte) ([]byte, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(wrappedDEK) < nonceSize {
		return nil, ErrInvalidCiphertext
	}
	nonce, ciphertext := wrappedDEK[:nonceSize], wrappedDEK[nonceSize:]
	dek, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return dek, nil
}

// EncryptSecret encrypts plaintext with a DEK using AES-256-GCM.
// Returns nonce || ciphertext.
func EncryptSecret(dek, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptSecret decrypts ciphertext with a DEK using AES-256-GCM.
// Expects nonce || ciphertext format.
func DecryptSecret(dek, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCiphertext
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}
