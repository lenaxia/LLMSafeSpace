# Worklog: US-38.6: Fix Controller Metrics Gauge Drift

**Date:** 2026-06-13
**Session:** Address PR #141 review feedback — move Dec() after Status().Update in handleTerminating, add multi-cycle drift test, add Status().Update failure rollback test, rebase onto origin/main.
**Status:** Complete

---

## Objective

Address reviewer-requested fixes on PR #141 (US-38.6 controller gauge drift). The primary bug: `handleTerminating` performed the `WorkspacesRunning` gauge `Dec()` BEFORE `Status().Update`, so a failed status update left the gauge decremented while the workspace remained Active — causing a double-decrement on the next reconcile attempt. Main had moved (now `c1fd99b0`), so a rebase was required.

---

## Work Completed

### Rebase onto origin/main (c1fd99b0)
**Status:** ✅ Complete. Rebased cleanly with no conflicts. The branch was previously based on `7cb41aaa` (Epic 41 PR).

### Remove dead `workspaceID` field from `workspaceConfig`
**Status:** ✅ Fixed. The branch had introduced an unused `workspaceID string` field into `workspaceConfig` in `api/internal/handlers/proxy.go:52`. It was never assigned or read (the map key `h.wsConfig[workspaceID]` already tracks identity). Removed it to keep the struct minimal and consistent with origin/main.

### Fix handleTerminating double-decrement (primary bug)
**Status:** ✅ Fixed. `controller/internal/workspace/phase_terminating.go`:
- Captured `wasActive := workspace.Status.PodIP != ""` BEFORE any mutation/status update.
- Moved the `WorkspacesRunning.Dec()` call to AFTER the successful `r.Status().Update(ctx, workspace)`.
- On a failed status update, the function returns early and the gauge is NOT decremented — so the retry correctly decrements once.
- Existing `TestGaugeDrift_Terminating_WithPodIP_Decrements` and `TestGaugeDrift_Terminating_NoPodIP_NoDecrement` continue to pass, confirming the happy path still decrements exactly once.

### Add multi-cycle drift test
**Status:** ✅ Added `TestCreatingActiveCycle_TenCycles_NoGaugeDrift` in `gauge_drift_test.go`. Simulates 10 Creating→Active→Creating cycles (Inc/Dec pairs) followed by a single Inc, then asserts the gauge reads 1.0 (not 11.0), proving no per-cycle drift accumulates.

### Add Status().Update failure rollback test
**Status:** ✅ Added `TestGaugeDrift_Terminating_StatusUpdateFailure_NoDecrement` in `gauge_drift_test.go`. Uses `controller-runtime`'s fake-client `WithInterceptorFuncs` to make `SubResourceUpdate` ("status") return an error. A Terminating workspace with `PodIP != ""` (wasActive) is reconciled; the reconcile returns the injected error and the test asserts the gauge remains at 1.0 — i.e. no decrement occurred on the failed update path.

---

## Key Decisions

1. **`wasActive` capture before mutation.** PodIP != "" is the established proxy for "counted in WorkspacesRunning" (already used in the original branch code and across the 9 Active→Creating transition sites). Capturing it before the status update preserves the correct semantics even though the update clears `Status.PodIP`.
2. **Dec AFTER successful update, not guarded by rollback Inc.** The reviewer's preferred pattern: move the Dec after a confirmed successful update rather than Dec-then-rollback-Inc-on-error. This is simpler and leaves no possibility of a leaked decrement.
3. **Interceptor for failure injection.** `interceptor.Funcs.SubResourceUpdate` (subResourceName == "status") is the correct hook to fail `Status().Update` specifically, without failing the final `Update` (metadata) call.
4. **Removed dead `workspaceID` struct field.** It was never populated; the map key carries identity. Kept origin/main's lean version of `workspaceConfig`.

---

## Assumptions

- **Assumption:** `Status().Update` is the only status-write in `handleTerminating`. **Validated:** verified by reading `phase_terminating.go` — the single `r.Status().Update(ctx, workspace)` at the failed-update guard is the only status write before finalizer removal.
- **Assumption:** `WithInterceptorFuncs` routes `Status().Update` through `SubResourceUpdate`. **Validated:** read `sigs.k8s.io/controller-runtime@v0.20.3/pkg/client/interceptor/intercept.go:154` — `subResourceInterceptor.Update` calls `s.funcs.SubResourceUpdate(ctx, s.client, s.subResourceName, ...)` with `subResourceName == "status"`.

---

## Blockers

None.

---

## Tests Run

```bash
go build ./controller/... ./api/...
# (clean — both build)

go test ./controller/... -timeout 120s -count=1
# ok  controller
# ok  controller/internal/common
# ok  controller/internal/metrics
# ok  controller/internal/webhooks
# ok  controller/internal/workspace

go test ./controller/internal/workspace/ -run 'TestCreatingActiveCycle_TenCycles_NoGaugeDrift|TestGaugeDrift_Terminating_StatusUpdateFailure_NoDecrement' -v -count=1
# --- PASS: TestCreatingActiveCycle_TenCycles_NoGaugeDrift
# --- PASS: TestGaugeDrift_Terminating_StatusUpdateFailure_NoDecrement
```

---

## Next Steps

- Force-push the branch and confirm PR #141 CI is green.
- If the reviewer requests it, backport the "Dec after successful update" pattern to the 9 Active→Creating transition sites in `phase_active.go` and `health.go` (those currently use Inc-rollback-on-error, which is also correct but a different idiom).

---

## Files Modified

| File | Change |
|---|---|
| `api/internal/handlers/proxy.go:51` | Remove unused `workspaceID` field from `workspaceConfig` struct |
| `controller/internal/workspace/phase_terminating.go` | Capture `wasActive` before update; move `WorkspacesRunning.Dec()` to after successful `Status().Update` |
| `controller/internal/workspace/gauge_drift_test.go` | Add `TestCreatingActiveCycle_TenCycles_NoGaugeDrift` and `TestGaugeDrift_Terminating_StatusUpdateFailure_NoDecrement` |
