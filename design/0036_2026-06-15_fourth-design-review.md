# 0036: Fourth Design Review — Edge Cases & Dead Code

**Date:** 2026-06-15
**Status:** Final review of 0031
**Focus:** Edge cases, dead code, deployment ordering

---

## S17 (DEAD CODE): PendingOrgCleaner is dead code after D1

**Verified in code:** `PendingOrgCleaner` (`pending_org_cleaner.go`) runs on a goroutine (`app.go:581`), queries for `pending_activation` orgs older than 7 days, verifies their Stripe checkout status, and either activates or hard-deletes them.

D1 eliminates the self-service flow — platform admins create orgs that are immediately `active`. No orgs are created in `pending_activation` state. The cleaner runs every cycle, finds zero pending orgs, and wastes a Stripe API call per run (if configured).

**Impact:** Dead code, not harmful. But it references `HardDeleteOrg` which has the same workspace-nulling bug as the old `SoftDeleteOrg` (F6). And it references the deleted self-service flow in comments, which would confuse future readers.

**Recommendation:** Disable the cleaner in `app.go` (don't start the goroutine). Don't delete the code — it will be reused when the billing portal epic adds self-service checkout. Add a comment: "Disabled: no self-service org creation. Re-enable when billing portal ships."

---

## S18 (EDGE CASE): Platform admin can create org for a user already in another org

**The scenario:** Platform admin enters an email for user X. User X is already a member of org A. D8 enforces single-org via `UNIQUE(user_id)` on `org_memberships`. The `CreateOrgWithAdmin` transaction inserts the org, then the membership — the membership INSERT fails with a unique constraint violation. The transaction rolls back (atomic — org is not created). But the error surfaces as a generic 500 or constraint-violation message.

**Impact:** No data corruption (transactional), but poor UX — the admin gets a cryptic DB error instead of "this user is already in an organization."

**Fix:** Add a pre-check in `OrgsHandler.Create` (Story 2): call `GetUserOrgID(ownerUserID)` before creating the org. If non-empty, return 409: "user is already a member of another organization."

---

## S19 (CLARIFICATION): `created_by` vs owner in D1 flow

**The ambiguity:** `OrgsHandler.Create` sets `CreatedBy: userID` (the platform admin who called the API). `CreateOrgWithAdmin(ctx, org, adminUserID, ...)` takes `adminUserID` as the org's first admin. In the new D1 flow, these are different people — the creating admin and the resolved owner.

The current handler uses the **same** `userID` for both: `CreatedBy: userID` and `CreateOrgWithAdmin(ctx, newOrg, userID, ...)`.

With D1's `ownerEmail` resolution, the handler should:
- Set `CreatedBy` to the platform admin's user ID
- Pass `ownerUserID` (resolved from email) to `CreateOrgWithAdmin`

**Impact:** If the implementer doesn't notice this distinction, the org's `created_by` field would be the owner instead of the admin, or the org's first admin would be the creating admin instead of the owner.

**Fix:** Explicitly document in Story 2 that `created_by` = platform admin, org membership `user_id` = resolved owner. The handler changes from `CreateOrgWithAdmin(ctx, newOrg, userID, wrappedDEK)` to `CreateOrgWithAdmin(ctx, newOrg, ownerUserID)` (no wrappedDEK — S10).

---

## S20 (DEPLOYMENT): Migration and code must deploy atomically

**The issue:** The API service runs migrations on startup. Story 1's migration drops `org_key_members` and `pending_key_wrap`. The code in the same release removes all references to them. If deployed correctly (new binary + migration together), there's no gap.

But: if an operator rolls back the binary without rolling back the migration, the old binary references a dropped table/column and crashes on every org query. This is standard migration risk — not unique to this design — but the blast radius is wide (every `IsOrgAdmin`, `IsOrgMember`, `GetOrgMember`, etc. query).

**Recommendation:** Document in Story 1 that the migration is **non-reversible without data loss**. The down migration should recreate the table/column as nullable/empty, not attempt to restore data. Standard practice — just needs explicit documentation.

---

## S21 (SIDE BENEFIT): Password recovery no longer affects org credentials

**Observation:** With the old org DEK model, password recovery (`RecoverAccount`) destroyed the user's personal DEK AND their ability to unwrap the org DEK. Other admins had to re-wrap. With D7 (server KEK), org credentials are entirely independent of user passwords. Password recovery only affects personal credentials.

**Impact:** None — this is strictly better than before. No action needed. Worth noting as a benefit in the PR description.

---

## Summary

| Finding | Severity | Action |
|---------|----------|--------|
| S17: PendingOrgCleaner dead code | Cleanup | Disable goroutine, add comment. Add to Story 1 or 2. |
| S18: Creating org for user already in org | Edge case | Pre-check in Story 2. |
| S19: `created_by` vs owner distinction | Clarification | Document in Story 2. |
| S20: Migration is non-reversible | Deployment note | Document in Story 1. |
| S21: Password recovery benefit | None | Note in PR. |

No structural blockers found. The remaining issues are cleanup (S17), UX polish (S18), and documentation (S19, S20). The design is ready for implementation.
