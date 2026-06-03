# Worklog: Sidebar Auto-Expand/Collapse Child Sessions

**Date:** 2026-06-03
**Session:** Implement auto-expand/collapse of child sessions in nav panel with user setting
**Status:** Complete

---

## Objective

When a parent session is activated in the sidebar, its child sessions should automatically expand. When navigating away to a session outside that parent's subtree, the children should automatically collapse. Both behaviours are controlled by a new `autoExpandChildren` user setting (default: on).

---

## Work Completed

- **`frontend/src/components/layout/Sidebar.tsx`** — rewrote the `WorkspaceSessionList` expansion `useEffect`:
  - Added `useRef<string | undefined>` to track the previously selected session across renders.
  - When `autoExpandChildren` is `true` (default):
    - On navigation to any session: adds the active session itself to `expanded` (so its children are visible), plus its full ancestor chain (existing behaviour).
    - On navigation away: computes root ancestor of both prev and next via `ancestorChain`; if they differ, removes every ID in the previous chain from `expanded` — collapsing that subtree.
  - When `autoExpandChildren` is `false`: falls back to the original ancestor-chain-only expansion (no auto-collapse). Ancestor chain still expands so the active session is always visible.
  - Added `import { useUserSetting }` from `../../hooks/useUserSettings`.

- **`frontend/src/components/settings/AppearanceTab.tsx`** — added a "Navigation" section with an **Auto-expand child sessions** toggle switch (accessible `role="switch"` button). Reads/writes via `useUserSettings` → persisted to localStorage + API like all other settings.

- **`frontend/src/components/layout/Sidebar.hierarchy.test.tsx`** — added 5 new tests in a `Sidebar — auto-expand/collapse setting` describe block:
  1. Auto-expands parent's children when navigating to a parent (setting on, default).
  2. Does NOT auto-expand parent's children when setting is off.
  3. Ancestor chain still expands when setting is off (safety — active session always visible).
  4. Auto-collapses previous parent's subtree when navigating to a different root (setting on); verified via `fireEvent.click` navigation.
  5. Does NOT auto-collapse when setting is off.

---

## Key Decisions

- Auto-collapse is defined as: "the root ancestor of the previously active session differs from the root ancestor of the newly active session." This avoids collapsing when navigating between siblings or children within the same subtree.
- The previous session is tracked via `useRef` rather than state to avoid triggering an additional render cycle.
- The collapse only removes IDs in the *previous chain* from `expanded`, not a wholesale reset. Sessions the user manually expanded in unrelated subtrees are unaffected.
- Navigation simulation in tests uses `fireEvent.click` on the session row (which calls `navigate()` inside the component), since `MemoryRouter` with `rerender` + new `initialEntries` does not trigger route param updates.

---

## Tests Run

```
npx vitest run src/components/layout/Sidebar.hierarchy.test.tsx
→ 15 tests passed

npx vitest run
→ 73 test files, 609 tests passed
```

---

## Next Steps

None for this feature. If the auto-collapse behaviour needs refinement (e.g. "only collapse if the user did not manually expand"), the `prevSelectedRef` approach makes it straightforward to add a `manuallyExpandedRef` set.

---

## Files Modified

- `frontend/src/components/layout/Sidebar.tsx`
- `frontend/src/components/settings/AppearanceTab.tsx`
- `frontend/src/components/layout/Sidebar.hierarchy.test.tsx`
- `worklogs/0135_2026-06-03_sidebar-auto-expand-collapse-children.md`
