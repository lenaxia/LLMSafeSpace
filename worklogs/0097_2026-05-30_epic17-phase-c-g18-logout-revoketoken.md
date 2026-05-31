# 0097 — Epic 17 Phase C/G3: wire RevokeToken into /auth/logout (G18)

**Date:** 2026-05-30
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, finding G18
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes G18 (Phase 4 RT-4.13). Pre-fix, `POST /api/v1/auth/logout`
only cleared the `lsp_session` cookie; the JWT remained valid and
could be replayed via re-supplying the cookie OR via Authorization
header. The threat-model promise that logout invalidates the active
session was unmet despite the `RevokeToken` function being correct
(landed in worklog 0078 with proper dual-key cache writes).

This commit wires the existing `RevokeToken` into the logout handler
with three fail-safe properties:

1. **Best-effort revocation, guaranteed cookie clear.** Errors from
   RevokeToken are logged at Warn but do NOT prevent the cookie
   clear or 204 response. Logout must always succeed from the
   user's perspective.
2. **API-key bypass.** Tokens with the `lsp_` prefix are NOT passed
   to RevokeToken (which only handles JWTs); their lifecycle is
   `/api-keys/:id DELETE`.
3. **Header + cookie both supported.** ExtractToken's existing
   priority order (header > cookie) is preserved so SDK callers
   sending `Authorization: Bearer ...` and browser sessions sending
   the cookie both work.

---

## Stated assumptions (validated up-front)

- **A1** — `RevokeToken(token string) error` exists on the auth
  Service and writes to BOTH `token:<hash>` and `token:<jti>` cache
  keys. (Validated: `api/internal/services/auth/auth.go:155-230`.)
- **A2** — `ValidateToken` checks both keys so revocation is visible
  to subsequent calls. (Validated: `auth.go:278-onwards`.)
- **A3** — The current logout handler at
  `api/internal/server/router.go:329-333` only clears the cookie.
  (Validated: read it.)
- **A4** — `utilities.ExtractToken` is the standard accessor for
  JWT-or-API-key extraction across the codebase. (Validated:
  `api/internal/utilities/token_extractor.go:35`.)
- **A5** — `RevokeToken` is NOT in the public `interfaces.AuthService`
  yet, only on the concrete `*Service`. (Validated: it's missing.)
  → Adding it is a public API change; updated all consumers.

---

## Changes

### Code

1. `api/internal/server/router.go`:
   - Renamed the `logger` package import to `apilogger` to avoid the
     parameter-name shadow that was masking method calls.
   - Threaded `*apilogger.Logger` into `registerAuthRoutes`.
   - Rewrote the `/logout` handler to:
     - extract via Authorization header OR `lsp_session` cookie;
     - skip empty tokens and `lsp_` API keys;
     - call `authSvc.RevokeToken(token)`;
     - log `Warn` on revoke failure, then unconditionally clear the
       cookie and return 204.

2. `api/internal/interfaces/interfaces.go` — added
   `RevokeToken(token string) error` to the AuthService interface.

3. `api/internal/mocks/middleware_mocks.go` — added the corresponding
   mock impl on `MockAuthMiddlewareService`.

4. `api/internal/middleware/tests/auth_test.go` — added the same to
   the tests-local `MockAuthService` so middleware tests compile.

### Tests

5. `api/internal/server/router_frontend_auth_test.go` — five new
   `TestG18Logout_*` tests:
   - `TestG18Logout_RevokesCookieToken` — the legitimate browser path.
   - `TestG18Logout_RevokesBearerToken` — SDK / API client path.
   - `TestG18Logout_NoTokenSkipsRevoke` — no-token edge case (200 → 204).
   - `TestG18Logout_APIKeySkipsRevoke` — API-key prefix correctly
     bypasses revocation.
   - `TestG18Logout_RevokeFailureStillClearsCookie` — cache-outage
     fail-safe.

---

## Skeptical-validator pass

A separate validator agent re-derived the threat from RT-4.13,
inspected the wiring, attempted bypasses, and ran mutation tests.
Result: **HOLD WITH FOLLOWUP**.

- **Wiring**: every contract (header > cookie priority, API-key skip,
  Warn-not-Error on failure, always 204) verified against the code.
- **Bypass attempts**: 6 tried, all behave as designed. The most
  notable: with both Bearer header AND cookie set to different
  values, only the header token is revoked (header-priority).
  Real-world risk: low — the SPA never sets both. Documented in the
  handler comment.
- **Mutation tests**: 3 mutations applied (disable revoke block,
  break API-key prefix, break cookie clear); each made the relevant
  test fail. Restored.
- **Logger leakage**: confirmed RevokeToken error strings do not
  contain the raw token bytes.
- **Three minor follow-ups** identified, all non-blocking:
  1. `lsp_` prefix is hardcoded; should read from
     `Auth.APIKeyPrefix` config. The current code is fail-safe
     (best-effort revoke on misclassified API key), so this is
     cosmetic.
  2. Header-priority documented in handler comment.
  3. No end-to-end integration test that ValidateToken returns
     "revoked" after the HTTP logout. The two halves are tested
     independently (handler→mock + service→cache); chain coverage
     is implied by the mock contract.

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 30s -run TestG18Logout ./api/internal/server/...` | PASS (5/5) |
| `go test -count=1 -timeout 60s ./api/...` | PASS |
| `go test -count=1 -timeout 60s ./controller/... ./charts/llmsafespace/...` | PASS |
| `go build ./api/... ./controller/...` | clean |
| Mutation: disable revoke block | TestG18Logout_RevokesCookieToken FAIL ✓ |
| Mutation: break API-key prefix to "xx_" | TestG18Logout_APIKeySkipsRevoke FAIL ✓ |
| Mutation: SetCookie maxAge -1 → 3600 | TestG18Logout_RevokeFailureStillClearsCookie FAIL ✓ |

**`pkg/secrets` build is broken in the working tree** but that's the
other agent's uncommitted secrets-mgmt WIP (their reference to
`s.verifyWorkspaceOwner` doesn't exist yet). My changes don't touch
`pkg/secrets`. Their tree will compile when they finish their work.

---

## Live re-pentest plan (after CI builds the API image)

1. CI builds and ships API image.
2. `helm upgrade llmsafespace ./charts/llmsafespace -n default --reuse-values`.
3. Re-run RT-4.13 from `phase-4/run-phase4.py`:
   - Login → token X.
   - GET /auth/me with X → 200.
   - POST /auth/logout with X → 204 (cookie cleared, RevokeToken called).
   - GET /auth/me with X → 401 "Invalid or expired token".
4. Repeat with Bearer header instead of cookie. Same expectation.
5. Verify `local/test-auth.sh` end-to-end script also passes.

---

## Files changed

- `api/internal/server/router.go` (handler rewrite, package alias)
- `api/internal/interfaces/interfaces.go` (added RevokeToken)
- `api/internal/mocks/middleware_mocks.go` (added mock impl)
- `api/internal/middleware/tests/auth_test.go` (added mock impl)
- `api/internal/server/router_frontend_auth_test.go` (5 new tests)

---

## Tracker update

`MASTER-TRACKER.md`:
- G18 → MINE / live-pending
- RT-4.13 (Phase 4) → resolved by this PR
- RT-2.13 (Phase 2 dup) → resolved by this PR

---

## Next finding

Phase C/G4 — F1.2.3 + F1.2.4 + F1.2.5 (controller pod-spec, NetPol-per-workspace, package init container).
