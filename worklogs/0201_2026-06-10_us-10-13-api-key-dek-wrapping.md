# 0201 — US-10.13 API Key DEK Wrapping

**Date:** 2026-06-10
**Status:** Complete — all tests pass, pending merge

---

## Objectives

Implement US-10.13 Part 2: API Key DEK Wrapping. Enable API key sessions to access secret management operations (CreateSecret, UpdateSecret, DecryptSecretValue, PrepareSecretsForInjection) by wrapping the user's DEK with a KEK derived from the raw API key at creation time. Addresses the hard gap documented in US-10.10: API key sessions called `KeyService.GetDEK(ctx, "")` which always returned cache miss → HTTP 403.

---

## Design Approach

Simplified Part 2 (no full RootKeyProvider interface). Uses the existing `LLMSAFESPACE_MASTER_SECRET` / `dekMasterKey()` pattern for `key_ciphertext` storage, matching what `RedisDEKCache` already does for DEK-at-rest wrapping.

**Key derivation chain:**
```
api_kek = HKDF-SHA256(rawKey, kek_salt, "llmsafespace-apikey-kek")
wrapped_dek = AES-256-GCM(api_kek, user_dek)
key_ciphertext = AES-256-GCM(dekMasterKey(), rawKey)   ← enables DEK re-wrap on rotation
```

**Session ID for API key sessions:**
```
sessionID = "apikey:" + SHA-256(rawKey)
```
Deterministic per key. Both `validateAPIKey` and `AuthMiddleware` compute it independently from the raw token — no interface changes needed.

---

## Assumptions Validated

1. `secrets.KeyService.GetDEK` and `CacheDEK` exist on `KeyService` — **partially false**: `GetDEK` existed, `CacheDEK` did not. Added `CacheDEK` as a pass-through to `cache.CacheDEK`. Verified via `pkg/secrets/key_service.go:183`.
2. `dekMasterKey()` is accessible from `app` package — **true**, already defined in `app/secrets_adapters.go:418`.
3. `DeriveKEK` accepts a raw key as `[]byte` — **true**, no length requirement enforced. Verified via `pkg/secrets/crypto.go:30`.
4. `AuthMiddleware` in `services/auth/auth.go` is called for all authenticated routes — **true** for routes using `services.GetAuth().AuthMiddleware()`. The separate `middleware/auth.go` is used in some paths; updated both.
5. `GetUserByAPIKey` query matches on `api_keys.key` column — **true**, column stores hash since migration 000017. Verified via `api/internal/services/database/database.go:267`.

---

## Changes

### New Files
- `api/migrations/000019_api_key_dek_wrapping.up.sql` — adds `decrypt_access`, `kek_salt`, `wrapped_dek`, `dek_synced`, `key_ciphertext` to `api_keys`
- `api/migrations/000019_api_key_dek_wrapping.down.sql`
- `charts/llmsafespace/migrations/000019_api_key_dek_wrapping.{up,down}.sql` — chart mirror (required by `pkg/repolint` chart-sync test)
- `api/internal/services/auth/auth_apikey_dek_test.go` — 9 unit tests (TDD-first)
- `api/internal/services/auth/auth_apikey_dek_e2e_test.go` — 6 e2e regression tests

### Modified Files

**`api/internal/handlers/models_test.go`**
- `TestListModels_AgentUnreachable`: changed `staticPodIPResolver` from `127.0.0.99` to `192.0.2.1` (RFC 5737 TEST-NET-1). The previous IP was reachable via loopback when `opencode serve` listens on `0.0.0.0:4096` in the same sandbox.

**`pkg/types/types.go`**
- `CreateAPIKeyRequest`: added `DecryptAccess bool`
- `APIKey`: added `DecryptAccess`, `DekSynced`, `KekSalt`, `WrappedDEK`, `KeyCiphertext` (latter three `json:"-"` — never serialised)

**`pkg/secrets/key_service.go`**
- Added `CacheDEK(ctx, sessionID, dek, ttl)` — pass-through to `cache.CacheDEK`
- Added `APIKeyStore` interface + `APIKeyRecord` type for DEK re-wrap
- Added `SetAPIKeyStore(store, masterKey)` setter
- Added `rewrapAPIKeyDEKs(ctx, userID, newDEK)` — called at end of `RotateKeyWithPassword`

**`api/internal/interfaces/interfaces.go`**
- `AuthService.CreateAPIKey`: added `sessionID string` parameter
- `DatabaseService`: added `GetAPIKeyRecordByHash`, `UpdateAPIKeyDEK`, `ListAPIKeysWithDecrypt`

**`api/internal/services/database/database.go`**
- `CreateAPIKey`: extended to 14 parameters, stores all new columns
- `ListAPIKeys`: returns `decrypt_access`, `dek_synced`
- Added `GetAPIKeyRecordByHash` — returns full `api_keys` row by hash
- Added `UpdateAPIKeyDEK` — updates `wrapped_dek`, `kek_salt`, `dek_synced`
- Added `ListAPIKeysWithDecrypt` — returns all active `decrypt_access=true` keys for a user

**`api/internal/services/auth/auth.go`**
- `Service` struct: added `masterKey []byte`
- Added `SetMasterKey(key []byte)`
- Added `apiKeyDEKTTL()` — reads from config, defaults to 24h (was hardcoded)
- Extended `KeyServiceInterface`: added `GetDEK`, `CacheDEK`
- Removed `hashToken` function — replaced with `pkg/utilities.HashString` (Fix #5: eliminated duplication between auth.go and middleware/auth.go)
- `CreateAPIKey`: when `req.DecryptAccess=true`, validates `masterKey != nil` and `sessionID != ""`, fetches DEK via `keyService.GetDEK`, generates `kek_salt`, derives `api_kek`, wraps DEK, encrypts raw key as `key_ciphertext`
- `validateAPIKey`: after user lookup, fetches API key record via `GetAPIKeyRecordByHash`; if `decrypt_access=true` and `wrapped_dek` present, derives `api_kek`, decrypts DEK, caches under `"apikey:"+HashString(rawKey)` with configurable TTL. Decrypt failures are logged at Error and are non-fatal to auth (key still authenticates, just without DEK)
- `AuthMiddleware` / `OptionalAuthMiddleware`: uses `pkg/utilities.HashString` for sessionID

**`api/internal/middleware/auth.go`**
- Uses `pkg/utilities.HashString` for sessionID (removed `hashTokenForMiddleware` duplication)
- Removed unused `crypto/sha256` and `encoding/hex` imports

**`api/internal/config/config.go`**
- Added `APIKeyDEKTTL time.Duration` to Auth config section

**`api/internal/server/router.go`**
- `CreateAPIKey` handler: extracts `sessionID` from Gin context, passes to `authSvc.CreateAPIKey`

**`api/internal/app/app.go`**
- Calls `authSvc.SetMasterKey(dekMasterKey())`
- Calls `keyService.SetAPIKeyStore(&apiKeyStoreAdapter{db: dbSvc}, dekMasterKey())`

**`api/internal/app/secrets_adapters.go`**
- Added `apiKeyStoreAdapter` — adapts `DatabaseService` to `secrets.APIKeyStore`

**`api/internal/services/auth/auth_revocation_test.go`**
- Updated `hashToken` calls to `pkgutil.HashString`

### Mock / Test Infrastructure Updates
- `api/internal/mocks/database.go`: added `GetAPIKeyRecordByHash`, `UpdateAPIKeyDEK`, `ListAPIKeysWithDecrypt`
- `api/internal/mocks/middleware_mocks.go`: updated `CreateAPIKey` signature
- `api/internal/services/auth/auth_sessionid_test.go`: added `GetDEK`, `CacheDEK`, `GetAPIKeyRecordByHash`, `UpdateAPIKeyDEK`, `ListAPIKeysWithDecrypt` stubs to `mockDB` and `trackingKeyService`
- `api/internal/services/auth/auth_e2e_secrets_test.go`: same stubs on `fullMockDB`
- `api/internal/services/auth/auth_e2e_all_test.go`: added `TestE2E_APIKey_CreateWithDecryptAccess_SecretsOperationSucceeds`, `apiKeyAwareDB` (stores and retrieves API keys by hash), updated `capturingKeyService` with `GetDEK`/`CacheDEK`. Fixed `GetUserByEmail` and `GetUser` to return copies (not stored pointers) — Login clears `PasswordHash` on the returned user, which was mutating the stored value.
- `api/internal/services/auth/auth_test.go`: updated `fakeKeyService` with `GetDEK`/`CacheDEK`, fixed `CreateAPIKey` call sites to pass `sessionID`
- `api/internal/server/router_auth_test.go`, `router_auth_security_test.go`: updated mock expectations for new `CreateAPIKey` signature
- `api/internal/middleware/tests/auth_test.go`: updated `MockAuthService.CreateAPIKey` signature

---

## Tests Written (TDD)

**Unit tests (`auth_apikey_dek_test.go`):**
- `TestCreateAPIKey_WithDecryptAccess_StoresWrappedDEK` — verifies DB write has `wrapped_dek`, `kek_salt`, `key_ciphertext`, `dek_synced=true`
- `TestCreateAPIKey_WithDecryptAccess_RequiresJWTSession` — empty sessionID → error containing "JWT session required"
- `TestCreateAPIKey_WithDecryptAccess_RequiresMasterKey` — nil masterKey → error containing "master key not configured"
- `TestCreateAPIKey_WithDecryptAccess_DEKNotAvailable` — keyService returns error → propagated
- `TestCreateAPIKey_WithoutDecryptAccess_NoWrappedDEK` — verifies DB write has nil `wrapped_dek`, `kek_salt`, `key_ciphertext`
- `TestValidateAPIKey_WithDecryptAccess_UnlocksDEK` — verifies DEK is cached after auth
- `TestValidateAPIKey_WithoutDecryptAccess_NoDEK` — no DEK cache write for non-decrypt keys
- `TestValidateAPIKey_WrappedDEKCorrupt_Fails` — corrupt wrapped_dek is logged, auth still succeeds
- `TestCreateAPIKey_DEKRoundTrip` — full cryptographic round-trip: creates key, re-derives `api_kek`, decrypts `wrapped_dek`, asserts recovered DEK == original; also decrypts `key_ciphertext`

**E2E test (`auth_e2e_all_test.go`):**
- `TestE2E_APIKey_CreateWithDecryptAccess_SecretsOperationSucceeds` — register → login → create API key with `decryptAccess:true` → use raw API key to POST /secrets → GET /secrets returns the created secret

**E2E regression tests (`auth_apikey_dek_e2e_test.go`):**
- `TestE2E_APIKey_WithoutDecryptAccess_SecretsOperation403` — API key without `decryptAccess` gets 403 on POST /secrets
- `TestE2E_APIKey_WithDecryptAccess_SessionIDConsistency` — DEK cached under `"apikey:" + HashString(rawKey)` after first API-key-authenticated request; secrets operations succeed
- `TestE2E_APIKey_DEKUnwrapCorrupt_GracefulDegradation` — corrupt `wrapped_dek` → auth succeeds but secrets operations return 403
- `TestE2E_APIKey_CreateWithoutDecryptAccess_NoDEKColumns` — non-decrypt key has nil `WrappedDEK`, `KekSalt`, `KeyCiphertext` and `DekSynced=false`
- `TestE2E_APIKey_RewrapAfterRotation` — `RotateKeyWithPassword` re-wraps DEK for all `decrypt_access=true` keys; API key still works for secrets after rotation
- `TestE2E_APIKey_DEKTTLMatters` — DEK expired from cache → re-authentication via `validateAPIKey` re-caches DEK → subsequent secrets operations succeed

**SQL-level database tests (`database_test.go`):**
- `TestCreateAPIKey_WithDEKWrappingColumns` — verifies INSERT with all 14 params including `kek_salt`, `wrapped_dek`, `key_ciphertext`
- `TestCreateAPIKey_WithoutDEKWrappingColumns` — verifies INSERT with nil DEK columns
- `TestGetAPIKeyRecordByHash_WithDEKColumns` — verifies SELECT returns all DEK columns correctly
- `TestGetAPIKeyRecordByHash_NullDEKColumns` — verifies NULL BYTEA columns scan as nil
- `TestGetAPIKeyRecordByHash_NotFound` — returns nil, nil
- `TestUpdateAPIKeyDEK` — verifies UPDATE sets `wrapped_dek`, `kek_salt`, `dek_synced`
- `TestUpdateAPIKeyDEK_SyncFailure` — verifies UPDATE with nil DEK data and `synced=false`
- `TestListAPIKeysWithDecrypt` — verifies SELECT returns multiple keys with DEK columns

---

## Test Results

```
ok  github.com/lenaxia/llmsafespace/api/internal/services/auth     12.0s
ok  github.com/lenaxia/llmsafespace/api/internal/services/database  0.0s
ok  github.com/lenaxia/llmsafespace/api/internal/server             0.1s
ok  github.com/lenaxia/llmsafespace/api/internal/middleware/tests   0.0s
ok  github.com/lenaxia/llmsafespace/pkg/secrets                     0.0s
ok  github.com/lenaxia/llmsafespace/api/internal/handlers          20.8s
ok  github.com/lenaxia/llmsafespace/pkg/repolint                    0.1s
```

All 7 packages pass with zero failures.

---

## Fixes Applied (Session 2)

1. **`TestListModels_AgentUnreachable` failure** — ✅ Fixed. `127.0.0.99` is a loopback address reachable when `opencode serve` listens on `0.0.0.0:4096` inside the same sandbox. Changed to `192.0.2.1` (RFC 5737 TEST-NET-1).
2. **Duplicated `hashToken` function** — ✅ Fixed. Removed `hashToken` from `auth.go` and `hashTokenForMiddleware` from `middleware/auth.go`. Both now use `pkg/utilities.HashString`.
3. **Hardcoded 24h DEK TTL** — ✅ Fixed. `validateAPIKey` now uses `s.apiKeyDEKTTL()` which reads `config.Auth.APIKeyDEKTTL`, defaulting to 24h. Config field added.
4. **Silent DEK unwrap failure** — Partially addressed. Auth still succeeds (by design — the API key authenticates the user), but the DEK is simply not cached. Secrets operations return 403 with `"encryption key not available; re-authenticate"`. The error is logged at Error level with the key ID. Future: could add a context flag to differentiate the error message.
5. **Mock DB `GetUserByEmail` mutation bug** — ✅ Fixed. `apiKeyAwareDB.GetUserByEmail` and `GetUser` now return copies of the stored user, preventing `Login` (which clears `PasswordHash` on the returned object) from corrupting the stored data.
6. **Mock DB `UpdateAPIKeyDEK` / `ListAPIKeysWithDecrypt` were no-ops** — ✅ Fixed. Now properly update and retrieve DEK columns in the in-memory store.

---

## Known Limitations / Deferred

1. **`dek_synced=false` 403 enforcement** — design specifies that stale keys should return `"API key DEK re-sync in progress. Retry shortly."` at auth time. Currently `validateAPIKey` still authenticates and simply has no DEK cached. Enforcement deferred pending operator feedback on UX.
2. **Background retry job for `dek_synced=false` keys** — design item 9 (background retry every 5 minutes). Not implemented; the `rewrapAPIKeyDEKs` call at rotation time covers the immediate case.
3. **`rewrapAPIKeyDEKs` final DB write failure** — if `UpdateAPIKeyDEK(ctx, key.ID, wrappedDEK, key.KekSalt, true)` fails, the key retains old `wrapped_dek` with `dek_synced=true`. Next `validateAPIKey` unwraps the stale DEK. Low probability (requires DB outage concurrent with rotation). Documented as known limitation; not blocking.
