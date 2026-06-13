# Worklog: Epic 41 — Adversarial Review Bug Fixes

**Date:** 2026-06-13
**Session:** Post-merge adversarial revalidation found three bugs
**Status:** Complete

---

## Objective

Adversarial revalidation of Epic 41 implementation found three bugs missed during initial implementation and review.

---

## Work Completed

### BUG 1 (HIGH): `onSessionIdle` sessionIndex nesting

`sessionIndex.RecordMessage` and `fetchAndPersistTitle` were nested inside `if h.activityTracker != nil` in `onSessionIdle` (proxy.go:1074-1080). If `activityTracker` is nil but `sessionIndex` is set, session index recording and title persistence were silently skipped.

**Fix:** Separated into independent nil checks.

**Regression test:** `TestProxy_OnSessionIdle_SessionIndexIndependentOfActivityTracker` — sets `sessionIndex` without `activityTracker` and verifies `RecordMessage` still fires.

### BUG 2 (MEDIUM, pre-existing): Double `releaseConnection`

In `proxyToWorkspace`, when `checkAndAddActiveSession` fails (max sessions), line 526 called `releaseConnection` explicitly, then the `defer` at line 522 called it again. The guard `if h.connCount[workspaceID] > 0` prevented underflow but decremented twice, permanently losing one connection slot.

**Fix:** Removed the explicit `releaseConnection` at line 526. The `defer` handles it correctly.

**Regression test:** `TestProxy_ProxyToWorkspace_NoDoubleReleaseOnMaxSessions` — verifies connection count is 0 after max-sessions rejection.

### BUG 3 (LOW): `isSessionActive` exclusive lock for reads

`isSessionActive` and `activeSessionCount` used `sync.Mutex` with `Lock()` for read-only operations. This serialized all `SendPromptAsync` calls across all workspaces on the same replica.

**Fix:** Changed `activeMu` from `sync.Mutex` to `sync.RWMutex`. Converted `isSessionActive` and `activeSessionCount` to use `RLock()`.

**Regression test:** `TestProxy_IsSessionActive_ConcurrentReads` — 100 concurrent goroutines reading `isSessionActive`.

### Cleanup

- Removed last reference to `workspaceConfig.workspaceID` field from `proxy_input_test.go`
- Removed dead assignment `cfg.workspaceID = workspaceID` from `shouldAutoApprovePermissions`

---

## Key Decisions

1. **Independent nil checks over nested** — `activityTracker` and `sessionIndex` are independently injected dependencies. Neither should gate the other.

2. **RWMutex over Mutex for activeSess reads** — `SendPromptAsync` is a hot path that only reads `activeSess`. Exclusive locking was unnecessarily broad.

---

## Assumptions Stated and Validated

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | `activityTracker` and `sessionIndex` are initialized independently | Verified: `Start()` sets `activityTracker`, `SetSessionIndex()` sets `sessionIndex` |
| A2 | `releaseConnection` guard prevents underflow but causes count leak | Verified: `if h.connCount[workspaceID] > 0` prevents negative but double-decrement is real |
| A3 | `RWMutex` is safe for all activeMu usage patterns | Verified: write sites use `Lock()`, read sites use `RLock()` |

---

## Blockers

None.

---

## Tests Run

```bash
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go test -timeout 120s -race ./api/internal/handlers/ -count=1
# ok  23.042s
```

---

## Next Steps

None — all bugs fixed and tested.

---

## Files Modified

| File | Change |
|------|--------|
| `api/internal/handlers/proxy.go` | BUG 1: independent nil checks; BUG 2: remove double release; BUG 3: RWMutex; remove dead workspaceID assignment |
| `api/internal/handlers/proxy_test.go` | 3 new regression tests |
| `api/internal/handlers/proxy_input_test.go` | Remove dead workspaceID assertion |
