# Worklog: Migrate Platform Admin Portal out of Settings

**Date:** 2026-06-22
**Session:** Moved the platform-admin functionality out of the personal Settings menu into a dedicated `/admin` portal, mirroring the org-admin portal (`/orgs/:id`).
**Status:** Complete

---

## Objective

Migrate the platform admin portal out of the Settings menu and into its own portal similar to the org admin portal. The six admin-only tabs previously embedded in `SettingsPage` (Platform Credentials, Organisations, Users, Platform Audit, Relay, Instance Settings) were to become sections of a standalone `/admin` portal built on the existing `PortalLayout`, reachable from the sidebar.

---

## Work Completed

### Created `PlatformAdminLayout` (mirrors `OrgAdminLayout`)
- `frontend/src/components/platform-admin/PlatformAdminLayout.tsx` — new layout. Renders `PortalLayout` titled "Platform Administration" with a back link to `/chat` and six nav sections (Users, Organisations, Credentials, Relay, Settings, Audit). Guards on `user.role === "admin"`; non-admins get an access-denied screen with a back-to-chat link (mirrors `OrgAdminLayout`'s 403/404 error UI). No async fetch needed — role is read from the auth context (unlike org-admin which fetches the org).
- `frontend/src/components/platform-admin/PlatformAdminLayout.test.tsx` — TDD test (written first): title render, back link, all six nav items, active section content, access-denied for non-admin, access-denied for null user. Uses `vi.hoisted` for a mutable auth mock to cover multiple roles in one file.

### Wired the `/admin` route (`frontend/src/router.tsx`)
- Added `/admin` as a sibling of `/orgs/:id` inside `RequireAuth` (full-screen portal, NOT inside `AppShell` — identical topology to the org-admin portal). Index redirects to `users`. Child routes map to the existing prop-less tab components (`PlatformUsersTab`, `OrgSettingsTab`, `AdminProviderCredentialsTab`, `RelayTab`, `AdminSettingsPage`, `PlatformAuditTab`).

### Removed admin tabs from `SettingsPage` (`frontend/src/pages/SettingsPage.tsx`)
- Stripped the six `adminOnly` tabs and their conditional renders. Removed the now-unused `useAuth`/`isAdmin` filtering and the imports for `AdminSettingsPage`, `AdminProviderCredentialsTab`, `OrgSettingsTab`, `PlatformUsersTab`, `PlatformAuditTab`, `RelayTab`. Settings now holds only personal tabs: Preferences, Provider Keys, Secrets, API Keys, My Organisation.

### Added sidebar entry (`frontend/src/components/layout/Sidebar.tsx`)
- Added a `Shield` icon button in the footer icon group (before the org-admin `Building2` button), shown only when `user?.role === "admin"`, navigating to `/admin`. Mirrors the existing org-admin entry pattern.

### Updated tests
- `frontend/src/pages/SettingsPage.test.tsx` — rewritten to assert only personal tabs render and that platform-admin tabs are absent. Removed the two `AdminSettingsPage`-specific tests (they tested SettingsPage's admin sub-rendering, which no longer exists).
- `frontend/src/pages/AdminSettingsPage.test.tsx` — received the two moved tests (404 "Admin access required" path, and save-error toast), adapted to this file's mock schema (toggle "Registration" switch instead of "Debug Mode"). Preserves coverage in the correct home.
- `frontend/tests/e2e/relay-admin.spec.ts` — all relay tests now navigate directly to `/admin/relay` instead of `/settings` + clicking a "Relay" button. The non-admin test asserts the access-denied screen (stronger than the old "button not visible" assertion).

---

## Key Decisions

- **Full-screen portal, not inside AppShell.** Placed `/admin` as a sibling of `/orgs/:id` (both inside `RequireAuth`, outside `AppShell`). This is identical to the org-admin portal topology and matches "similar to the org admin portal." A portal is a focused administrative surface; the chat sidebar is not relevant there.
- **Index redirects to `users`.** No fabricated "overview" tab (no platform-wide stats API exists, and inventing one would be scope creep beyond the migration). Redirecting to the most actionable section is the faithful, non-over-engineered choice.
- **Kept tab components in `components/settings/`.** Moving 6 components + their tests into `components/platform-admin/` would be large churn for zero functional benefit. They are reusable, prop-less sections referenced by the router. Only the new `PlatformAdminLayout` lives in `components/platform-admin/`.
- **`Shield` icon for the sidebar entry.** Semantically distinct from the org-admin `Building2`; clearly signals platform-level administration.
- **Access guard is client-side role check.** `PlatformAdminLayout` checks `user.role !== "admin"`. The backend already enforces admin on every `/admin/*` and `/api/v1/admin/*` route, so the client guard is UX, not security.

---

## Assumptions (validated)

1. The six admin tab components take no props and are self-contained → verified via grep on their `export function` signatures; all are `()`-arity. ✓ (router renders them directly as route elements)
2. `RequireAuth` guarantees a non-null `user` before rendering the portal → verified in `router.tsx:26-31` (returns `<Outlet/>` only when `!loading && user`). PlatformAdminLayout's `!user` branch is therefore defensive. ✓
3. `PortalLayout` relative `NavLink` resolution works for nested `/admin/*` routes → proven by the shipped org-admin portal (`/orgs/:id/*`) which uses the identical relative-link pattern. ✓
4. No other UI surface linked to the settings admin tabs → verified via repo-wide grep for `platform-credentials|admin-orgs|admin-users|platform-audit` (only `SettingsPage.tsx` + tests). The only e2e consumer was `relay-admin.spec.ts` (updated). ✓
5. Playwright route precedence: last-registered handler wins → the non-admin e2e test overrides `/auth/me` to `role:"user"` after the `beforeEach` admin mock; the override fulfills first. This is the same override pattern the original test used. ✓

---

## Blockers

None.

---

## Tests Run

- `npm run typecheck` (frontend) — PASS, no errors.
- `npx vitest run src/components/platform-admin/ src/pages/SettingsPage.test.tsx src/pages/AdminSettingsPage.test.tsx src/components/layout/PortalLayout.test.tsx` — 4 files, 35 tests, PASS.
- `npm run test` (full frontend unit suite) — 117 files, 1254 tests, PASS.
- `npm run lint` (eslint) — PASS, no errors.
- `npm run build` (`tsc -b && vite build`) — PASS (chunk-size warnings are pre-existing shiki grammar bundles, unrelated).
- Playwright e2e (`relay-admin.spec.ts`) — updated to new routes; not executed locally (requires running dev server + browser binaries, run in CI).

---

## Next Steps

- Optional: add a brief note to `README-LLM.md` documenting the `/admin` portal (currently the README only documents the org-admin portal). Not required — the README does not document the old settings admin tabs either, so nothing is now stale.
- Consider code-splitting the `/admin` portal with `React.lazy` so the admin bundles don't load for regular users (currently all admin tab components are eagerly imported in `router.tsx`). Separate optimization, not part of this migration.

---

## Files Modified

- `frontend/src/components/platform-admin/PlatformAdminLayout.tsx` (created)
- `frontend/src/components/platform-admin/PlatformAdminLayout.test.tsx` (created)
- `frontend/src/router.tsx`
- `frontend/src/pages/SettingsPage.tsx`
- `frontend/src/pages/SettingsPage.test.tsx`
- `frontend/src/pages/AdminSettingsPage.test.tsx`
- `frontend/src/components/layout/Sidebar.tsx`
- `frontend/tests/e2e/relay-admin.spec.ts`
- `frontend/tsconfig.tsbuildinfo` (regenerated build cache)
- `worklogs/0516_2026-06-22_migrate-platform-admin-portal.md` (this file; NNNN replaced at merge)
