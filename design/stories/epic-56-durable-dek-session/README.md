# Epic 56: Durable DEK for JWT Sessions

**Status:** Planning
**Created:** 2026-06-25
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
    salt   = kek_salt,                              // random per session, stored in DB
    secret = jwt_signing_key || jti,                 // signing-key bytes + 16-byte jti
    info   = "llmsafespaces-jwt-session-dek-kek",
    length = 32,
)
```

Why JWT signing key + jti, not the full token:

- **Stored material** (`kek_salt`, `wrapped_dek`) is useless without the signing key in memory. Stolen DB → unwraps nothing.
- **Presented JWT validates** under one of N signing keys in the rotation window. Whichever key validates is the one used to derive the KEK. Multi-key rotation already exists in `auth.go:131-137` and is reused as-is.
- **jti is in the JWT payload**, so we don't need to store it client-side or transmit it separately. The JWT itself is what the user always presents.
- **Signing key is never stored alongside `wrapped_dek`.** Even an attacker with full DB access cannot unwrap without dumping the API process memory — same property as the master KEK.

Note: this is a deliberate departure from a more typical "password-derived KEK" pattern. We can't use password here because the API process doesn't hold the password after login — and persisting it would violate zero-knowledge. The JWT signing key is the cleanest available "server holds in memory only" secret.

### Login (`auth.go:889` extended)

```
// Existing
keyService.UnlockDEK(ctx, userID, password, jti, tokenDur)
// New
KEK := HKDF(jwtSigningKey || jti, salt, "...dek-kek")
wrappedDEK := EncryptSecret(KEK, dek)
INSERT INTO jwt_sessions(jti, user_id, wrapped_dek, kek_salt, expires_at) VALUES (...)
zeroBytes(KEK)
```

Atomic: same handler that caches to Redis writes durably. Failure to write durable row is logged but does not fail login (Redis cache is still valid for the JWT's lifetime).

### GetDEK rehydrate-on-miss (`key_service.go`)

```go
func (s *KeyService) GetDEK(ctx, sessionID string) ([]byte, error) {
    if dek, _ := s.cache.GetDEK(ctx, sessionID); dek != nil {
        return dek, nil                                  // fast path
    }
    // Redis miss. Try durable rehydrate.
    return s.rehydrateDEKFromJWTSession(ctx, sessionID)
}

func (s *KeyService) rehydrateDEKFromJWTSession(ctx, sessionID) {
    if !looksLikeJTI(sessionID) {
        return nil, ErrDEKNotInCache  // API-key sessions auto-rehydrate from api_keys; not this path
    }
    row := SELECT user_id, wrapped_dek, kek_salt, expires_at FROM jwt_sessions WHERE jti = sessionID
    if row == nil || row.expires_at <= NOW() {
        return nil, ErrDEKUnavailable
    }
    // Caller must have presented a valid JWT that ValidateToken accepted.
    // ValidateToken stores the matched signing key in the request context as
    // `jwt_signing_key`. The rehydrate path reads it from there.
    signingKey := ctx.Value(jwtSigningKeyContextKey)
    if signingKey == nil {
        return nil, ErrDEKUnavailable  // no JWT in context — caller is API-key or controller
    }
    KEK := HKDF(signingKey || jti, row.kek_salt, "...")
    dek, err := DecryptSecret(KEK, row.wrapped_dek)
    if err != nil {
        return nil, ErrDEKUnavailable  // wrong signing key / corrupted row
    }
    // Re-cache to Redis with remaining TTL.
    ttl := time.Until(row.expires_at)
    s.cache.CacheDEK(ctx, sessionID, dek, ttl)
    return dek, nil
}
```

The "matched signing key in request context" piece requires a small change in `ValidateToken` to set the context after validation. Mechanical.

### Soft-unlock endpoint

```
POST /api/v1/auth/unlock-dek
Body: { password }
Auth: JWT or API-key required (AuthMiddleware)

Behavior:
1. Extract userID, sessionID from auth context.
2. Call keyService.UnlockDEK(userID, password, sessionID, remaining_jwt_ttl).
3. Backfill jwt_sessions row if missing (uses the freshly-unlocked DEK + current JWT signing key).
4. Return 204 on success.
5. Return 401 "incorrect password" on derive failure. Do NOT invalidate the JWT.
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
| DB compromise alone | Without signing-key (process memory), `wrapped_dek` is unrecoverable. Same property as master-KEK wrapped admin/org creds |

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

## Stress test (10 attacks)

1. **Login race** — two near-simultaneous logins for the same user create two distinct jti rows. PK on jti prevents collision. ✓
2. **Concurrent rehydrate** — two requests miss Redis simultaneously, both go to DB, both re-cache. Last write wins; both DEKs are identical so no incorrectness. ✓
3. **JWT signing-key rotation mid-session** — JWT issued under key A, validation now happens under either A or B (both in window). Rehydrate works as long as the matching key is in `jwtPreviousSecrets`. ✓
4. **Password change mid-session** — `RevokeAllUserSessions` deletes the durable row; new JWT after re-login writes a new one. ✓
5. **DEK rotation (US-50.4)** — durable row's `wrapped_dek` is now stale. Rehydrate produces old DEK; decrypt of any secret fails. The next chat error enrichment surfaces `dek_unavailable` (Epic 57) or `secret_decrypt_failed`; soft-unlock recovers. ✓
6. **Backfill** — existing JWT, no durable row. Rehydrate fails. Soft-unlock backfills. ✓
7. **Process memory dump alone** — attacker gets signing key but no DB. Cannot unwrap (needs `kek_salt + wrapped_dek` from DB). ✓
8. **DB dump alone** — attacker gets `kek_salt + wrapped_dek` but no signing key. Cannot unwrap (HKDF needs signing key). ✓
9. **Master KEK rotation** — Redis cache is wrapped with master KEK; rotation invalidates the cache only. Durable row is wrapped with JWT-derived KEK, unaffected. Rehydrate works. ✓
10. **PG long outage** — login can't write durable row; Redis cache is still valid for the JWT's lifetime. When PG recovers, login is allowed to retry-write or skip; existing sessions degrade gracefully (Redis-only, until next Valkey restart). ✓

---

## Definition of done

- [x] Design doc (this file).
- [ ] Migration 000045 (new table + indexes + down).
- [ ] `KeyService.GetDEK` rehydrate path with multi-key rotation.
- [ ] Login durable write.
- [ ] `POST /api/v1/auth/unlock-dek`.
- [ ] Revocation evicts durable row.
- [ ] Janitor goroutine.
- [ ] All tests above pass.
- [ ] CI green; PR merged.
- [ ] Deployed to live cluster.
- [ ] Verified on `valkey-766d6df8dd-qshl7`: a deliberate Valkey restart followed by an HTTP request to a workspace endpoint demonstrates DEK auto-rehydrate (Redis key reappears).
