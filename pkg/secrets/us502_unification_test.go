// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

// US-50.2 unification test matrix — organized by failure mode (per the design's
// Risk Mitigation & Testing Strategy). Every failure mode must have at least one
// test that would catch it. The story does not merge until the full matrix passes
// with -race -count=1.
//
// Failure modes:
//   1 — Ciphertext orphaned by the refactor (data loss)
//   2 — Wrong provider wired to wrong handler (cross-decrypt)
//   3 — Nil provider reaches a hot path (503 or panic)
//   5 — Concurrency (provider shared across goroutines)
//
// Modes 4 (boot-order regression) and 6 (E2E injection) are integration tests
// that live in the credential_precedence_test.go suite (already updated to use
// providers) and the app package's integration tests.

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Failure mode 1: Round-trip compatibility (ciphertext not orphaned) ----
//
// A ciphertext encrypted by the OLD code path (AdminKeyDeriver + EncryptSecret)
// must decrypt under the NEW code path (provider.Decrypt). Uses a fixed master
// key and fixed plaintext for determinism. If any fails, the refactor has
// orphaned production data — do not merge.

// TestRoundTripCompatibility_ProviderCredentials_Admin proves that a ciphertext
// encrypted with deriveServerKey("provider-credentials") + EncryptSecret (the old
// Layer-2 path) decrypts correctly through a StaticKeyProvider wrapping the same
// derived key (the new unified path).
func TestRoundTripCompatibility_ProviderCredentials_Admin(t *testing.T) {
	// Fixed inputs (deterministic).
	masterKey := makeFixedMasterKey(1)
	derivedKey := deriveTestKey(t, masterKey, "provider-credentials")
	plaintext := []byte(`{"provider":"anthropic","apiKey":"sk-ant-test123"}`)

	// OLD path: encrypt with raw derived key + EncryptSecret.
	ciphertext, err := EncryptSecret(derivedKey, plaintext)
	require.NoError(t, err)

	// NEW path: decrypt through the provider wrapping the same derived key.
	provider := mustTestProvider(t, derivedKey)
	decrypted, err := provider.Decrypt(context.Background(), ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestRoundTripCompatibility_ProviderCredentials_Org(t *testing.T) {
	masterKey := makeFixedMasterKey(1)
	derivedKey := deriveTestKey(t, masterKey, "org-credentials")
	plaintext := []byte(`{"provider":"openai","apiKey":"sk-org-test456"}`)

	ciphertext, err := EncryptSecret(derivedKey, plaintext)
	require.NoError(t, err)

	provider := mustTestProvider(t, derivedKey)
	decrypted, err := provider.Decrypt(context.Background(), ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestRoundTripCompatibility_APIKeyCiphertext(t *testing.T) {
	masterKey := makeFixedMasterKey(1)
	derivedKey := deriveTestKey(t, masterKey, "dek-cache")
	plaintext := []byte("lsp_apikey_dek_roundtrip_test")

	ciphertext, err := EncryptSecret(derivedKey, plaintext)
	require.NoError(t, err)

	provider := mustTestProvider(t, derivedKey)
	decrypted, err := provider.Decrypt(context.Background(), ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestRoundTripCompatibility_OrgSSOSecret(t *testing.T) {
	// org_sso_configs uses the same RootKeyProvider as api_keys ("dek-cache"
	// purpose pre-US-50.7). Proving compatibility for this purpose string
	// covers the SSO client-secret decryption path.
	masterKey := makeFixedMasterKey(1)
	derivedKey := deriveTestKey(t, masterKey, "dek-cache")
	plaintext := []byte("oidc-client-secret-sso-test")

	ciphertext, err := EncryptSecret(derivedKey, plaintext)
	require.NoError(t, err)

	provider := mustTestProvider(t, derivedKey)
	decrypted, err := provider.Decrypt(context.Background(), ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// ---- Failure mode 2: Purpose isolation (no cross-decrypt) ----
//
// A handler holding provider A must NOT successfully decrypt ciphertexts meant
// for provider B. If it does, either (a) it silently returns wrong data, or
// (b) the wiring is transposed. Both are bugs.

func TestPurposeIsolation_AdminCannotDecryptOrg(t *testing.T) {
	masterKey := makeFixedMasterKey(1)
	adminKey := deriveTestKey(t, masterKey, "provider-credentials")
	orgKey := deriveTestKey(t, masterKey, "org-credentials")

	orgCipher, err := EncryptSecret(orgKey, []byte("org-secret"))
	require.NoError(t, err)

	adminProvider := mustTestProvider(t, adminKey)
	_, err = adminProvider.Decrypt(context.Background(), orgCipher)
	assert.ErrorIs(t, err, ErrDecryptionFailed, "admin provider must NOT decrypt org ciphertext")
}

func TestPurposeIsolation_OrgCannotDecryptAdmin(t *testing.T) {
	masterKey := makeFixedMasterKey(1)
	adminKey := deriveTestKey(t, masterKey, "provider-credentials")
	orgKey := deriveTestKey(t, masterKey, "org-credentials")

	adminCipher, err := EncryptSecret(adminKey, []byte("admin-secret"))
	require.NoError(t, err)

	orgProvider := mustTestProvider(t, orgKey)
	_, err = orgProvider.Decrypt(context.Background(), adminCipher)
	assert.ErrorIs(t, err, ErrDecryptionFailed, "org provider must NOT decrypt admin ciphertext")
}

func TestPurposeIsolation_APIKeyProvCannotDecryptProviderCreds(t *testing.T) {
	masterKey := makeFixedMasterKey(1)
	apiKey := deriveTestKey(t, masterKey, "dek-cache")
	adminKey := deriveTestKey(t, masterKey, "provider-credentials")

	adminCipher, err := EncryptSecret(adminKey, []byte("admin-cred"))
	require.NoError(t, err)

	apiKeyProvider := mustTestProvider(t, apiKey)
	_, err = apiKeyProvider.Decrypt(context.Background(), adminCipher)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestPurposeIsolation_ProviderCredsProvCannotDecryptAPIKey(t *testing.T) {
	masterKey := makeFixedMasterKey(1)
	apiKey := deriveTestKey(t, masterKey, "dek-cache")
	adminKey := deriveTestKey(t, masterKey, "provider-credentials")

	apiCipher, err := EncryptSecret(apiKey, []byte("api-key-ct"))
	require.NoError(t, err)

	adminProvider := mustTestProvider(t, adminKey)
	_, err = adminProvider.Decrypt(context.Background(), apiCipher)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

// ---- Failure mode 5: Concurrency (provider goroutine-safe) ----
//
// RootKeyProvider.Decrypt is called from concurrent request handlers. The
// provider must be goroutine-safe.

func TestProvider_ConcurrentDecrypt_NoRace(t *testing.T) {
	key := makeFixedMasterKey(1)
	provider := mustTestProvider(t, deriveTestKey(t, key, "provider-credentials"))

	// Pre-encrypt N distinct ciphertexts.
	const n = 100
	plaintexts := make([][]byte, n)
	ciphertexts := make([][]byte, n)
	for i := range plaintexts {
		plaintexts[i] = []byte("concurrent-secret-" + string(rune('A'+i%26)))
		ct, err := provider.Encrypt(context.Background(), plaintexts[i])
		require.NoError(t, err)
		ciphertexts[i] = ct
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			dec, err := provider.Decrypt(context.Background(), ciphertexts[idx])
			if err != nil {
				errs <- err
				return
			}
			if string(dec) != string(plaintexts[idx]) {
				errs <- ErrDecryptionFailed
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent decrypt failed: %v", err)
	}
}

func TestProvider_ConcurrentEncryptDecrypt_NoRace(t *testing.T) {
	key := makeFixedMasterKey(1)
	provider := mustTestProvider(t, deriveTestKey(t, key, "org-credentials"))

	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pt := []byte("mixed-concurrent-" + string(rune('a'+idx%26)))
			ct, err := provider.Encrypt(context.Background(), pt)
			if err != nil {
				errs <- err
				return
			}
			dec, err := provider.Decrypt(context.Background(), ct)
			if err != nil {
				errs <- err
				return
			}
			if string(dec) != string(pt) {
				errs <- ErrDecryptionFailed
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent encrypt+decrypt failed: %v", err)
	}
}

// ---- Helpers ----

// makeFixedMasterKey returns a deterministic 32-byte master key seeded by `seed`.
func makeFixedMasterKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

// deriveTestKey mimics what deriveServerKey does in the app package: HKDF-SHA256
// with the server salt and the given purpose string. This lets round-trip tests
// prove compatibility without importing the app package (avoiding a circular
// dependency).
func deriveTestKey(t *testing.T, masterKey []byte, purpose string) []byte {
	t.Helper()
	key, err := DeriveKEKFromKey(masterKey, []byte("llmsafespaces-server"), purpose)
	require.NoError(t, err)
	return key
}

// mustTestProvider wraps a raw key as a StaticKeyProvider for tests.
func mustTestProvider(t *testing.T, key []byte) *StaticKeyProvider {
	t.Helper()
	p, err := NewStaticKeyProvider(key)
	require.NoError(t, err)
	return p
}
