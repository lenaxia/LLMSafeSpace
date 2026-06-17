# Worklog: Story 2 — Admin-Only Org Creation + Email Resolution

**Date:** 2026-06-17
**Session:** Implement Story 2 from design 0031 (D1) — make POST /api/v1/orgs platform-admin only, resolve owner email → user ID server-side, remove the self-service Stripe Checkout flow. TDD → implement → adversarial review → PR.
**Status:** Complete — awaiting orchestrator review/merge

---

## Objective

Implement Story 2 of design `0031_2026-06-15_org-access-control-portal-architecture.md` (D1):

1. `POST /api/v1/orgs` returns **403** for non-platform-admins.
2. Platform admins create orgs by providing an `ownerEmail`; the backend resolves it to a user ID (404 if not found) and creates the org already `active` with the requested plan (default `enterprise`), adding the resolved user as the org's first admin member.
3. The caller admin is recorded as `CreatedBy`; the owner is the email-resolved user.
4. The self-service Stripe Checkout branch is removed entirely from the `Create` handler.
5. `StripeProvider` and the Stripe webhook handler are **retained** (webhook still syncs subscription status; the per-org `Checkout`/`Portal` handlers remain for plan upgrades). Self-service will be rebuilt in a future billing-portal epic.

This reverses shipped Epic 43 Phase 1 functionality — that is intended and confirmed (the self-service flow's DEK bootstrap was broken; D7/Story 1 already removed the DEK).

Builds on PR #188 (Story 1, eliminate org DEK) and PR #199 (org credential UI unification).

---

## Assumptions (stated per Rule 7, validated with evidence)

| # | Assumption | Validation |
|---|---|---|
| A1 | `GetUserByEmail` already exists on `*database.Service` returning `(*types.User, error)` with `(nil, nil)` for not-found | **Confirmed** — `api/internal/services/database/database.go:113-141` |
| A2 | The handler-local `orgStore` interface did NOT include an email→userID lookup | **Confirmed** — `api/internal/handlers/orgs.go:18-41` (original). Added `GetUserIDByEmail` to both the handler-local interface and the `OrgStore` interface in `pg_org_store.go`. |
| A3 | Users are hard-deleted (no `deleted_at` column on `users`), so the lookup needs no soft-delete filter | **Confirmed** — `api/migrations/000001_initial_schema.up.sql:1-10` (users table has no `deleted_at`); `api/migrations/000014_*.up.sql:43` comment: "workspaces use soft-delete (deleted_at column); users are hard-deleted" |
| A4 | `isPlatformAdmin(c)` reads `userRole` from the gin context | **Confirmed** — `api/internal/handlers/org_billing.go:47-51` |
| A5 | The auth service normalizes emails to lowercase + trim on register/login, so the DB stores lowercased emails | **Confirmed** — `api/internal/services/auth/auth.go:586` (`Email: strings.ToLower(strings.TrimSpace(req.Email))`) and `:682` (login). **Led to a finding** — the handler must normalize `ownerEmail` before lookup. |
| A6 | `CreateOrgWithAdmin(org, adminUserID)` inserts `org.CreatedBy` as SQL `created_by` and `adminUserID` as the membership user | **Confirmed** — `api/internal/services/database/pg_org_store.go:94-126`. So passing `callerID` as `org.CreatedBy` and `ownerID` as the `adminUserID` param yields the desired semantics. |
| A7 | `PlanID` already existed on `CreateOrgRequest`; `Password` was already removed by worklog 0306/PR #186 | **Confirmed** — `pkg/types/types.go:692-696` (pre-change) had only `Name`, `Slug`, `PlanID` |
| A8 | No SDK or swagger code references `CreateOrgRequest` (so no contract break) | **Confirmed** — `grep -rln "CreateOrgRequest" sdks/ api/docs/` returns nothing; no `@Param`/`@Router` annotations on the org handlers |
| A9 | The slug uniqueness race (TOCTOU between `GetOrgBySlug` and insert) is handled by the partial unique index + `isDuplicateErr` | **Confirmed** — `api/migrations/000030_organizations_status.up.sql:45` partial index `ON organizations(LOWER(slug)) WHERE deleted_at IS NULL`; handler checks `isDuplicateErr` after `CreateOrgWithAdmin` |
| A10 | `OrgBilling`/`SetBilling`/`Checkout`/`Portal` must be retained (per task) | **Confirmed by task** — webhook still syncs subscription status; checkout still used for plan upgrades. Only the `Create`-time self-service branch + `provisionStripe` helper were removed. |

---

## Work Completed

### Types (DTO)

- **`pkg/types/types.go`** — `CreateOrgRequest` gained `OwnerEmail string` with `binding:"required,email"`; updated the struct doc to cite design 0031 D1. `CreateOrgResponse` doc updated (CheckoutURL retained for future billing portal but always empty from create).

### Backend handler

- **`api/internal/handlers/orgs.go`** — Rewrote `Create`:
  - Non-admin → **403** `"only platform admins can create organizations"` (checked before binding the body).
  - Resolves `ownerEmail` (lowercased + trimmed) → user ID via new `GetUserIDByEmail`; unknown → **404** `"owner not found"`; DB error → 500.
  - Creates the org with `CreatedBy = callerID`, owner resolved from email; org is activated (`status=active`, `subscription=active`, plan from request or default `enterprise`).
  - Removed the entire self-service branch (status=`pending_activation` + Stripe Checkout provisioning).
  - Removed the `provisionStripe` helper (dead code after branch removal).
  - `orgStore` interface: removed now-unused `GetUserSalt` and `CreateBillingAccount` (no handler references them after the rewrite — zero tech debt, Rule 5); added `GetUserIDByEmail`. Kept `GetStripeCustomerID` (used by `resolveCustomerID`/Checkout/Portal).
- **`api/internal/handlers/org_billing.go`** — Untouched. `OrgBilling`, `SetBilling`, `Checkout`, `Portal`, `resolveCustomerID` all retained. `isPlatformAdmin` reused.

### Database store

- **`api/internal/services/database/pg_org_store.go`** — Added `GetUserIDByEmail` to the `OrgStore` interface and implemented it on `*PgOrgStore` (single-column `SELECT id FROM users WHERE email = $1`; returns `("", nil)` on `sql.ErrNoRows`). No `deleted_at` filter (users are hard-deleted, A3). This is a focused lookup, not a search/list endpoint (design D1/D10, account-enumeration prevention).

### Frontend (minimal update — full portal redesign is Stories 6–8, out of scope)

- **`frontend/src/api/orgs.ts`** — `CreateOrgRequest` gained required `ownerEmail: string`.
- **`frontend/src/components/settings/OrgSettingsTab.tsx`** —
  - "New Organisation" button now shows **only** when `useAuth().user?.role === "admin"`.
  - `CreateOrgForm` gains an `ownerEmail` field (email input) and a `planId` `<select>` (free/team/business/enterprise, default enterprise); posts all three.
  - Added 403/404-specific error messages (403 → "Only platform admins can create organisations"; 404 → "No user found with that owner email").

### What was NOT touched (per scope)

- `StripeProvider` (`pkg/billing/stripe_provider.go`) — **retained**; the Stripe webhook handler still syncs subscription status.
- Single-org enforcement (Story 3), workspace attribution (Story 4), membership-gated `verifyOwner` (Story 5), PortalLayout/sidebar (Stories 6–7), admin org-management full UI (Story 8) — all out of scope.
- No schema migration needed (no new column/index/table; users table already satisfies the lookup).

---

## Key Decisions

1. **`GetUserIDByEmail` as a new focused store method** (not reusing `GetUserByEmail`). `GetUserByEmail` loads `password_hash` and the full user row; org creation only needs the `id`. A single-column query avoids loading secrets needlessly (Security principle). Added to both the handler-local `orgStore` interface (testability) and the `OrgStore` interface in `pg_org_store.go` (so `*PgOrgStore` satisfies both).
2. **Email normalized before lookup** (`strings.ToLower(strings.TrimSpace(...))`). The auth service stores lowercased+trimmed emails (A5); without normalization, an admin typing a mixed-case email would get a spurious 404. Discovered in adversarial review (see below).
3. **Removed `GetUserSalt` + `CreateBillingAccount` from the handler-local `orgStore` interface.** After the rewrite, no handler references them (the DEK/salt path died with Story 1; `CreateBillingAccount` died with `provisionStripe`). Keeping them would be dead code (Rule 5). The `billingAccounts` mock map is retained because `GetStripeCustomerID` (used by Checkout/Portal tests) still reads it.
4. **Default plan = enterprise** when the admin omits `planId`, matching the prior admin-branch behavior. The frontend `<select>` defaults to enterprise too.
5. **403 returns a clear message** `"only platform admins can create organizations"` (per task deliverable #2) and is checked before body binding (non-admins don't even pay the parse cost).
6. **`StripeProvider` + webhook retained** with this one-line note: the webhook still syncs subscription status for existing/active orgs; the per-org Checkout/Portal endpoints remain for plan upgrades. Only the org-creation-time self-service provisioning was removed.

---

## Adversarial Self-Review (Rule 11)

### Phase 1 + Phase 2 (validated each)

| # | Finding | Verdict | Resolution |
|---|---|---|---|
| F1 | `ownerEmail` not normalized — DB stores lowercased emails (auth.go:586/682); mixed-case input would 404 | **REAL bug** | Fixed: `strings.ToLower(strings.TrimSpace(req.OwnerEmail))` in `Create`. Regression test `TestCreateOrg_Admin_OwnerEmailNormalizedBeforeLookup`. |
| F2 | Account enumeration via 404 "owner not found" | **False alarm** | The 403 gate precedes every email lookup; only platform admins (trusted) can reach the 404 path. Design D10 forbids enumeration in the *invitation/member* flow for non-admins, not admin org creation. Documented. |
| F3 | TOCTOU on slug uniqueness (two concurrent admins, same slug) | **False alarm** | Partial unique index `ON organizations(LOWER(slug)) WHERE deleted_at IS NULL` (migration 030) + `isDuplicateErr` check after insert → second request gets 409. Pre-existing, correct. |
| F4 | Partial state if `CreateOrgWithAdmin` succeeds but `UpdateOrgStatus` fails (org left `pending_activation`) | **Pre-existing behavior, preserved** | Identical structure to the prior admin branch (and the self-service branch's pending-on-failure was by-design, reaped by cron). Out of scope for Story 2 — task says "existing behavior preserved". A pending org with no billing account is a clean state an admin can retry. Documented. |
| F5 | Dead code: `provisionStripe` + `CreateBillingAccount` handler usage | **REAL (introduced by the rewrite)** | Fixed: deleted `provisionStripe`; removed `CreateBillingAccount` + `GetUserSalt` from the handler interface + their mock methods + the `salts` mock map. |
| F6 | `CreatedBy` correctness after owner change | **False alarm** | `newOrg.CreatedBy = callerID` then `CreateOrgWithAdmin` inserts `created_by` from the struct. Asserted in `TestCreateOrg_Admin_KnownEmail_CreatesActiveOrgWithResolvedOwner`. |
| F7 | Frontend: does `OrgSettingsTab` now require `AuthProvider` in tree? | **False alarm** | `SettingsPage` already wraps it; root mounts `AuthProvider`. Existing `SettingsPage.test.tsx` mocks `useAuth`; new `OrgSettingsTab.test.tsx` mocks it too. |

### Phase 3

One real bug (F1, email normalization) fixed with a regression test. One real tech-debt item (F5, dead code) removed. All other findings documented as false alarms or pre-existing/out-of-scope. **Zero real findings remaining.**

---

## Tests

### Backend — new (Rule 0 TDD: tests written first, confirmed red, then implemented)

**`api/internal/handlers/orgs_admin_create_test.go`** (new):
- Happy: `Admin_KnownEmail_CreatesActiveOrgWithResolvedOwner` — 201, status=active, plan=enterprise, sub=active, no checkout URL, Stripe **not** invoked, owner added as admin member, `CreatedBy`=caller.
- Happy: `Admin_DefaultPlanIsEnterprise` — omitted planId → enterprise.
- Happy: `Admin_SelfEmail_OwnerEqualsCreator` — admin creates for their own email (owner==creator) → works.
- Happy: `Admin_OwnerEmailNormalizedBeforeLookup` — mixed-case email resolves (regression for F1).
- Unhappy: `NonAdmin_Returns403` — non-admin → 403, no org created, Stripe not invoked.
- Unhappy: `Admin_UnknownEmail_Returns404` — 404, no org created, Stripe not invoked.
- Unhappy: `Admin_MissingOwnerEmail_Returns400`.
- Unhappy: `Admin_InvalidOwnerEmail_Returns400`.
- Unhappy: `Admin_DuplicateSlug_Returns409`.
- Unhappy: `Admin_LookupError_Returns500`.
- Preserved: `Admin_SlugLowercased`.

**`api/internal/services/database/pg_org_store_test.go`** (new):
- `GetUserIDByEmail_Found`.
- `GetUserIDByEmail_NotFound_ReturnsEmptyNoError` — confirms `("", nil)` contract.
- `GetUserIDByEmail_DBError`.

### Backend — updated (removed tests for deleted self-service flow)

**`api/internal/handlers/org_create_billing_test.go`** — removed `TestCreateOrg_RegularUser_PendingActivationWithCheckoutURL`, `TestCreateOrg_NoBillingConfigured_StillCreatesPending`, `TestCreateOrg_StripeCustomerCreationFails_LeavesPending` (all test the deleted self-service create branch). Removed now-redundant `TestCreateOrg_SlugLowercasedAndStored` + `TestCreateOrg_PlatformAdmin_ActiveEnterprise` (better-covered by the new admin-create tests). Kept all Checkout/Portal tests (billing endpoints retained).

**`api/internal/handlers/orgs_test.go`** — removed obsolete `TestOrgsHandler_Create_SlugConflict` + `TestOrgsHandler_Create_Success` (non-admin path now 403s; better-covered in the new file). Removed `GetUserSalt`/`CreateBillingAccount` mock methods + `salts` map.

### Frontend — new

**`frontend/src/components/settings/OrgSettingsTab.test.tsx`** (new): lists orgs; shows New Org button to admin; hides from non-admin; collects ownerEmail+plan and posts them; surfaces 404 "no user" message.

---

## Tests Run

- `go vet ./api/internal/handlers/ ./api/internal/services/database/ ./pkg/types/ ./api/internal/app/` — **PASS**
- `gofmt -l` on all changed Go files — **clean**
- `go build ./api/... ./pkg/...` — **PASS**
- `go test -timeout 60s -race ./api/internal/handlers/` — **PASS** (39.8s)
- `go test -timeout 60s -race ./api/internal/services/database/` — **PASS** (1.2s)
- `go test -timeout 90s ./api/internal/server/ ./api/internal/app/` — **PASS** (wiring intact)
- `npx tsc --noEmit` (frontend) — **PASS**
- `npx vitest run` (frontend) — **PASS** (104 files, 1089 tests, including the 5 new OrgSettingsTab tests)

TDD cycle observed: new Story 2 tests were run against the unchanged handler first and confirmed red (403/404/owner-resolution failing), then the handler was implemented and re-run green.

---

## Blockers

None.

---

## Next Steps

1. Orchestrator runs a separate validator agent on this PR; iterate on any real findings.
2. After merge, Story 3 (single-org enforcement + no self-removal, D6/D8) is unblocked — depends on this story's resolved-owner flow.
3. Story 8 (admin org-management full UI) will replace the minimal form added here with a proper AdminOrganisationsTab component; the `ownerEmail` contract and 403/404 semantics established here will carry over.

---

## Files Modified

### New files
- `api/internal/handlers/orgs_admin_create_test.go`
- `api/internal/services/database/pg_org_store_test.go`
- `frontend/src/components/settings/OrgSettingsTab.test.tsx`

### Modified files
- `pkg/types/types.go` — `CreateOrgRequest.OwnerEmail` (required, email-validated); doc updates
- `api/internal/handlers/orgs.go` — admin-only `Create`; removed `provisionStripe`; interface cleanup (+`GetUserIDByEmail`, −`GetUserSalt`/`CreateBillingAccount`)
- `api/internal/handlers/orgs_test.go` — mock updates (+`GetUserIDByEmail`/`usersByEmail`, −`GetUserSalt`/`CreateBillingAccount`/`salts`); removed obsolete create tests
- `api/internal/handlers/org_create_billing_test.go` — removed self-service create tests; fixed imports
- `api/internal/services/database/pg_org_store.go` — `OrgStore.GetUserIDByEmail` interface method + `PgOrgStore.GetUserIDByEmail` impl
- `frontend/src/api/orgs.ts` — `CreateOrgRequest.ownerEmail`
- `frontend/src/components/settings/OrgSettingsTab.tsx` — admin-only create button; ownerEmail + planId fields; 403/404 messages

### Additional findings caught by the lint/tsc gates
- **staticcheck SA1012 (nil context):** the new test passed `nil` as the context to the mock's `GetOrgMember`; golangci-lint flagged it. Fixed by passing `context.Background()` (the mock ignores it, but the lint rule is correct — never pass a nil Context).
- **tsc TS2532 (possibly-undefined array index):** `mockCreate.mock.calls[0][0]` failed strict tsc even though vitest (esbuild) accepted it. Fixed with a non-null assertion after an explicit length check.
- **misspell (`behaviour` → `behavior`):** golangci-lint misspell caught the British spelling in a test comment. Fixed.

### Automated reviewer round 2 (REQUEST CHANGES — one item) — addressed
Round 2 confirmed all round-1 items resolved but flagged incomplete dead-code cleanup: I had removed `GetUserSalt`/`CreateBillingAccount` from the *handler-local* interface, but their **store-layer** declarations remained with zero callers. Verified independently (Rule 7): `grep -rn "\.GetUserSalt(\|\.CreateBillingAccount(" --include="*.go"` returns **zero** call sites repo-wide; both are defined only on `*PgOrgStore`; the only `OrgStore` consumer is `OrgMembershipChecker` which needs just `IsOrgMember`/`IsOrgAdmin`. Removed:
- `GetUserSalt` from the `OrgStore` interface (`pg_org_store.go:48`) + its `*PgOrgStore` impl.
- `CreateBillingAccount` impl from `*PgOrgStore` (it was not on the interface; standalone dead method). `SetBillingAccountSubscription` (retained — different method, may be used by the webhook path) is unaffected.

### Automated reviewer round 1 (REQUEST CHANGES) — all addressed
The CI reviewer requested changes for untested `Create` error paths and two minor issues. All resolved:
- **Added `TestCreateOrg_Admin_UpdateOrgStatusFails_Returns500`** — exercises the partial-state path (create succeeds, activate fails → 500, org left `pending_activation`). Added `updateStatusErr` field to the mock.
- **Added `TestCreateOrg_Admin_CreateOrgGenericError_Returns500`** — exercises the generic `createErr` branch via the existing mock field.
- **Added `TestCreateOrg_Admin_CreateOrgDuplicateTOCTOU_Returns409`** — exercises the `isDuplicateErr` branch by returning a `*pgconn.PgError{Code:"23505"}` from the mock (simulates the race between `GetOrgBySlug` and insert).
- **Fixed `CreateOrgResponse.UserRole` semantics:** previously hardcoded `OrgRoleAdmin`; now `""` when caller ≠ owner (caller is not a member) and `OrgRoleAdmin` only when caller == owner. Aligns with the `OrgResponse` doc ("the calling user's membership context"). Frontend ignores the field from the create response, so no functional impact. Locked in with assertions in the known-email and self-email tests.
- **Fixed stale comment** in `org_billing.go:53` (`resolveCustomerID`) that referenced the removed self-service flow.

Pre-existing concerns the reviewer flagged but that are explicitly out of scope (documented, not fixed here):
- Two-step create+activate is not transactional; `pending_org_cleaner` may hard-delete a partially-created org. Pre-existing (same structure as the prior admin branch), tracked outside Story 2.
- `OrgSettingsTab.slugify()` produces hyphens but `CreateOrgRequest.Slug` binding is `alphanum` — multi-word names would 400. Predates this PR; the slug field is editable so the user can correct it.

### Worklog numbering
The task prompt stated the next free worklog was 0311, but `origin/main` kept advancing during this session. The renumber history:
- 0311 (initial pick) — `origin/main` had a `0311_…_placeholder.md`; renumbered to **0313**.
- 0313 (first push) — by the time CI ran, main had merged `0313_…_epic-44-design.md`, `0314_…_epic-45-design.md`, and `0315_…_repolint-auto-renumber-post-rebase.md`. CI Lint failed on the 0313 collision.
- Merged `origin/main` (which also landed PR #200 — repolint auto-renumber-on-rebase) and renumbered to **0316** (`bin/repolint` confirmed next free).

All renames used `git mv` (no force-push; main's history untouched per Rule 10 — used a merge + normal push rather than rebase + force-push).

### Branch
`feat/epic43-0031-story2-admin-org-creation` (from `main`)
