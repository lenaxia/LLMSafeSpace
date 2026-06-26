# Worklog: Epic 56 — Durable DEK for JWT Sessions (design)

**Date:** 2026-06-26
**Session:** Design pass for Epic 56, the foundational fix for Invariant 2 ("DEK should be available for the full JWT lifetime, as close to zero-knowledge as possible") that is violated in production today. Opened the design doc as PR #411 and iterated through two review passes.

**Status:** Design doc complete, two review passes adversarial-reviewed, all findings addressed. Pending `/merge` (`/design` PRs hold for an explicit `/merge`).

---

## Why this design exists

Production observation: `valkey-766d6df8dd-qshl7` runs without persistence (no PVC, no AOF/RDB) so every Valkey restart drops every `dek:<jti>` Redis entry. JWTs continue to validate (signature-based) for their full lifetime (up to 30 days via `rememberMeDuration=720h`). The result: a workspace owner can be "authed but DEK missing," and the existing `pushSecretsToAgent` path silently drops user-DEK content.

The workspace secret-delivery regression on `d95b6751-...` traced to Epic 35 deleting `refreshEphemeralSecrets`, but the deeper root cause is this DEK-durability gap. Epic 56 fixes the foundation; Epic 57 will rebuild the workspace reconciler on top of it.

## Key design decisions

### 1. Wrapping KEK = matched JWT signing key + jti

KEK derived from `HKDF(matched_signing_key_bytes || jti.String(), kek_salt, "...dek-kek")`. Three properties:

- DB compromise alone cannot unwrap: requires the signing key from API process memory.
- Memory dump alone cannot unwrap: requires `kek_salt + wrapped_dek` from DB.
- Multi-key rotation already exists at `auth.go:131-137` (`jwtPreviousSecrets`); the rehydrate path reuses this window without modification.

Mirrors the existing `api_keys.WrappedDEK` design for `decrypt_access=true` API keys, where the KEK is derived from the API key plaintext at use time.

### 2. Soft-unlock endpoint as universal recovery hatch

`POST /api/v1/auth/unlock-dek` (~50 LOC). Takes password, calls `UnlockDEK`, returns 204. Never invalidates the JWT.

Covers the residual cases that auto-rehydrate cannot:
- Pre-feature JWTs (no durable row yet) — first DEK-needing action triggers soft-unlock, which backfills.
- US-50.4 DEK rotation made the `wrapped_dek` stale.
- Row corruption / DB restore from older backup.
- Future failure classes we can't anticipate.

### 3. Honest threat-model framing

The original design equated the signing-key's "DB alone is useless" property with the master KEK's same property. Pass-1 review correctly flagged this as inaccurate: the master KEK is delivered via read-only file mount, the signing key is delivered via env var. Signing-key compromise via config leak unwraps every `jwt_sessions` row across all users.

Compensating control (now in the doc): signing-key compromise already lets the attacker forge JWTs and impersonate any user. The DB-row-decryption capability is a marginal increment over the pre-existing JWT-forgery catastrophe.

### 4. Dependency inversion (caller passes matched key explicitly)

Original design had `KeyService.rehydrate` reading `ctx.Value("jwt_signing_key")` — an upward dependency from `pkg/secrets` (low-level) on middleware-owned context keys. Pass-1 review pushed back: the codebase pattern is "middleware sets context, handler forwards explicitly to KeyService."

Refactored to:
```go
func (s *KeyService) GetDEK(ctx context.Context, sessionID string, matchedSigningKey []byte) ([]byte, error)
```

API-key callers, controller-internal callers, and any non-JWT path pass `nil` and skip auto-rehydrate — correct (no JWT means no KEK material to derive from).

### 5. Token validation cache interaction (pass-2 finding)

`ValidateTokenWithClientIP` caches `token:<hash> → userID` and returns on cache hit without parsing the JWT. Plausible LRU eviction order: DEK key evicted while token-validation key retained. On the next request, the middleware never parses → matched key never computed → `GetDEK(_, _, nil)` → `ErrDEKUnavailable` even though the durable row exists.

Fix: store `userID|matchedKeyIndex` (not just `userID`) in the token-validation cache. Cache hit can surface the matched key without re-parsing. Tiny value-format change, no extra Redis round-trip.

The Valkey-restart scenario (the epic's primary trigger) is NOT affected — both caches flush together. The gap is specifically the LRU-eviction-of-DEK-only path, which is plausible given today's production observation of "1 DEK key cluster-wide."

## Adversarial review

12 attacks documented in the design doc. Highlights of what was caught during pass-1:

- **[HIGH]** soft-unlock backfill was wrapping with the *active* signing key, not the *matched* one. Post-rotation, that produces an unwrap failure exactly when the user needs auto-recovery. Fixed.
- **[MED]** dependency inversion (`pkg/secrets` reaching up into middleware context). Refactored to explicit parameter passing.
- **[MED]** threat-model misrepresentation (master-KEK vs signing-key delivery). Corrected.
- **[LOW]** wrong change-site reference (`ValidateToken` vs `parseTokenAcceptingRotatedKeys`). Fixed.
- **[LOW]** `GetDEK` pseudocode swallowing Redis errors. Distinguished `redis.Nil` (miss) from transient error (log + still rehydrate).

Pass-2 added the token-cache-interaction note ([MED]) before merge, plus minor polish (duplicate header, risk-table cross-reference to threat-model section, worklog deliverable).

## Implementation scope (sized)

| Component | Estimated LOC | Notes |
|---|---|---|
| Migration `000045` (table + indexes + down) | ~30 | Both `api/migrations/` and `charts/llmsafespaces/migrations/` |
| `parseTokenAcceptingRotatedKeys` extension to return matched key | ~30 | The real change site, not `ValidateToken` |
| Token validation cache value format change (userID → userID|keyIndex) | ~20 | New finding from pass-2 |
| Auth middleware `c.Set("jwt_signing_key", ...)` after parse + cache | ~10 | |
| `KeyService.GetDEK` signature change + rehydrate body | ~80 | Plus ~10 callsite updates |
| Login durable write | ~30 | Active key at issue time |
| Soft-unlock endpoint | ~50 | + tests |
| Revocation eviction of durable row | ~10 | |
| Janitor goroutine | ~30 | |
| Tests | ~250 | TDD per Rule 0 |
| **Total** | **~540** | One PR, no splitting |

## Definition of done (carried forward to implementation)

See the design doc's DoD section. Most important live verification: after deploy, a deliberate `kubectl rollout restart deployment/valkey` followed by an HTTP request to a workspace endpoint must demonstrate DEK auto-rehydrate (new `dek:<jti>` key appears in Redis; logs show "durable rehydrate succeeded"). Additionally, a JWT issued before deploy, after rotation, soft-unlocks and successfully rehydrates post-Valkey-restart (the [HIGH] regression case).

## Refs

- PR #411 (this design)
- PR #407 (Epic 35 bootstrap fix that surfaced the underlying DEK-durability gap)
- Epic 50 (master KEK hardening)
- PR #228 (multi-key JWT rotation window) — `jwtPreviousSecrets` infrastructure reused by Epic 56
- Forthcoming Epic 57 — workspace secret-delivery reconciler that depends on Epic 56

## Assumptions

- Valkey will be made persistent eventually (separate ops work, not in this epic's scope). Epic 56 enforces Invariant 2 even when Valkey is ephemeral, but a persistent Valkey would also satisfy Invariant 2 in a different way — both can coexist.
- The JWT signing key delivery is via env var today (`Auth.JWTSecret`). Migrating to file-mount (matching `masterSecret.deliveryMethod=file`) is a follow-up out of scope.
- `jwtPreviousSecrets` retention is operator-controlled and assumed to cover at least one rotation cycle; in practice this means a few weeks. Rotation cadence and policy is out of scope.
- The `decrypt_access=false` API-key auth path (which has no DEK by design) is correct as-is; Epic 56 does not change it.

## Stress test outcome

12/12 attacks have a defined resolution in the design doc. The two that surfaced new requirements (rotated-key soft-unlock backfill; token-validation-cache LRU edge case) are both addressed by design changes captured in the doc before merge.

No findings remain open.
