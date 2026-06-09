# Worklog 0191 — Frontend: Fix mobile sidebar swipe UX

**Date:** 2026-06-09
**PR:** #70 (squash-merged into main)
**Area:** frontend/layout

---

## Summary

Fixed two root causes of broken mobile sidebar swipe: browser back-navigation
gesture leaking through, and no visual tracking during the swipe (jarring snap).

---

## Problem

On mobile viewports, swiping from the left edge to open the nav sidebar was
broken in two ways:

1. **Browser back-swipe leaked through.** React's synthetic `onTouchMove` is
   passive by default, so `e.preventDefault()` was a no-op. The browser's
   native back/forward navigation gesture fired instead — the page slid left
   as if navigating back, then snapped when the sidebar appeared.

2. **No visual feedback during swipe.** The sidebar only appeared/disappeared
   on `touchEnd` after crossing a threshold. There was no finger-following
   transform during the gesture, making it unclear whether navigation occurred
   or the menu just appeared.

---

## Root cause analysis

### Cause 1: Passive touch listeners

React attaches synthetic touch events as passive listeners. Calling
`preventDefault()` on a passive `touchmove` is ignored by the browser, so the
overshoot-to-go-back gesture was never suppressed.

### Cause 2: Binary state change with no animation

The original `handleTouchMove` only called `preventDefault()` (which didn't
work — see above). It never tracked the finger position or applied visual
transforms. The sidebar's CSS `transition-transform` class only animated on
`setSidebarOpen(true/false)` state change in `handleTouchEnd`.

---

## Changes

### `frontend/src/hooks/useSwipeableSidebar.ts` (new)

Extracted custom hook containing all swipe gesture logic:

- **Native DOM listeners** with `{ passive: false }` on `touchmove` so
  `preventDefault()` actually blocks browser back navigation
- **Visual tracking**: sidebar and overlay follow the finger via direct DOM
  `style.transform`/`style.opacity` during `touchmove`
- **CSS transition settle**: on `touchEnd`, inline styles are cleared so the
  CSS `transition-transform` class smoothly animates to final position
- **1/3 commit threshold**: drag past 1/3 of sidebar width to commit open/close,
  otherwise snaps back
- **Multi-touch guard**: `e.touches.length > 1` ignored
- **`enabled` prop**: clean desktop/mobile toggle without conditional hook calls
- **`isOpenRef`**: avoids stale closure over `isOpen` in event handlers
- **Effect cleanup**: only React's return cleanup removes listeners (no manual
  `pendingCleanup` — that was a bug found by the automated reviewer, see below)

### `frontend/src/hooks/useSwipeableSidebar.test.ts` (new)

14 unit tests:

- Edge swipe to open: happy path, outside edge zone, below settle threshold
- Swipe to close: happy path, below settle threshold
- Vertical scroll passthrough (no interception)
- Visual tracking: sidebar transform during swipe, overlay opacity, style
  cleanup after settle
- `preventDefault` on horizontal swipe, passthrough on vertical
- Disabled state (no listeners attached)
- Cleanup on unmount
- **Consecutive gestures** (regression test for listener-removal bug)

### `frontend/src/components/layout/AppShell.tsx`

- Replaced inline `handleTouchStart/Move/End` callbacks with `useSwipeableSidebar`
- Added `containerRef`, `sidebarWrapperRef`, `overlayRef` refs
- Overlay now always rendered on mobile with CSS opacity/pointer-events
  transition (no mount/unmount flash between inline style clear and React
  re-render)
- `SIDEBAR_WIDTH_PX = 256` named constant matching `w-64`

### `worklogs/0184_2026-06-08_message-queue-while-streaming.md` → `0190_...`

Renumbered duplicate worklog (0184 was shared with auto-abort-stuck-question)
to fix `repolint` duplicate version check. Pre-existing issue, not introduced by
this PR but required to clear CI.

---

## Automated review findings

The CI automated reviewer (OpenCode) found one critical bug in the initial
implementation:

**`pendingCleanup` listener removal bug.** The initial version called
`pendingCleanup.current()` inside `onEnd`, which removed all touch event
listeners after the first gesture completed. Because the effect's dependency
array contained only stable references (refs + useState setter), the effect
never re-ran to re-attach them. The swipe gesture worked exactly once per
component mount.

Fix: removed `pendingCleanup` entirely. React's effect return cleanup already
handles listener removal on unmount. Added regression test (consecutive
gestures on same hook instance).

Verdict after fix: **APPROVE**.

---

## Test results

```
npx vitest run src/hooks/useSwipeableSidebar.test.ts src/components/layout/AppShell.test.tsx
→ 17 tests passing
npx tsc --noEmit → clean
npx eslint → clean (1 pre-existing warning in AppShell.tsx)
CI: all checks green including Frontend (unit + typecheck + e2e), Lint, review
```

---

## Key design decisions

| Decision | Rationale |
|---|---|
| Custom hook extraction | Separates gesture logic from layout, independently testable |
| Native DOM listeners | Required for `{ passive: false }` to actually prevent browser back |
| Direct DOM style manipulation during swipe | Avoids React re-renders on every touchmove frame (60fps) |
| Clear inline styles on touchEnd | Lets CSS transition class animate the settle smoothly |
| Overlay always mounted (opacity toggle) | Prevents flash between inline style clear and React re-render |
| `isOpenRef` over `isOpen` in deps | Avoids re-attaching listeners on every open/close toggle |
