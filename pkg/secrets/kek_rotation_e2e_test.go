// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

// kek_rotation_e2e_test.go closes the gap identified in the rotation audit:
// rotation_test.go proves the NEW key decrypts post-rotation, but no test
// asserts the OLD key can NO LONGER decrypt. The "old key fails" guarantee is
// the entire point of KEK rotation — without it, a leaked old keyfile still
// compromises every row. This test also verifies the full round-trip: encrypt
// under old → rotate → decrypt under new succeeds → decrypt under old fails.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_KEKRotation_OldKeyCanNoLongerDecrypt is the central rotation
// invariant guard. It rotates a row from oldKey to newKey through the real
// RotationCoordinator, then asserts:
//  1. The new key successfully decrypts the re-wrapped ciphertext.
//  2. The old key FAILS to decrypt the re-wrapped ciphertext (the guarantee
//     that was previously untested).
//  3. The original plaintext is preserved (no corruption during re-wrap).
//
// A bug that re-wraps under the SAME key (no-op rotation), or that leaves the
// old key able to decrypt, fails assertion (2).
func TestE2E_KEKRotation_OldKeyCanNoLongerDecrypt(t *testing.T) {
	ctx := context.Background()

	oldKey := deterministicRotationKey(0xAA)
	newKey := deterministicRotationKey(0xBB)

	oldProv, err := NewStaticKeyProvider(oldKey)
	require.NoError(t, err)
	newProv, err := NewStaticKeyProvider(newKey)
	require.NoError(t, err)

	oldProviders := map[string]RootKeyProvider{"provider-credentials": oldProv}
	newProviders := map[string]RootKeyProvider{"provider-credentials": newProv}

	// Seed a row encrypted under the OLD key (version 1).
	originalPlaintext := []byte(`{"provider":"anthropic","apiKey":"sk-rotate-me"}`)
	oldCiphertext, err := EncryptSecret(oldKey, originalPlaintext)
	require.NoError(t, err)

	store := newMockRotationStore()
	store.addRow("provider_credentials", "cred-1", "admin", oldCiphertext, 1)

	coord := NewRotationCoordinator(store, oldProviders, newProviders)
	result, err := coord.RotateTable(ctx, "provider_credentials", "", 2, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Processed, "exactly one row must be rotated")
	assert.Equal(t, 0, result.Failed)

	// (1) The NEW key must decrypt the re-wrapped ciphertext.
	rewrappedRow := store.rows["provider_credentials"][0]
	newPlaintext, err := newProv.Decrypt(ctx, rewrappedRow.Ciphertext)
	require.NoError(t, err, "new key must decrypt the re-wrapped ciphertext")
	assert.Equal(t, string(originalPlaintext), string(newPlaintext),
		"rotation must preserve the original plaintext — corruption is a data-loss bug")
	assert.Equal(t, 2, rewrappedRow.KeyVersion, "row must be stamped with the new key version")

	// (2) THE KEY ASSERTION: the OLD key must FAIL to decrypt the re-wrapped
	// ciphertext. This is the guarantee that was missing — rotation_test.go
	// only checked the new key succeeds.
	_, oldDecryptErr := oldProv.Decrypt(ctx, rewrappedRow.Ciphertext)
	require.Error(t, oldDecryptErr,
		"OLD key must NOT decrypt re-wrapped ciphertext — if it can, the rotation was a no-op "+
			"and a leaked old keyfile still compromises every row")
}

// TestE2E_KEKRotation_AlreadyAtTargetVersion_Skipped verifies idempotency: a
// row already at the target version is skipped (not re-encrypted, not failed).
// A bug that re-encrypts already-rotated rows would waste cycles and risk
// data corruption on every run.
func TestE2E_KEKRotation_AlreadyAtTargetVersion_Skipped(t *testing.T) {
	ctx := context.Background()
	newKey := deterministicRotationKey(0xBB)
	newProv, err := NewStaticKeyProvider(newKey)
	require.NoError(t, err)

	store := newMockRotationStore()
	// Row already at version 2 (the target).
	ct, err := EncryptSecret(newKey, []byte(`{"already":"rotated"}`))
	require.NoError(t, err)
	store.addRow("provider_credentials", "cred-done", "admin", ct, 2)

	coord := NewRotationCoordinator(store,
		map[string]RootKeyProvider{"provider-credentials": newProv},
		map[string]RootKeyProvider{"provider-credentials": newProv},
	)
	result, err := coord.RotateTable(ctx, "provider_credentials", "", 2, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Processed, "already-rotated rows must not be re-processed (idempotency)")
	assert.Equal(t, 0, result.Failed, "already-rotated rows must not be counted as failures")

	// The row must be unchanged — still version 2, still the original ciphertext.
	row := store.rows["provider_credentials"][0]
	assert.Equal(t, 2, row.KeyVersion, "idempotent re-run must not change the key version")
	assert.Equal(t, ct, row.Ciphertext, "idempotent re-run must not change the ciphertext")
}

// TestE2E_KEKRotation_DecryptFailure_RecordsRowID verifies the unhappy path:
// a row encrypted under an unknown key (not in oldProviders) is recorded as a
// failure with its row ID, and does NOT abort the entire rotation.
func TestE2E_KEKRotation_DecryptFailure_RecordsRowID(t *testing.T) {
	ctx := context.Background()
	oldProv, err := NewStaticKeyProvider(deterministicRotationKey(0xAA))
	require.NoError(t, err)
	newProv, err := NewStaticKeyProvider(deterministicRotationKey(0xBB))
	require.NoError(t, err)

	unknownKey := deterministicRotationKey(0xFF) // not in oldProviders
	rogueCT, err := EncryptSecret(unknownKey, []byte("rogue"))
	require.NoError(t, err)

	store := newMockRotationStore()
	store.addRow("provider_credentials", "cred-rogue", "admin", rogueCT, 1)

	coord := NewRotationCoordinator(store,
		map[string]RootKeyProvider{"provider-credentials": oldProv},
		map[string]RootKeyProvider{"provider-credentials": newProv},
	)
	result, err := coord.RotateTable(ctx, "provider_credentials", "", 2, false)
	require.NoError(t, err, "a per-row decrypt failure must not abort the rotation")
	assert.Equal(t, 1, result.Failed, "the rogue row must be counted as failed")
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "cred-rogue", result.Errors[0].RowID,
		"the failure must record the row ID for operator triage")
}

// TestE2E_KEKRotation_DryRun_DoesNotMutate verifies the dry-run contract:
// rows are counted but NOT re-encrypted. A bug that writes during dry-run
// would cause an operator running --dry-run to accidentally rotate.
func TestE2E_KEKRotation_DryRun_DoesNotMutate(t *testing.T) {
	ctx := context.Background()
	oldProv, err := NewStaticKeyProvider(deterministicRotationKey(0xAA))
	require.NoError(t, err)
	newProv, err := NewStaticKeyProvider(deterministicRotationKey(0xBB))
	require.NoError(t, err)

	oldCT, err := EncryptSecret(deterministicRotationKey(0xAA), []byte("dry-run"))
	require.NoError(t, err)
	store := newMockRotationStore()
	store.addRow("provider_credentials", "cred-dry", "admin", oldCT, 1)

	coord := NewRotationCoordinator(store,
		map[string]RootKeyProvider{"provider-credentials": oldProv},
		map[string]RootKeyProvider{"provider-credentials": newProv},
	)
	result, err := coord.RotateTable(ctx, "provider_credentials", "", 2, true)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Processed, "dry-run must count the row")
	assert.Len(t, store.updates, 0, "dry-run must NOT write any updates")

	// The row must be unchanged — still version 1, still old ciphertext.
	row := store.rows["provider_credentials"][0]
	assert.Equal(t, 1, row.KeyVersion, "dry-run must not change the key version")
	assert.Equal(t, oldCT, row.Ciphertext, "dry-run must not change the ciphertext")
}

// deterministicRotationKey returns a 32-byte key where every byte == seed.
func deterministicRotationKey(seed byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed
	}
	return k
}
