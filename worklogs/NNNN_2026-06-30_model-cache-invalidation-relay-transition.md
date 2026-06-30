# Worklog: Cache invalidation on relayInjected transition (#467)

**Date:** 2026-06-30
**Session:** Fix issue #467 — close the ~20s stale-providerID window after relay injection completes
**Status:** Complete

---

## Objective

A fresh workspace's first `POST /sessions/.../prompt` was silently failing for free-tier models in the ~20s window after the relay injector completed. The frontend was sending `providerID: "opencode"` in prompts when the workspace's `agent-config.json` had already been swapped to `opencode-relay`. opencode rejected the request because the source provider was now in `disabled_providers` and the requested free model lived only under `opencode-relay`. The user-visible symptom was a generic "send failed" with no diagnostic.

Root cause: the API server's `modelCache` (5s TTL) stored the pre-injection annotated catalog and served it for the entire TTL window, regardless of whether the underlying `relayInjected` state had flipped to `true` in the meantime. Combined with agentd's own 15s `providerCache`, the bad-data window was up to ~20s.

---

## Assumptions (stated + validated)

1. **`models[]` and `currentModelProviderID` in a single response come from the same `annotated` slice and must agree.** → Validated by tracing `models_handler.go:159-165` — both fields are derived from `annotated`, which is either fully cached or fully fresh. A response with `models[]` disagreeing with `currentModelProviderID` is impossible (I initially claimed otherwise in the issue body — corrected in `issuecomment-4846086399` after pulling the full response body of request `2d0c6aac` and confirming all entries were stale together).
2. **The cache was the only source of staleness.** → Validated: pre-fix code at `models_handler.go:99-107` reads payload-or-nothing from cache, and on cache hit serves it without any check against the live relay state.
3. **`relayChecker` is cheap enough to invoke on every cache hit.** → Validated by inspecting `pkg/agentd/readyz.go` — agentd's `/v1/readyz` reads its 15s `providerCache` in the steady state, so the round-trip is dominated by in-cluster network latency (~1ms). The worst case is one extra round-trip per cache-hit request when the relay subsystem is enabled.
4. **The reverse `true→false` transition is symmetric.** → Validated by the regression test `TestListModels_CacheInvalidatedOnReverseTransition`: seeding the cache with `RelayInjected: true` and letting the live checker return `false` evicts the entry and re-annotates correctly.

---

## Work Completed

### Investigation (with timeline-precise repro from production)

- Pulled the full body of the failing `/models` response (`request_id=2d0c6aac` at T+28s after pod start) from the prod API logs. Confirmed the entire snapshot was internally consistent and stale: `currentModelProviderID:"opencode"`, every free-tier `models[i].providerID="opencode"`, `selected:true` on `big-pickle` under `opencode`.
- Compared against a later response (`1fe01fac` at T+49s) showing all fields had flipped to `opencode-relay`.
- Located the cache layer at `api/internal/handlers/models_handler.go:96-107` and the agentd-side TTL at `workspace-agentd/relay_injector.go` / `pkg/agentd`. Composed window: API 5s + agentd 15s = ~20s.

### Fix

- `api/internal/handlers/models_handler.go`: added a cache-hit guard that calls `relayChecker` and compares against the cached payload's `RelayInjected`. On mismatch, evict the entry and force the fresh-fetch path. Carries the `livenessChecked + liveRelayInjected` pair into the fetch block so a transition never double-calls `relayChecker` (the fetch path's own check at `:148` reuses the result).
- The guard is gated on `cacheHit && relayActive && relayChecker != nil` so it has zero cost when the relay subsystem is disabled.

### Tests (TDD)

Wrote the failing test first; verified red; implemented fix; verified green.

- **`TestListModels_CacheInvalidatedOnRelayInjectedTransition`** (root-cause regression): seed cache with `RelayInjected: false`, wire live checker returning `true`, assert response reflects `opencode-relay`. Initial run failed with `expected "opencode-relay", actual "opencode"` against the pre-fix code.
- **`TestListModels_CacheSteadyState_NoRefetchWhenInjectedMatches`** (added in review): seed cache with `RelayInjected: true`, wire live checker returning `true`, assert `agentClient.ListModels` is NOT called (instrumented the test server with an `atomic.Int64` counter). Adversarial validation: flipped the guard's `!=` to `==` and confirmed this test caught the always-evict regression.
- **`TestListModels_CacheInvalidatedOnReverseTransition`** (added in review): seed cache with `RelayInjected: true`, wire live checker returning `false`, assert response reflects the non-relayed state (`providerID: "opencode"` on the free model).

---

## Decisions

- **Layer 1 (cache invalidation on transition) over Layer 2 (lower API TTL to 1-2s).** Layer 1 closes the window cleanly; Layer 2 only reduces it. Layer 1 also degrades to no-op when the relay is disabled.
- **Withdrew the frontend cross-check Layer 2 proposal from the original issue.** It would not have helped: the response is internally consistent during the stale window (models[] and currentModelProviderID agree). I posted the correction at `issues/467#issuecomment-4846086399` before writing code that fixed the wrong layer.

---

## Follow-ups

- **`scrapeRouterMetrics` silent error swallowing** (`api/internal/handlers/relay_admin.go:634-645`) — same anti-pattern in three places, surfaced during #466 review. Pre-existing, out of scope for this fix. Should open a separate issue.

---

## Linked

- Issue: #467
- PR: #472
- Related PRs in same investigation: #470 (RBAC create-secrets), #471 (relay-router NetworkPolicy)
