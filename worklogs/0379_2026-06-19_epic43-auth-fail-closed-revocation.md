# Worklog: Epic 43 — Auth Middleware Fail-Closed + Per-User Revocation (F3/F4)

**Date:** 2026-06-19
**Session:** Fix worklog 0372 findings F3 + F4 (user-suspension enforcement). Part of a 4-PR remediation split by subsystem; this PR owns the auth middleware + revocation primitives. Each finding was independently re-verified against source (Rule 11 Phase 2) before fixing.
**Status:** Complete

---

## Objective

Close the two HIGH-severity authz findings from worklog 0372:
- **F3** — the auth middleware failed OPEN on any `GetUser` DB error, letting a suspended user regain access during a DB blip (on every authenticated request's hot path).
- **F4** — suspending a user did not revoke their live JWTs/API keys; only the per-request `GetUser` check (which failed open per F3) enforced.

---

## Work Completed

### F3 — Auth middleware fail-closed
`api/internal/services/auth/auth.go` `AuthMiddleware`: a `GetUser` error now aborts 503 ("unable to verify account status") instead of falling through to `c.Next()`; a missing user row aborts 401 ("account not found"). `GetUser` is the load-bearing authz gate; denying legitimate users during a DB outage is the correct security posture.

### F4 — Per-user revocation marker
The worklog-0372 proposed fix (iterate sessions via `RevokeToken`) is infeasible — validated via grep that there is **no per-user session store** (`RevokeToken` takes a token, not a userID; no `ListUserSessions`/`RevokeAllForUser`). The right-sized mechanism for revocation-by-principal with stateless JWTs is a Redis marker.

Added `MarkUserSuspended`/`ClearUserSuspended` to `auth.Service` (key `user_suspended:<userID>`) and to `interfaces.AuthService`. The middleware consults the marker on the **DB-error branch** (precise 401 vs 503 during outage) and **heals stale markers** on the active-user branch. `GetUser` stays authoritative — the marker is resilience + precise labelling, never the sole enforcement.

**Adversarial finding (found + fixed before completion):** the natural design (reject on a marker hit *before* `GetUser`) has a stale-marker false-positive: if `ClearUserSuspended` failed during an unsuspend, an active user would be blocked for up to the marker TTL. Redesigned so `GetUser` is always consulted; `TestAuthMiddleware_StaleMarker_Healed` locks this in.

**Review-driven fix:** the marker TTL initially used `tokenDuration` (24h), but remember-me tokens live `RememberMeDuration` (720h default) — so the marker could expire before a remember-me token, leaving a 24h gap. Fixed: `max(tokenDuration, rememberMeDuration)` via `suspensionMarkerTTL`.

---

## Key Decisions

1. **Marker over session enumeration** — validated no per-user session store exists; the Redis `user_suspended:<userID>` marker is the standard revocation-by-principal pattern for stateless JWTs.
2. **`GetUser` stays authoritative; marker is resilience + healing** — so a stale marker (failed unsuspend) self-heals and the security guarantee never depends on Redis.
3. **TTL = max(tokenDuration, rememberMeDuration)** — covers the longest-lived token so remember-me sessions stay gated.
4. **`OptionalAuthMiddleware` does NOT consult the marker** — it would add a stale-marker false-positive for no benefit; `GetUser` already resolves suspension there.
5. **Widened `interfaces.AuthService`** with the two new methods (their proper auth-domain home); updated both hand-written mocks.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 120s -count=1 ./api/internal/services/auth/...` — green.
- New (`auth_suspension_test.go`): fail-closed on DB error (503), missing row (401), active user still allowed, marker write/clear, marker rejects during DB outage (401), stale marker healed, marker TTL covers remember-me (unit), error-path for Mark/Clear.
- CI runs the full suite + lint.

---

## Next Steps

This PR is the **dependency root** for the companion db/handler PR (F6/F7), which consumes `MarkUserSuspended`/`ClearUserSuspended` via `svc.GetAuth()` as a `platformUserRevoker`. That PR branches off main **after this PR merges**.

---

## Files Modified

- `api/internal/services/auth/auth.go` — F3 fail-closed; F4 marker primitives + middleware integration + stale-marker healing + `maxTokenTTL`.
- `api/internal/services/auth/auth_suspension_test.go` (new) — F3/F4 + TTL + error-path tests.
- `api/internal/interfaces/interfaces.go` — widened `AuthService`.
- `api/internal/mocks/middleware_mocks.go`, `api/internal/middleware/tests/auth_test.go` — mock methods for the widened interface.
- `pkg/types/auth.go` — pre-existing gofmt drift fix (struct field alignment) per Rule 5.
- `charts/llmsafespaces/templates/prometheus-rules.yaml` — pre-existing rename-miss fix: 3 alerts (`SSEBrokerDroppingEvents`, `SafeModeActive`, `StatusUpdateConflicts`) were left singular `LLMSafeSpace*` by the module-rename PR, failing `TestMonitoring_PrometheusRule_ContainsAllAlerts` on main. Completed the rename to `LLMSafeSpaces*` (Rule 5: no pre-existing errors). Bundled here because it blocked this PR's full-test-suite CI gate; also applied to companion PRs #265/#266.
