# Worklog: US-38.12: Add Agentd Graceful Shutdown

**Date:** 2026-06-13
**Session:** Implement graceful shutdown for workspace-agentd (Epic 38, US-38.12)
**Status:** Complete

---

## Objective

Make workspace-agentd shut down cleanly on SIGTERM/SIGINT: cancel background goroutines, drain HTTP servers concurrently within a bounded budget, and ensure the relay injector respects context cancellation rather than blocking on `time.Sleep`.

---

## Work Completed

### Graceful shutdown plumbing (main.go)

- Added `signal.NotifyContext` for `SIGTERM`/`SIGINT` to produce `rootCtx`.
- Derived `bgCtx` from `rootCtx` and passed it to the three background goroutines: SSE tracker (`subscribe`), `fillGaps`, and `refreshIsHealthyLoop`.
- Tracked all background goroutines with a `sync.WaitGroup` for bounded join.
- Moved the user server startup into a goroutine with an error channel (`srvErr`), replacing both `log.Fatal` calls with the error-channel pattern.
- Added concurrent server shutdown with a 25-second budget via a separate `sync.WaitGroup` (`adminSrv` + `userSrv`).
- Added `bgCancel()` followed by a bounded `bgWg.Wait()` (5-second timeout) in the shutdown sequence so background goroutines don't leak past pod termination.

### Relay injector context threading (relay_injector.go)

- Threaded `rootCtx` through `startRelayInjector` and into `fetchFreeModels`.
- Added `ctx.Done()` checks to the relay injector health-wait loop.
- Replaced `time.Sleep(5 * time.Second)` in the model-fetch retry loop with a context-aware `select { ctx.Done() / time.After() }` so a shutdown signal interrupts the retry immediately instead of blocking for the full 5 seconds.

### Review-driven fixes (this PR)

1. **Removed unrelated `workspaceID` field** from `workspaceConfig` in `api/internal/handlers/proxy.go` — it was accidentally introduced in the original commit and has nothing to do with agentd shutdown.
2. **Context-aware retry sleep** — the `time.Sleep(5 * time.Second)` in the model-fetch retry loop was not context-aware; replaced with `select { ctx.Done() / time.After() }` so SIGTERM interrupts the retry immediately.
3. **Meaningful tests** — the original PR shipped three tests that exercised only the Go standard library (`http.Server.Shutdown`, `context.WithCancel`), not the actual agentd shutdown code. Added two tests that exercise real production functions:
   - `TestFetchFreeModels_RespectsContextCancellation` — starts a slow httptest server, calls `fetchFreeModels` with a 100ms context timeout, verifies it returns with an error in under 1s.
   - `TestStartRelayInjector_ExitsOnContextCancellation` — calls `startRelayInjector` with a HealthCheck that always returns false, cancels the context after 200ms, verifies the goroutine exits within 1s via `runtime.NumGoroutine`, and confirms `KillOpenCode` is never called.
4. **Worklog** — this file.

---

## Key Decisions

- **Two-layer context (`rootCtx` → `bgCtx`).** `rootCtx` is the signal context; `bgCtx` is derived from it. On shutdown we first `bgCancel()` to tell background goroutines to wind down, then join them with a 5-second timeout. This prevents a stuck background goroutine from blocking pod termination indefinitely.

- **Concurrent server shutdown (25s budget).** `adminSrv` and `userSrv` are shut down concurrently via `sync.WaitGroup` rather than sequentially. With a 500ms-per-request worst case and a 25-second budget, both servers drain well within Kubernetes' 30-second grace period.

- **`runtime.NumGoroutine` for exit verification.** The relay injector goroutine has no `done` channel. Rather than adding one solely for testability, the test polls `runtime.NumGoroutine` to confirm the goroutine count drops after cancellation. This is stable in a unit test environment where no other goroutines are spawning/dying.

- **Context-aware httptest handler in the cancellation test.** The test server handler uses `select { time.After / r.Context().Done() }` rather than a bare `time.Sleep` so that `httptest.Server.Close()` returns promptly after the client disconnects (Go 1.22+ waits for active handlers during `Close`).

---

## Blockers

None.

---

## Tests Run

- `go build ./cmd/workspace-agentd/...` — passes.
- `go test ./cmd/workspace-agentd/... -timeout 60s` — passes (all non-binary-build tests complete in ~6s).
- Targeted runs with `-race` for shutdown and relay-injector tests — clean.
- `TestStartRelayInjector_RetriesWhenZeroModels` (5s retry test) — passes, confirming the new `select`-based retry loop still functions correctly.

---

## Files Modified

- `cmd/workspace-agentd/main.go` — graceful shutdown plumbing (signal context, WaitGroup, error channels, concurrent server shutdown).
- `cmd/workspace-agentd/relay_injector.go` — context-aware retry sleep, `ctx` threading.
- `cmd/workspace-agentd/main_test.go` — added `TestFetchFreeModels_RespectsContextCancellation` and `TestStartRelayInjector_ExitsOnContextCancellation`; added `runtime` import.
- `cmd/workspace-agentd/relay_injector_test.go` — context plumbing for existing tests.
- `worklogs/0251_2026-06-13_us38-12-agentd-graceful-shutdown.md` (this file).

### Files reverted/cleaned

- `api/internal/handlers/proxy.go` — removed unrelated `workspaceID` field from `workspaceConfig` (was accidentally included in the original commit).
