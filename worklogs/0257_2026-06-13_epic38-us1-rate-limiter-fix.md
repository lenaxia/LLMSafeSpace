# Worklog: US-38.1 — Fix Rate Limiter (Atomic Redis Operations, Bounded Memory)

**Date:** 2026-06-13
**Story:** Epic 38, US-38.1 — rate limiter correctness & resource safety
**Status:** Complete

---

## Objective

Address review feedback on PR #142 (`fix/epic-38-us1-rate-limiter`). The original branch fixed three latent defects in the rate limiter (sliding-window stub, non-atomic fixed-window increment, unbounded in-memory token-bucket map) but introduced unrelated changes, a duplicate Redis connection pool, an unfixed growth leak in the in-memory backend, and had gaps in test coverage.

---

## Work Completed

### Review item 1 — Removed unrelated changes
- Rebased `fix/epic-38-us1-rate-limiter` onto `origin/main`, resolving the `proxy.go` conflict in favour of `origin/main` and dropping the unrelated `workspaceID` field that had been added to `workspaceConfig`.
- Reverted an unrelated worklog rename (`0245_…_epic41-message-queue…` → `0246_…`) that collided with the existing `0246` worklog.

### Review item 3 — Eliminated duplicate Redis client
- The original commit opened a second `redis.NewClient` (its own pool) in `services/services.go` purely for the rate limiter, duplicating the cache service's pool to the same Redis instance.
- Added `cache.Service.GetClient() *redis.Client` (`api/internal/services/cache/cache.go`) to expose the cache service's existing client for sharing.
- `services.New` now constructs the rate limiter with `ratelimit.NewWithRedisClient(log, cacheService.GetClient())` — a single shared pool. To avoid a double-close, the redis backend's `Close()` is a no-op (the cache service owns and closes the client in `Stop()`).

### Review item 6 — Bounded `memoryBackend` growth
- The `memoryBackend` (used by `NewWithMemory`) had the same unbounded-map-growth leak class that was fixed for the token-bucket `localBuckets`: expired/abandoned keys were never purged, only lazily overwritten.
- Mirrored the `Service.maybeCleanup` pattern: each `memCounter`/`memWindow` now carries a `lastAccess` timestamp, and every backend op runs `maybeCleanup`, which evicts entries idle longer than the TTL. Added `NewMemoryBackendWithCleanup(ttl, interval)` for deterministic tests.

### Review item 5 — Added missing unit tests
- `TestIncrement_FixedWindowExpiryReset_Redis` / `…_Memory`: increment, advance past the window TTL (miniredis `FastForward` / sleep), assert the counter resets to 1.
- `TestGetWindowEntries_SlidingWindow` / `…_Memory`: assert `GetWindowEntries` returns sorted members and the correct tail after pruning old entries. (Scores kept inside double precision so Redis `ZADD` cutoffs are exact.)
- `TestRedisBackend_ErrorPath`: tear down miniredis before use and assert `Increment`/`AddToWindow`/`CountInWindow`/`GetTTL` surface errors (no silent zeros).
- `TestMemoryBackend_CleanupStaleEntries`: assert idle counters/windows are swept from the maps.

### Review item 4 — Added integration test
- `api/internal/middleware/tests/rate_limit_integration_test.go`: drives `RateLimitMiddleware` → real `ratelimit.Service` → miniredis end-to-end for both `fixed_window` and `sliding_window` strategies, asserting requests under the limit pass and the over-limit request gets HTTP 429 with the expected `X-RateLimit-*` headers.

### Review item 2 — This worklog.

---

## Key Decisions

- **Borrowed-client lifecycle.** The rate limiter borrows the cache service's Redis client, so its backend `Close()` is intentionally a no-op rather than closing a client it does not own. This prevents a double-close when the cache service stops, and follows the "don't close what you don't own" idiom. Tests that own a miniredis client close it explicitly in their cleanup.
- **`memoryBackend` cleanup mirrors `Service` cleanup.** Reusing the same `lastAccess` + lazy-sweep pattern keeps the two bounded-growth mechanisms consistent and easy to reason about.
- **Test scores within double precision.** Redis sorted-set scores are float64; `time.Now().UnixNano()` (~1.7e18) exceeds 2^53 and collapses neighbouring values. The window-pruning test in `TestGetWindowEntries_*` uses small, well-separated scores so cutoff boundaries are exact. The existing `TestSlidingWindow_ProperCount` (production-style nano cutoffs) continues to validate realistic behaviour.

---

## Blockers

None.

---

## Tests Run

- `go build ./...` — passes.
- `go vet` on changed packages — passes.
- `go test ./api/internal/services/ratelimit/... ./api/internal/middleware/... -timeout 60s -race` — passes (unit + integration, including `-race`).

---

## Files Modified

- `api/internal/services/cache/cache.go` — added `GetClient()`.
- `api/internal/services/services.go` — reuse cache client (no duplicate pool); keep metering wiring; wire rate-limiter Start/Stop.
- `api/internal/services/ratelimit/backend.go` — redis `Close()` no-op; `memoryBackend` `lastAccess` + `maybeCleanup`; `NewMemoryBackendWithCleanup`.
- `api/internal/services/ratelimit/ratelimit.go` — Backend-based service (atomic Lua ops, bounded token buckets).
- `api/internal/services/ratelimit/ratelimit_test.go` — new unit tests + fixed test helper.
- `api/internal/middleware/tests/rate_limit_integration_test.go` — new end-to-end integration tests.
- `worklogs/0257_2026-06-13_epic38-us1-rate-limiter-fix.md` (this file).
