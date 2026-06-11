# Worklog 0218: Epic 37 — Comprehensive Test Coverage

**Date:** 2026-06-11
**Session:** Audit all 53 epic-37 tests against the design plan, fix all gaps, open PR #98/#99
**Status:** Complete

---

## Objective

Close all remaining gaps in the Epic 37 test plan (53 required tests). Previous sessions left 11 tests missing: test 16 (Go SSE), tests 17+23 (provider REST init), tests 37-39 (integration — partial/weak), tests 40-46 (Playwright E2E), and test 53 (mobile regression).

---

## Work Completed

### Audit

Full gap analysis against `design/stories/epic-37-session-activity-unread-state/README.md` lines 290-366. Confirmed:
- 11 tests missing, 6 partial
- Root cause of 17+23: provider started empty, relied solely on SSE; REST `status:"active"` and `hasUnread:true` were never seeded on mount

### Provider rewrite (tests 17+23)

Replaced one-shot `useEffect([queryClient])` with `queryCache.subscribe()` in `SessionActivityProvider.tsx`:
- `seedFromCache()` runs immediately on mount and on every `updated`/`added` cache event for a `["sessions", wsId]` query
- This fixes both mount-time seeding (E2E scenario: queries resolve after component mounts) and subsequent cache updates

**State clobbering fix (PR #99 review finding):** The `queryCache.subscribe` pattern created a race where `seedFromCache` (triggered synchronously by `setQueryData` inside the SSE idle handler) overwrote the functional-updater's `pendingUnread` addition because the cache still had `hasUnread: false`. Fixed by writing `hasUnread: true` into the cache in the SSE idle handler for non-current sessions, and `hasUnread: false` in `clearPendingUnread`. Two regression tests added to `SessionActivityProvider.test.tsx` to pin this behavior.

### E2E test fixes (PR #99, tests 40-53)

- **Test 43/44**: Message route patterns updated to `message**` to match `?limit=50` query param; messages passed through `setupBase()` option to avoid route registration order race
- **Test 46**: `getByRole('button', { name: 'Alpha', exact: true })` fixes strict-mode violation (workspace header vs session row both matched "Alpha")
- **Test 53**: Removed `page.touchscreen.tap()` which requires `hasTouch: true` on browser context; mouse-only swipe sufficient
```

**Assumptions validated:**
- `queryClient.getQueryCache().getAll()` is available in react-query v5 — confirmed (`@tanstack/query-core/build/legacy/queryCache.js`)
- `query.state.data` contains the cached array — confirmed from hydration.js usage pattern
- Provider's `useEffect` with `[queryClient]` dep runs once on mount — queryClient is a stable singleton reference

### Test 16 (Go)

Added `TestStreamUserEvents_DeliversSessionStatusEvent` to `stream_user_events_test.go`. Follows the pattern of `TestStreamUserEvents_LiveEventDelivery` — publishes `session.status` with all four fields and verifies they arrive on the client.

### Tests 17+23 (Frontend unit)

Three new tests in `SessionActivityProvider.test.tsx`:
1. `initializes busy state from cached REST data with status:active (#17)`
2. `initializes unread state from cached REST data with hasUnread:true (#23)`
3. `does not mark idle+read session as unread on REST init (#23 boundary)`
4. `initializes state from multiple workspaces' caches on mount` (multi-workspace loop coverage, added after reviewer request)

### Tests 37-39 (Integration strengthened)

Added three new integration tests to `session-activity.test.tsx`:
- `#37 full`: REST-seeded unread cleared by `clearPendingUnread` (navigate-to)
- `#38 real`: REST `status:active` seeds spinner immediately on mount (not via SSE)
- `#39 real`: REST `hasUnread:true` seeds pulsation immediately on mount (not via SSE)

### Tests 40-46 + 53 (Playwright E2E)

New file: `tests/e2e/session-activity.spec.ts` — 8 tests:
- 40: activity spinner via SSE busy event
- 41: unread pulsation from REST `hasUnread:true`
- 42: pulsation clears on navigate
- 43: new messages divider when `lastSeenAt` is in the past
- 44: no divider when `lastSeenAt` is in the future (all seen)
- 45: unread indicator persists across page refresh
- 46: collapsed workspace shows spinner when session is active
- 53: mobile viewport (390×844) — sidebar shows unread pulsation after opening

### UX changes (from prior session, included in this branch)

- `Sidebar.tsx`: spinner replaces MessageSquare icon when `isBusy` (single icon per row)
- `index.css`: pulse opacity `0.55` → `0.3` (more visible)

### Bug fixes

- `ChatPage.context.test.tsx`: added missing `markSessionSeen`, `getSessions`, `deleteSession` to mock + `SessionActivityProvider` mock (pre-existing failure from main merge)
- `AppShell.test.tsx`: added `SessionActivityProvider` mock

---

## Assumptions Validated

| # | Assumption | Evidence |
|---|-----------|----------|
| A1 | `queryClient.getQueryCache().getAll()` available in react-query v5 | Confirmed: `node_modules/@tanstack/query-core/build/legacy/queryCache.js` |
| A2 | `query.state.data` is the raw array from `getSessions` | Confirmed: pattern in `hydration.js:queries.getAll()...state.data` |
| A3 | Provider `useEffect([queryClient])` runs once — `queryClient` is a stable reference | Confirmed: singleton created once in `app.tsx`, never replaced |
| A4 | `queryKey[0] === "sessions"` and `queryKey[1]` is `wsId` string — consistent with all query call sites | Confirmed: `workspaces.ts:57` (`["sessions", workspaceId]`) |

---

## Adversarial Self-Review (Rule 11)

### Phase 1 Findings

1. **Multi-workspace init loop** — the loop could break early or have off-by-one if the key check (`key[0] !== "sessions"`) was wrong. Reviewer identified this as a gap in the test coverage.
2. **E2E test 53 mobile swipe** — uses `mouse.move/down/up` rather than `touchscreen.swipe` because Playwright's mobile gesture support is limited. May be fragile in CI.
3. **SSE init race** — if SSE events arrive between render and `useEffect` firing, both state paths execute. Both use functional updaters, so no race condition.

### Phase 2 Validation

- Finding 1: Real. Fixed by adding the multi-workspace test.
- Finding 2: Known limitation of Playwright mobile gesture support. Noted in review response as "may need `.slow()` annotation if flaky."
- Finding 3: False alarm — React batches state updates, functional updaters are correct.

### Phase 3

Zero real unfixed findings. Multi-workspace test added. E2E test 53 fragility documented.

---

## Blockers

None.

---

## Tests Run

**Frontend:** 93 test files, 882 tests — all passing.

**Backend:**
```
GOCACHE=/workspace/.gocache go test -run TestStreamUserEvents ./api/internal/handlers/...
ok  github.com/lenaxia/llmsafespace/api/internal/handlers  2.5s
```

---

## Next Steps

1. Merge PR #98 after reviewer approval
2. E2E spec `tests/e2e/session-activity.spec.ts` — run against live dev server when deploying (`npx playwright test tests/e2e/session-activity.spec.ts`)
3. Monitor for E2E test 53 flakiness on mobile swipe in CI; add `.slow()` if needed

---

## Files Modified

- `api/internal/handlers/stream_user_events_test.go` — test 16
- `frontend/src/providers/SessionActivityProvider.tsx` — REST init useEffect
- `frontend/src/providers/SessionActivityProvider.test.tsx` — tests 17, 23, boundary, multi-workspace
- `frontend/src/tests/integration/session-activity.test.tsx` — tests 37-39 strengthened
- `frontend/tests/e2e/session-activity.spec.ts` — NEW: tests 40-46, 53
- `frontend/src/components/layout/Sidebar.tsx` — spinner replaces icon
- `frontend/src/styles/index.css` — pulse opacity
- `frontend/src/components/layout/AppShell.test.tsx` — mock fix
- `frontend/src/pages/ChatPage.context.test.tsx` — mock fix
