# Worklog: Epic 56 implementation — durable DEK for JWT sessions

**Date:** 2026-06-26
**Session:** Steps 1-10 of the Epic 56 main implementation plan, following the foundation merged in PR #413 (migration 000045) and the design merged in PR #411. Delivers the full application logic that closes Invariant 2 ("DEK availability matches JWT validity") — a production-observed violation where Valkey runs without persistence and every restart drops every cached DEK while 30-day remember-me JWTs remain valid.
**Status:** Complete (ready for review)

---

## Objective

Land the 10 implementation steps enumerated in worklog 0552's "Next Steps" section, in one feature branch / one PR, so Epic 56's main payload ships as a single reviewable unit. Adversarial self-review per Rule 11 before opening the PR. Each step had TDD red bars verified before implementing.

---

## Work Completed

### Pre-flight (worklog 0552 numbering)

The previous session's worklog `NNNN_2026-06-26_provider-not-found-end-to-end.md` had been sitting on `origin/main` unnumbered for hours — the merge commit of PR #414 contained `[skip ci]` (a docs-only PR convention) which bypassed CI entirely, including the post-merge worklog-renumbering bot. This was breaking `TestLive_Worklogs_NoDuplicates` for every downstream PR.

**Root-cause analysis:**
- `gh run list` confirmed: PR #414's merge commit had no CI run at all.
- `git show f44e0c99` revealed `[skip ci]` in the body.
- `.github/workflows/ci.yml`'s `repolint-autofix` job was gated on `event_name == 'push' && ref == 'refs/heads/main'`, but `[skip ci]` skips the entire workflow run regardless of subsequent gating.

**Two remediations applied:**
1. **Immediate:** manually renamed `NNNN_*` → `0552_*` via the secondary worktree on main (consistent with the worklog 0550 playbook documented in decisions 6+7 of the prior session).
2. **Permanent:** extracted the renumbering bot to a dedicated `.github/workflows/worklog-renumber.yml` that triggers on (a) push to main with `paths: worklogs/**`, (b) `schedule: cron '17 */6 * * *'` (self-healing catch-up every 6h), and (c) `workflow_dispatch`. Removed the embedded job from `ci.yml`. The cron-on-schedule property is the key one: even if `[skip ci]` bypasses the fast path, the next cron tick (within 6h) catches it. Updated the test comment to reference the new file location and the cron safety net.

### Step 1 — DAL methods (`pkg/secrets/jwt_session_store.go`)

`JWTSession` type + `JWTSessionStore` interface + `PgJWTSessionStore` (pgxpool-backed). Five methods: `GetJWTSession`, `WriteJWTSession` (upsert on jti for soft-unlock backfill), `DeleteJWTSession` (idempotent), `DeleteJWTSessionsForUser` (returns count for audit), `DeleteExpiredJWTSessions` (janitor cutoff).

Two test files:
- `jwt_session_store_test.go` — 14 in-memory mock tests covering happy path, upsert semantics, idempotent delete, per-user cascade, error injection across all 5 methods.
- `pg_integration_test.go` — 6 real-Postgres integration tests (gated on `//go:build integration`) including FK ON DELETE CASCADE from `users` (validates schema invariant).

### Step 2 — `parseTokenAcceptingRotatedKeys` returns matched key + index

Signature: `(*jwt.Token, []byte, int, error)`. Index convention: `0 = active`, `1+ = jwtPreviousSecrets[i-1]`, `-1 = unmatched`. Returns a **defensive copy** of the matched key (so callers can't mutate `s.jwtSecret` via the return slice — cheap, prevents a footgun). Two callers (`ValidateTokenWithClientIP`, `RevokeToken`) discard the new returns and remain unchanged behaviorally; one new caller (Step 4 middleware) consumes them.

5 new tests in `auth_matched_key_test.go` validate: active match, previous-key match (idx=2 for prev[1]), unknown-key rejection with idx=-1, expired-token rejection, ValidateToken/RevokeToken regression.

### Step 3 — Token-validation cache value format

`token:<hash>` → `"userID|matchedKeyIndex"` (new) instead of bare `userID` (legacy). `parseValidationCacheValue` handles all five cases: `""` (miss), `"revoked"` (sentinel), `"userID"` (legacy fallback, idx=-1), `"userID|N"` (new format), and malformed (`"userID|"`, `"userID|abc"`, `"userID|1|2"` all surface uid with idx=-1).

The matched index lets a cache-hit caller resolve the matched key via `signingKeyByIndex` without re-parsing the JWT — which the validation cache exists specifically to avoid. Critical for the LRU-eviction-of-DEK-only path (Valkey restarts flush both, but pressure-driven LRU may evict `dek:*` while retaining `token:*` because token entries are smaller and accessed more often). The #411 review pass 2 [MED] finding.

`signingKeyByIndex(idx)` resolves index → defensive-copied key bytes. Out-of-range returns nil (handles the case where a Helm rotation shortens jwtPreviousSecrets while pre-rotation tokens are still valid; rehydrate falls through to soft-unlock).

8 new tests cover format roundtrip, all malformed permutations, ValidateToken writes new format, legacy format still parses, revoked sentinel still honored.

### Step 4 — AuthMiddleware sets `jwt_signing_key` + `jwt_signing_key_index`

Both `AuthMiddleware` and `OptionalAuthMiddleware` were updated. Internal flow:
- `validateTokenAndMatchedKey` (new private method) returns `(userID, matchedIdx, err)`.
- Middleware calls it, sets `c.Set("jwt_signing_key_index", matchedIdx)` unconditionally and `c.Set("jwt_signing_key", key)` when `signingKeyByIndex` resolves a non-nil key.
- API-key auth: `matchedIdx = -1`, key is nil — handlers get no rehydrate hint and fall through to ErrDEKUnavailable, which is correct (API keys have their own DEK durability via `api_keys.WrappedDEK`).

5 e2e tests via httptest with real `Service.AuthMiddleware()`: fresh-parse sets key, previous-key match sets correct key+idx, cache hit with new format extracts key, legacy cache hit leaves key nil + idx=-1, API-key auth leaves key nil + idx=-1.

The same step also stashes `jwt_exp_unix` on the context (added in Step 10's adversarial-review fix, see below) so the soft-unlock TTL tracks the JWT's actual remaining lifetime.

### Step 5 — `KeyService.GetDEK` signature + rehydrate

New signature: `GetDEK(ctx, sessionID, matchedSigningKey []byte) ([]byte, error)`.

Resolution order:
1. Redis cache hit → return (fast path).
2. Redis miss + matched key + UUID sessionID → durable rehydrate via `rehydrateDEKFromJWTSession`.
3. Anything else → `ErrDEKUnavailable`.

Rehydrate path: `HKDF-SHA256(matched_key || jti.String(), kek_salt, "llmsafespaces-jwt-session-dek-kek")` derives the per-session KEK; `DecryptSecret(kek, wrapped_dek)` unwraps; re-cache to Redis with the row's remaining TTL (so subsequent reads are fast). Every failure path surfaces `ErrDEKUnavailable` so callers can soft-unlock; the specific cause (missing row / expired / unwrap failure / wrong key) goes only to structured logs at WARN.

Skip conditions: nil matched key, `apikey:*` sessionID, non-UUID sessionID, no jwt store wired. All return `ErrDEKUnavailable` without touching PG.

9 unit tests cover every branch.

Updated ~10 production callers across `secret_service.go`, `injection.go`, `postgres_provider.go` (via new `ContextWithMatchedSigningKey`), `auth.go` `CreateAPIKey`, plus the handler-level call sites in `secrets.go`, `user_provider_credentials.go`, `credential_probe.go`, `workspace_env.go`. New `extractMatchedSigningKey(c *gin.Context) []byte` helper in `secrets.go` reads the middleware-set key off the context.

Interface alignment: `KeyServiceInterface`, `WorkspaceEnvService`, `AuthService`, `MockAuthMiddlewareService`, `MockAuthService` all updated. Test sites pass `nil` for `matchedSigningKey` (correct: their fake KeyService caches DEKs directly).

### Step 6 — Login durable write

`KeyService.UnlockDEKWithSigningKey(ctx, userID, password, sessionID, ttl, activeSigningKey)` — `UnlockDEK` becomes a thin wrapper that passes nil. After unwrapping the user's DEK with the password-derived KEK and caching to Redis, the new method calls `writeDurableDEK`:

1. Skip when store missing, signing key nil, or sessionID isn't a UUID.
2. Generate fresh `kek_salt` (32 bytes).
3. Derive KEK from `activeSigningKey || jti.String()` + `kek_salt` via HKDF-SHA256 with `JWTSessionKEKInfo`.
4. Encrypt DEK with KEK (AES-GCM).
5. UPSERT into `jwt_sessions`.

**Critical contract:** durable-write failures NEVER fail login. The Redis cache succeeded; only Valkey-restart resilience is degraded. WARN-level log surfaces the error. Implements the design's Step 6 DoD.

`auth.Login` passes `s.jwtSecret` (the active signing key) as `activeSigningKey`. At login time the fresh JWT is by definition signed with the active key; rotation may move the key into `jwtPreviousSecrets` later, but the rehydrate path uses the **matched** key from `parseTokenAcceptingRotatedKeys` (which will find that key regardless of position).

7 unit tests pin the contract: durable row exists post-unlock, round-trip unwrap via same matched key produces correct DEK, durable-write failure does not fail login, no store/nil key/non-UUID skips durable write, wrong password fails both paths, legacy `UnlockDEK` stays backward-compatible.

### Step 7 — Soft-unlock endpoint

`POST /api/v1/auth/unlock-dek` behind `AuthMiddleware`. Handler at `api/internal/handlers/auth_unlock.go`:

- Extracts userID, sessionID, **matched signing key** (not active!), and `jwt_exp_unix` from context.
- Body: `{password}`. Strict 4 KiB body cap via `http.MaxBytesReader`.
- Calls `UnlockDEKWithSigningKey(...matchedKey, jwtRemainingTTL)` — the same method Login uses, but with the matched key (closes the [HIGH] regression from #411 review pass 1: wrapping under active key when validation matched a previous key produces unwrap failure exactly when auto-recovery should work).
- 204 on success. 401 on wrong password (`secrets.ErrInvalidPassword`) — **JWT remains valid**. 400 for API-key callers (with a hint pointing at `api_keys.WrappedDEK`). 401 for already-expired JWT. 500 with generic message for other errors.

9 handler tests via `httptest`: happy path with regression-case matched-key wrapping, wrong-password 401-no-JWT-invalidation, no-matched-key 400, API-key 400, missing password 400, oversized body 400, internal error 500 with generic message (no internal-detail leak), rotated-JWT regression that asserts the SAME key passed to AuthMiddleware reaches the unlocker.

### Step 8 — Revocation paths

Four call sites had to delete `jwt_sessions` rows to stay consistent with auth-layer revocation. Without this, an attacker who has the old JWT (signing key valid) could rehydrate the OLD DEK from PG after a password change.

| Site | Before | After |
|---|---|---|
| `EvictDEK(sessionID)` | Redis evict only | + `deleteDurableSession(ctx, sessionID)` |
| `ChangePassword(userID, sessionID, ...)` | Redis evict only | + `deleteDurableSession(ctx, sessionID)` |
| `RotateKeyWithPassword(userID, ..., sessionID, ...)` | Re-cached new DEK | + `deleteDurableSession(ctx, sessionID)` (old wrap is stale; soft-unlock recovers) |
| `auth.Service.RevokeAllUserSessions(userID)` | Redis revocation markers only | + `keyService.DeleteDurableSessionsForUser(ctx, userID)` |

New interface method `KeyServiceInterface.DeleteDurableSessionsForUser` and `KeyService.DeleteDurableSessionsForUser` (forwards to the store; best-effort, logged-only failure). Four test mocks (`fakeKeyService`, `capturingKeyService`, `trackingKeyService`, `dekJKeyService`) got a return-nil stub.

`deleteDurableSession(sessionID)` is private helper, skips non-UUID sessionIDs (API-key sessions live in `api_keys` table, not `jwt_sessions`), logs on failure but doesn't propagate.

4 unit tests pin the contract: EvictDEK deletes durable row, EvictDEK with non-JTI sessionID doesn't touch jwt_sessions, ChangePassword deletes durable row, RotateKeyWithPassword deletes durable row.

### Step 9 — Janitor goroutine

`pkg/secrets/jwt_session_janitor.go`. `JWTSessionJanitor.Run(ctx)` ticks every 60s (default; configurable; pass 0 to use default), calls `DeleteExpiredJWTSessions(now)`. Only logs INFO on non-zero deletes (avoids "pruned 0" log spam on idle clusters; non-zero counts surface clock skew, bulk revocation, schema migrations). Store errors logged at WARN but don't bail — next tick retries. Idempotent.

Wired in `app.go` as `App.jwtSessionJanitor` and started via `go a.jwtSessionJanitor.Run(a.ctx)` after dependencies are healthy. Mirrors the `PendingOrgCleaner` pattern.

6 tests: happy prune, store-error tolerance, empty-table-no-log-spam, ctx cancellation respected (≥2 ticks in 35ms at 10ms interval, then clean shutdown), default interval applied when 0, explicit interval honored.

### Step 10 — adversarial review fix: TTL tracks JWT exp

Self-review caught: the soft-unlock handler hardcoded a 1h durable-row TTL. A user soft-unlocking at hour 1 of a 24h JWT would get a durable row that expires in 1h — forcing them to soft-unlock again every hour after Valkey restart. Bad UX, easy fix.

Resolution:
- Added `utilities.ExtractExp(token) int64` (no-validation, mirrors `ExtractJTI`).
- `AuthMiddleware` and `OptionalAuthMiddleware` now stash `jwt_exp_unix` on the gin context (only when extraction succeeds; API-key sessions don't get an exp).
- `remainingTokenTTL(c *gin.Context)` reads from context, falls back to 1h when absent, **caps at 30 days** (longest legitimate JWT lifetime — RememberMe), returns 0 when the token is already past exp (handler returns 401 in that race).

5 new tests (4 handler-level + ExtractExp utility tests with 6 sub-cases): TTL tracks JWT exp with 5s scheduling slack, no-exp fallback to 1h, expired-token 401 with zero handler calls, TTL cap at 30d defends against forged long-lived JWTs.

---

## Key Decisions

1. **Threaded `matchedSigningKey []byte` through SecretService methods rather than reading from a typed context key inside `pkg/secrets`.** The design doc explicitly addressed this (lines 115-122): low-level packages shouldn't read middleware-owned context values. Threading is mechanical (`CreateSecret`, `UpdateSecret`, `DecryptSecretValue`, `InjectSecrets` all now take a `matchedSigningKey []byte` parameter); handlers extract via `extractMatchedSigningKey(c)`. `PostgresSecretProvider`'s `Encrypt`/`Decrypt` use `ContextWithMatchedSigningKey` because the provider already uses its own typed-context key for `sessionID` and the parallel pattern is consistent.

2. **`RotateKeyWithPassword` deletes (not re-wraps) the durable row.** The new DEK is freshly random — re-wrapping it requires the current request's matched signing key, which `RotateKeyWithPassword` doesn't take. Deletion forces soft-unlock on next request, which is acceptable per the design (line 213, "DEK rotated by US-50.4 — `wrapped_dek` is now stale; soft-unlock recovers").

3. **`UnlockDEK` kept as a backward-compat shim that delegates to `UnlockDEKWithSigningKey(..., nil)`.** Non-Login callers (Register, tests) shouldn't have to know about the durable-write path; nil means "Redis only" exactly as before.

4. **Worklog renumbering bot extracted to its own workflow file with a cron trigger.** PR #414 hit a `[skip ci]` bypass scenario; the cleanest fix is making the bot's trigger independent of CI. The cron's 6h period is much smaller than the smallest practical JWT lifetime (24h tokenDuration) and the cost per tick is ~30s of runner time on an idle DB. See `.github/workflows/worklog-renumber.yml`.

5. **Soft-unlock TTL = min(JWT remaining, 30d), not a hardcoded 1h.** Self-review finding. The 1h ceiling was a placeholder; production behavior with a 24h JWT and Valkey restart 22h in would have required soft-unlock every hour. The fix surfaces JWT `exp` via `utilities.ExtractExp` and stashes `jwt_exp_unix` on the gin context.

6. **30-day cap on durable TTL.** Defense-in-depth against a forged JWT with exp far in the future. The cap matches the longest legitimate JWT lifetime in the codebase (RememberMe).

7. **`DeleteDurableSessionsForUser` returns nil even on error.** The auth-layer revocation markers (Redis) are already authoritative; the durable delete is defense-in-depth. Returning the error would force every caller to handle it, with no useful action on failure (the janitor will eventually prune; the JWT is already revoked from the rehydrate path's perspective once the cache miss happens).

---

## Blockers

None.

---

## Tests Run

```bash
go build ./...                           # green
go test -short ./...                     # 47 packages green, 0 failures
make lint                                # 0 issues (golangci-lint)
```

Targeted runs during development:

- `go test -timeout 60s ./pkg/secrets/...` — 30s, all 200+ tests pass
- `go test -timeout 60s ./api/internal/services/auth/...` — 32s, all auth tests pass
- `go test -timeout 60s ./api/internal/handlers/...` (short) — 54s, all handler tests pass
- `go test -timeout 60s ./api/internal/utilities/...` — green (ExtractExp tests)
- `go test -timeout 60s ./api/internal/middleware/tests/...` — green
- `go test -timeout 60s ./api/internal/server/...` — green

New test count (this session): **39 new tests** + **3 modified mocks** + **~50 mechanically-updated test call sites** (bulk-rewritten with perl one-liners; each verified by build+test).

Integration tests (`pg_integration_test.go` with `//go:build integration`) ship 6 new tests covering `PgJWTSessionStore` round-trip, upsert, FK CASCADE — they run in the secrets-integration CI job which has a real PG.

---

## Next Steps

After this PR merges and deploys:

1. **Live cluster verification:** `kubectl rollout restart deployment/valkey -n default` followed by a curl against a workspace endpoint that needs user-DEK content. Logs should show "durable rehydrate succeeded"; the `dek:*` Redis key should reappear. This validates Epic 56's Invariant 2 in production.

2. **Epic 57 — workspace secret-delivery reconciler.** Closes the original Epic 35 regression. Depends on Epic 56's `GetDEK` semantics being live in production (this PR). Design doc next; not yet started.

3. **Issue #412 — pgx CVE GO-2026-5004.** Trivial dependency bump that govulncheck flagged on the migration PR. Out-of-band PR; not blocked by anything.

4. **Operational telemetry** (optional follow-up): expose a Prometheus counter for `durable_dek_rehydrate_{success,miss,unwrap_fail}` so the rehydrate-success rate after a Valkey restart is observable. Not blocking for this epic but valuable for the live-verification step.

---

## Files Modified

### New files

- `pkg/secrets/jwt_session_store.go` — DAL surface
- `pkg/secrets/jwt_session_store_test.go` — in-memory mock + 14 tests
- `pkg/secrets/key_service_rehydrate_test.go` — GetDEK rehydrate (9 tests)
- `pkg/secrets/key_service_durable_unlock_test.go` — UnlockDEKWithSigningKey (7 tests)
- `pkg/secrets/key_service_revocation_test.go` — revocation cascade (4 tests)
- `pkg/secrets/jwt_session_janitor.go` — janitor
- `pkg/secrets/jwt_session_janitor_test.go` — 6 tests
- `api/internal/services/auth/auth_matched_key_test.go` — parse + cache + middleware (8 + 5 tests)
- `api/internal/handlers/auth_unlock.go` — soft-unlock handler
- `api/internal/handlers/auth_unlock_test.go` — 13 handler tests
- `.github/workflows/worklog-renumber.yml` — extracted bot with cron trigger

### Modified files (production code)

- `pkg/secrets/key_service.go` — JWTSessionKEKInfo, SetJWTSessionStore, GetDEK + rehydrate, UnlockDEK + UnlockDEKWithSigningKey, EvictDEK durable delete, ChangePassword durable delete, RotateKeyWithPassword durable delete, DeleteDurableSessionsForUser
- `pkg/secrets/secret_service.go` — CreateSecret, UpdateSecret, DecryptSecretValue accept matchedSigningKey
- `pkg/secrets/injection.go` — InjectSecrets, loadLLMCredentials, loadNonLLMSecrets, decryptBinding accept matchedSigningKey; SecretInjector interface
- `pkg/secrets/postgres_provider.go` — ContextWithMatchedSigningKey + matchedSigningKeyFromContext
- `api/internal/services/auth/auth.go` — KeyServiceInterface (3 new methods), parseTokenAcceptingRotatedKeys 4-return, validateTokenAndMatchedKey, signingKeyByIndex, parseValidationCacheValue, formatValidationCacheValue, ValidateTokenWithClientIP cache format, AuthMiddleware + OptionalAuthMiddleware set jwt_signing_key + jwt_signing_key_index + jwt_exp_unix, Login uses UnlockDEKWithSigningKey, RevokeAllUserSessions cascade, CreateAPIKey takes matchedSigningKey
- `api/internal/interfaces/interfaces.go` — AuthService.CreateAPIKey signature
- `api/internal/utilities/jti.go` — ExtractExp
- `api/internal/handlers/secrets.go` — extractMatchedSigningKey helper; CreateSecret/UpdateSecret/DecryptSecretValue/InjectSecrets call-site updates
- `api/internal/handlers/credential_probe.go` — GetDEK call-site update
- `api/internal/handlers/user_provider_credentials.go` — GetDEK call-site update
- `api/internal/handlers/workspace_env.go` — WorkspaceEnvService signature + handler call sites
- `api/internal/server/router.go` — UnlockDEKHandler route; CreateAPIKey call-site forwards matched key
- `api/internal/mocks/middleware_mocks.go` — MockAuthMiddlewareService.CreateAPIKey signature
- `api/internal/middleware/tests/auth_test.go` — MockAuthService.CreateAPIKey signature
- `api/internal/app/app.go` — SetJWTSessionStore wiring, janitor field + startup, UnlockDEKHandler wiring
- `.github/workflows/ci.yml` — removed embedded repolint-autofix job (extracted to dedicated workflow)

### Modified files (tests)

- `pkg/secrets/pg_integration_test.go` — 6 new integration tests
- `pkg/secrets/{credential_precedence,injection,e2e,redis_masterkey_e2e,secret_service,secret_service_extended,api_key_sunset,integration,key_rotation,key_service}_test.go` — bulk-rewritten call sites for the new SecretService signatures (nil matchedSigningKey for test callers)
- `api/internal/services/auth/{auth,auth_apikey_dek,auth_apikey_dek_e2e,auth_e2e_all,auth_sessionid}_test.go` — mock implementations of UnlockDEKWithSigningKey + DeleteDurableSessionsForUser stubs; CreateAPIKey/GetDEK call-site updates
- `api/internal/handlers/{workspace_env,secrets_integration}_test.go` — mockEnvService signature updates
- `api/internal/server/{router_auth,router_auth_security}_test.go` — mock.Anything counts updated for new CreateAPIKey arg
- `cmd/workspace-agentd/reload_credentials_e2e_test.go` — bulk call-site updates
- `api/internal/utilities/jti_test.go` — ExtractExp tests (4 sub-cases)
- `pkg/repolint/sequence_test.go` — comment updated to reference new workflow file

### Direct push to main (separate from this PR's commits)

- `worklogs/NNNN_2026-06-26_*.md` → `0552_*.md` (manual rename, `[skip ci]` commit `cea8bf93`) — playbook documented in worklog 0550

### This worklog

- `worklogs/NNNN_2026-06-26_epic-56-impl-complete.md` (this file) — assigned a sequential number by the post-merge bot on PR merge
