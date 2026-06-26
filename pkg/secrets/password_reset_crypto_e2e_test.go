// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

// password_reset_crypto_e2e_test.go verifies the cryptographic-erasure half
// of the password-reset guarantee. The materialization-layer half lives in
// api/internal/handlers/pod_bootstrap_e2e_test.go (TestE2E_PasswordReset_*).
//
// The claim under test (key_service.go:114 InitializeUserKeys, called by
// password_reset.go:278): after a password reset, the user's wrapped DEK is
// overwritten in the store. The OLD DEK (used to encrypt pre-reset secrets) is
// no longer retrievable, so pre-reset ciphertext is permanently undecryptable
// by the system. This is the crypto basis for "no future materialization can
// resurrect them" (database.go:790).
//
// Existing coverage (key_service_test.go:158 TestKeyService_Reinit_Upserts)
// asserts the wrapped DEK CHANGES after reinit, but never proves the new DEK
// FAILS to decrypt old ciphertext. This test closes that gap with real AES-GCM.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_PasswordReset_OldDEKCannotDecryptAfterReinit proves the crypto
// erasure invariant end-to-end:
//  1. InitializeUserKeys(pw1) → DEK1, encrypt a secret.
//  2. Capture DEK1 (the attacker-equivalent: bytes that existed pre-reset).
//  3. InitializeUserKeys(pw2) → DEK2 (reset overwrites the wrapped DEK).
//  4. UnlockDEK(pw2) → the system now only has DEK2.
//  5. DecryptSecret(DEK2, oldCiphertext) MUST FAIL — the new DEK cannot
//     resurrect the old secret.
//
// This test fails if InitializeUserKeys ever reuses a DEK, or if UnlockDEK
// ever returns a DEK that can decrypt pre-reset ciphertext.
func TestE2E_PasswordReset_OldDEKCannotDecryptAfterReinit(t *testing.T) {
	ctx := context.Background()
	userID := "user-reset-crypto"

	store := newMockKeyStore()
	cache := newTestDEKCache()
	keySvc := NewKeyService(store, cache)

	// (1) First init: establishes DEK1 wrapped under pw1.
	_, err := keySvc.InitializeUserKeys(ctx, userID, []byte("password-1"))
	require.NoError(t, err)

	// Unlock DEK1 into the cache so we can encrypt a secret with it.
	const sessPre = "sess-pre-reset"
	require.NoError(t, keySvc.UnlockDEK(ctx, userID, []byte("password-1"), sessPre, time.Hour))
	dek1, err := keySvc.GetDEK(ctx, sessPre, nil)
	require.NoError(t, err)
	require.Len(t, dek1, 32, "DEK must be 32 bytes")

	// Encrypt a secret under DEK1 (the "pre-reset" ciphertext).
	oldPlaintext := []byte(`{"provider":"openai","apiKey":"sk-pre-reset-leak"}`)
	oldCiphertext, err := EncryptSecret(dek1, oldPlaintext)
	require.NoError(t, err)

	// (2) RESET: InitializeUserKeys with a new password overwrites the wrapped
	// DEK. The old wrapped DEK is gone from the store.
	_, err = keySvc.InitializeUserKeys(ctx, userID, []byte("password-2"))
	require.NoError(t, err)

	// (3) Evict the cached old DEK (password_reset.go:312 RevokeAllUserSessions
	// does this). Without eviction, the old session could still decrypt —
	// eviction is part of the reset contract.
	require.NoError(t, keySvc.EvictDEK(ctx, sessPre))

	// (4) Unlock with the NEW password → the system now holds only DEK2.
	const sessPost = "sess-post-reset"
	require.NoError(t, keySvc.UnlockDEK(ctx, userID, []byte("password-2"), sessPost, time.Hour))
	dek2, err := keySvc.GetDEK(ctx, sessPost, nil)
	require.NoError(t, err)

	// (5) THE ASSERTION: DEK2 must NOT decrypt the old ciphertext.
	_, decryptErr := DecryptSecret(dek2, oldCiphertext)
	require.Error(t, decryptErr,
		"new DEK must NOT decrypt pre-reset ciphertext — if it can, the reset erasure guarantee is broken")

	// Sanity: DEK2 must differ from DEK1 (a reuse bug would make the above
	// decrypt succeed silently).
	assert.NotEqual(t, dek1, dek2,
		"DEK must change across InitializeUserKeys — a reused DEK breaks the erasure guarantee")

	// Sanity: the OLD DEK (captured bytes) still technically decrypts — the
	// guarantee is operational (key gone from store), not that AES-GCM
	// ciphertext becomes undecryptable. Document this so a reader doesn't
	// mistake the threat model: an attacker who captured the raw DEK bytes
	// pre-reset can still decrypt; the defense is that the SYSTEM no longer
	// holds those bytes.
	decrypted, legacyErr := DecryptSecret(dek1, oldCiphertext)
	require.NoError(t, legacyErr,
		"old DEK bytes (if captured) still decrypt — the guarantee is that the system discarded them, not that AES-GCM ciphertext self-destructs")
	assert.Contains(t, string(decrypted), "sk-pre-reset-leak")
}

// TestE2E_PasswordReset_ReinitTwice_OldPasswordFailsUnlock verifies that after
// a reset, attempting to unlock with the OLD password fails (the old wrapped
// DEK is gone). This guards a regression where a reset might leave the old
// wrapped DEK row in place, allowing the old password to still unlock.
func TestE2E_PasswordReset_ReinitTwice_OldPasswordFailsUnlock(t *testing.T) {
	ctx := context.Background()
	userID := "user-old-pw"
	store := newMockKeyStore()
	cache := newTestDEKCache()
	keySvc := NewKeyService(store, cache)

	_, err := keySvc.InitializeUserKeys(ctx, userID, []byte("old-password"))
	require.NoError(t, err)

	// Reset with new password.
	_, err = keySvc.InitializeUserKeys(ctx, userID, []byte("new-password"))
	require.NoError(t, err)

	// Old password must fail to unlock — the wrapped DEK it corresponds to
	// is gone (overwritten by the UPSERT at CreateUserKey).
	err = keySvc.UnlockDEK(ctx, userID, []byte("old-password"), "sess-old-pw", time.Hour)
	assert.Error(t, err,
		"old password must NOT unlock after reset — the old wrapped DEK was overwritten")
}
