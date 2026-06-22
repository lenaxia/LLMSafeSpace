# Worklog: Org admin "Verify" button — force-verify a member bypassing email

**Date:** 2026-06-22
**Session:** Add a per-member "Verify" action to the org admin dashboard so an org admin can mark a pending (unverified-email) member as email-verified without going through the email-validation token flow.
**Status:** Complete

---

## Objective

The org admin "Members" table previously surfaced no information about a member's email-verification state, and offered no way for an admin to bypass the email-validation flow when a member could not (or did not) complete it (e.g. the verification email never arrives, or the admin has confirmed the member's identity out-of-band). Add a "Verify" button visible only for members whose `users.email_verified` is `false`, backed by a new org-admin-only endpoint that flips the flag and records the override in the org audit log.

---

## Work Completed

### Backend (Go)

### DTO and SQL changes

- `pkg/types/orgs.go` — Added `EmailVerified bool` to the `OrgMember` struct. Doc-comment explains that the verification state lives on the `users` row (not the membership) and that single-org enforcement (D8) means there is exactly one user per member.
- `api/internal/services/database/pg_org_store.go`:
  - `GetOrgMember` and `ListOrgMembers` SELECT now include `u.email_verified`; scan target updated.
  - Added `MarkUserEmailVerified(ctx, userID string) error` — issues `UPDATE users SET email_verified = TRUE, updated_at = NOW() WHERE id = $1`. Idempotent. Added to the `OrgStore` interface.

### Handler and route

- `api/internal/handlers/orgs.go`:
  - Extended the handler-local `orgStore` interface with `MarkUserEmailVerified` and `LogOrgEvent`.
  - Added `(*OrgsHandler).VerifyMember` handling `POST /api/v1/orgs/:id/members/:userID/verify`. Idempotent (200 on already-verified). Emits a `member.verify` org-scoped audit event with the actor, target user ID, and the member's email in metadata.
  - Added optional `orgsLogger` (mirrors `policyLogger` / `ssoLogger` shape: `Warn(msg string, args ...any)`) and `SetLogger` setter, mirroring the existing `SetBilling` setter pattern so the constructor signature stays stable. Used to surface non-fatal audit-emission failures rather than swallowing them (Rule 3).
- `api/internal/server/router.go` — Registered `POST /members/:userID/verify` under `orgAdminGroup` (already guarded by `middleware.OrgAdminGuard`).
- `api/internal/app/app.go` — Wired `orgsHandler.SetLogger(log)` after construction.

### Frontend (TypeScript / React)

- `frontend/src/api/orgs.ts`:
  - Added `emailVerified: boolean` to the `OrgMember` interface.
  - Added `orgsApi.verifyMember(id, userId)` calling the new endpoint.
- `frontend/src/components/org-admin/OrgMembersTab.tsx`:
  - Added an "Email Status" column showing a `Verified` / `Pending` badge (`Badge` `default` / `warning` variants).
  - Added a `Verify` button in `MemberActions` rendered only when `!member.emailVerified`. Calls `verifyMember` then `refresh()`.

### Tests

- `api/internal/handlers/orgs_test.go`:
  - Extended `mockOrgStore` with `MarkUserEmailVerified`, `LogOrgEvent`, and call-capture fields. Mock mirrors verification onto the membership row so a subsequent `GetOrgMember` reflects `EmailVerified=true` (mirrors the real SQL behaviour).
  - Added `TestOrgsHandler_VerifyMember_Success` (happy path — verifies the audit event shape and the persisted state), `_AlreadyVerified_Idempotent`, `_NotFound` (404, no DB write, no audit), `_MarkVerifiedError_500`, and `_AuditFailureNonFatal` (proves a successful verification is not undone by an audit-table outage; uses a `warnCaptureLogger` to assert the warning surfaces via `SetLogger`).
- `api/internal/services/database/pg_org_store_test.go`:
  - `TestPgOrgStore_ListOrgMembers_IncludesEmailVerified` — regression guard: the SELECT must include `u.email_verified`, otherwise the bool zero-value would silently render every member as verified.
  - `TestPgOrgStore_GetOrgMember_IncludesEmailVerified` — single-row path used by the handler.
  - `TestPgOrgStore_MarkUserEmailVerified_IssuesCorrectUpdate` — regex-anchors the full `UPDATE users SET email_verified = TRUE, updated_at = NOW() WHERE id = $1` so a regression that drops the `WHERE id` scoping (updating ALL users) is caught.
  - `TestPgOrgStore_MarkUserEmailVerified_DBError` — error propagation.
- `frontend/src/components/org-admin/OrgMembersTab.test.tsx` (new): 6 tests — list loading, Email Status column, Verified/Pending badges, Verify-button visibility rules (only for unverified), `verifyMember` call + refresh on click, and action-button hiding for non-admins.

---

## Key Decisions

1. **Verification state lives on `users.email_verified`, not on `org_memberships`.** A user has exactly one account and (under single-org enforcement, D8) belongs to at most one org, so there is no separate per-membership verification concept. The new endpoint writes the user row, consistent with the existing `EmailVerifyHandler.Verify` (`api/internal/handlers/email_verify.go:121`) which also calls `UpdateUser(EmailVerified: &verified)`.

2. **Force-verify is org-admin only, not platform-admin.** The endpoint sits under `orgAdminGroup` (OrgAdminGuard). Org admins are trusted actors who already manage their members' access (promote/demote/remove); adding the verify override to the same surface matches the existing trust boundary. Platform admins can still do it via the existing user-suspend/unsuspend surface if needed.

3. **Audit log emitted via `LogOrgEvent` (domain='org', action='member.verify')** with metadata `{"email": <member email>}`. The audit_log CHECK constraint is only on `domain` (not `action`), so a new action string requires no migration. Pattern matches `policy.set` / `sso.domain.verify`.

4. **Audit-emission failure is non-fatal but logged, not swallowed.** If the audit-log INSERT fails (e.g. table outage), the verification itself still succeeds — the admin's intent was recorded on the user row, and rolling it back would leave the user unverified with no recourse. The error is surfaced via `h.logger.Warn(...)` (Rule 3 — no swallowed errors) when a logger is wired. `SetLogger` follows the existing `SetBilling` setter pattern to avoid changing the constructor signature and breaking the 4 existing test call-sites.

5. **`MarkUserEmailVerified` lives on `PgOrgStore` (not on `database.Service.UpdateUser`).** Rationale: the handler's data-access surface is the org store, and the org store already owns other user-level writes (`SuspendUserGuardedByLastAdmin`, `GetUserIDByEmail`, `GetUserEmail`, `GetUserOrgID`). Adding a focused `UPDATE users SET email_verified = TRUE WHERE id = $1` is one statement with no dynamic SQL, while `database.Service.UpdateUser` builds a dynamic query from `UserUpdates` (overkill for a single boolean).

6. **Frontend uses a `Pending` badge rather than hiding the row.** Surfacing the state is more useful than hiding it; the admin can see at a glance which members have not completed verification and act on each individually.

---

## Assumptions

| # | Assumption | Validation |
|---|---|---|
| A1 | The `OrgMember` DTO is consumed only by the org-admin members table and `POST /invitations/:token/accept` response. Adding `EmailVerified` is additive and non-breaking. | Confirmed by `rg "OrgMember"` — only callers are `OrgMembersTab.tsx`, `orgs.ts`, `invitations.go` (accept response), and `pg_org_store.go`. JSON tag is `emailVerified`; old clients ignoring unknown fields are unaffected. |
| A2 | The `audit_log.action` column has no CHECK constraint, so `member.verify` requires no migration. | Confirmed by reading `api/migrations/000028_audit_log.up.sql` (CHECK only on `domain`) and `000034_audit_log_org.up.sql` (only adds 'org' to the domain list). |
| A3 | `OrgAdminGuard` is sufficient authorization — no additional last-admin / self-verify guard is needed. | Confirmed: the endpoint does not touch role or membership, only the user's `email_verified` flag. There is no "last verified admin" invariant to protect. Verifying oneself is harmless. |
| A4 | `MarkUserEmailVerified` is safe to call with a bare `userID` (no org scoping in the SQL). | Confirmed: the handler pre-checks `GetOrgMember(orgID, targetUserID)` returns non-nil before calling `MarkUserEmailVerified`, so the user is guaranteed to be a member of this org. The user_id is taken from the URL path, not the body. |
| A5 | `SetLogger` setter (rather than constructor arg) is consistent with the existing OrgsHandler pattern. | Confirmed: `SetBilling` is the established setter pattern in `orgs.go:74`. The PolicyHandler and SSOHandler constructors take a logger directly, but those are newer handlers; OrgsHandler predates them and the setter avoids breaking 4 test call-sites and the app.go wiring. |
| A6 | The existing `captureLogger` name in `email_test.go` collides with a new test type — must rename. | Confirmed: `email_test.go:57` declares `captureLogger` with an `Error(msg, err, _ ...any)` method. My new logger needs `Warn(msg, _ ...any)`, so the types are distinct and cannot share the name. Renamed to `warnCaptureLogger`. |

---

## Adversarial self-review

**Phase 1 — Findings generated:**

1. *Initial draft swallowed the audit-log error with `_ = err`.* Rule 3 forbids swallowed errors. Fixed by adding `orgsLogger` + `SetLogger` and surfacing the warning. Regression-tested in `TestOrgsHandler_VerifyMember_AuditFailureNonFatal`.
2. *Could a non-admin reach the endpoint via route shadowing?* Re-checked: the route is registered under `orgAdminGroup` which uses `OrgAdminGuard`. The frontend hides the button when `!isAdmin`. Defence in depth: even if the button were visible, the middleware would 403 a non-admin.
3. *Is verifying an already-verified member safe?* Yes — idempotent `UPDATE ... SET email_verified = TRUE` is a no-op row write. Tested.
4. *Could the audit metadata leak the member's email to unauthorized viewers?* The audit log is itself org-admin-only (`GET /orgs/:id/audit` requires OrgAdminGuard), so the email is visible only to admins who can already see the members table. No new exposure.
5. *Does the SQL change to `ListOrgMembers`/`GetOrgMember` break any existing test?* Ran the full `api/internal/services/database` and `api/internal/handlers` suites — all pass.
6. *Frontend: does `emailVerified` need to be optional for backward compat with cached responses?* No — the API always returns it now, and the type is non-optional. `tsc --noEmit` passes.

**Phase 2 — All findings either fixed (1) or documented as false alarms with rationale (2–6).**

**Phase 3 — Remediations complete; regression tests added for each real finding.**

---

## Blockers

None.

---

## Tests Run

```bash
# Backend — affected packages
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go test -timeout 60s ./api/internal/handlers/ -run 'TestOrgsHandler_VerifyMember' -v   # 5/5 PASS
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go test -timeout 60s ./api/internal/services/database/ -run 'TestPgOrgStore_(ListOrgMembers|GetOrgMember|MarkUserEmailVerified)' -v   # 4/4 PASS

# Backend — full sweep (no regressions introduced)
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go test -timeout 120s ./api/... ./pkg/...   # all PASS except pre-existing pkg/repolint worklog-numbering collision on origin/main (unrelated)

# Backend — formatting
gofmt -l api/internal/handlers/orgs.go api/internal/handlers/orgs_test.go api/internal/services/database/pg_org_store.go api/internal/services/database/pg_org_store_test.go api/internal/server/router.go api/internal/app/app.go pkg/types/orgs.go   # no output (clean)

# Backend — vet
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go vet ./api/internal/handlers/ ./api/internal/services/database/ ./api/internal/server/ ./api/internal/app/   # clean

# Frontend
npm run test -- src/components/org-admin/OrgMembersTab.test.tsx   # 6/6 PASS
npm run test                                                       # 1191/1191 PASS (113 files)
npm run typecheck                                                  # clean
npm run lint                                                       # clean
```

The single failing test in the repo (`pkg/repolint/TestLive_Worklogs_NoMainlineCollisions`) is a pre-existing worklog-numbering collision between local and `origin/main` (local `0460_us-33.1-metrics-cardinality-fixes.md` vs remote `0460_epic-50-us50.1-master-kek-file-mount.md`). It is unrelated to this change. This worklog uses `0474` (the next number above the highest on `origin/main`, which is `0473`) to avoid adding a new collision.

---

## Next Steps

- (Optional) Wire a confirmation dialog in `OrgMembersTab` before force-verifying, matching the destructive-action confirmation pattern in `DangerZone.tsx`. The current implementation matches the existing lightweight UX of Promote/Demote/Remove (no confirmation). Defer until product weighs in.
- (Optional) Add `member.verify` to the audit-log action allowlist if/when a CHECK constraint is added to `audit_log.action` (none today).
- Consider surfacing the verify action on the platform-admin user dashboard too (`AdminUsersPage`) for cross-org visibility — separate PR.

---

## Files Modified

- `pkg/types/orgs.go` — added `EmailVerified` to `OrgMember`.
- `api/internal/services/database/pg_org_store.go` — `GetOrgMember` / `ListOrgMembers` SELECT includes `u.email_verified`; new `MarkUserEmailVerified` + interface entry.
- `api/internal/services/database/pg_org_store_test.go` — 4 new SQL-level tests.
- `api/internal/handlers/orgs.go` — `orgStore` interface gains `MarkUserEmailVerified` + `LogOrgEvent`; new `VerifyMember` handler; new `orgsLogger` + `SetLogger`.
- `api/internal/handlers/orgs_test.go` — mock extended; 5 new handler tests + `warnCaptureLogger`.
- `api/internal/server/router.go` — registered `POST /orgs/:id/members/:userID/verify`.
- `api/internal/app/app.go` — wired `orgsHandler.SetLogger(log)`.
- `frontend/src/api/orgs.ts` — `OrgMember.emailVerified`; `orgsApi.verifyMember`.
- `frontend/src/components/org-admin/OrgMembersTab.tsx` — Email Status column + Verify button.
- `frontend/src/components/org-admin/OrgMembersTab.test.tsx` — new, 6 tests.
