# Worklog: Stories 7-9 — Sidebar Org Button + Settings Rework + Danger Zone

**Date:** 2026-06-18
**Session:** Implement Stories 7, 8, 9 from design 0031 (D1, D9, D11, D12) — frontend org UX.
**Status:** Complete — awaiting orchestrator review/merge

---

## Objective

- **Story 7 (D11, D12):** Org button in sidebar bottom tray (admins only). Replace OrgSettingsTab with MyOrganisationTab (read-only, members). Move admin org creation to an admin-only "Organisations" tab.
- **Story 8 (D1):** Admin settings → Organisations section with create form (owner email, name, slug, plan). Already implemented in Story 2's OrgSettingsTab update — now moved to admin-only tab.
- **Story 9 (D9):** Danger Zone section in portal Overview. Type-to-confirm org self-delete.

---

## Work Completed

### Story 7: Sidebar org button + Settings rework

- **`frontend/src/components/layout/Sidebar.tsx`** — Added `orgsApi.list()` query. If the user has an org and their role is "admin", shows a Building2 icon button in the bottom tray (between username and settings gear) linking to `/orgs/{orgId}`. Org members (non-admin) don't see the button — they access org info via "My Organisation" in Settings.
- **`frontend/src/components/settings/MyOrganisationTab.tsx`** (new) — read-only org info card for members. Shows org name, slug, role, member count, plan, status. "Manage organisation →" link for admins.
- **`frontend/src/pages/SettingsPage.tsx`** — renamed "organisations" tab → "my-organisation" (visible to all users, shows MyOrganisationTab). Added admin-only "Organisations" tab (shows OrgSettingsTab with the create form from Story 2).

### Story 8: Admin org management UI

The admin org creation form was already implemented in Story 2 (OrgSettingsTab with admin-only create button + ownerEmail + planId). In this story, it's now in the admin-only "Organisations" tab of Settings (separated from the member-facing "My Organisation" tab).

### Story 9: Danger Zone

- **`frontend/src/components/org-admin/DangerZone.tsx`** (new) — type-to-confirm org self-delete. Renders a red-bordered card in the portal Overview. The delete button is disabled until the user types the exact org name. Calls `orgsApi.delete(orgId)` then navigates to `/chat`.
- **`frontend/src/components/org-admin/OrgOverviewTab.tsx`** — added DangerZone for admins only (`isAdmin` from outlet context).

---

## Tests Run

- `npx tsc --noEmit` — PASS
- `npx vitest run` — PASS (106 files, 1103 tests)

---

## Files Modified

### New files
- `frontend/src/components/settings/MyOrganisationTab.tsx`
- `frontend/src/components/org-admin/DangerZone.tsx`

### Modified files
- `frontend/src/components/layout/Sidebar.tsx` — org button in bottom tray (D12)
- `frontend/src/pages/SettingsPage.tsx` — tab rework (D11)
- `frontend/src/components/org-admin/OrgOverviewTab.tsx` — DangerZone for admins (D9)

### Branch
`feat/epic43-0031-stories7-9-frontend-org-ux` (from `main`)
