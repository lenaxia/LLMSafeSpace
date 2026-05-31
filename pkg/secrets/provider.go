// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import "context"

// OwnerType distinguishes user-owned from org-owned secrets.
type OwnerType string

const (
	OwnerTypeUser OwnerType = "user"
	OwnerTypeOrg  OwnerType = "org"
)

// SecretOwner identifies the owner of a secret (user or org).
type SecretOwner struct {
	ID   string
	Type OwnerType
}

// SecretProvider defines the encryption/decryption interface for user secrets.
// V1: PostgresSecretProvider (HKDF + AES-GCM + session cache)
// Future: VaultSecretProvider, HSMSecretProvider
type SecretProvider interface {
	// Encrypt encrypts plaintext with the owner's current DEK.
	Encrypt(ctx context.Context, owner SecretOwner, plaintext []byte) (ciphertext []byte, keyVersion int, err error)

	// Decrypt decrypts ciphertext using the appropriate DEK version.
	Decrypt(ctx context.Context, owner SecretOwner, ciphertext []byte, keyVersion int) (plaintext []byte, err error)

	// RotateKey generates a new DEK for the owner. Old DEK retained for lazy migration.
	RotateKey(ctx context.Context, owner SecretOwner) (newKeyVersion int, err error)

	// DEKAvailable returns true if the owner's DEK is currently cached (active session).
	DEKAvailable(ctx context.Context, owner SecretOwner) bool
}
