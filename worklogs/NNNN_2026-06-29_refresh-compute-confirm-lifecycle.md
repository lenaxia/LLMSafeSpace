# Worklog: Refresh-compute confirmation dialog + lifecycle kebab section

**Date:** 2026-06-29
**Session:** Frontend UX follow-up to PR #452 (refresh compute) — gate refresh behind a destructive-action confirmation and declutter the workspace kebab with a Lifecycle section.
**Status:** Complete

---

## Objective

PR #452 shipped "Refresh compute" firing from a single kebab click, but refresh halts all in-progress work and reprovisions the workspace from scratch. Two UX fixes were requested:

1. Show a confirmation dialog before refreshing, warning that current work will be interrupted, with an option to cancel.
2. Reorganise the workspace kebab so refresh, suspend, and delete live in a dedicated lifecycle section (or submenu) rather than the flat action list.

---

## Assumptions (stated + validated)

1. **`@radix-ui/react-dialog` is the established modal pattern** — `WorkspaceSettingsDrawer` uses `Dialog.Root/Portal/Overlay/Content`. → Validated: `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx:1,90-94`. The new `ConfirmDialog` mirrors that structure (centered content rather than a right-side drawer).
2. **`KebabMenu` has no section/submenu concept** — it splits items into non-destructive (top) and destructive (bottom with divider). → Validated: `KebabMenu.tsx` pre-change. Extending it requires either a section field or a submenu. A labelled section (divider + header) fits the flat component better than nested popovers and matches the user's "dedicated lifecycle section" option.
3. **The session kebab (Sidebar line ~775) and ChatPage kebab put `Delete` last in their item arrays and rely on the destructive-bottom auto-sort.** → Validated. Any change to the render path must preserve their exact layout, so the sectioned render only activates when an item declares a `section`.
4. **Radix `Dialog.Content` renders `role="dialog"`, not `alertdialog`.** → Validated during review: an initial closed-state test asserted `role="alertdialog"` absence, which is tautological (always null). Corrected to `role="dialog"`.

---

## Work Completed

### New `ConfirmDialog` (`components/ui/ConfirmDialog.tsx`)
- Reusable Radix-Dialog modal mirroring `WorkspaceSettingsDrawer`'s overlay/content.
- Props: `title`, `description`, optional `note`, `confirmLabel`, `cancelLabel` (default "Cancel"), `destructive` (red confirm button), `onConfirm`, `open`, `onOpenChange`.
- `onConfirm` only fires the action; the caller closes the dialog (preserves control for in-flight state). Radix handles overlay-click/Escape → `onOpenChange(false)`.

### `KebabMenu` section support (`components/ui/KebabMenu.tsx`)
- Added optional `section?: string` to `KebabMenuItem`.
- Two render paths: when no item declares a `section`, the legacy two-phase layout (non-destructive first, footer, divider, destructive last) runs unchanged — zero impact on the session/ChatPage kebabs. When any item declares a `section`, items render in array order with a labelled divider (uppercase muted header) on each section change; destructive styling applies inline. Footer renders last either way.
- Refactored shared `ItemButton` and `Footer` helpers to avoid duplication between paths.

### Sidebar wiring (`components/layout/Sidebar.tsx`)
- "Refresh compute" kebab item now opens `ConfirmDialog` (`showRefreshConfirm` state) instead of firing `onRefreshCompute` directly.
- Dialog copy: title "Refresh compute?", body explains it halts current work and reprovisions with the latest image + current resource defaults, note "Your files and workspace data are preserved."
- Refresh, Suspend/Resume, and Delete grouped under `section: "Lifecycle"`. Settings, Copy link, Rename stay in the default (unsectioned) top group.

### Tests
- `ConfirmDialog.test.tsx`: closed/open render (`role="dialog"`), confirm→onConfirm, cancel→onOpenChange(false), destructive vs default button style.
- `KebabMenu.test.tsx`: section header renders; legacy layout preserved when no sections; destructive styling within a section; multi-section header-on-change; sectioned-mode click→onClick+close.
- `Sidebar.test.tsx`: Lifecycle section grouping; refresh opens dialog and fires on confirm; refresh does NOT fire on cancel. New refresh tests isolated in a dedicated describe with `beforeEach(vi.clearAllMocks)` (mirrors the session-delete test pattern).

---

## Key Decisions

- **Section, not submenu.** A labelled divider fits the existing flat `KebabMenu` far better than nested popovers (which would need portal positioning, escape handling, and a nested state machine). The user offered "section or submenu"; the section is the simpler, idiomatic choice.
- **Two render paths, gated on `hasSections`.** A single unified path would have changed the visual layout of the session/ChatPage kebabs (losing their divider-before-Delete). Gating on `hasSections` guarantees zero regression for menus that pre-date sections while giving the workspace kebab its lifecycle grouping. The cost is a small branch; the benefit is provable non-regression.
- **`onConfirm` does not close the dialog.** The caller closes after firing the action. This keeps control of in-flight/disabled state (the kebab item shows "Refreshing compute…" + disabled while pending) in one place and matches Radix's controlled-open model.
- **Delete keeps `window.confirm`.** Only refresh was requested to get the rich dialog. `ConfirmDialog` is built reusable and exposes a `destructive` variant precisely so Delete (and other destructive ops) can migrate to it later — but that migration is out of scope here.

---

## Blockers

None.

---

## Tests Run

- `vitest run` full frontend suite — **1240 tests pass** (114 files).
- `tsc --noEmit` — clean.
- `eslint` on all changed files — clean.

---

## Next Steps

- Migrate Delete (and the ChatPage/session destructive actions) from `window.confirm` to `ConfirmDialog` for consistency — separate PR.
- Consider an optional Playwright e2e for the refresh-confirm flow (a sidebar Playwright suite exists at `tests/e2e/sidebar-hierarchy.spec.ts`); RTL integration coverage currently exercises the wiring.

---

## Files Modified

- `frontend/src/components/ui/ConfirmDialog.tsx` (new)
- `frontend/src/components/ui/ConfirmDialog.test.tsx` (new)
- `frontend/src/components/ui/KebabMenu.tsx`
- `frontend/src/components/ui/KebabMenu.test.tsx`
- `frontend/src/components/layout/Sidebar.tsx`
- `frontend/src/components/layout/Sidebar.test.tsx`
