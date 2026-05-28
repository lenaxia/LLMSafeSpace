# Worklog: Epic 10 Live Production Validation

**Date:** 2026-05-28
**Session:** Full live production validation of Epic 10 against deployed cluster
**Status:** Complete — 7/10 phases pass, 3 fail due to known bugs

---

## Objective

Execute all 10 validation phases from the Epic 10 production validation plan against the live k8s deployment at `safespace.thekao.cloud`. Record every HTTP status code and response body. State and verify all assumptions per Rule 7.

---

## Assumptions Stated & Verified

| # | Assumption | Verified? | Evidence |
|---|-----------|-----------|----------|
| A1 | Deployed image `sha-ded3c83` is built from git commit `ded3c83` | YES | `git show ded3c83` shows matching commit message and file changes |
| A2 | Commit `ded3c83` contains the sessionID middleware fix (Bug 2 from worklog 0061) | YES | `git show ded3c83:api/internal/services/auth/auth.go:576` shows `c.Set("sessionID", jti)` |
| A3 | Commit `ded3c83` does NOT contain the ChangePassword bcrypt fix | YES | `git merge-base --is-ancestor 573e8ad ded3c83` returns exit code 1. Commit `573e8ad` is a LATER commit on main |
| A4 | Both API pods run the same image | YES | Both pods show `ghcr.io/lenaxia/llmsafespace/api:sha-ded3c83` via `kubectl get pods -o custom-columns` |
| A5 | In-memory adapters are wired into production binary (not PgKeyStore/PgSecretStore) | YES | `app.go:97-98` uses `&dbKeyStoreAdapter{}` and `&dbSecretStoreAdapter{}` unconditionally. No conditional logic for PgKeyStore/PgSecretStore |
| A6 | Port-forward routes to a single pod; in-memory state is pod-local | YES | `kubectl port-forward svc/llmsafespace-api 28080:8080` connects through service to one endpoint. Data persists across requests within same port-forward session |
| A7 | ChangePassword in deployed version only re-wraps DEK, does not update bcrypt hash | YES | `git show ded3c83:pkg/secrets/key_service.go:165-198` — function derives new KEK, re-wraps DEK, calls `UpdateWrappedDEK`. No bcrypt update anywhere in the function |
| A8 | Legacy credentials 500 error on `ws-validation` is because it's not a valid UUID | CORRECTED | Error message: `"invalid input syntax for type uuid: \"ws-validation\" (SQLSTATE 22P02)"`. Not a K8s CRD issue — it fails at the SQL query level |
| A9 | DB has 1 user_keys row from manually-inserted mike@kao.family key material | YES | `SELECT count(*) FROM user_keys` returns 1. `SELECT user_id FROM user_keys` returns `4382f558-a03b-437c-bb83-7de0a82ab612` (mike) |
| A10 | Workspace env vars stored via same in-memory adapter (not separate storage) | YES | `handlers/secrets.go:166-280` — `SetWorkspaceEnv` calls `h.svc.CreateSecret`/`h.svc.UpdateSecret` which go through the same `dbSecretStoreAdapter` |
| A11 | Key rotation generates new DEK (keyVersion increments) | PARTIAL | API returns `keyVersion: 2` and subsequent secret creation succeeds. Cannot verify DEK material change due to in-memory storage. The keyVersion increment and successful post-rotation encryption are the strongest available evidence |
| A12 | Audit entries are in-memory only (not persisted to DB) | YES | `SELECT count(*) FROM secret_audit_log` returns 0, but API returns 18 entries. Confirmed in-memory via `dbSecretStoreAdapter.audit []AuditEntry` |

---

## Prerequisites

| Check | Result |
|---|---|
| API pods (x2) running | `sha-ded3c83`, 1/1 Running, 0 restarts |
| `/livez` | `{"status":"ok"}` |
| `/readyz` | `{"status":"ready"}` |
| Migration 8 applied | `SELECT version FROM schema_migrations` → 8 |
| `user_keys` table exists | YES |
| `user_secrets` table exists | YES |
| `user_secret_bindings` table exists | YES |
| `secret_audit_log` table exists | YES |

---

## Validation Results

### Phase 1: Authentication — PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Register `epic10-test` | 201 | 201, token + user returned | PASS |
| Login | 200, non-empty token | 200, 248-char JWT | PASS |

### Phase 2: Secret CRUD — PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Create LLM provider secret | 201 | 201, id=`8f6c455b...` | PASS |
| Get secret — value NOT in response | no value | Value absent (grep count = 0) | PASS |
| Create SSH key secret | 201 | 201 | PASS |
| Create env secret | 201 | 201 | PASS |
| List all | 3 | 3 | PASS |
| Update secret | 204 | 204 | PASS |
| Delete secret | 204 | 204 | PASS |
| List after delete | 2 | 2 | PASS |

### Phase 3: Validation Errors — PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Invalid type | 400 | 400 `"invalid secret type: invalid-type"` | PASS |
| SSH key missing metadata | 400 | 400 `"ssh-key requires metadata with key_type field"` | PASS |
| Duplicate name | 409 | 409 `"secret with this name already exists"` | PASS |
| Unauthenticated | 401 | 401 `"Authorization token required"` | PASS |

### Phase 4: Workspace Bindings — PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Bind 2 secrets to workspace | 204 | 204 | PASS |
| Get bindings | 2 | 2 | PASS |

### Phase 5: Workspace Env Overrides — PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Set 2 env vars | 204 | 204 | PASS |
| Get env vars (names only, no values) | names, no values | 3 names returned, grep for `redis://localhost` = 0 | PASS |
| Delete env var | 204 | 204 | PASS |

### Phase 6: Key Rotation — PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Rotate key | 200, keyVersion: 2 | 200, `{"keyVersion":2}` | PASS |
| Create secret after rotation | 201 | 201 | PASS |
| Wrong password | 403 | 403 | PASS |

### Phase 7: Password Change — FAIL (deployed code missing bcrypt fix)

| Step | Expected | Actual | Status |
|---|---|---|---|
| Change password | 204 | 204 | PASS |
| Old password login | 401 | **200** | **FAIL** |
| New password login | 200, token | **401** | **FAIL** |
| Secrets accessible with new token | >= 3 | **N/A** (new password login fails) | **FAIL** |
| Create secret with old password token | should work | **403** `"encryption key not available; re-authenticate"` | **FAIL** |

**Root cause (verified):** Deployed image `sha-ded3c83` does NOT contain commit `573e8ad` which fixes ChangePassword to update the bcrypt hash. The `KeyService.ChangePassword` (`key_service.go:165-198`) only re-wraps the DEK with the new password. The users table `password_hash` column is never updated.

**Cascading effect:** After ChangePassword:
1. bcrypt unchanged → old password still works for login
2. DEK re-wrapped with new password → old password can't unwrap it
3. DEK unlock at login fails silently → secret creation returns 403
4. New password can't login at all → user is locked out of secrets

**Fix exists in commit `573e8ad`** but is NOT deployed. Deploying `sha-573e8ad` or later would fix this.

### Phase 8: Audit Log — PARTIAL PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Audit entry count | >= 5 | 18 | PASS |
| Contains create, update, delete, bind, read | all 5 | create, update, delete, bind (**missing read**) | PARTIAL |
| Audit persisted to DB | rows in secret_audit_log | **0 rows** (in-memory only) | FAIL |

**Findings:**
1. `read` action not logged — GetSecret/GetBindings do not write audit entries
2. All audit data is in-memory (`dbSecretStoreAdapter.audit` slice) — lost on pod restart
3. The PostgreSQL `secret_audit_log` table exists but is never written to

### Phase 9: Legacy Credential Deprecation Headers — PASS

| Step | Expected | Actual | Status |
|---|---|---|---|
| Deprecation header | `true` | `Deprecation: true` | PASS |
| Sunset header | date | `Sunset: 2027-01-01` | PASS |
| Link header | successor-version | `Link: </api/v1/secrets>; rel="successor-version"` | PASS |

Note: The endpoint returns 500 for `ws-validation` because it's not a valid UUID (SQL parse error). Headers are present regardless of response status.

### Phase 10: Database Verification — FAIL (all data in-memory)

| Query | Expected | Actual | Status |
|---|---|---|---|
| user_keys for test user | key_version >= 2, dek_len > 0 | **0 rows** | FAIL |
| user_secrets for test user | ct_len > 0, no plaintext | **0 rows** | FAIL |
| secret_audit_log for test user | multiple action types | **0 rows** | FAIL |
| No plaintext in ciphertext | 0 | 0 (vacuously true — no rows) | N/A |
| Total user_keys | >= 2 | 1 (only mike's manually-inserted key) | FAIL |

**Root cause (verified):** `app.go:97-98` wires `&dbKeyStoreAdapter{}` and `&dbSecretStoreAdapter{}` — both are in-memory Go maps/slices. The `db` field (`interfaces.DatabaseService`) is passed but never used for any queries. PostgreSQL-backed `PgKeyStore` and `PgSecretStore` exist in `pkg/secrets/` but are never imported or instantiated.

---

## Summary

### Phase Results

| Phase | Result |
|---|---|
| 1. Authentication | PASS |
| 2. Secret CRUD | PASS |
| 3. Validation Errors | PASS |
| 4. Workspace Bindings | PASS |
| 5. Workspace Env Overrides | PASS |
| 6. Key Rotation | PASS |
| 7. Password Change | **FAIL** — bcrypt not updated, DEK state corrupted |
| 8. Audit Log | **PARTIAL** — in-memory only, missing `read` action |
| 9. Deprecation Headers | PASS |
| 10. Database Verification | **FAIL** — all data in-memory, nothing persisted |

### Success Criteria Assessment

- [x] Secret CRUD works (create, get, list, update, delete) — **only in-memory**
- [x] Value NEVER appears in API responses
- [x] Validation errors return correct status codes (400, 409, 401)
- [x] Bindings work (set, get) — **only in-memory**
- [x] Workspace env overrides work (set, get, delete) — **only in-memory**
- [x] Key rotation works and secrets still accessible after — **only in-memory**
- [ ] **Password change works** — FAIL: bcrypt not updated, DEK state corrupted, user locked out
- [ ] **Audit log records all operations** — PARTIAL: missing `read`, in-memory only
- [x] Deprecation headers present on legacy endpoints
- [ ] **Database contains only ciphertext, never plaintext** — FAIL: database contains nothing at all

### Critical Bugs (Production Blockers)

**BUG A: In-memory adapters — zero persistence (Severity: CRITICAL)**
- All secrets, keys, bindings, and audit log are stored in Go maps/slices in a single API pod
- Data lost on pod restart, redeployment, or OOM kill
- With 2 API replicas, each pod has independent state — requests may hit different pods
- PgKeyStore and PgSecretStore exist but are not wired in
- File: `api/internal/app/secrets_adapters.go` (222 lines of in-memory adapters)
- Fix: Replace `dbKeyStoreAdapter` with `PgKeyStore` and `dbSecretStoreAdapter` with `PgSecretStore` in `app.go:97-98`

**BUG B: ChangePassword doesn't update bcrypt hash (Severity: CRITICAL)**
- Calling ChangePassword puts the user in an unrecoverable state:
  - Old password works for login but can't unlock DEK (re-wrapped with new password)
  - New password can't login (bcrypt unchanged)
  - All secret operations fail with 403
- Fix exists in commit `573e8ad` but is not yet deployed
- Fix: Deploy `sha-573e8ad` or later

### Secondary Gaps

1. `read` action not logged in audit (GetSecret/GetBindings don't call LogAudit)
2. Audit not persisted to PostgreSQL (in-memory only)
3. `unbind` action not logged (SetBindings replaces without logging removals)
4. `rotate` action not logged (rotation is in KeyService, not SecretService)
5. AsyncAuditLogger exists in `pkg/secrets/pg_secret_store.go` but not wired in

---

## Recommended Next Steps

### Priority 0: Wire PostgreSQL adapters (unblocks persistence)

1. Expose `pgxpool.Pool` from `interfaces.DatabaseService` (or create a separate pool in `app.go`)
2. Replace `&dbKeyStoreAdapter{}` with `secrets.NewPgKeyStore(pool)` in `app.go:97`
3. Replace `&dbSecretStoreAdapter{}` with `secrets.NewPgSecretStore(pool)` in `app.go:98`
4. Redeploy and re-run this validation

### Priority 1: Deploy bcrypt fix

5. Build and deploy `sha-573e8ad` or later to fix ChangePassword
6. Re-run Phase 7

### Priority 2: Audit gaps

7. Add `read` action logging in GetSecret and GetBindings handlers
8. Add `unbind` diff logging in SetBindings
9. Wire AsyncAuditLogger for production

---

## Files Reviewed (Assumption Verification)

- `api/internal/app/app.go` — wiring (lines 90-101)
- `api/internal/app/secrets_adapters.go` — in-memory adapters (full file, 222 lines)
- `api/internal/services/auth/auth.go` — sessionID fix (verified at line 576)
- `api/internal/handlers/secrets.go` — workspace env handlers (lines 166-280)
- `pkg/secrets/key_service.go` — ChangePassword (lines 165-198)

## Database Queries Executed

```sql
SELECT version FROM schema_migrations;  -- 8
SELECT count(*) FROM user_keys;         -- 1
SELECT count(*) FROM user_secrets;      -- 0
SELECT count(*) FROM user_secret_bindings;  -- 0
SELECT count(*) FROM secret_audit_log;  -- 0
SELECT user_id, key_version FROM user_keys;  -- 1 row (mike)
\dt  -- 15 tables including all 4 Epic 10 tables
```
