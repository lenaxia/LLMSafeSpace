# Worklog: Epic 10 Verification & Gap Analysis

**Date:** 2026-05-28
**Session:** Full verification of Epic 10 (Multi-Tenant Trust & Secret Management) — code review + live runtime testing
**Status:** Complete — 2 critical runtime bugs found

---

## Objective

Verify Epic 10 implementation is functional and correct at runtime. Test the full secret management lifecycle against a live k8s deployment: key initialization, DEK unlock, secret CRUD, bindings, audit, and key rotation.

---

## Verification Method

1. Read Epic 10 design spec (560 lines) in full
2. Thorough code review of all 20+ implementation files (pkg/secrets/, api/internal/handlers/, api/internal/app/)
3. Review all 19 test files (~158 test functions, ~5000 lines)
4. Deploy to k8s, verify migrations, test endpoints with real HTTP requests
5. Test authenticated secret CRUD flow with real JWT token

---

## Runtime Verification Results

### Infrastructure

| Check | Result |
|---|---|
| Pods (api x2, controller, frontend) | All 1/1 Running, 0 restarts |
| `/livez` / `/readyz` | OK / Ready |
| Valkey (Redis) | Running as `valkey` service |
| Postgres | Running, 15 tables |

### Route Registration (from Gin debug output)

All Epic 10 routes confirmed registered and serving:

```
POST   /api/v1/secrets                    → SecretsHandler.CreateSecret
GET    /api/v1/secrets                    → SecretsHandler.ListSecrets
GET    /api/v1/secrets/audit              → SecretsHandler.GetAuditLog
GET    /api/v1/secrets/:id                → SecretsHandler.GetSecret
PUT    /api/v1/secrets/:id                → SecretsHandler.UpdateSecret
DELETE /api/v1/secrets/:id                → SecretsHandler.DeleteSecret
PUT    /api/v1/workspaces/:id/bindings    → SecretsHandler.SetBindings
GET    /api/v1/workspaces/:id/bindings    → SecretsHandler.GetBindings
PUT    /api/v1/workspaces/:id/env         → SecretsHandler.SetWorkspaceEnv
GET    /api/v1/workspaces/:id/env         → SecretsHandler.GetWorkspaceEnv
DELETE /api/v1/workspaces/:id/env/:name   → SecretsHandler.DeleteWorkspaceEnv
POST   /api/v1/account/rotate-key         → RotateKeyHandler.RotateKey
POST   /api/v1/account/change-password    → RotateKeyHandler.ChangePassword
POST   /api/v1/account/recover            → RotateKeyHandler.RecoverAccount
```

Legacy credential routes with deprecation headers also registered.

### Unauthenticated Access (Verified)

All endpoints return `401 {"error":"Authorization token required"}` without a valid JWT.

---

## Critical Bugs Found at Runtime

### BUG 1: Migration Version Collision (FIXED)

**Severity:** CRITICAL — Epic 10 tables never created
**Root cause:** Two migration files shared version `000006`:
- `000006_settings.up.sql` (Epic 9)
- `000006_user_keys.up.sql` (Epic 10)

Goose considers `6` already applied after settings, so `user_keys` and `user_secrets` were never created.

**Fix applied:** Renamed to `000007_user_keys` and `000008_user_secrets`. Also added copies to `charts/llmsafespace/migrations/`.

**Verification:** After deploying fix, migration `8` applied clean. All 4 Epic 10 tables now exist:
- `user_keys` ✓
- `user_secrets` ✓
- `user_secret_bindings` ✓
- `secret_audit_log` ✓

### BUG 2: sessionID Not Set in Gin Context (NOT FIXED)

**Severity:** CRITICAL — Secret CRUD completely non-functional at runtime
**Root cause:** `api/internal/services/auth/auth.go` auth middleware sets `userID` and `userRole` in Gin context but does NOT set `sessionID` (the JWT's `jti` claim). The `SecretsHandler.extractAuth()` reads `c.Get("sessionID")` which always returns empty string.

**Impact:**
- Login calls `keyService.UnlockDEK(ctx, userID, password, jti, ttl)` — this caches the DEK in Redis keyed by `jti`
- Secret handlers call `extractAuth(c)` → `sessionID = ""` → `secretService.CreateSecret(ctx, userID, "", req)` → looks up DEK with sessionID="" → not found → `403 {"error":"encryption key not available; re-authenticate"}`

**Evidence:** Tested with real login + JWT token. List secrets returns `{"secrets":[]}` (200), but Create always returns 403.

**Fix needed:** In `api/internal/services/auth/auth.go` `AuthMiddleware()`, add:
```go
jti := utilities.ExtractJTI(tokenString)
if jti != "" {
    c.Set("sessionID", jti)
}
```

### BUG 3: No Key Initialization Path for Existing Users (NOT FIXED)

**Severity:** MEDIUM — Pre-Epic 10 users cannot use secrets
**Root cause:** `InitializeUserKeys` is only called during registration. Users created before Epic 10 have no `user_keys` row. Login silently skips DEK unlock (`HasKeys` returns false). There is no API endpoint or migration to bootstrap keys for existing users.

**Evidence:** Manually inserted key material for `mike@kao.family` via SQL to enable testing.

**Fix needed:** Add an endpoint or auto-init path:
- Option A: `POST /api/v1/account/init-keys` — requires password, returns recovery key
- Option B: Auto-init on first secret creation attempt (lazy bootstrap)
- Option C: Migration script that generates key material for all existing users

---

## Code Review Summary

### Backend (pkg/secrets/) — COMPLETE with gaps

| Component | File | Status |
|---|---|---|
| Crypto (HKDF + AES-256-GCM) | `crypto.go` | COMPLETE — correct constants, info strings, key sizes |
| Key Service | `key_service.go` | COMPLETE — init, unlock, evict, password change, recovery |
| Secret Service | `secret_service.go` | COMPLETE — CRUD with encryption, metadata validation |
| Postgres Provider | `postgres_provider.go` | COMPLETE — combines key + secret services |
| PG Key Store | `pg_key_store.go` | COMPLETE |
| PG Secret Store | `pg_secret_store.go` | COMPLETE with AsyncAuditLogger bonus |
| Redis DEK Cache | `redis_cache.go` | COMPLETE — TTL-based session caching |
| Secret Injection | `injection.go` | COMPLETE — API-side JSON preparation |
| Types | `types.go` | COMPLETE — all 5 secret types, request/response types |
| Provider interface | `provider.go` | COMPLETE — SecretOwner for org extensibility |

### API Layer — COMPLETE with one missing endpoint

| Component | File | Status |
|---|---|---|
| Secrets Handler | `handlers/secrets.go` | COMPLETE — 12 handler methods |
| Rotate Key Handler | `handlers/secrets.go` | COMPLETE — rotate, change-password, recover |
| Route Registration | `server/router.go` | COMPLETE — all routes registered |
| App Wiring | `app/app.go` + `app/secrets_adapters.go` | COMPLETE (but adapters are in-memory for dev) |

### Missing Endpoint

| Route | Spec | Status |
|---|---|---|
| `GET /api/v1/account/recovery-key` | Regenerate recovery key | NOT IMPLEMENTED |

### Test Coverage — 158 test functions, comprehensive

| Area | Tests | Status |
|---|---|---|
| Crypto operations | 36 | COMPLETE — round-trips, wrong key, tampered, empty, large, binary, unicode |
| Key service | 16 + 10 unhappy | COMPLETE — all lifecycle stages, error paths |
| Key rotation | 4 | COMPLETE — but no lazy re-encryption test |
| Secret service | 20 + 8 extended | COMPLETE — all types, concurrent access, cross-tenant isolation |
| Injection | 6 | COMPLETE — multi-type, cross-tenant |
| Integration | 7 | COMPLETE — full stack, recovery flow |
| Postgres integration | 8 (build-tagged) | COMPLETE — real DB |
| E2E | 1 (15 phases) | COMPLETE |
| Handler tests | 9 + 17 + 5 | COMPLETE — unauthenticated, validation, HTTP round-trips |
| Redis cache | 4 | COMPLETE — TTL expiry verified |
| JTI extraction | 3 | COMPLETE |

---

## Spec Compliance Matrix

### US-10.1: Key Wrapping & User Key Lifecycle — **PARTIAL**

| Requirement | Status |
|---|---|
| DEK generation (256-bit random) | DONE |
| KEK derivation (HKDF-SHA256) | DONE |
| Key wrapping (AES-256-GCM) | DONE |
| Session-based DEK caching (Redis) | DONE (but sessionID bug prevents use) |
| DEK eviction on logout | NOT VERIFIED (no explicit test) |
| Recovery key generation | DONE |
| Password change (O(1) re-wrap) | DONE |
| Password reset with recovery key | DONE |
| Password reset without recovery key (wipe secrets) | NOT IMPLEMENTED |

### US-10.2: User Secret Store (CRUD) — **COMPLETE (code) / BLOCKED (runtime)**

| Requirement | Status |
|---|---|
| Encrypted secret storage | DONE |
| All 5 secret types | DONE |
| Type-specific metadata validation | MOSTLY DONE (missing git-credential `host` and llm-provider `provider`) |
| GET never returns plaintext | DONE |
| Duplicate name per user rejected | DONE |

### US-10.3: Workspace Secret Bindings — **COMPLETE**

### US-10.4: Pod Secret Injection — **PARTIAL**

| Requirement | Status |
|---|---|
| API-side JSON preparation | DONE |
| Init container rewrite | NOT VERIFIED — needs controller code check |
| Ephemeral K8s Secret creation | NOT VERIFIED |
| Secret materialization to paths | NOT VERIFIED |

### US-10.5: Audit Logging — **PARTIAL**

| Requirement | Status |
|---|---|
| Append-only audit table | DONE |
| create/read/update/delete/bind actions logged | DONE |
| unbind action logged | NOT LOGGED (SetBindings replaces without logging removals) |
| rotate action logged | NOT LOGGED (rotation is in KeyService, not SecretService) |
| Async audit write | BONUS: AsyncAuditLogger exists but NOT WIRED IN |
| Cross-tenant isolation | DONE (query filters by userID) |

### US-10.6: Virtual Namespace Tenant Isolation — **NOT STARTED**

### US-10.7: S3 Shared Folder — **NOT STARTED**

### US-10.8: Lazy DEK Rotation — **NOT FUNCTIONAL**

| Requirement | Status |
|---|---|
| Key rotation creates new DEK | DONE |
| Store old wrapped_dek_prev for lazy migration | NOT IMPLEMENTED — old DEK discarded |
| Lazy re-encryption on access | NOT IMPLEMENTED |
| Rotation deadline enforcement | NOT IMPLEMENTED |
| Admin-forced platform-wide rotation | NOT IMPLEMENTED |

### US-10.9: Legacy Credential API Compatibility — **NOT BRIDGED**

| Requirement | Status |
|---|---|
| Legacy PUT/DELETE still work | YES (routes registered, deprecation headers set) |
| Legacy creates encrypted user_secrets | NO — still creates K8s Secrets, NOT user_secrets |
| Deprecation headers | DONE (Deprecation: true, Sunset: 2027-01-01) |

---

## Design Decision Compliance

| # | Decision | Compliant? |
|---|---|---|
| Zero-knowledge at rest | YES — AES-256-GCM, ciphertext only in DB |
| Login is the unlock event | YES (code) / BLOCKED (sessionID bug) |
| User-level secrets, workspace-level bindings | YES |
| Graceful rotation (password change = O(1)) | YES |
| Design for Vault, build for Postgres | YES — SecretProvider interface |
| owner_id/owner_type for org extensibility | PARTIAL — SecretProvider uses SecretOwner, but DB schema uses user_id |

---

## Recommended Next Steps for Epic 10 Agent

### Priority 0: Fix Runtime Bugs (blocks all testing)

1. **Fix sessionID in auth middleware** — Add `c.Set("sessionID", jti)` in `auth.go` `AuthMiddleware()`
2. **Add key initialization path for existing users** — endpoint or auto-init on first secret creation

### Priority 1: Complete Lazy DEK Rotation (US-10.8)

3. **Add `wrapped_dek_prev` field** to `UserKeyRecord` and `user_keys` table
4. **Retain old DEK** after rotation for decrypting old-version secrets
5. **Implement lazy re-encryption** in `DecryptSecretValue` — check key_version, re-encrypt if stale

### Priority 2: Complete Legacy Bridge (US-10.9)

6. **Bridge `PUT /workspaces/:id/credentials`** to create `llm-provider` user_secret + binding
7. **Bridge `DELETE /workspaces/:id/credentials`** to unbind + delete user_secret

### Priority 3: Missing Pieces

8. **Add `GET /api/v1/account/recovery-key`** endpoint for recovery key regeneration
9. **Log `unbind` and `rotate` audit actions** in SecretService
10. **Wire AsyncAuditLogger** into production flow
11. **Validate git-credential metadata** (`host`, `protocol`) and llm-provider metadata (`provider`)
12. **Replace in-memory adapters** (`dbKeyStoreAdapter`, `dbSecretStoreAdapter`) with real Postgres adapters

---

## Files Reviewed

### pkg/secrets/ (12 files)
- `types.go`, `provider.go`, `store.go`, `crypto.go`
- `key_service.go`, `secret_service.go`, `postgres_provider.go`
- `pg_key_store.go`, `pg_secret_store.go`, `redis_cache.go`, `injection.go`

### API Layer (5 files)
- `api/internal/handlers/secrets.go`
- `api/internal/server/router.go`
- `api/internal/app/app.go`, `api/internal/app/secrets_adapters.go`
- `api/internal/services/auth/auth.go`

### Migrations (2 files)
- `api/migrations/000007_user_keys.up.sql` (renamed from 006)
- `api/migrations/000008_user_secrets.up.sql` (renamed from 007)

### Test Files (19 files, ~158 tests)
- `pkg/secrets/`: crypto_test, crypto_boundary_test, key_service_test, key_service_unhappy_test, secret_service_test, secret_service_extended_test, injection_test, integration_test, pg_integration_test, e2e_test, key_rotation_test, redis_cache_test
- `api/internal/handlers/`: secrets_test, secrets_extended_test, secrets_integration_test, secrets_test_helpers_test
- `api/internal/app/`: secrets_wiring_test, e2e_http_test
- `api/internal/utilities/`: jti_test

---

## Action Taken

- **Fixed migration version collision** — renamed 006→007, 007→008, added to Helm chart
- **Set `mike@kao.family` as admin** in database
- **Manually initialized key material** for mike to enable partial testing
- Deployed fix as revision 50, verified migration 8 applied clean
