# Worklog: Code-split the /admin portal via React.lazy

**Date:** 2026-06-23
**Session:** Lazy-load the platform-admin portal (layout + 6 sections) with `React.lazy` so the admin bundles are never downloaded by users who don't open the portal.
**Status:** Complete

---

## Objective

Move the `/admin` portal to `React.lazy` so its bundles are code-split out of the main entry. Non-admin users (and admins who never open the portal) should not download the admin code (tables, relay dashboard, settings forms, etc).

This was the follow-up optimisation noted in worklog `0516`.

---

## Work Completed

### PortalLayout — Suspense boundary around the Outlet
- `frontend/src/components/layout/PortalLayout.tsx` — wrapped `<Outlet>` in `<Suspense>` with a centred `<Spinner size="lg" />` fallback. This is the necessary plumbing for any lazy route child: without a Suspense boundary around the Outlet, a suspending child would bubble up and replace the entire portal (nav + header + content) with a fallback, hiding the navigation while a section loads.
- This is a shared component (used by both org-admin and platform-admin portals). For non-lazy children (org-admin tabs today) `<Suspense>` is a no-op — it renders children immediately and never shows the fallback. So it is safe for the org-admin portal.

### router.tsx — React.lazy for the 7 admin modules
- Converted the 7 `/admin` imports to `React.lazy` (named exports → `.then((m) => ({ default: m.X }))`):
  - `PlatformAdminLayout` (the portal shell)
  - `PlatformUsersTab`, `OrgSettingsTab`, `AdminProviderCredentialsTab`, `RelayTab`, `AdminSettingsPage`, `PlatformAuditTab`
- Wrapped the `/admin` route `element` in a route-level `<Suspense>` (`portalFallback`, a full-screen spinner mirroring `RequireAuth`'s loading state). This covers the **layout** chunk's initial load; PortalLayout's internal Suspense covers each **section** chunk.

### Tests (TDD)
- `frontend/src/components/layout/PortalLayout.test.tsx` — added 2 tests written first (red → green):
  - renders the loading fallback in the content area while a child suspends, AND keeps the portal nav visible (proves the Suspense boundary is local to the Outlet, not the whole layout);
  - renders the child content once the lazy promise resolves (fallback disappears).
- Verified the existing `PlatformAdminLayout.test.tsx` (renders the layout directly, not via the router) is unaffected by router-level lazy.

---

## Key Decisions

- **React.lazy over React Router's `lazy` route property.** The user explicitly asked for `React.lazy`. React Router v6.4's native `lazy` (returns `{ Component }`, Router-managed Suspense) would avoid manual `<Suspense>` placement, but `React.lazy` + `<Suspense>` is the requested, well-understood pattern.
- **Two Suspense layers (route + Outlet).** Route-level Suspense covers the layout chunk (full-screen spinner, since the nav doesn't exist yet); PortalLayout's Outlet Suspense covers section chunks (content-area spinner, nav stays visible). This gives correct UX at each granularity.
- **PortalLayout Suspense change is shared, not admin-only.** It is the correct plumbing for lazy children in ANY portal. A scoped Suspense only in `PlatformAdminLayout` is not possible without duplicating the Outlet rendering that `PortalLayout` owns. The change is a no-op for the eager org-admin children.
- **Per-section chunks, not one admin mega-chunk.** Each of the 6 sections is its own lazy chunk, so an admin visiting `/admin/users` does not pull the relay dashboard or audit table. Verified in the build output.
- **No error boundary added.** A chunk-load failure (network) makes `React.lazy` throw; the existing top-level `<ErrorBoundary>` in `App.tsx` already wraps the entire router and catches it. No new boundary needed.

---

## Assumptions (validated)

1. The 7 admin modules are imported only from `router.tsx` in production → verified via grep (other references are test files that import directly and are unaffected). ✓
2. `<Suspense>` with non-lazy children is a no-op → React semantics; the org-admin test suite (which uses PortalLayout) passes unchanged (full suite green). ✓
3. The top-level app has an error boundary to catch chunk-load failures → confirmed `<ErrorBoundary>` in `App.tsx:20` wraps `<RouterProvider>`. ✓
4. `React.lazy` works with named exports via `.then(m => ({ default: m.X }))` → standard pattern; typecheck + build pass. ✓
5. `createBrowserRouter` accepts a `<Suspense>`-wrapped element → `element` is a `ReactNode`; verified by successful build + tests. ✓

---

## Blockers

None.

---

## Tests Run

- `npm run typecheck` — PASS.
- `npm run lint` — PASS.
- `npx vitest run` (full frontend suite) — 117 files / 1256 tests PASS.
- `npm run build` — PASS. Build output confirms 7 separate admin chunks emitted:
  - `PlatformAdminLayout` (0.80 kB), `AdminSettingsPage` (2.55 kB), `PlatformAuditTab` (4.23 kB), `PlatformUsersTab` (4.26 kB), `OrgSettingsTab` (7.48 kB), `AdminProviderCredentialsTab` (15.41 kB), `RelayTab` (18.40 kB) — ~52 kB raw / ~20 kB gzip total, now entirely out of the entry bundle.

---

## Next Steps

None required. Optional (separate PR): apply the same `React.lazy` split to the `/orgs/:id` org-admin portal, which currently eager-loads its tab components.

---

## Files Modified

- `frontend/src/router.tsx`
- `frontend/src/components/layout/PortalLayout.tsx`
- `frontend/src/components/layout/PortalLayout.test.tsx`
