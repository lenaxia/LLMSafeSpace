# Worklog: Worklog 0372 Validation Findings — All Fixes

**Date:** 2026-06-19
**Session:** Implement and validate fixes for every REAL finding in worklog 0372 (Epic 43 Phase 3 & 5 post-merge validation). Each finding was independently re-verified against source (Rule 11 Phase 2) before fixing; the one design flaw found during this session's own adversarial review (a stale-marker false-positive in F4) was fixed with a regression test before completion.
**Status:** Complete

---

## Objective

Worklog 0372 documented 13 findings (F1–F13) against PR #254 plus 2 false alarms. This session's goal: fix every REAL code/config finding, validate the fixes via the full test+build+lint+chart-render suite, and surface no new issues. Process findings (F2/F12/F13) are addressed by documentation, not code.

---

## Work Completed

### CRITICAL — F1: Org-suspension was dead code in Helm deployments

**Root cause (re-verified):** `controller/internal/controller/controller.go:36` gates `OrgStatusClient` construction on `apiServiceURL != ""`; `charts/llmsafespace/templates/controller-deployment.yaml` never passed `--api-service-url`, so `OrgStatusClient` was always nil and `applyOrgSuspension` (`controller/internal/workspace/org_suspend.go:41`) short-circuited.

**Fix:**
- `charts/llmsafespace/values.yaml`: added `controller.apiServiceURL` (default `""` → chart derives the in-cluster URL) and top-level `internalToken` (default `""` → auto-generated).
- `charts/llmsafespace/templates/controller-deployment.yaml`: derive `--api-service-url` from release name + namespace + API port when unset; always wire `LLMSAFESPACE_INTERNAL_TOKEN` env from the credentials Secret.
- `charts/llmsafespace/templates/api-deployment.yaml`: wire `LLMSAFESPACE_INTERNAL_TOKEN` env.
- `charts/llmsafespace/templates/secret.yaml`: generate + persist an `internal-token` key (same rotation model as jwt/master secrets).

### HIGH — F3: Auth middleware failed open on DB error

**Root cause (re-verified):** `api/internal/services/auth/auth.go:947-956` only acted when `err == nil && user != nil`; any `GetUser` error fell through to `c.Next()`, letting a suspended user regain access during a DB blip.

**Fix:** Fail closed — `GetUser` error → 503 ("unable to verify account status"); `user == nil` → 401 ("account not found"); suspended → 401.

### HIGH — F4: No token revocation on user suspension

**Root cause (re-verified):** `SuspendUser` only flipped DB status; the user's JWT/API key stayed valid. The worklog's proposed fix (iterate sessions via `RevokeToken`) is infeasible — there is no per-user session store (validated via grep). The right-sized mechanism for stateless-JWT revocation-by-principal is a Redis marker.

**Fix:** Added `MarkUserSuspended`/`ClearUserSuspended` to `auth.Service` (key `user_suspended:<userID>`, TTL = tokenDuration) and to the `interfaces.AuthService` interface. `SuspendUser` writes the marker (best-effort); `UnsuspendUser` clears it. The middleware consults the marker on the DB-error branch (precise 401 vs 503 during outage) and heals stale markers on the active-user branch.

**Adversarial finding (found + fixed this session):** the initial design rejected on a marker hit BEFORE `GetUser`, which meant a stale marker (left when `ClearUserSuspended` failed during an unsuspend, e.g. Redis blip) would block an active user for up to `tokenDuration`. Redesigned so `GetUser` stays authoritative: marker is consulted only on the DB-error branch (resilience) and the active-user branch (healing). Regression test `TestAuthMiddleware_StaleMarker_Healed` locks this in.

### HIGH — F5: Internal org-status endpoint unauthenticated by default

**Root cause (re-verified):** `api/internal/handlers/internal_org_status.go:48-53` only checked the token when `LLMSAFESPACE_INTERNAL_TOKEN` was set (chart default = unset) → fully open. Doc comment falsely claimed a NetworkPolicy was the primary boundary (none exists for the API pod). Used `!=` (timing leak).

**Fix:** Invert the default — fail-closed 403 when the token is unset; `crypto/subtle.ConstantTimeCompare` for the comparison. Added opt-in `api-network-policy.yaml` (gated on `networkPolicy.apiIngressRestricted`, default false — a default-on API default-deny would lock users out given deployment-specific ingress selectors). The token gate is the load-bearing control; the NetworkPolicy is defense-in-depth.

### MEDIUM — F6: `users.active` / `users.status` desync (latent)

**Fix:** `SetUserStatus` (`database.go:261`) now mirrors `active = (status == 'active')` so the two columns cannot drift. `SuspendUserGuardedByLastAdmin` (F7) sets both atomically in the same path.

### MEDIUM — F7: Last-admin check TOCTOU

**Root cause (re-verified):** `SuspendUser` did `OrgsWhereUserIsLastActiveAdmin` (SELECT) → `SetUserStatus` (UPDATE) with no transaction; two concurrent admin suspensions could orphan an org. The safe `SELECT ... FOR UPDATE` pattern already existed in `pg_org_store.go:410-512` but wasn't reused.

**Fix:** Added `PgOrgStore.SuspendUserGuardedByLastAdmin(ctx, userID, force)` — one transaction that `SELECT … FOR UPDATE`s the admin rows of every org the user administers, re-runs the last-admin check inside the tx, then `UPDATE users SET status='suspended', active=false`. Refactored `OrgsWhereUserIsLastActiveAdmin` to share its SQL via a `queryer`-based helper (no duplication). `SuspendUser` handler now calls this atomic method; the now-unused `OrgsWhereUserIsLastActiveAdmin` was removed from the handler's `platformAdminOrgStore` interface (and its mock fields) to avoid dead code (Rule 5).

### MEDIUM — F8: OIDC auto-provision ignored `email_verified`

**Fix:** `oidcClaims` now decodes `email_verified`; `HandleCallback` rejects with `ErrEmailUnverified` before any account binding (covers both auto-provision and login-match). `org_sso.go` `errorReason` maps it to a client-safe `email_unverified` token.

### MEDIUM — F9: `memberOf` fallback untested

**Fix:** Added `TestCallback_MemberOfGroups_MappedToRole` (Azure-AD-style token using `memberOf` instead of `groups`) + `TestEffectiveGroups_MergesGroupsAndMemberOf` unit test. Production code already worked; this closes the coverage gap.

### MEDIUM — F10: PKCE verifier not validated by the fake IdP

**Fix:** Fake IdP (`sso_test.go`) gained an `/authorize` handler that records the S256 `code_challenge`; `/token` now validates `codeChallenge(verifier) == challenge` when a challenge was recorded. `TestCallback_PKCEBinding_FullFlow` + `TestCallback_PKCEBinding_WrongVerifierRejected` prove the binding is enforced (would catch a client regression dropping the verifier). Production PKCE was already correct; this is a test-only gap fix.

### LOW — F11: Redirect URL trusts X-Forwarded-Proto/Host

**Fix:** `resolveCallbackURL` (`org_sso.go`) now logs a warning when it falls back to forwarded headers (so operators see the gap), with an accurate security-note comment. SSO callbacks are infrequent (once per login) so the per-call warning is not noisy.

### Process findings (no code fix)

- **F2 (process):** "Pre-existing CI failure" claim that justified merging on red — no code; documented as a process discipline rule (never merge on red without independent verification).
- **F12 (worklogs):** US-43.10/19/20 worklogs were missing on main; the `docs/epic43-validation-findings` branch added them (0372–0376). This worklog is numbered 0377 to avoid collision.
- **F13 (assumptions):** Rule 7 assumptions for this session are recorded in "Key Decisions" below.

---

## Key Decisions

1. **F4 marker over session enumeration.** Assumption validated: there is no per-user session/token store (grep for `ListUserSessions|RevokeAllForUser` returns nothing); `RevokeToken` takes a token, not a userID. The Redis `user_suspended:<userID>` marker is the standard revocation-by-principal pattern for stateless JWTs. Verified `*auth.Service` has `cacheService` (`auth.go:106`) and `tokenDuration` (`auth.go:116`).
2. **F3 fail-closed is the load-bearing guarantee; F4 marker is resilience + precise labelling.** Validated that `GetUser` returns `(nil, nil)` on missing row and `(nil, err)` on DB error (`database.go:103-108`), so the two fail-closed branches are distinct and correct. The marker only changes a suspended user's DB-outcome from 503→401 (both deny) and heals stale markers — it does not change enforcement, which is why the stale-marker bug was caught and fixed.
3. **F5 fail-closed requires F1 to wire the token on both sides.** Validated the controller sends `X-Internal-Token` when its token is non-empty (`org_status_client.go:137-139`) and reads it from `LLMSAFESPACE_INTERNAL_TOKEN` (`controller/main.go:104`); both deployments now mount the same Secret key.
4. **F7 reuses the existing locking pattern.** Validated `PgOrgStore` and `database.Service` share the same `*sql.DB` (`app.go:318` `NewPgOrgStore(dbSvc.DB)`), so a cross-table transaction (org_memberships lock + users update) is sound.
5. **F8 requires `email_verified==true` strictly** (absent → reject). Per OIDC spec; org admins are responsible for configuring a compliant IdP. The fake IdP defaults the claim to true so existing happy-path tests represent a well-configured IdP; the rejection test sets it false.
6. **F5 NetworkPolicy is opt-in (default off).** A default-on API default-deny would lock users out given deployment-specific ingress-controller labels; the token gate is the primary control. Validated via `TestF5_ApiNetworkPolicy_DefaultOff` + `TestF5_ApiNetworkPolicy_OptIn`.
7. **Widened `interfaces.AuthService`** with `MarkUserSuspended`/`ClearUserSuspended` (their proper auth-domain home) rather than type-asserting to `*auth.Service` in `app.go`. Updated both hand-written mocks (`mocks/middleware_mocks.go`, `middleware/tests/auth_test.go`).

---

## Blockers

None.

---

## Tests Run

All run from repo root; helm on PATH at `$HOME/.local/bin/helm`.

- `go build ./...` → PASS (0 errors).
- `go vet ./...` → PASS (0 errors).
- `gofmt -l` across `api controller pkg cmd` → clean (also fixed a pre-existing `pkg/types/auth.go` struct-alignment issue per Rule 5).
- `go test -timeout 400s -count=1 ./...` → **55 packages OK, 0 FAIL**.
- `go test ./charts/llmsafespace/...` (`helm template` subprocess) → PASS, including new `TestF1_*` and `TestF5_*` chart tests.
- New/updated test coverage added: `auth_suspension_test.go` (F3/F4 incl. stale-marker healing), `pg_org_store_test.go` (F6/F7 atomicity), `platform_admin_test.go` (F4 revoker wiring + best-effort), `internal_org_status_test.go` (F5 fail-closed flip), `sso_test.go` + `org_sso_idp_helpers_test.go` (F8/F9/F10).
- `helm template test-release charts/llmsafespace -n llms` → renders with `--api-service-url=http://<release>-api.<ns>.svc:8080`, `LLMSAFESPACE_INTERNAL_TOKEN` env on both API+controller, and `internal-token` in the Secret.

---

## Next Steps

1. Open a PR for this branch (`fix/epic43-validation-findings-372`) and run the automated reviewer; iterate to APPROVE per the MANDATORY review-iterate-approve-merge cycle.
2. Coordinate merge ordering with `docs/epic43-validation-findings` (worklogs 0372–0376) so numbering stays consistent (this worklog is 0377).
3. After merge, consider the long-term F6 follow-up: drop the legacy `active` column entirely once all readers migrate to `status`.
4. Consider a re-triggerable relay injector (documented design fragility #2 in README-LLM.md) as a separate effort — out of scope here.

---

## Files Modified

**Production code:**
- `api/internal/services/auth/auth.go` — F3 fail-closed; F4 marker primitives + middleware integration + stale-marker healing; OptionalAuthMiddleware reverted to GetUser-only.
- `api/internal/services/database/database.go` — F6 `SetUserStatus` mirrors `active`.
- `api/internal/services/database/pg_org_store.go` — F7 `SuspendUserGuardedByLastAdmin` + shared `lastActiveAdminOrgsQuery`/`scanLastActiveAdminOrgs`.
- `api/internal/services/sso/sso.go` — F8 `EmailVerified` claim + `ErrEmailUnverified`.
- `api/internal/handlers/platform_admin.go` — F4 revoker wiring + F7 atomic suspend path.
- `api/internal/handlers/internal_org_status.go` — F5 fail-closed + `ConstantTimeCompare`.
- `api/internal/handlers/org_sso.go` — F8 `errorReason` mapping + F11 forwarded-header warning.
- `api/internal/interfaces/interfaces.go` — widened `AuthService` with `MarkUserSuspended`/`ClearUserSuspended`.
- `api/internal/app/app.go` — wire revoker into `NewPlatformAdminHandler`.
- `api/internal/mocks/middleware_mocks.go`, `api/internal/middleware/tests/auth_test.go` — mock methods for the widened interface.
- `pkg/types/auth.go` — gofmt fix (pre-existing).

**Tests:**
- `api/internal/services/auth/auth_suspension_test.go` (new) — F3/F4.
- `api/internal/services/database/pg_org_store_test.go` — F6/F7.
- `api/internal/services/database/database_test.go` — updated `TestSetUserStatus` for F6 query shape.
- `api/internal/handlers/platform_admin_test.go` — F4/F7 handler tests + mock revoker.
- `api/internal/handlers/platform_admin_list_test.go`, `api/internal/server/router_admin_platform_list_test.go` — constructor signature + interface updates.
- `api/internal/handlers/internal_org_status_test.go` — F5 fail-closed flip.
- `api/internal/services/sso/sso_test.go` — F8/F9/F10 + fake IdP `/authorize`+PKCE validation + email_verified default.
- `api/internal/handlers/org_sso_idp_helpers_test.go` — email_verified default in `signRS256`.

**Chart:**
- `charts/llmsafespace/values.yaml` — `controller.apiServiceURL`, `internalToken`, `networkPolicy.apiIngressRestricted` + `apiIngressSourcePodSelector`.
- `charts/llmsafespace/templates/controller-deployment.yaml` — `--api-service-url` + `LLMSAFESPACE_INTERNAL_TOKEN` env.
- `charts/llmsafespace/templates/api-deployment.yaml` — `LLMSAFESPACE_INTERNAL_TOKEN` env.
- `charts/llmsafespace/templates/secret.yaml` — `internal-token` generation.
- `charts/llmsafespace/templates/api-network-policy.yaml` (new) — opt-in F5 defense-in-depth.
- `charts/llmsafespace/chart_test.go` — `TestF1_*` + `TestF5_*`.
