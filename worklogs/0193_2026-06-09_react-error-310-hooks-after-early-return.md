# 0193 — React error #310: hooks-after-early-return + repo-wide static guard

**Date:** 2026-06-09

## Problem

Production site (`safespace.thekao.cloud`) threw React error #310
("Rendered more hooks than during the previous render") in the browser
after PR #69 (message queue while streaming) was deployed.

Stack trace pointed to `vi` in the app bundle at `ChatPage.tsx:624`.

Root cause: PR #69 defined `doSendNow` and its ref-sync `useEffect` after
the early return guard in `ChatPage`:

```tsx
// line 567 — early return when no workspaceId
if (!workspaceId) { return ... }

// line 624 — useEffect ONLY reached when workspaceId is set ← VIOLATION
useEffect(() => { doSendNowRef.current = doSendNow; });
```

On the first render with no `workspaceId` (e.g. `/chat` route with nothing
selected), the component exits at the early return — this `useEffect` is
never registered. On the next render when `workspaceId` becomes set, the
component calls the extra hook. React detects the count mismatch and
throws error #310.

The same pattern existed independently in `DiskUsageBar.tsx`:
`useState`, `useEffect`, and `useCallback` were all defined after
`if (allMetrics.length === 0) return null` (line 83). This was found
during the audit that produced the repo-wide static test.

## Fix

### ChatPage.tsx (PR #72)

Moved `doSendNow` and the ref-sync `useEffect` above the early return.
All dependencies were already in scope above the guard — no other code
needed to move. Added an inline comment explaining the ordering constraint
so future contributors don't reintroduce it.

### DiskUsageBar.tsx (committed directly to main, `0ff1e02b`)

Moved `useState` × 2, `useEffect`, and `useCallback` above
`if (allMetrics.length === 0) return null`. Hooks default to their
initial state when `allMetrics` is empty; the early return immediately
follows so the initial state is never visible.

### Rules of Hooks static test (`frontend/src/test/rules-of-hooks.test.ts`)

New test that scans every `.ts`/`.tsx` source file (excluding tests) and
asserts:
1. No built-in React hook call appears after an early `return` in any
   function body.
2. No built-in React hook call appears inside a conditional or loop block.

Uses a brace-depth heuristic — not a full AST parse, but sufficient to
catch the production-incident class of bug without a parser dependency.
If a violation is introduced, CI fails immediately with the file, line
number, and a description of the fix needed.

### Regression test (`ChatPage.hookcount.test.tsx`)

Two complementary checks:
1. **Static**: parse `ChatPage.tsx` source and assert no hook calls appear
   after the `if (!workspaceId)` early return guard. Deterministic,
   immune to React scheduler timing.
2. **Runtime**: render `ChatPage` with no params (early-return path) then
   re-render the same instance with params set (full path). Asserts the
   component doesn't crash.

Note: React 19 concurrent mode defers low-priority re-renders, so
`console.error` interception does not reliably catch error #310 in jsdom.
The static check is the primary regression guard.

## TDD process

Per project standards, the test was written first on the unfixed code
(confirming the static check correctly identifies the violation), then the
fix was applied and both checks were verified passing.

## Assumptions validated

- All dependencies of `doSendNow` (`modelsData`, `send`, `setSseStreamParts`,
  refs, etc.) are declared above the early return — confirmed by reading
  `ChatPage.tsx` lines 31–567.
- `rbac.scope=cluster` only adds read-only (get/list/watch) at cluster
  scope; CRUD remains namespace-scoped — confirmed by reading
  `charts/llmsafespace/templates/rbac.yaml` ClusterRole rules.
- The static test correctly detects violations — verified by injecting a
  synthetic `useEffect` after the early return in a node script and
  confirming the parser flags it.
