# Worklog 0453 — Frontend: Mobile-Friendly Collapsible Sidebar in Org Admin Portal

**Date:** 2026-06-20
**Session:** Made the org management portal (`/orgs/:id`) mobile-friendly by extracting the chat page's collapsible-sidebar machinery into shared units and wiring both layouts to them. Also fixed two pre-existing lint errors surfaced during validation.
**Status:** Complete

---

## Objective

The org admin portal rendered a hard-coded 192px left sidebar with no collapse behaviour on mobile. The chat page (`AppShell`) had a full swipe + overlay + hamburger drawer, but none of that machinery was reused — `PortalLayout` reimplemented the layout chrome from scratch. Fix the mobile UX gap AND the underlying component-reuse gap in a single change.

---

## Work Completed

### Root cause

`frontend/src/router.tsx` defines `/orgs/:id` → `OrgAdminLayout` → `PortalLayout` as a **sibling** of the `AppShell` subtree, not nested under it. `PortalLayout` rendered `<nav className="w-48 ...">` with zero responsive logic. `PortalLayout.test.tsx` even documented this as intentional ("renders as full-screen without AppShell").

### Fix: shared mobile-sidebar units

Extracted three reusable units (TDD — tests written and confirmed red first):

- `hooks/useCollapsibleSidebar.ts` — owns `open` state, the three refs (`container`/`sidebar`/`overlay`), `useSwipeableSidebar` wiring, and close-on-route-change (skipping initial mount). Width-configurable (default 256).
- `components/layout/SidebarDrawer.tsx` — overlay + fixed/transform drawer wrapper on mobile; relative inline wrapper on desktop. `desktopClassName` prop for layout-specific positioning.
- `components/layout/SidebarToggleButton.tsx` — hamburger/X button with correct `aria-label`/`aria-expanded`.

Both `AppShell` and `PortalLayout` now consume all three. `PortalLayout` adds a hamburger to its header on mobile, wraps its `<nav>` in `SidebarDrawer`, and closes the drawer on nav click. Desktop layout preserved (192px nav).

AppShell-specific logic (initial auto-open when mobile + no session, `mainRef.focus()`) stays in `AppShell`; the generic close-on-route-change moved into the shared hook.

### Pre-existing lint errors fixed (surfaced during validation, per Rule 11)

- `src/api/orgs.ts:32` — empty interface → type alias (`@typescript-eslint/no-empty-object-type`).
- `src/pages/ChatPage.sse.test.tsx:103` — module-level `let capturedNavigate` reassigned inside a component → holder object with `.current` property (`react-hooks/globals`).

---

## Assumptions (Rule 7) and validation

- **A-MOBILE-DEFAULT:** `useIsMobile()` → true when viewport < 768px. **VALIDATED** at `useMediaQuery.ts:19-21`.
- **A-SWIPE-REFS:** `useSwipeableSidebar` mutates DOM via refs; the hook must own the refs and pass them through. **VALIDATED** at `useSwipeableSidebar.ts:36-143`.
- **A-AUTOOPEN-APPSHELL-ONLY:** AppShell's initial-mount auto-open (mobile + no session) is AppShell-specific, NOT generic. **VALIDATED** at `AppShell.tsx:33-38` (pre-refactor) — depends on `useMatches` session detection which is AppShell's concern. Stays in AppShell.
- **A-NAVLINK-ONCLICK:** react-router `NavLink` accepts `onClick`. **VALIDATED** by PortalLayout mobile test "closes the drawer when a nav item is clicked".
- **A-PORTAL-WIDTH:** PortalLayout nav is `w-48` (192px); AppShell drawer is `w-64` (256px). The hook's `sidebarWidth` option preserves each layout's exact visual width. **VALIDATED** by SidebarDrawer test "sets drawer width via inline style to match sidebarWidth on mobile".
- **A-EFFECT-ORDER:** On initial mount, the hook's close-on-route-change effect runs (returns early via `isInitialMount`), then AppShell's effect calls `sidebar.setOpen(true)`. No conflict. **VALIDATED** by AI reviewer's trace and by all 1182 tests passing.
- **A-COLLISION:** Two pre-existing `0441` worklog entries exist; this entry uses `0442` to avoid further collision. **VALIDATED** via `ls worklogs/`.

---

## Key Decisions

1. **Option 2 (shared units) over Option 1 (nest under AppShell).** Option 1 would have been simpler but would have forced the org portal's tab nav into the chat sidebar, conflating two navigation models. Option 2 makes the mobile machinery a first-class reusable layer — correct long-term.

2. **Hook owns close-on-route-change, AppShell owns auto-open.** Close-on-route-change is universal (any drawer should close when you navigate). Auto-open-on-first-load is a chat-page UX choice (show the workspace list immediately). Separating these keeps the hook general and AppShell specific.

3. **`sidebarWidth` is hook-configured, not hard-coded in the drawer.** AppShell uses 256px (`w-64`), PortalLayout uses 192px (`w-48`). The drawer reads the width from the hook state and applies it via inline style on mobile — matching each layout's original visual exactly.

4. **Empty interface → type alias (not removing the alias).** `CreateOrgResponse` is used as a semantic name at the call site (`api.post<CreateOrgResponse>`). Keeping it as a type alias preserves readability while satisfying the lint rule. Removing it entirely would lose the semantic distinction between "create response" and "generic org response".

5. **Holder object for captured navigate (not `useState`).** The test captures `useNavigate()` for driving real route changes against the same mounted instance. `useState` would trigger re-renders; a mutable holder object (`navigateRef.current = ...`) is the standard test-capture pattern and satisfies `react-hooks/globals`.

---

## Tests Run

```
# New unit tests (TDD red → green)
npx vitest run src/hooks/useCollapsibleSidebar.test.tsx \
  src/components/layout/SidebarDrawer.test.tsx \
  src/components/layout/SidebarToggleButton.test.tsx
→ 26 passed

# Layout + org-admin integration
npx vitest run src/components/layout/AppShell.test.tsx \
  src/components/layout/PortalLayout.test.tsx \
  src/components/org-admin/OrgAdminLayout.test.tsx
→ 27 passed

# Affected test after lint fix
npx vitest run src/pages/ChatPage.sse.test.tsx
→ 64 passed

# Full frontend suite
npx vitest run
→ 1182 passed (112 files)

npm run typecheck  → clean (tsc --noEmit)
npm run lint       → 0 errors (8 pre-existing warnings, none in changed files)
```

CI on PR #315: all 34 checks green (lint, full Go suite w/ race, frontend e2e, all container builds, coverage 67.6% ≥ 50% floor).

---

## Blockers

None.

---

## Next Steps

- Monitor PR #315 for further review feedback; squash-merge after final APPROVE.
- Pre-existing `0441` worklog collision (two entries with the same number) should be resolved in a separate cleanup PR — not touched here to avoid breaking references.

---

## Files Modified

- `frontend/src/hooks/useCollapsibleSidebar.ts` — new (shared hook: state + refs + swipe + close-on-route-change)
- `frontend/src/hooks/useCollapsibleSidebar.test.tsx` — new (10 tests)
- `frontend/src/components/layout/SidebarDrawer.tsx` — new (overlay + drawer wrapper, mobile/desktop modes)
- `frontend/src/components/layout/SidebarDrawer.test.tsx` — new (11 tests)
- `frontend/src/components/layout/SidebarToggleButton.tsx` — new (hamburger/X button)
- `frontend/src/components/layout/SidebarToggleButton.test.tsx` — new (5 tests)
- `frontend/src/components/layout/AppShell.tsx` — refactored to consume shared units
- `frontend/src/components/layout/AppShell.test.tsx` — added auto-open regression tests
- `frontend/src/components/layout/PortalLayout.tsx` — refactored to consume shared units + mobile drawer
- `frontend/src/components/layout/PortalLayout.test.tsx` — +8 mobile-drawer tests
- `frontend/src/api/orgs.ts` — empty interface → type alias (lint fix)
- `frontend/src/pages/ChatPage.sse.test.tsx` — module-level let → holder object (lint fix)
