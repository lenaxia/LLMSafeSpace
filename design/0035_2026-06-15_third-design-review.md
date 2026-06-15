# 0035: Third Design Review — Structural Issues

**Date:** 2026-06-15
**Status:** Review of 0031 after 0033 and 0034 corrections
**Focus:** Structural issues that would cause implementation problems or impact functionality

---

## S9 (IMPLEMENTATION BLOCKER): Design references `workspace_metadata` table — actual table is `workspaces`

**Verified in code:**
- `api/migrations/000002_workspaces.up.sql:1`: `CREATE TABLE IF NOT EXISTS workspaces (...)`
- `pg_org_store.go:245,792`: `UPDATE workspaces SET org_id = NULL`
- `database.go:555`: `SELECT ... FROM workspaces w`
- No `workspace_metadata` table exists anywhere in the codebase or migrations

The design's D4 migration SQL says:
```sql
UPDATE workspace_metadata SET org_id = $2 WHERE user_id = $1 AND org_id IS NULL
```

This would fail at runtime. Every reference to `workspace_metadata` in the design must be `workspaces`.

**Impact:** Any implementer copy-pasting the migration SQL would get a syntax error. The design's S1 list filter query would also fail.

**Fix:** Replace all `workspace_metadata` with `workspaces` in 0031.

---

## S10 (IMPLEMENTATION BLOCKER): `CreateOrgWithAdmin` inserts into `org_key_members` — which D7 drops

**Verified in code:** `CreateOrgWithAdmin` (`pg_org_store.go`) runs a 3-statement transaction:
1. INSERT into `organizations`
2. INSERT into `org_memberships`
3. INSERT into `org_key_members` (the wrapped DEK)

D7 drops the `org_key_members` table. The function signature still takes `adminWrappedDEK []byte` and inserts into a table that won't exist.

**Impact:** Story 2 (org creation) cannot work until `CreateOrgWithAdmin` is rewritten. The function signature, the transaction body, and the caller (`OrgsHandler.Create`) all need to change. The caller currently derives a KEK from the password, generates a DEK, wraps it, and passes the wrapped DEK to this function — all of which is deleted by D7.

**Fix:** This is part of Story 1, not Story 2. The migration and the function rewrite must ship together. The design's Story 1 scope lists "Delete `OrgKeyService`, `org_key_members`" but doesn't explicitly list `CreateOrgWithAdmin` rewrite. Add it.

---

## S11 (IMPLEMENTATION BLOCKER): `AcceptInvitationTx` sets `pending_key_wrap` and inserts into the dropped column

**Verified in code:** `AcceptInvitationTx` (`pg_org_store.go`) does:
```go
pendingKeyWrap := role == types.OrgRoleAdmin
INSERT INTO org_memberships (org_id, user_id, role, pending_key_wrap, created_at) VALUES ($1, $2, $3, $4, NOW())
```

D7 drops the `pending_key_wrap` column. This INSERT fails.

**Impact:** Story 4 (workspace migration on join) depends on invitation acceptance working. But invitation acceptance itself is broken by D7's migration. The dependency is implicit but critical.

**Fix:** Story 1 must also update `AcceptInvitationTx` to remove the `pending_key_wrap` column reference. This is the same migration as D7 — the column drop and the code fix must ship atomically.

**Wider impact:** Every query that references `pending_key_wrap` (15+ references found in `pg_org_store.go`) must be updated in Story 1. This is a larger blast radius than the design acknowledges. Story 1 is underestimated.

---

## S12 (FUNCTIONAL GAP): Org deletion blocked by `OrgHasActiveWorkspaces` — but D4 says workspaces are always org-attributed

**Verified in code:** `OrgsHandler.Delete` calls `OrgHasActiveWorkspaces` which queries:
```sql
SELECT COUNT(*) FROM workspaces WHERE org_id = $1 AND deleted_at IS NULL
```

If any workspaces exist for the org, deletion returns 409 Conflict.

With D4, org members can only create org-attributed workspaces. So every org with any member activity will have active workspaces. The org admin **can never delete the org** without first deleting every workspace — which they can't do from the portal (D11 says no workspace management actions from the portal).

**Impact:** D9 (org self-delete) is effectively impossible. The Danger Zone UI would always show "organization has active workspaces."

**Options:**
1. **Remove the guard for soft delete.** Allow soft delete with active workspaces. Workspaces become frozen (consistent with F6). Pods keep running until Phase 5.
2. **Keep the guard, require workspace cleanup first.** But D11 says no workspace management from the portal — so the admin has no UI to delete workspaces. This is a dead end.
3. **Allow the admin to bulk-delete workspaces from the Danger Zone.** "Delete org and all workspaces" with multi-step confirmation.

**Recommendation:** Option 1. Remove the `OrgHasActiveWorkspaces` guard from the delete handler. Frozen workspaces are the documented consequence of org deletion (D4, F6, S1). The guard was written for a model where org deletion nulled `org_id` — that no longer happens.

---

## S13 (FUNCTIONAL GAP): The frontend has no way to know if the user is an org admin

**Verified in code:** The `User` type (`frontend/src/api/types.ts`) has:
```ts
interface User { id, username, email, role, active, createdAt }
```

The `role` field is the **platform** role (`"admin"` or `"user"`), set by `users.role` in the DB. There is no org role or org membership info in the `/auth/me` response.

D12 says the sidebar shows an org button for org admins. But the frontend has no data to make that decision. The `orgsApi.list()` call exists, but it's not called on app load — the sidebar would need to fetch org info to know whether to show the button.

**Impact:** Story 7 (sidebar org button) can't be implemented as described without either:
- Adding org info to `/auth/me` response
- Adding a `GET /users/me/org` endpoint (mentioned in S7/0034 but not wired to the frontend)
- Calling `orgsApi.list()` on every sidebar render (wasteful)

**Recommendation:** Extend `/auth/me` to include `orgRole` (or `orgId` + `orgRole`). The `GetUserOrgID` helper (S7) already provides the data server-side. One query, added to the `/auth/me` handler, gives the frontend everything it needs at app load. Update Story 2 or Story 7.

---

## S14 (INCONSISTENCY): Org admin can read all org workspaces via `IsOrgAdmin`, but ListWorkspaces only shows user-owned workspaces

**Verified in code:**
- `verifyOwner` grants org admins access to ANY org workspace (via `IsOrgAdmin`)
- `ListWorkspaces` filters by `user_id = $1` only — shows only the user's own workspaces
- `ListOrgWorkspaces` (`pg_org_store.go:286`) exists and lists all org workspaces — used by the portal's Workspaces tab

This is actually **correct and consistent** — the portal uses `ListOrgWorkspaces`, the sidebar uses `ListWorkspaces`. But the design's S1 fix adds an org membership filter to `ListWorkspaces` (the sidebar query). Let me verify that's still correct after S1:

After S1, `ListWorkspaces` returns workspaces where:
- `user_id = $1` AND
- (workspace has no org_id OR user is a current member of the workspace's org)

This means the sidebar shows the user's own workspaces that are still accessible. The portal shows ALL org workspaces via `ListOrgWorkspaces`. These are different views with different access paths. No inconsistency.

**No action needed.** Verified correct.

---

## S15 (MISSING): `OrgCredentialsHandler` constructor and `OrgsHandler` constructor both take `*OrgKeyService` — must change signature

**Verified in code:**
- `NewOrgCredentialsHandler(store, orgKeySvc *secrets.OrgKeyService, authSvc)` — takes OrgKeyService
- `NewOrgsHandler(store, orgKeySvc *secrets.OrgKeyService, dekCache, authSvc)` — takes OrgKeyService

With D7 deleting `OrgKeyService`, both constructor signatures break. They need:
- `NewOrgCredentialsHandler` — replace `orgKeySvc` with a `serverKeyDeriver` (or `deriveServerKey` function)
- `NewOrgsHandler` — remove `orgKeySvc` entirely (no DEK operations)

The design's deleted code list mentions "OrgKeyService wiring in app.go" but doesn't call out that the handler constructors change signature. All callers and tests that construct these handlers need updating.

**Impact:** Story 1 blast radius includes every test that constructs `OrgsHandler` or `OrgCredentialsHandler`. This is another indicator that Story 1 is larger than estimated.

---

## S16 (DESIGN QUESTION): Platform admin creating an org for themselves — they become both platform admin and org admin

**The problem:** D1 says platform admins create orgs. If a platform admin enters their own email as the owner, they become an org admin of the new org. Now they have two roles: platform admin (`users.role='admin'`) and org admin (`org_memberships.role='admin'`).

Is this intended? The admin settings page shows platform-admin tools. The sidebar shows an org button. The `/orgs/:slug` route works. The `/admin/*` routes work. No technical conflict.

But: the admin settings page is supposed to have an "Organisations" section (Story 8) for creating orgs. If the admin is also an org admin, do they see BOTH the "Create org" form (platform admin view) AND the sidebar org button (org admin view)?

**Assessment:** This is fine. The admin sees the org button (they're an org admin) AND the admin settings (they're a platform admin). These are different contexts with different routes. No action needed — just document it as expected behavior.

---

## Summary of Required Changes to 0031

| Finding | Severity | Change |
|---------|----------|--------|
| S9: `workspace_metadata` → `workspaces` | **Implementation blocker** | Replace all occurrences in design SQL. |
| S10: `CreateOrgWithAdmin` inserts into dropped table | **Implementation blocker** | Add to Story 1 scope. Rewrite function signature + body. |
| S11: `AcceptInvitationTx` references dropped column | **Implementation blocker** | Add to Story 1 scope. Update all 15+ `pending_key_wrap` references. Story 1 is underestimated. |
| S12: Org deletion blocked by active workspaces | **Functional gap** | Remove `OrgHasActiveWorkspaces` guard from delete handler. Add to Story 5 or 9. |
| S13: Frontend can't determine org admin status | **Functional gap** | Extend `/auth/me` to include org role. Update Story 2 or 7. |
| S14: ListWorkspaces vs ListOrgWorkspaces | Verified correct | No action. |
| S15: Handler constructor signatures break | **Underestimated scope** | Add to Story 1. All handler tests need updating. |
| S16: Admin creating org for self | Expected behavior | Document only. |

**Story 1 effort revision:** The design says 6h. With S10, S11, S15 included (function rewrites, 15+ query updates, constructor changes, test updates), realistically **8-10h**. This is the story that touches the most code and has the highest risk.
