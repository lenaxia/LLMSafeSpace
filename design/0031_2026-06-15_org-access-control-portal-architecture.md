# 0031: Org Access Control & Portal Architecture

**Date:** 2026-06-15
**Status:** Design — ready for implementation (reviewed per 0033)
**Supersedes:** Epic 43 W1 self-service flow (`design/stories/epic-43-organization-management/README.md:163-180`)
**Depends on:** Epic 43 Phases 1-4 (merged), Epic 11 (complete)
**Companion analysis:** `0032_2026-06-15_org-access-open-questions-analysis.md`, `0033_2026-06-15_design-review-consistency-pass.md`, `0034_2026-06-15_second-design-review.md`, `0035_2026-06-15_third-design-review.md`

---

## Motivation

Epic 43 shipped org management, billing, feature gating, and audit. But the access model assumed self-service org creation by any user. The product direction has changed:

- Individual users should **not** create or manage orgs
- Orgs are created by **platform admins** (any plan, manual billing) or via **paid subscription** (deferred — billing portal not yet built)
- The org admin panel needs to be a **standalone portal**, not buried in user settings
- The org admin panel should have **read-only workspace visibility** (no workspace management from the portal)
- We need a **reusable portal layout pattern** for future portals (billing, platform admin)

Additionally, tracing the org credential injection flow revealed a **latent production bug**: the org DEK is only cached on admin login (24h TTL), so org credentials silently stop injecting into member workspaces when no admin has logged in recently. This design eliminates the org DEK entirely.

---

## Decisions (all settled)

### D1: Org creation — platform admins only, direct creation

`POST /api/v1/orgs` rejects non-platform-admins with 403. Platform admins create orgs directly:

1. Admin enters owner email + org name + plan
2. `POST /api/v1/orgs { name, slug, ownerEmail, planId }` (admin-only)
3. Backend resolves email → user ID (one query, not a search endpoint). If not found → 404.
4. Org created (status=active, selected plan, sub=active). Owner added as admin.
5. Owner logs in, sees org button in sidebar. Done.

No entitlement table, no two-step flow, no owner action required. The org is just there when the owner next logs in. Since the org DEK is eliminated (D7), no password or key setup is needed at creation time.

Self-service subscription → org auto-creation is deferred to the billing portal epic.

### D2: Portal architecture — PortalLayout pattern

Extract a reusable `PortalLayout` component from the existing `OrgAdminLayout`. Future portals (billing, platform admin) adopt the same primitive. Each portal is a top-level route sibling under `RequireAuth`, not nested under `AppShell`.

```
/chat/*         → AppShell (sidebar + workspaces)
/settings/*     → AppShell (user settings — keeps sidebar)
/orgs/:slug/*   → PortalLayout (org admin — admin only)
/billing/*      → PortalLayout (billing — future)
/platform/*     → PortalLayout (platform admin — future, Phase 5)
```

### D3: Route rename — /admin/orgs/:slug → /orgs/:slug

The `/admin` namespace is reserved for platform admin (`/api/v1/admin/*`). Org admin is a separate context. Renamed to avoid mental-model confusion.

### D4: Workspace attribution — always org-attributed

When a user is in an org, all workspaces they create are org-attributed (`org_id` set automatically). Users cannot create personal workspaces while part of an org.

- **On join:** existing personal workspaces migrate to org-attributed (single `UPDATE workspaces SET org_id = $2 WHERE user_id = $1 AND org_id IS NULL`). After migration, org credentials are bound to the newly-attributed workspaces via `BindCredentialToAllOrgWorkspaces` (F7 in 0033 — without this, migrated workspaces don't receive org credentials until next credential reload).
- **On leave (admin offboarding):** org-attributed workspaces stay with the org (`org_id` unchanged). The user loses access via membership-gated check (D5).
- **On org deletion (soft):** `org_id` is NOT nulled (F6 in 0033 — changed from current behavior). Workspaces remain org-attributed but the org is soft-deleted. `IsOrgMember` returns false (checks `deleted_at IS NULL`) → workspaces are frozen (no access by anyone). This prevents former members from walking away with org workspaces as personal workspaces.
- **For non-org users:** all workspaces are personal (`org_id` null). Backend rejects `org_id` on create.

### D5: Membership-gated creator access

`verifyOwner` gains one check: for org-attributed workspaces, the creator must be a **current** org member. Offboarded users lose access automatically — no account suspension needed.

```go
if meta.UserID == userID {
    if meta.OrgID != nil && *meta.OrgID != "" {
        isMember, _ := orgStore.IsOrgMember(ctx, *meta.OrgID, userID)
        if !isMember {
            return ForbiddenError  // creator left the org
        }
    }
    return nil
}
```

Personal workspaces (no `org_id`) are unaffected — creator always has access.

### D6: No self-removal from org

Users cannot remove themselves from an org. Only org admins can remove members (offboarding). `RemoveMember` rejects if `targetUserID == callerUserID`.

Removed members keep their account, personal workspaces (none if they were in an org — D4), and personal credentials. Their org-attributed workspaces stay with the org, frozen until a workspace transfer feature is built (future).

### D7: Eliminate org DEK — server KEK for org credentials

Org credentials (LLM provider keys) are encrypted with `deriveServerKey("org-credentials")` — a purpose-scoped server KEK separate from the admin credential key (`"provider-credentials"`). The org DEK, `OrgKeyService`, `OrgAwareKeyService`, `org_key_members` table, `pendingKeyWrap` column, and `accept-key`/`rotate-key` endpoints are **deleted entirely**.

**Why:** The org DEK only protected org credentials (user secrets use the user DEK). The cache-based model was broken — org credentials silently stopped injecting after 24h without admin login. Server KEK is always available. Separate label from admin credentials maintains domain separation (F4 in 0033).

**What's deleted (~1000 lines):**
- `pkg/secrets/org_key_service.go`
- `pkg/secrets/org_aware_key_service.go`
- `org_key_members` table (migration to drop)
- `pending_key_wrap` column (migration to drop)
- `POST /orgs/:id/accept-key` endpoint
- `POST /orgs/:id/rotate-key` endpoint
- Key-setup banner UI
- `UnlockAllOrgDEKs` calls in auth login
- `RewrapAllOrgDEKsForAdmin` call in password change handler (F10 in 0033)
- `OrgAwareKeyService` wrapper in app.go — base `KeyService` wired directly (F11 in 0033)

**What changes:**
- `OrgCredentialsHandler.Create` uses `deriveServerKey("org-credentials")` instead of `GetOrgDEK`
- `decryptBinding` for `owner_type='org'` uses server KEK (label `"org-credentials"`) instead of org DEK
- `injection.go` derives both `"provider-credentials"` (admin) and `"org-credentials"` (org) keys upfront when respective bindings are present (F4 in 0033)
- `IsOrgAdmin` query drops `pending_key_wrap = false` check — admins have immediate access (F5 in 0033). This is correct: with no DEK, there's nothing to "set up."
- Org credentials always inject — the "set up once, all members get it" use case works reliably

**No existing data to migrate:** No orgs exist (user confirmed). All new org credentials are server-KEK-encrypted from the start (F3 in 0033).

### D8: Single-org enforcement

One membership per user, enforced at schema level:

```sql
CREATE UNIQUE INDEX idx_org_memberships_single_user ON org_memberships(user_id);
```

No existing orgs exist (not a production system), so no migration conflict.

### D9: Org deletion — self-delete (soft)

Org admins can delete their own org via a Danger Zone section in the portal (type-to-confirm). Soft delete only (modified `SoftDeleteOrg` — sets `deleted_at`, does NOT null `workspaces.org_id`). Workspaces remain org-attributed and become frozen — `IsOrgMember` returns false (checks `deleted_at IS NULL`), so no one can access them via the API. This prevents former members from walking away with org workspaces as personal workspaces. Phase 5 adds suspension as a less-destructive alternative.

### D10: Invitation model — email only, no user search

Admins submit bulk emails. All emails get invitations uniformly. Recipients must authenticate and accept. The system never reveals which emails have existing accounts (no enumeration). Existing users and new users go through the same flow.

**Bulk email infrastructure:** the current synchronous loop with per-hour cap (50/hr) is sufficient for now. When batch sizes exceed ~50, add a fire-and-forget goroutine with rate limiting. A Redis-backed queue is deferred to enterprise scale.

### D11: PortalLayout keeps Workspaces tab (read-only)

The org admin portal shows a read-only list of org workspaces (who owns each, status, last activity). No create/delete/session actions from the portal — those stay in the chat sidebar for the workspace owner. API-level read-only enforcement is deferred (UI-level read-only is sufficient for now).

### D12: Sidebar org button

The sidebar bottom tray shows an org button (between username and settings gear) for org admins only. Org members (non-admin) do not see the button — they access read-only org info via a "My Organisation" section in Settings.

### D13: Account model — independent accounts

Accounts are independent (Model A). Users own their accounts. Orgs have memberships. Joining an org doesn't transform the account; leaving doesn't delete it. Personal credentials persist regardless of org membership. This is consistent with GitHub/Slack patterns.

---

## Scope Changes from Epic 43

| Area | Epic 43 design | This design |
|------|----------------|-------------|
| Org creation | Any user, self-service via Stripe | Platform admins only, direct creation with ownerEmail (D1) |
| Org credential encryption | Per-admin wrapped DEK | Server KEK (D7) |
| `pendingKeyWrap` flow | Required for all admins | Eliminated (D7) |
| Settings tab | "Organisations" — create/list/delete | Removed. "My Organisation" (read-only, members only) |
| Org admin panel route | `/admin/orgs/:slug` | `/orgs/:slug` (D3) |
| Org admin panel access | From Settings tab | Sidebar org button (D12) |
| Workspace attribution | Optional `org_id` | Always org-attributed for org members (D4) |
| Workspace access | Creator always | Creator + current membership (D5) |
| Org removal | Admin or self | Admin only, no self-removal (D6) |
| Org DEK | Per-org key, admin-wrapped | Eliminated (D7) |
| User search | N/A | Not built — invitations use email only (D10) |

---

## Portal Architecture

### PortalLayout component

`frontend/src/components/layout/PortalLayout.tsx` — extracted from `OrgAdminLayout`:

```tsx
interface PortalLayoutProps {
  title: string
  backLink: string
  backLabel?: string
  badges?: React.ReactNode
  navItems: NavItem[]
  context: unknown
}
```

Structure:
```
┌─────────────────────────────────────────────┐
│ [← Back] | Title [badges]    [meta info]    │
├──────────┬──────────────────────────────────┤
│ Nav      │                                  │
│ Item 1   │         <Outlet />               │
│ Item 2   │                                  │
│ Item 3   │                                  │
└──────────┴──────────────────────────────────┘
```

- Full-screen (no AppShell/Sidebar)
- Not wrapped in `SessionActivityProvider`
- Responsive: nav collapses to horizontal tab bar on mobile

### PortalLayout consumers

| Portal | Route | Layout | Access |
|--------|-------|--------|--------|
| Org admin | `/orgs/:slug` | PortalLayout | Org admins only |
| Billing | `/billing` | PortalLayout | Authenticated users (future) |
| Platform admin | `/platform` | PortalLayout | Platform admins (future, Phase 5) |

---

## User Flows

### Flow 1: Platform admin creates org

```
1. Platform admin → Admin Settings → Organisations → "New Organisation"
2. Enters: owner email, org name (default: "<ownerUsername>'s Org"), slug (auto), plan dropdown
3. POST /api/v1/orgs { name, slug, ownerEmail, planId }
   - Backend resolves ownerEmail → userId (404 if user doesn't exist)
   - Creates org (status=active, selected plan, sub=active)
   - Adds owner as admin (no pendingKeyWrap — D7 eliminated it)
   - No password needed (no org DEK — D7)
4. Org created. Owner receives email notification.

Owner accesses org:
5. Owner logs in → sidebar shows org button
6. Clicks → /orgs/:slug (PortalLayout)
7. Full admin access immediately — no key setup needed
```

### Flow 2: Org admin manages their org

```
1. Org admin logs in
2. Sidebar bottom tray shows org button
3. Clicks → /orgs/:slug (PortalLayout)
4. Tabs: Overview, Members, Credentials, Workspaces (read-only), Audit, Billing
5. No workspace management actions — workspaces managed from /chat sidebar
```

### Flow 3: Org admin adds members (invitations)

```
1. Org admin → portal → Members tab
2. Enters email addresses (bulk) + role (admin/member)
3. POST /orgs/:id/invitations { emails, role }
   - All emails get invitations (uniform — no enumeration)
   - Rate limited: 50/hr per org
   - Each email gets a token-based invitation link
4. Recipients click link → see org name, inviter, role
5. Must authenticate (register or login) → accept
6. On accept:
   - Membership created
   - Personal workspaces migrated to org-attributed (D4)
   - Org credentials immediately available (server KEK — D7)
7. New admin sees sidebar org button
```

### Flow 4: Org admin offboards a member

```
1. Org admin → portal → Members tab → Remove
2. DELETE /orgs/:id/members/:userID
   - Rejects if targetUserID == callerUserID (no self-removal — D6)
   - Removes membership
3. Offboarded user:
   - Loses access to org workspaces (membership-gated check — D5)
   - Org-attributed workspaces disappear from sidebar (ListWorkspaces filters by membership — S1)
   - Org-attributed workspaces stay with org (frozen, org admin read-only via IsOrgAdmin)
   - Account persists, can still log in
   - Personal credentials persist
   - Workspace pods keep running until manually stopped or Phase 5 suspension (S4)
```

### Flow 5: Org admin deletes org

```
1. Org admin → portal → Overview → Danger Zone
2. Types org name to confirm
3. DELETE /orgs/:id
   - Soft delete (sets deleted_at)
   - workspaces.org_id is NOT nulled (F6 — workspaces stay org-attributed, become frozen)
   - IsOrgMember returns false for all members (org has deleted_at set)
   - Frozen workspaces disappear from members' sidebars (ListWorkspaces filters by membership — S1)
   - Workspace pods keep running until manually stopped or Phase 5 suspension (S4)
4. Org button disappears from all members' sidebars
```

### Flow 6: Org member (non-admin)

```
1. Member logs in
2. No org button in sidebar (members don't access the portal — D13)
3. Settings → "My Organisation" (read-only): org name, plan, member count
4. Workspaces created with org_id automatic (D4)
5. Cannot see other members' workspaces (D6 visibility — admin only)
```

### Flow 7: Individual user (no org)

```
1. User logs in
2. No org button, no org settings tab
3. Settings: Preferences, Provider Keys, Secrets, API Keys
4. Workspaces are personal (org_id null)
5. POST /api/v1/orgs → 403 (D1)
```

---

## Backend Changes

### Schema migrations

```sql
-- Migration: drop org DEK infrastructure (D7)
DROP TABLE IF EXISTS org_key_members;
ALTER TABLE org_memberships DROP COLUMN IF EXISTS pending_key_wrap;

-- Migration: single-org enforcement (D8)
CREATE UNIQUE INDEX idx_org_memberships_single_user ON org_memberships(user_id);

-- Migration: SoftDeleteOrg no longer nulls workspace org_id (F6 in 0033)
-- (No schema change — the UPDATE workspaces SET org_id = NULL line is removed
-- from the SoftDeleteOrg function. Workspaces keep org_id after org deletion.)
```

### Endpoint changes

| Endpoint | Change |
|----------|--------|
| `POST /api/v1/orgs` | Non-admin → 403. Admin provides `ownerEmail` (resolved to user ID server-side, 404 if not found). `Password` field removed (no DEK). |
| `GET /api/v1/auth/me` | Extend response to include `orgId` and `orgRole` (or null if no org). Frontend uses this to show/hide the sidebar org button. S13 in 0035. |
| `POST /api/v1/orgs/:id/accept-key` | **Deleted** (D7). |
| `POST /api/v1/orgs/:id/rotate-key` | **Deleted** (D7). |
| `DELETE /api/v1/orgs/:id/members/:userID` | Reject self-removal (D6). |
| `OrgCredentialsHandler.Create` | Use `deriveServerKey("org-credentials")` instead of `GetOrgDEK` (D7). |
| `OrgCredentialsHandler.Update` | Same server KEK change (D7). |
| `decryptBinding` (org branch) | Use server KEK (`"org-credentials"` label) instead of org DEK (D7). |
| `injection.go` | Derive `"org-credentials"` key when org bindings are present (F4 in 0033). |
| `IsOrgAdmin` | Drop `pending_key_wrap = false` from query (F5 in 0033). |
| `verifyOwner` | Add membership-gate for org workspaces (D5). |
| `ListWorkspaces` | Filter out frozen workspaces — add org membership check to the query so offboarded/deleted-org users don't see inaccessible workspaces (S1 in 0034). |
| `CreateWorkspace` | Enforce org attribution for org members (D4). Requires `GetUserOrgID` helper. |
| `InvitationsHandler.Accept` | Migrate personal workspaces to org + bind org credentials (D4, F7 in 0033). Also check cross-org membership before insert — reject with 409 if user is already in another org (S3 in 0034). |
| `OrgsHandler.Delete` | Remove `OrgHasActiveWorkspaces` guard — with D4 (always org-attributed) every active org has workspaces, making deletion impossible. Workspaces become frozen instead (S12 in 0035). |
| `SoftDeleteOrg` | Remove `UPDATE workspaces SET org_id = NULL` (F6 in 0033). |
| `RotateKeyHandler.ChangePassword` | Remove `RewrapAllOrgDEKsForAdmin` call (F10 in 0033). |
| `app.go` | Wire base `KeyService` directly instead of `OrgAwareKeyService` (F11 in 0033). |

### New helper functions

- `GetUserOrgID(ctx, userID) (string, error)` — returns the user's single org ID (or empty string). Used by `CreateWorkspace` (auto-attribute), invitation acceptance (cross-org check), and frontend API (`GET /users/me/org` for sidebar button visibility). S7 in 0034.

### Deleted code

- `pkg/secrets/org_key_service.go` — entire file
- `pkg/secrets/org_aware_key_service.go` — entire file
- `pkg/secrets/org_key_service_test.go` — entire file
- `UnlockAllOrgDEKs` calls in auth login
- `OrgKeyService` wiring in `app.go`
- Key-setup UI (banner, accept-key form)
- `org_key_members` table usage

---

## Frontend Changes

### New files
- `frontend/src/components/layout/PortalLayout.tsx` — reusable portal layout
- `frontend/src/components/settings/MyOrganisationTab.tsx` — read-only member view
- `frontend/src/components/admin/AdminOrganisationsTab.tsx` — platform admin org creation
- `frontend/src/components/org-admin/DangerZone.tsx` — org self-delete with type-to-confirm

### Modified files
- `frontend/src/router.tsx` — rename route `/admin/orgs/:slug` → `/orgs/:slug`
- `frontend/src/pages/SettingsPage.tsx` — replace "organisations" tab with "my-organisation" (conditional)
- `frontend/src/components/org-admin/OrgAdminLayout.tsx` — refactor to use PortalLayout, keep Workspaces tab
- `frontend/src/components/layout/Sidebar.tsx` — add org button to bottom tray
- `frontend/src/api/orgs.ts` — remove accept-key/rotate-key, add ownerEmail to create

### Deleted files
- `frontend/src/components/settings/OrgSettingsTab.tsx` — replaced by MyOrganisationTab + AdminOrganisationsTab

---

## Implementation Stories

### Story 1: Eliminate org DEK (D7)
Backend-only. Migrate org credentials to server KEK (`"org-credentials"` label). Delete `OrgKeyService`, `OrgAwareKeyService`, `org_key_members`, `pendingKeyWrap`. Update: `decryptBinding`, `OrgCredentialsHandler.Create/Update`, `injection.go`, `IsOrgAdmin`, `IsOrgMember`, `RotateKeyHandler.ChangePassword`, `app.go` wiring, **`CreateOrgWithAdmin`** (remove `adminWrappedDEK` param + `org_key_members` insert), **`AcceptInvitationTx`** (remove `pending_key_wrap` from INSERT), **all 15+ queries referencing `pending_key_wrap`** in `pg_org_store.go`, **`NewOrgsHandler` and `NewOrgCredentialsHandler` constructor signatures** (remove `*OrgKeyService` param, add server key deriver), **all handler tests** that construct these handlers.

**Effort:** 10h (revised up from 6h — S10, S11, S15 in 0035 revealed larger blast radius)
**Risk:** Touches credential injection path, org creation, invitation acceptance, password change. Requires migration to drop table/column. Every query touching `pending_key_wrap` and every handler/test touching `OrgKeyService` must be updated atomically.
**Verification:** Org credentials inject reliably without admin login (the bug fix). Existing credential CRUD tests pass. `IsOrgAdmin` works without `pending_key_wrap`. `CreateOrgWithAdmin` works without `org_key_members`. Invitation acceptance works without `pending_key_wrap`. Password change doesn't call org key rewrap.

### Story 2: Restrict org creation + email resolution (D1)
Backend. `POST /api/v1/orgs` → 403 for non-admins. Admin provides `ownerEmail` (resolved to user ID, 404 if not found). Remove `Password` from `CreateOrgRequest`.

**Effort:** 3h
**Depends on:** Story 1 (no password/KEK at creation)
**Verification:** Non-admin → 403. Admin with valid email → org created, owner added as admin. Admin with unknown email → 404.

### Story 3: Single-org enforcement + no self-removal + cross-org invitation check (D6, D8, S3)
Backend. Migration adds unique index on `org_memberships(user_id)`. `RemoveMember` rejects self-removal. Invitation acceptance checks cross-org membership before insert (rejects with clear 409 if user is already in another org). Add `GetUserOrgID` helper (S7).

**Effort:** 3h
**Verification:** Second membership rejected at the DB level. Self-removal rejected. Invitation acceptance by user already in another org → clear 409 (not a DB constraint violation).

### Story 4: Workspace attribution + migration on join (D4, F7)
Backend. `CreateWorkspace` enforces org attribution. `InvitationsHandler.Accept` migrates personal workspaces AND binds org credentials to them.

**Effort:** 4h
**Depends on:** Story 3 (single-org)
**Verification:** Org member creates workspace → org_id set. Non-org user → org_id null. On join → personal workspaces migrated + org credentials bound.

### Story 5: Membership-gated creator access + workspace list filter + SoftDeleteOrg fix + delete guard removal (D5, F6, S1, S12)
Backend. `verifyOwner` gains membership check for org workspaces. `ListWorkspaces` filters out workspaces where the user is no longer an org member (frozen workspaces disappear from sidebar). `SoftDeleteOrg` stops nulling workspace org_id. `OrgsHandler.Delete` removes the `OrgHasActiveWorkspaces` guard (with always-org-attributed workspaces, the guard makes deletion impossible).

**Effort:** 4h
**Depends on:** Story 4
**Verification:** Offboarded user cannot access org workspaces. Offboarded user does not see org workspaces in sidebar list. Creator who is still a member can access + see. Personal workspace creator always can. Org deletion succeeds even with active workspaces (they become frozen, not converted to personal, not visible in sidebar).

### Story 6: PortalLayout + route rename (D2, D3)
Frontend. Extract PortalLayout from OrgAdminLayout. Rename route `/admin/orgs/:slug` → `/orgs/:slug`.

**Effort:** 4h
**Verification:** Portal layout renders. Route works. Old route redirects or 404s.

### Story 7: Sidebar org button + Settings tab rework (D11, D12)
Frontend. Org button in sidebar bottom tray (admins only). "My Organisation" read-only tab (members). Remove OrgSettingsTab.

**Effort:** 4h
**Depends on:** Story 6
**Verification:** Admin sees org button. Member sees My Organisation tab. Non-org user sees neither.

### Story 8: Admin org management UI (D1)
Frontend. Admin settings → Organisations section. Create form: owner email, name, slug, plan dropdown.

**Effort:** 3h
**Depends on:** Story 2
**Verification:** Admin enters email + plan → org created. Owner appears in org.

### Story 9: Danger Zone — org self-delete (D9)
Frontend. Danger Zone section in portal Overview. Type-to-confirm.

**Effort:** 2h
**Depends on:** Story 6
**Verification:** Type wrong name → blocked. Type correct name → soft delete. Workspaces frozen (not converted to personal).

### Story 10: Bulk email improvements (D10)
Backend. Fire-and-forget goroutine with rate limiting for large batches.

**Effort:** 3h
**Deferred:** Can ship after Stories 1-9. Current synchronous loop works for small orgs.
**Verification:** 50+ emails don't block the API response. Rate limited per SES limits.

---

## Total Effort

| Stories | Effort |
|---------|--------|
| Stories 1-9 (core) | 31h |
| Story 10 (deferred) | 3h |
| **Total** | **34h** |

After review (0033 + 0034 + 0035): Story 1 revised from 6h to 10h (S10/S11/S15 — larger blast radius). Entitlement model removed (-7h). SoftDeleteOrg fix, credential re-seeding, workspace list filter, cross-org invitation check, GetUserOrgID helper, delete guard removal, /auth/me extension added. Revised core: **~31h**.

---

## Deferred to Future Epics

- **Workspace transfer** — reassigning frozen workspaces from departed members to active members (D7 in Epic 43 deferred this; this design leaves workspaces frozen until it's built)
- **Self-service subscription** — billing portal → Stripe checkout → org auto-creation
- **API-level read-only workspace enforcement** — org admins can technically write to org workspaces via API today
- **LDAP/POSIX import** — produces email lists for the existing invitation flow
- **Phase 5 Platform Operations** — admin dashboard, org/user suspension, cross-org audit (US-43.18-43.20)
- **Sub-orgs/teams** — flat membership for now (Epic 43 D6 deferred this)
