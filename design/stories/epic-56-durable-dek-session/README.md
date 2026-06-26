# Epic 56: Durable DEK for JWT Sessions

**Status:** Planning
**Created:** 2026-06-25
**Last Revision:** 2026-06-26 (review pass 2: added token-validation-cache interaction analysis — store `userID|matchedKeyIndex` in the token cache so a cache hit can surface the matched key without re-parsing; removed duplicate section header; rephrased the risk-table DB-compromise-alone row to cross-reference the threat-model section; added the worklog file to the diff).

**Earlier Revisions:**
- 2026-06-26 (review pass 1: corrected [HIGH] soft-unlock backfill ambiguity — wrap under matched key, not active key; rewrote threat-model framing so signing-key delivery is not equated with master-KEK delivery; refactored `GetDEK` so the low-level `pkg/secrets` package no longer reads middleware-owned context keys — caller passes the matched signing key explicitly; corrected change-site reference from `ValidateToken` to `parseTokenAcceptingRotatedKeys`; fixed `GetDEK` Redis-error handling to distinguish miss vs. transient error; added thundering-herd analysis as stress test #12; added regression test for rotated-key soft-unlock backfill; documented chart-migrations parity requirement; noted worklog deliverable).
**Depends On:** US-50.x (master KEK at rest), existing JWT multi-key rotation window
**Unblocks:** Epic 57 (workspace secret-delivery reconciler — Epic 35 regression fix)
**Priority:** High — closes Invariant 2 ("DEK availability matches JWT validity") that is violated in production today (Valkey runs without persistence; every Valkey restart drops every cached DEK while JWTs remain valid for up to 30 days).

---

## Why this exists

### Production observation

Workspace `d95b6751-...` had user-bound credentials but the user-DEK content (ssh-key, env-secret, user provider creds) never reached the running pod. The root cause investigation traced Epic 35 (PR #378) deleting `refreshEphemeralSecrets` and its three lifecycle callers — but resolving that revealed a deeper invariant violation:

**The `dek:<sessionID>` Redis entries are the only home for the user's DEK during a session.** On `valkey-766d6df8dd-qshl7`, only 1 DEK key exists for the entire cluster, despite multiple users having active 30-day remember-me JWTs. Every user whose DEK was evicted (by Valkey restart or LRU pressure) has a valid JWT they can't decrypt user content with.

### Invariants this epic enforces

1. **DEKs are encrypted at rest** (already met today via master-KEK wrapping in Redis; preserved by this epic).
2. **DEK is available for the full JWT lifetime — up to 30 days for remember-me — as close to zero-knowledge as possible.** Violated today; fixed by this epic.
3. **API / SDK / MCP are first-class DEK citizens.** Already met for API keys with `decrypt_access=true` (durable `wrapped_dek` in `api_keys` table). This epic mirrors that pattern for JWT sessions, closing the asymmetry.

### Anti-invariant

**No forced logout for DEK availability problems.** Identity (JWT validity) and decryption capability (DEK availability) are decoupled concerns. A user with a valid JWT but a Redis-evicted DEK should auto-recover transparently, or — in residual cases — recover via a soft "re-enter password" prompt that does not invalidate their session.

---

## Design

### Schema

```sql
CREATE TABLE jwt_sessions (
    jti          UUID PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wrapped_dek  BYTEA NOT NULL,           -- DEK wrapped with KEK derived from JWT signing key + jti
    kek_salt     BYTEA NOT NULL,           -- 32 random bytes; binds wrapped_dek to a specific HKDF derivation
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_jwt_sessions_user_id ON jwt_sessions(user_id);
CREATE INDEX idx_jwt_sessions_expires_at ON jwt_sessions(expires_at);
```

`expires_at` enables a periodic janitor to delete rows past JWT expiration (avoids unbounded growth).

### KEK derivation

The KEK that wraps `wrapped_dek` is derived at issue time from material that requires the JWT to be presented to reconstruct:

```
KEK = HKDF-SHA256(
    salt   = kek_salt,                                       // 32 random bytes per session, stored in DB
    secret = matched_jwt_signing_key || jti_as_canonical_str, // matched validation key + 36-char canonical UUID string
    info   = "llmsafespaces-jwt-session-dek-kek",
    length = 32,
)
```

**"matched_jwt_signing_key" is precise: the specific signing key under which the JWT validated.** Not necessarily `jwtSecret` (the active key) — could be any key in `jwtPreviousSecrets` if the JWT was issued before the most recent rotation. Every write site (login, soft-unlock backfill, US-50.4 rewrite) MUST wrap with the same matched key the rehydrate path will derive from when the JWT is presented later. This is non-negotiable: writing with `jwtSecret` and validating-then-rehydrating later under a previous key produces unwrap failure exactly when the user needs auto-recovery.

The HKDF `secret` field is built as `matched_signing_key_bytes || []byte(jti.String())`. The jti is the 36-character canonical UUID form (e.g. `e56a38cf-8f1a-46df-b8e3-5771d2e9054f`), matching what `uuid.New().String()` produces in `auth.go:426` and what is stored in `jwt_sessions.jti UUID`. There is no length-prefix delimiter; with HMAC-SHA256 as the HKDF PRF and a fixed-format UUID suffix, the structure is collision-free without one. (If a future change introduces variable-length suffixes, add a length prefix at that point.)

Why JWT signing key + jti, not the full token:

- **Stored material** (`kek_salt`, `wrapped_dek`) is useless without the matched signing key in memory. Stolen DB alone → unwraps nothing.
- **Presented JWT validates** under one of N signing keys in the rotation window. Whichever key validates is the one used to derive the KEK. Multi-key rotation already exists in `auth.go:131-137` and is extended (see Implementation note below) to surface the *matched* key, not just the parsed claims.
- **jti is in the JWT payload**, so we don't need to store it client-side or transmit it separately. The JWT itself is what the user always presents.
- **Signing key is never stored alongside `wrapped_dek`.** Even an attacker with full DB access cannot unwrap without dumping the API process memory.

**Threat model — important framing:**

The "DB alone is useless" property is genuine but the signing key's confidentiality is STRICTLY WEAKER than the master KEK's (US-50.1). Specifically:

- The master KEK is delivered via a **read-only file mount** (`/var/run/secrets/.../master-secret`, `masterSecret.deliveryMethod=file` by default) to keep it out of `/proc/1/environ`.
- The JWT signing key is delivered via **env var / Helm value** (`config.go: Auth.JWTSecret`, wired at `auth.go:240`). It appears in pod env, Helm values, and config backups. Leak channels exist for the signing key that do not exist for the master KEK.
- The signing key is a **single global secret** — compromise unwraps *every* `jwt_sessions` row across all users, not just one user's.
- Signing-key compromise is already catastrophic: an attacker with the signing key can forge JWTs and impersonate any user. So `wrapped_dek` reachability under signing-key compromise is a marginal increment over the pre-existing catastrophe, not a new failure mode. This is the honest framing.

Mitigations for the gap above (signing-key-vs-master-KEK delivery asymmetry):

- Document operator guidance for signing-key rotation cadence (out of scope for this epic; should be a separate ops document).
- Consider migrating `Auth.JWTSecret` delivery to file-mount under a follow-up epic, matching `masterSecret.deliveryMethod=file`. Not in scope here.

Note: this is a deliberate departure from a more typical "password-derived KEK" pattern. We can't use password here because the API process doesn't hold the password after login — and persisting it would violate zero-knowledge. The JWT signing key is the cleanest available "server holds in memory only" secret.

### Login (`auth.go:889` extended)

```
// Existing
keyService.UnlockDEK(ctx, userID, password, jti, tokenDur)
// New
//   jwtSigningKey is the *active* key at login time — by definition the key
//   this fresh JWT will validate under for its lifetime (or until rotation
//   moves it into jwtPreviousSecrets, at which point validation still
//   succeeds via the multi-key window).
KEK := HKDF(jwtSigningKey || jti, salt, "...dek-kek")
wrappedDEK := EncryptSecret(KEK, dek)
INSERT INTO jwt_sessions(jti, user_id, wrapped_dek, kek_salt, expires_at) VALUES (...)
zeroBytes(KEK)
```

Atomic: same handler that caches to Redis writes durably. Failure to write durable row is logged but does not fail login (Redis cache is still valid for the JWT's lifetime).

### GetDEK rehydrate-on-miss (`key_service.go`)

The caller (auth middleware) MUST pass the matched signing key into `GetDEK` explicitly. The low-level `pkg/secrets.KeyService` package does NOT read from `gin.Context` / middleware-owned context keys — that would be an upward-dependency violation (the codebase pattern at `auth.go:1244-1248` and `UnlockDEK`'s signature is "middleware sets context values, handler reads them and passes explicitly to KeyService"). The rehydrate path follows the same pattern.

```go
// New signature: caller passes the matched signing key (or nil if no JWT).
// API-key callers, controller-internal callers, and any other non-JWT path
// pass nil — they will not auto-rehydrate, which is correct (no JWT means
// no KEK material to derive from).
func (s *KeyService) GetDEK(ctx context.Context, sessionID string, matchedSigningKey []byte) ([]byte, error) {
    dek, err := s.cache.GetDEK(ctx, sessionID)
    if err != nil {
        // Distinguish a real Redis error from a miss. We may still attempt
        // rehydrate for resilience, but a real error is logged at warn so
        // operators can see when Redis is unhealthy. The existing GetDEK
        // (key_service.go:217-226) fails closed; this change preserves that
        // for the "no rehydrate available" case but allows recovery when
        // we have the durable row.
        s.logger.Warn("Redis DEK lookup failed; will attempt durable rehydrate", "error", err)
    } else if dek != nil {
        return dek, nil // fast path: Redis hit
    }

    // Redis miss (or transient error). Try durable rehydrate.
    return s.rehydrateDEKFromJWTSession(ctx, sessionID, matchedSigningKey)
}

func (s *KeyService) rehydrateDEKFromJWTSession(ctx context.Context, sessionID string, matchedSigningKey []byte) ([]byte, error) {
    if !looksLikeJTI(sessionID) {
        return nil, ErrDEKNotInCache // API-key sessions rehydrate from api_keys table, not this path
    }
    if matchedSigningKey == nil {
        return nil, ErrDEKUnavailable // no JWT in caller's context → caller is API-key or controller
    }
    row := SELECT user_id, wrapped_dek, kek_salt, expires_at FROM jwt_sessions WHERE jti = sessionID
    if row == nil || row.expires_at <= NOW() {
        return nil, ErrDEKUnavailable // pre-feature backfill case, or expired JWT
    }
    KEK := HKDF(matchedSigningKey || sessionID, row.kek_salt, "llmsafespaces-jwt-session-dek-kek")
    dek, err := DecryptSecret(KEK, row.wrapped_dek)
    if err != nil {
        return nil, ErrDEKUnavailable // wrong signing key / corrupted row / US-50.4 stale wrapped_dek
    }
    // Re-cache to Redis with remaining TTL.
    s.cache.CacheDEK(ctx, sessionID, dek, time.Until(row.expires_at))
    return dek, nil
}
```

The "matched signing key" piece requires extending the existing multi-key parse loop at `parseTokenAcceptingRotatedKeys` (`auth.go:1259-1297`). Today it parses with the active key, then each previous key, and **discards which one matched**. The fix returns the matched key alongside the parsed token, and the auth middleware sets it in the gin context (`c.Set("jwt_signing_key", matched)`). Handlers that call `GetDEK` extract it from context and pass it explicitly — same pattern as `userID` / `sessionID`.

#### Token validation cache interaction (#411 pass-2 [MED])

`ValidateTokenWithClientIP` (`auth.go:456-464`) maintains a token-validation cache: `token:<hash>` → `userID`, TTL = min(remaining JWT lifetime, 1h). On a cache hit, the function returns `userID` **without parsing the JWT** — so the matched signing key is never computed.

Gap scenario: Redis LRU pressure evicts `dek:<jti>` but RETAINS `token:<hash>` (the token-validation entries are smaller and accessed more often). The next request hits the token cache, the middleware never parses the JWT, no matched key in gin context. `GetDEK` receives `matchedSigningKey == nil` → returns `ErrDEKUnavailable` even though the durable row exists and the JWT is valid.

The Valkey-restart scenario (the epic's primary trigger) is NOT affected because both caches flush together — token gets re-parsed on the next request, matched key becomes available. This gap is specifically the **LRU-eviction-of-DEK-only** path, which is plausible given the production observation of "1 DEK key cluster-wide" today.

**Fix (chosen for implementation):** store the matched key index alongside the userID in the token cache, so a cache hit can still surface the matched key without re-parsing.

```
token:<hash> → "userID|matchedKeyIndex"      // matched key index into [active, prev[0], prev[1], ...]
```

The middleware reads both fields on cache hit and sets `c.Set("jwt_signing_key", keys[matchedKeyIndex])`. Cost: one tiny string change in the cache value format; no parsing on cache hit; no extra Redis round-trip; no change to the rotation window code paths.

Alternative considered: have the middleware re-derive the matched key from `tokenString` on cache hit by calling the parse loop anyway. Rejected because it defeats the point of the validation cache (which exists to skip parsing on hot paths).

### Soft-unlock endpoint

```
POST /api/v1/auth/unlock-dek
Body: { password }
Auth: JWT or API-key required (AuthMiddleware)

Behavior:
1. Extract userID, sessionID, and matched_signing_key from auth context.
2. Call keyService.UnlockDEK(userID, password, sessionID, remaining_jwt_ttl).
3. Backfill jwt_sessions row. The wrapping KEK is derived from the SAME
   matched_signing_key the rehydrate path will derive from. This is critical:
   wrapping with the *active* jwtSecret produces a row that fails to
   rehydrate when the JWT validates under a previous key (post-rotation
   regression discovered in #411 review pass 1). Generate a fresh
   kek_salt — never reuse one across rewrites.
4. Return 204 on success.
5. Return 401 "incorrect password" on derive failure. Do NOT invalidate the JWT.

API-key callers (matched_signing_key == nil) cannot use this endpoint to
backfill a jwt_sessions row — they have no JWT to wrap against. Their DEK
recovery path is the existing api_keys.WrappedDEK design, which already
auto-rehydrates from the user-presented API key plaintext on every request.
A soft-unlock POST from an API-key context returns 400 "soft-unlock
requires a JWT session" with a hint that the API key already covers DEK
durability for that auth path.
```

This is the universal recovery hatch. The residual cases it covers:

- **Backfill at feature launch:** existing JWTs predate `jwt_sessions`. First DEK-needing action auto-rehydrate fails (no row); UI surfaces soft-unlock; soft-unlock backfills the row.
- **DEK rotation (US-50.4):** `wrapped_dek` is now stale; soft-unlock rewrites it.
- **Row corruption / DB restore from backup:** soft-unlock recreates the row.
- **Future failure classes:** always recoverable without forced logout.

### JWT revocation

`RevokeAllUserSessions` and `EvictDEK` already write revocation markers. Extension: same paths also `DELETE FROM jwt_sessions WHERE jti = ?` to keep state consistent.

### Janitor

A periodic goroutine deletes `jwt_sessions WHERE expires_at < NOW()`. Already a pattern in this codebase (`session_index_cleaner`, audit retention).

---

## Failure mode matrix

| Cause | Auto-recover? | Soft-unlock needed? |
|---|---|---|
| Valkey restart, JWT still valid | ✓ | No |
| Valkey LRU eviction | ✓ | No |
| Signing-key rotation within window | ✓ | No |
| Signing-key rotation older than window | N/A — JWT itself invalid; user re-logs in | No |
| `jwt_sessions` row missing (pre-feature backfill, ops error) | ✗ | Yes |
| DEK rotated by US-50.4 — `wrapped_dek` stale | ✗ | Yes |
| User changed password — `RevokeAllUserSessions` ran | N/A — JWT revoked; user re-logs in | No |
| Master KEK rotated (US-50.5) | ✓ — multi-version master-key support unwraps old `wrapped_dek` | No |
| API-key without `decrypt_access=true` | N/A — by design has no DEK | No (and shouldn't) |

The "soft-unlock needed" column is the small residual.

---

## Out of scope

- **Workspace secret-delivery reconciler** (the Epic 35 regression fix). That's Epic 57, which depends on this epic.
- **MFA / second-factor for soft-unlock.** Same threat model as login.
- **A non-password-based recovery flow.** If the user forgets their password, the existing password-reset flow already invalidates DEKs (US-49.5).
- **Cross-cluster DEK migration.** Out of scope.

---

## Risks

| Risk | Mitigation |
|---|---|
| `jwt_sessions` unbounded growth | Janitor goroutine deletes expired rows; index on `expires_at` makes the scan O(log N) |
| Login latency increase from durable write | One additional INSERT (~5ms) on a fast path; PG is the same DB the credentials store uses |
| Multi-key rotation window too narrow → users re-login on rotation | `jwtPreviousSecrets` already exists; rotation policy is operator-controlled |
| Backfill at feature launch surprises users with soft-unlock prompts | Communicate via release note; alternative would be force-logout at launch which is worse |
| Signing key compromise | Already a catastrophic event; this epic doesn't change that posture — attacker who steals signing key can forge JWTs regardless |
| DB compromise alone | Without signing-key (process memory), `wrapped_dek` is unrecoverable. Same narrow DB-alone property as master-KEK wrapped admin/org creds — but the signing key's delivery is strictly weaker than the master KEK's (see "Threat model" above). The DB-alone case is genuinely unrecoverable; the broader "compromised signing key from env/Helm leak" is dominated by the JWT-forgery threat that pre-existed Epic 56. |

---

## Test plan (TDD per Rule 0)

### Schema migration
- `jwt_sessions` table created with PK on jti, FK to users with ON DELETE CASCADE, indexes.
- Down migration drops the table.

### Login durable write
- After successful login, `jwt_sessions` row exists with correct jti, user_id, expires_at = JWT expiry.
- `wrapped_dek` is unwrappable with `HKDF(signingKey || jti, kek_salt, "...")` and matches the cached DEK.
- Login still succeeds if durable write fails (logged warn; Redis cache used).

### Redis rehydrate
- Cache Redis-hit: returns DEK from Redis without touching DB.
- Cache Redis-miss + durable row exists: rehydrates DEK, re-caches to Redis, returns.
- Cache Redis-miss + durable row missing: returns `ErrDEKUnavailable`.
- Multi-key rotation: durable row written under old key, current request validates under same old key (which is in `jwtPreviousSecrets`), rehydrate succeeds.
- Multi-key rotation: durable row written under key removed from window, JWT fails validation entirely (so we never reach GetDEK).

### Soft-unlock
- Happy path: password correct, DEK re-cached, durable row updated.
- Wrong password: 401, JWT remains valid, no state change.
- Backfill: durable row didn't exist before soft-unlock; exists after.
- Concurrent soft-unlock + auto-rehydrate: no race; one wins.
- **Soft-unlock backfill under a rotated JWT** (regression test for #411 pass-1 [HIGH] finding): user's JWT was issued under key A; rotation moved A into `jwtPreviousSecrets`, B is active. Soft-unlock must wrap with key A (matched validation key), not B (active key). After soft-unlock + Valkey restart, rehydrate must succeed.
- **Soft-unlock rewrite after US-50.4 DEK rotation**: row's `wrapped_dek` is stale. Soft-unlock generates a fresh `kek_salt`, derives KEK from the matched signing key, writes the new wrapped DEK. Subsequent Valkey restart rehydrates the *new* DEK successfully.
- **API-key caller cannot soft-unlock-to-backfill**: a POST to /auth/unlock-dek from an API-key context returns 400 with a hint pointing at the api_keys.WrappedDEK design.

### Revocation
- `RevokeAllUserSessions` deletes `jwt_sessions` rows for the user.
- `EvictDEK` deletes the specific row.

### Janitor
- Rows with `expires_at < NOW()` are deleted on next tick.
- Rows with `expires_at > NOW()` are preserved.

### Negative
- Stolen `wrapped_dek` blob + `kek_salt` (no JWT signing key) → cannot unwrap.
- Stolen JWT (signing key valid) + stolen DB → can unwrap (matches today's threat model — attacker with valid JWT has full session anyway).

---

## Stress test (12 attacks)

1. **Login race** — two near-simultaneous logins for the same user create two distinct jti rows. PK on jti prevents collision. ✓
2. **Concurrent rehydrate** — two requests miss Redis simultaneously, both go to DB, both re-cache. Last write wins; both DEKs are identical so no incorrectness. ✓
3. **JWT signing-key rotation mid-session** — JWT issued under key A, validation now happens under either A or B (both in window). Rehydrate works as long as the matching key is in `jwtPreviousSecrets` and is surfaced via `parseTokenAcceptingRotatedKeys`. ✓
4. **Soft-unlock backfill under rotated key (the #411 [HIGH] finding)** — JWT issued under A, B is now active, user soft-unlocks. Backfill MUST wrap with the matched key (A), not the active key (B). A test seeds this exact configuration and asserts that a subsequent rehydrate (after Valkey restart) succeeds. Failure of this property is the exact regression Epic 56 exists to prevent. ✓
5. **Password change mid-session** — `RevokeAllUserSessions` deletes the durable row; new JWT after re-login writes a new one. ✓
6. **DEK rotation (US-50.4)** — durable row's `wrapped_dek` is now stale. Rehydrate produces old DEK; decrypt of any secret fails. The next chat error enrichment surfaces `dek_unavailable` (Epic 57) or `secret_decrypt_failed`; soft-unlock recovers (with `kek_salt` regenerated, wrapped with matched key). ✓
7. **Backfill** — existing JWT, no durable row. Rehydrate fails. Soft-unlock backfills under the matched key. ✓
8. **Process memory dump alone** — attacker gets signing key but no DB. Cannot unwrap (needs `kek_salt + wrapped_dek` from DB). ✓
9. **DB dump alone** — attacker gets `kek_salt + wrapped_dek` but no signing key. Cannot unwrap (HKDF needs signing key). ✓ — **but note** signing-key delivery via env/Helm is strictly weaker than master-KEK delivery via file mount; signing-key compromise via config leak unwraps every row. Compensating control: signing-key compromise already lets the attacker forge JWTs, so user impersonation is the dominant threat, not DB-row decryption.
10. **Master KEK rotation** — Redis cache is wrapped with master KEK; rotation invalidates the cache only. Durable row is wrapped with JWT-derived KEK, unaffected. Rehydrate works. ✓
11. **PG long outage** — login can't write durable row; Redis cache is still valid for the JWT's lifetime. When PG recovers, login is allowed to retry-write or skip; existing sessions degrade gracefully (Redis-only, until next Valkey restart). ✓
12. **Thundering herd on Valkey restart / flush** — every active session's next request will miss Redis simultaneously and fall through to PG `SELECT` + HKDF + decrypt + Redis `SET`. The load shift is from Redis to PG: the PG `jwt_sessions` table is single-row-by-PK (O(1) lookups), and the rehydrate write back to Redis is cheap. Today's blast radius is tiny (~10 active sessions cluster-wide); even at 1000× scale, the herd is bounded by request rate, not by stored session count. **Document but do not redesign.** A pre-warming pattern (rehydrate batch on Valkey reconnect) can be a follow-up if production observability shows actual contention. ✓

---

## Definition of done

- [x] Design doc (this file).
- [x] Worklog `NNNN_2026-06-26_epic-56-durable-dek-design.md` (sentinel; post-merge bot assigns the number).
- [ ] Migration `000045` (new table + indexes + down). MUST be committed in **both** `api/migrations/` and `charts/llmsafespaces/migrations/` (the two are kept in lockstep; `make chart-sync-migrations` enforces this).
- [ ] `parseTokenAcceptingRotatedKeys` extended to return the matched key alongside the parsed token (the real change site, not `ValidateToken`).
- [ ] Auth middleware sets `c.Set("jwt_signing_key", matched)` after a successful parse so handlers can forward it explicitly to `KeyService.GetDEK`.
- [ ] `KeyService.GetDEK` signature extended to take `matchedSigningKey []byte`; all callers updated to pass it explicitly (no `ctx.Value` lookups in `pkg/secrets`).
- [ ] `KeyService.rehydrateDEKFromJWTSession` with multi-key rotation. Redis errors logged but rehydrate is attempted (resilience); a genuine `redis.Nil` is treated identically to an error from a rehydrate-flow standpoint.
- [ ] Login durable write (wraps under the active key at issue time).
- [ ] Soft-unlock endpoint `POST /api/v1/auth/unlock-dek`. Backfill wraps under the **matched** signing key (not the active key) and generates a fresh `kek_salt`. Returns 400 with a clear hint when called from an API-key auth context.
- [ ] Revocation paths (`RevokeAllUserSessions`, `EvictDEK`) delete `jwt_sessions` rows for consistency.
- [ ] Janitor goroutine prunes `expires_at < NOW()` rows.
- [ ] All tests above pass.
- [ ] CI green; PR merged.
- [ ] Deployed to live cluster.
- [ ] Verified on `valkey-766d6df8dd-qshl7`: a deliberate Valkey restart followed by an HTTP request to a workspace endpoint demonstrates DEK auto-rehydrate (Redis key reappears; logs show "durable rehydrate succeeded"). Also verified that a JWT issued before deploy, after rotation, soft-unlocks and successfully rehydrates post-Valkey-restart (the [HIGH] regression case from #411 review pass 1).

---

## References

- README-LLM.md — Master KEK delivery model (file mount vs env var).
- `design/0027_*-security-policy-v21.md` — authoritative security policy reference.
- `design/0021 §9` — credential hardening notes.
- `pkg/secrets/key_service.go:217-226` — existing `GetDEK` "fail closed on Redis error" behavior preserved for the no-rehydrate-available case.
- `api/internal/services/auth/auth.go:131-137` — existing `jwtPreviousSecrets` multi-key rotation infrastructure reused.
- `api/internal/services/auth/auth.go:1259-1297` — `parseTokenAcceptingRotatedKeys`, the real change site for surfacing the matched key.
