# 0203 — US-10.13 Part 1: API Key At-Rest Encryption (RootKeyProvider)

**Date:** 2026-06-10
**Status:** Complete — all tests pass

---

## Objectives

Implement US-10.13 Part 1: API Key At-Rest Encryption. Introduce the `RootKeyProvider` interface to abstract encryption of API keys at rest, replacing the direct `masterKey []byte` usage with a pluggable provider supporting static keys (dev), sealed keys (self-hosted), and future KMS/Vault backends. Also implements `allowed_cidrs` IP allowlisting and constant-time key comparison.

---

## Assumptions Validated

1. `key_prefix` column already exists (migration 000017) — **true**, verified `api/migrations/000017_api_key_hashing.up.sql:25`
2. `key` column stores hex-encoded SHA-256 hash — **true**, verified `auth.go:721-722`
3. `key_ciphertext` only populated for `decrypt_access=true` keys — **true**, verified `auth.go:770` was inside `if req.DecryptAccess` block
4. `s.masterKey` is `[]byte` from `dekMasterKey()` — **true**, verified `app.go:247`, `secrets_adapters.go:442-446`
5. `rewrapAPIKeyDEKs` uses `DecryptSecret(s.masterKey, ...)` directly — **true**, verified `key_service.go:560`
6. KMS/Vault require external SDKs — **true**, deferred to follow-up
7. `subtle.ConstantTimeCompare` is sufficient — **true**, standard Go crypto
8. `key` column VARCHAR is usable as indexed lookup column — **true**, unique index added in migration 000020
9. All existing DEK wrapping tests continue to pass — **true**, validated after refactor

---

## Changes

### New Files

- `pkg/secrets/root_key.go` — `RootKeyProvider` interface, `StaticKeyProvider`, `SealedKeyProvider`, `SealRootKey` helper
- `pkg/secrets/root_key_test.go` — 16 TDD tests for providers
- `api/migrations/000020_api_key_at_rest_encryption.up.sql` — adds `allowed_cidrs TEXT[]`, unique index `idx_api_keys_key_active`
- `api/migrations/000020_api_key_at_rest_encryption.down.sql` — drops index and column
- `charts/llmsafespace/migrations/000020_api_key_at_rest_encryption.{up,down}.sql` — chart mirror

### Modified Files

**`pkg/secrets/key_service.go`**
- Replaced `masterKey []byte` with `rootKeyProvider RootKeyProvider` on `KeyService`
- `SetAPIKeyStore` now takes `RootKeyProvider` instead of `[]byte`
- `rewrapAPIKeyDEKs` uses `s.rootKeyProvider.Decrypt(ctx, ...)` instead of `DecryptSecret(s.masterKey, ...)`

**`api/internal/services/auth/auth.go`**
- Replaced `masterKey []byte` with `rootKeyProvider RootKeyProvider` on `Service`
- `SetMasterKey` now creates a `StaticKeyProvider` internally (backward compatible)
- Added `SetRootKeyProvider(provider)` for direct injection
- `CreateAPIKey`: `key_ciphertext` encrypted via `rootKeyProvider.Encrypt()` for ALL keys (not just decrypt_access)
- `validateAPIKey`: added `clientIP` parameter; performs constant-time comparison via `subtle.ConstantTimeCompare`; enforces `allowed_cidrs` before DEK unlock
- `ValidateToken` now delegates to `ValidateTokenWithClientIP` for IP-aware validation
- Both `AuthMiddleware` and `OptionalAuthMiddleware` pass `c.ClientIP()` to the validation chain
- Added `ipInAnyCIDR` and `zeroBytes` helpers

**`api/internal/services/database/database.go`**
- `CreateAPIKey`: extended from 14 to 15 SQL parameters, adding `allowed_cidrs`
- `GetAPIKeyRecordByHash`: added `allowed_cidrs` to SELECT and Scan
- `ListAPIKeys`: added `allowed_cidrs` to SELECT and Scan
- Added `toNullableStringArray` helper using `pq.Array`

**`api/internal/config/config.go`**
- Added `RootKeyProvider`, `SealedKeyPath`, `PassphrasePath` to Security config section
- Added env var bindings: `LLMSAFESPACE_SECURITY_ROOTKEYPROVIDER`, `LLMSAFESPACE_SECURITY_SEALEDKEYPATH`, `LLMSAFESPACE_SECURITY_PASSPHRASEPATH`

**`api/internal/app/app.go`**
- Creates shared `RootKeyProvider` via `newRootKeyProvider(cfg, log)` — used by both auth service and key service

**`api/internal/app/secrets_adapters.go`**
- Added `newRootKeyProvider(cfg, log)` — factory selecting provider based on `cfg.Security.RootKeyProvider`: "sealed" → `SealedKeyProvider`, "static"/"" → `StaticKeyProvider`, unknown → error

**`pkg/types/types.go`**
- `CreateAPIKeyRequest`: added `AllowedCIDRs []string`
- `APIKey`: added `AllowedCIDRs []string`

### Test Updates

- `api/internal/services/auth/auth_apikey_dek_test.go` — 4 new tests + updated error message + updated `validateAPIKey` signature
- `api/internal/services/auth/auth_apikey_dek_e2e_test.go` — updated `SetAPIKeyStore` to use `StaticKeyProvider`
- `api/internal/services/auth/auth_test.go` — updated `validateAPIKey` calls with `clientIP` parameter
- `api/internal/services/database/database_test.go` — added 15th parameter to `CreateAPIKey` mocks, added `allowed_cidrs` column to `GetAPIKeyRecordByHash` mocks

---

## Tests Written (TDD)

**RootKeyProvider unit tests (`pkg/secrets/root_key_test.go`):**
- `TestStaticKeyProvider_RoundTrip` — encrypt/decrypt round-trip
- `TestStaticKeyProvider_DifferentCiphertextEachEncrypt` — random nonce
- `TestStaticKeyProvider_WrongKeyFailsDecrypt` — wrong key → ErrDecryptionFailed
- `TestStaticKeyProvider_TamperedCiphertextFailsDecrypt` — tamper → ErrDecryptionFailed
- `TestStaticKeyProvider_TruncatedCiphertextFails` — short input → ErrInvalidCiphertext
- `TestNewStaticKeyProvider_RejectsWrongSize` — 16, 64, nil → error
- `TestStaticKeyProvider_CancelledContext` — local AES ignores context
- `TestStaticKeyProvider_LargePlaintext` — 4KB round-trip
- `TestSealedKeyProvider_RoundTrip` — seal → load → encrypt → decrypt
- `TestSealedKeyProvider_WrongPassphraseFails` — wrong passphrase → unseal error
- `TestSealedKeyProvider_MissingSealedKeyFileFails` — missing file → error
- `TestSealedKeyProvider_MissingPassphraseFileFails` — missing file → error
- `TestSealedKeyProvider_CorruptedSealedKeyFails` — garbage → unseal error
- `TestSealedKeyProvider_TruncatedSealedKeyFails` — short data → error
- `TestSealedKeyProvider_EncryptDecryptWithRealAPIKeyData` — constant-time verify
- `TestSealRootKey_DeterministicFormat` — 92-byte minimum format check

**Auth integration tests (`auth_apikey_dek_test.go`):**
- `TestCreateAPIKey_NonDecryptKey_GetsKeyCiphertext` — non-decrypt key still gets key_ciphertext
- `TestCreateAPIKey_NoRootKeyProvider_NoKeyCiphertext` — without provider, no ciphertext
- `TestValidateAPIKey_ConstantTimeCompare_RejectsMismatch` — wrong key → error
- `TestCreateAPIKey_WithSealedKeyProvider` — SealedKeyProvider full round-trip
- `TestIPInAnyCIDR` — 8 table-driven CIDR matching cases
- `TestValidateAPIKey_CIDREnforcement` — allowed IP passes, disallowed IP rejected

---

## Test Results

```
ok  github.com/lenaxia/llmsafespace/api/internal/services/auth     10.3s
ok  github.com/lenaxia/llmsafespace/api/internal/services/database  0.0s
ok  github.com/lenaxia/llmsafespace/api/internal/server             0.1s
ok  github.com/lenaxia/llmsafespace/api/internal/middleware/tests   0.0s
ok  github.com/lenaxia/llmsafespace/pkg/secrets                     0.0s
ok  github.com/lenaxia/llmsafespace/api/internal/handlers          20.8s
ok  github.com/lenaxia/llmsafespace/pkg/repolint                    0.1s
```

All 32 packages pass with zero failures.

---

## Adversarial Self-Review Findings

| Finding | Severity | Resolution |
|---------|----------|------------|
| clientIP not passed from middleware to ValidateToken | Real gap | Fixed: both middlewares now pass `c.ClientIP()` |
| Two RootKeyProvider instances created | Minor | Fixed: shared single instance |
| CIDR not checked in fallback path | False alarm | Fallback has no RootKeyProvider → no CIDR data |
| Root key in memory | False alarm | By design |
| `zeroBytes` effectiveness | False alarm | Best-effort with `runtime.KeepAlive` |

---

## Known Limitations / Deferred

1. **KMS/Vault providers** — `KMSProvider` and `VaultTransitProvider` not implemented. The interface is ready for them; requires AWS SDK / Vault client dependencies.
2. **`seal-key` CLI subcommand** — design task 10. Not implemented. `SealRootKey` is available as a library function.
3. **Backfill script** — rows created before migration 000020 have `key_ciphertext = NULL` and `allowed_cidrs = NULL`. A backfill script is needed to populate `key_ciphertext` for existing API keys.
4. **Drop legacy `key` column** — the `key` column still stores the SHA-256 hash. The design calls for eventually renaming to `key_hash` (BYTEA). Deferred.
5. **Data key caching for network-backed providers** — KMS/Vault providers should cache a data key locally. Not implemented (no network providers yet).

---

## Next Steps

1. Implement `KMSProvider` (AWS KMS) and `VaultTransitProvider` (HashiCorp Vault) when cloud deployment is needed
2. Write backfill script for existing API key rows
3. Add `seal-key` CLI subcommand to `cmd/` directory
4. Monitor `idx_api_keys_key_active` unique index for constraint violations on migration
