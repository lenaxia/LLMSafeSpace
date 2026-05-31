# Epic 22: agentd Health-Endpoint Redesign

**Status:** Planning
**Created:** 2026-05-31 · **Last revised:** 2026-05-31 (audit pass 2 — major rework)
**Priority:** Medium-High
**Depends on:** none
**Related epics:**
- **Epic 21** — Workspace state machine (Change B's exponential backoff). Epic 22 reduces the rate at which Epic 21's backoff is invoked. Should land **before** Epic 21 to remove the first-order driver.
- **Epic 23** — Controller race hardening. Independent.

This epic owns the agentd health-endpoint surface. Three endpoints exist today (`/v1/healthz`, `/v1/readyz`, `/v1/statusz`); all three call out to opencode and starve under SSE load. Three K8s-level consumers depend on these endpoints (kubelet liveness probe, kubelet readiness probe, controller deep-introspection probe). The redesign re-scopes each endpoint to its proper responsibility and adds caching where it makes the path more robust without sacrificing correctness.

---

## Stated assumptions (each validated below)

| # | Assumption | Type |
|---|---|---|
| A1 | The pod spec includes a kubelet liveness probe pointing at `/v1/healthz` and a kubelet readiness probe pointing at `/v1/readyz` | Code-verified |
| A2 | The controller's `checkAgentHealth` polls `/v1/statusz` (not `/v1/healthz`) on a `healthCheckInterval = 15s` cadence with a 5s HTTP timeout | Code-verified |
| A3 | All three endpoints today make at least one HTTP call to opencode per request (`/global/health` for IsHealthy, `/provider`/`/config/providers`/`/session` for cachedState) | Code-verified |
| A4 | The handler at `/v1/statusz` holds a mutex (`cache.mu`) while making the three opencode calls inside `cachedState`, serializing all `/v1/statusz` requests behind one slow upstream call | Code-verified |
| A5 | The `connectedCacheTTL = 15 * time.Second` cache covers only the providers + sessions queries; `IsHealthy` is NOT cached today | Code-verified |
| A6 | When opencode is busy under SSE load, all four opencode HTTP endpoints can take longer than 5 seconds to respond, causing every health-checking endpoint in agentd to time out | Empirically verified (worklogs 0096, 0099, 0100) |
| A7 | The K8s pod spec is built in `controller/internal/workspace/controller.go:678-695`; changing the probe paths requires editing this file, not a Helm chart | Code-verified |

Hypotheses that did NOT survive verification:

- **R1**: "Adding a new `/v1/livez` endpoint solves the problem without touching existing endpoints." Refuted: the kubelet probes use `/v1/healthz` and `/v1/readyz`, both of which today call opencode. Adding a fourth endpoint and migrating only the controller's probe leaves the kubelet probes unfixed. The redesign must address all three existing endpoints.

---

## Verified ground truth

| # | Validates | Fact | How verified |
|---|---|---|---|
| F1 | A1 | LivenessProbe path is `/v1/healthz`; ReadinessProbe path is `/v1/readyz`; both target `agentd.AgentdPort` | `controller.go:680-694` |
| F2 | A1 | LivenessProbe: `InitialDelaySeconds=15, PeriodSeconds=30, TimeoutSeconds=5, FailureThreshold=6` → kubelet kills pod after ~3 minutes of failed probes. ReadinessProbe: `InitialDelaySeconds=10, PeriodSeconds=15, TimeoutSeconds=3, FailureThreshold=5` → kubelet stops sending traffic after ~1 minute of failed readiness | `controller.go:685, 694` |
| F3 | A2 | `checkAgentHealth` builds URL `http://%s:%d/v1/statusz`; `healthCheckInterval=15s`; `healthHTTPClient.Timeout=5s` | `controller.go:992, 952, 959` |
| F4 | A3 | `/v1/healthz` calls `client.IsHealthy` (which HTTP-GETs `/global/health` on opencode); `/v1/readyz` calls `cachedState` (3 opencode calls) and `client.IsHealthy`; `/v1/statusz` calls `client.IsHealthy` and `cachedState` | `cmd/workspace-agentd/main.go:374, 392-394, 408-409` |
| F5 | A4 | `cachedState` does `cache.mu.Lock(); defer cache.mu.Unlock()` then makes `client.ConnectedProviders`, `ConfiguredProviderCount`, `ListSessions` calls | `main.go:273-285` |
| F6 | A5 | `connectedCacheTTL = 15 * time.Second`; cache hit path skips the 3 opencode calls but the IsHealthy call is outside the cache | `main.go:270, 275, 282-284` |
| F7 | A6 | Worklog 0096 documented `/v1/statusz` timing out under SSE load; worklog 0100 documented identical behavior in the 2026-05-31 incident | Worklogs |
| F8 | A7 | Pod template builder at `controller.go:678` is in-controller (not Helm) | `grep buildPod controller.go` |

---

## Problem Statement

### Today's behavior under SSE load (verified)

When a workspace's opencode session is mid-stream — user is chatting, SSE delivering tokens — opencode's HTTP server is back-pressured. The four opencode endpoints agentd queries (`/global/health`, `/provider`, `/config/providers`, `/session`) all respond slowly. Three things happen in parallel:

1. **Kubelet readiness probe** (`/v1/readyz`, 3s timeout) starts failing. After 5 consecutive failures (~75s) the kubelet marks the pod NotReady; service traffic stops being routed to it. The user's API requests start failing.
2. **Kubelet liveness probe** (`/v1/healthz`, 5s timeout) starts failing. After 6 consecutive failures (~3 minutes) the kubelet kills the pod. The user's session is destroyed.
3. **Controller's `/v1/statusz` poll** (5s timeout, 15s rate-limit) starts timing out. After 3 consecutive failures (~45s) the controller deletes the pod itself, hitting the dying-pod misclassification bug (Epic 23 Story 1) and marking the workspace terminal Failed.

The kubelet liveness probe is the slowest fuse (~3 min) but eventually kills the pod independently of the controller. The controller's `/v1/statusz` poll is the fastest fuse (~45s).

### Why the design is wrong

The four endpoints (`/global/health`, `/v1/healthz`, `/v1/readyz`, `/v1/statusz`) conflate distinct concerns:

| K8s convention | Question it answers | Today's agentd implementation |
|---|---|---|
| Liveness | "Is the workspace-agentd process alive?" | `/v1/healthz` → calls opencode `/global/health` (deeply coupled to opencode) |
| Readiness | "Is the workspace ready to receive traffic?" | `/v1/readyz` → calls opencode 4 times |
| Deep status | "Tell me everything about the workspace" | `/v1/statusz` → calls opencode 4 times under a mutex |

Liveness should report "the agentd process itself is responding" — independent of opencode. Today it doesn't. Readiness should report "the workspace can serve user traffic" — meaningfully includes opencode but should be cheap to evaluate. Today it makes 4 sequential calls.

---

## Scope

**This epic owns:**

- Redesigning `/v1/healthz` to be a TRUE liveness check — answer "is the agentd process alive" with zero opencode round-trips. Always returns < 100ms.
- Redesigning `/v1/readyz` to be a fast readiness check — answer "is the workspace ready for traffic" with at most ONE cached opencode signal. Returns in < 200ms p99.
- Keeping `/v1/statusz` as the deep-introspection endpoint, expensive but correct. Documented as expensive.
- Updating the controller's `checkAgentHealth` to differentiate "is the pod alive" (cheap, frequent) from "do I need session/provider data" (expensive, infrequent).
- Updating the K8s pod spec to make sure kubelet probes use the right endpoint with the right semantics.

**This epic does NOT own:**

- opencode's own performance under SSE load. If opencode is genuinely unresponsive for minutes, that's an opencode bug or workload-design issue, not agentd's concern.
- General agentd refactoring (request handler reorganization, retry helpers, etc.).
- Replacing the in-process mux with two listeners (separate admin port). Documented as a stretch goal in US-22.8 but only if measured to be necessary.

---

## Robustness criteria

Epic 22 is done when **all** of the following hold:

1. **`/v1/healthz` returns 200 in < 100ms p99 even when opencode is fully unresponsive.** Bench: opencode mock returns after 30s; 100 parallel `/v1/healthz` requests; assert all complete < 100ms.
2. **`/v1/readyz` returns in < 200ms p99 with the cache warm; < 1s p99 on cache miss.** Cache refresh strategy is single-flight (one in-flight request blocks duplicates) so a slow opencode doesn't multiply load.
3. **Kubelet liveness probe never restarts a pod solely because opencode is busy.** Verified by chaos test: throttle opencode HTTP server to 30s response time; assert pod stays Running for ≥ 10 minutes.
4. **Kubelet readiness probe correctly marks the pod NotReady when opencode is genuinely unhealthy.** A truly-broken opencode (no `/global/health` response after retries) should result in NotReady within ~75s. Verified by chaos test that mocks opencode dead.
5. **Controller's pod-alive check uses `/v1/healthz` (after redesign) and never times out solely due to opencode load.** Verified: controller's `ConsecutiveHealthFailures` does not increment when opencode is throttled but agentd is alive.
6. **Controller's deep-status poll uses `/v1/statusz` on a slower cadence (proposed: 60s).** Failures of the deep poll do NOT increment `ConsecutiveHealthFailures` — they only mark fields as stale.
7. **`/v1/statusz` continues to work when opencode is fast.** Same response shape; no regression.
8. **No regression to other agentd endpoints** (proxy, upgrade, secrets-reload, etc.) which share the same mux.

---

## Endpoint Specification (post-fix — resolves ambiguity D)

### `/v1/healthz` — Liveness

**Question:** Is the workspace-agentd process alive and responding?

**Implementation:** ZERO opencode calls on the request path. Returns:

```json
{
  "healthy": true,
  "version": "<workspace-agentd build version>",
  "uptime_seconds": <int>
}
```

`healthy` is always `true` if the handler executes (a dead process can't respond, by definition). The field exists for forward compat; it's never set to `false` on this endpoint.

**Status code:** Always 200 (if the process responds at all).

**Performance contract:** p99 < 100ms.

**Consumers:** Kubelet liveness probe; controller's frequent pod-alive check.

### `/v1/readyz` — Readiness

**Question:** Is the workspace ready to receive user traffic?

**Implementation:** Reads cached `IsHealthy` result. Cache is refreshed by a background goroutine on a 5-second interval (eager refresh, not lazy — resolves ambiguity E). On startup, returns `ready=false` until the first refresh completes.

```json
{
  "ready": true,
  "providers_connected": ["opencode"],
  "providers_configured": 1,
  "agent_version": "1.15.12",
  "agent_type": "opencode"
}
```

`ready` is `true` iff:
- The background refresh has completed at least once.
- The most recent `IsHealthy` result was healthy.
- At least one provider is connected.

**Status code:** 200 if `ready`, else 503.

**Performance contract:** p99 < 200ms (only reads in-memory cache).

**Consumers:** Kubelet readiness probe.

**Cache TTL:** 5 seconds. Eagerly refreshed by a goroutine; if a refresh takes longer than the TTL, subsequent requests serve the stale value (with `staleness_seconds` field for observability) and the goroutine continues trying.

### `/v1/statusz` — Deep introspection

**Question:** Give me a full snapshot of the workspace's state — sessions, disk usage, connected providers, agent version.

**Implementation:** Unchanged from today's behavior. Calls opencode multiple times. Documented as expensive in code comment. May take seconds under SSE load.

```json
{
  "healthy": true, "ready": true,
  "connected": ["opencode"], "providers_configured": 1,
  "sessions": [...], "sessions_active": 0, "sessions_error": 0,
  "last_error": "", "agent_type": "opencode", "agent_version": "...",
  "uptime_seconds": ..., "disk": {...}
}
```

**Status code:** 200.

**Performance contract:** No upper bound. Caller must use a generous timeout (the controller's deep-poll uses 30s).

**Consumers:** Controller's deep-status poll (60s interval, drives session-list / disk-usage status fields). API service may also consume but that's existing behavior.

### Endpoint summary

| Endpoint | Calls opencode? | Latency p99 | Caller timeout | Consumers |
|---|---|---|---|---|
| `/v1/healthz` | No (zero round-trips) | < 100ms | 5s (kubelet), 5s (controller) | Kubelet liveness, controller cheap probe |
| `/v1/readyz` | Yes (background refresh, 5s TTL) | < 200ms | 3s (kubelet) | Kubelet readiness |
| `/v1/statusz` | Yes (multiple, under mutex) | unbounded | 30s (controller deep poll) | Controller status enrichment |

---

## Cache Refresh Strategy (resolves ambiguity E)

Two cache decisions:

### `/v1/readyz` cache refresh — eager goroutine

A background goroutine runs `refreshReadiness()` every 5 seconds:
- Calls `client.IsHealthy(ctx)` with a 4-second timeout (less than the 5s refresh interval to avoid pile-up).
- On success, stores the result in an atomic value.
- On failure, increments a stale-counter; the cached value remains served (with staleness signal). After 3 consecutive failures (~15s), `ready` flips to `false`.
- Does NOT use a per-request `singleflight.Group` because the goroutine is the sole caller of opencode for readiness purposes; user requests never trigger a refresh.

This is "eager" rather than "lazy" because:
- Lazy refresh (refresh on first request after TTL expiry) means the first user gets a slow response.
- Eager refresh decouples user-visible latency from opencode's response time entirely.
- The cost is one persistent goroutine and one HTTP call every 5 seconds, which is negligible.

### `/v1/statusz` cache — keep existing 15s TTL, lazy refresh

`/v1/statusz` is expensive by design. Lazy refresh on cache miss is fine because:
- The contract already says "no upper bound on latency."
- Callers expect to wait.
- Adding eager refresh would mean making 4 opencode calls every 5 seconds for an endpoint that's polled every 60 seconds. Wasteful.

The existing `cache.mu` mutex serialization is acceptable for `/v1/statusz` because:
- Concurrent statusz requests are rare (one controller, infrequent API service polls).
- Serialization prevents thundering-herd if many requests arrive together.
- Documented in the code comment for clarity.

### Single-writer goroutine for the cache

To avoid race conditions on the atomic-value `IsHealthy` cache, a single goroutine owns it. Reads from `/v1/readyz` are pure atomic loads. Writes happen only from the refresher goroutine. This eliminates the need for a mutex on the readiness path entirely — different from the legacy `cache.mu` (`/v1/statusz`) which stays as-is.

---

## Story breakdown

| Story | Title | Depends on | Acceptance criteria summary |
|---|---|---|---|
| US-22.1 | Refactor `/v1/healthz` to remove all opencode calls; return process-only liveness | none | Mock-call assertion: `client.IsHealthy` etc. NOT called from /v1/healthz; latency < 100ms even when opencode is throttled |
| US-22.2 | Add eager-refresh `IsHealthy` cache (5s interval, single owner goroutine) | US-22.1 | Cache hit serves in < 10ms; goroutine survives opencode failures; staleness exposed |
| US-22.3 | Refactor `/v1/readyz` to read from cache; remove inline opencode calls | US-22.2 | Mock-call assertion: only the refresher goroutine calls opencode; latency < 200ms p99 |
| US-22.4 | Document `/v1/statusz` as expensive in code comments; no behavioral change | none | Doc string accurately describes the contract; reviewers can audit consumers |
| US-22.5 | Add new controller probe path: `checkAgentLiveness` polls `/v1/healthz` (cheap, frequent) | US-22.1 | Controller has two probe types: liveness (15s) drives `ConsecutiveHealthFailures`; deep-status (60s) drives session-list fields |
| US-22.6 | Refactor controller's `checkAgentHealth` to call deep-status separately and never increment `ConsecutiveHealthFailures` on its failures | US-22.5 | Failures of the deep poll mark fields stale, do NOT trigger pod restart |
| US-22.7 | Verify kubelet readiness probe interaction with the new cached `/v1/readyz` semantics | US-22.3 | Pod transitions to NotReady within 60-90s of opencode dying (cache flips ready=false after 3 × 5s refresh failures = 15s; kubelet then needs `FailureThreshold=5 × PeriodSeconds=15s` = 75s of consecutive 503s, total ~75-90s wall-clock depending on probe phase alignment); a single slow refresh (one that misses the 4s budget but the next succeeds) MUST NOT flip ready=false |
| US-22.8 | (stretch) Split mux into two listeners: admin port (healthz/readyz/statusz) + user port (proxy, upgrade) | none | Heavy SSE proxy traffic on user port cannot starve the admin port at the listener layer; ship only if measured to be needed |

---

## Test plan

### Unit tests (Go test, in-process httptest)

- `/v1/healthz` returns 200 with version and uptime, even when the opencode mock returns errors / hangs / panics.
- `/v1/healthz` does NOT call any of `client.ConnectedProviders`, `ConfiguredProviderCount`, `ListSessions`, `IsHealthy` (verified via mock-call assertions).
- `/v1/readyz` reads from the cache; mock-call assertions verify only the refresher goroutine calls opencode.
- Refresher goroutine: starts on agentd boot; refreshes every 5s; survives opencode errors; updates `staleness_seconds`.
- `/v1/readyz` returns 503 when the cache says `ready=false` (e.g., 3 consecutive refresh failures).
- `/v1/statusz` continues to call all four opencode endpoints (regression — preserves existing contract).
- `/v1/statusz` cache TTL still works as before.

### Integration tests (in-process, real http.Server, real opencode-shaped mock)

- 100 parallel `/v1/healthz` requests when opencode mock takes 30s to respond. All return in < 100ms.
- 100 parallel `/v1/readyz` requests when opencode is responsive. All return in < 200ms; opencode receives only 1 request (cache).
- 100 parallel `/v1/readyz` requests when opencode is slow (15s response). All return < 200ms (serving stale cache); opencode receives 1 request (the refresh goroutine, which times out, gets retried by the next tick).
- 100 parallel `/v1/statusz` requests correctly serialize through `cache.mu` (regression).

### Chaos / failure-injection tests (integration test fixture, controllable opencode mock)

- **Slow opencode:** opencode mock responds with 30s delay. `/v1/healthz` < 100ms; `/v1/readyz` < 200ms (stale signal); `/v1/statusz` blocks (acceptable).
- **Panicking opencode:** opencode mock panics. `/v1/healthz` returns 200; `/v1/readyz` flips to 503 after 3 refresh failures (~15s); `/v1/statusz` returns error after timeout.
- **Workspace-agentd partial deadlock:** simulate a goroutine panic in the SSE tracker. `/v1/healthz` should fail (the agent process IS broken). Verify the K8s liveness probe restarts the pod.
- **Burst load:** 1000 concurrent `/v1/healthz` requests in 1 second. All complete < 100ms; no goroutine leak.

### End-to-end tests (envtest, sharing Epic 21 / Epic 23 test fixture)

- Pod boots with the post-fix probe configuration. Workspace becomes Ready; controller's pod-alive check passes.
- Simulate opencode SSE load. Assert: kubelet does NOT kill the pod (3-min liveness fuse never trips); kubelet does NOT mark NotReady (cache prevents readiness from flapping); controller does NOT increment `ConsecutiveHealthFailures` from `/v1/healthz` polls.
- Simulate genuine opencode death. Assert: kubelet correctly marks NotReady within 75-90s (per US-22.7's calculation); controller's deep poll fails but does NOT trigger pod restart (US-22.6 explicitly blocks `ConsecutiveHealthFailures` increment from deep-poll failures); the workspace remains in `Active` phase until Epic 21's Change B detects the situation through other signals (e.g., absence of activity, kubelet-driven pod readiness changes).

### Contract regression tests

- `/v1/statusz` response shape unchanged: same JSON keys, same value types, same status codes for happy/unhappy paths.
- All existing callers of `/v1/statusz` (controller `checkAgentHealth` post-rename to deep-status, API service status enrichment) continue to function with no code changes other than the controller's new `/v1/healthz` consumer.

---

## Sequencing

US-22.1 (process-only `/v1/healthz`) ships first — it's an isolated change with high impact and zero dependencies.

US-22.2 (refresher goroutine) and US-22.3 (`/v1/readyz` from cache) can ship together since they're tightly coupled.

US-22.4 (statusz docstring) is trivial and can ship anywhere.

US-22.5 + US-22.6 (controller side) ship together once US-22.1 lands.

US-22.7 (verify kubelet readiness behavior) verifies the prior stories under realistic conditions.

US-22.8 (mux split) is a stretch goal; only ships if metrics show the single-mux design is still a bottleneck after US-22.1–22.7.

This epic should land BEFORE Epic 21 because:
- Epic 21's backoff is meant to absorb the SECOND-order effect of agentd starvation.
- Epic 22 removes the FIRST-order cause.
- After Epic 22 lands, Epic 21's metrics (heavy instrumentation from Epic 23 Story 1) should validate whether Change B's proposed schedule is needed at all, or whether the failure rate drops below the threshold of concern.

---

## Out of scope

- opencode's internal SSE handling. If opencode is genuinely unresponsive for minutes, that's an opencode-side issue.
- Adding circuit-breaker-style fallback in agentd. Possible future work.
- A general "fast-path / slow-path" split for every agentd endpoint.

---

## Open questions for review

1. **Cache TTL.** 5s for `/v1/readyz` is a balance of staleness vs load. Acceptable? Shorter (2s) means faster recovery but more load; longer (15s) means more staleness.
2. **Refresher goroutine retry on failure.** Today's design: keep refreshing every 5s regardless of failures, flip `ready=false` after 3 consecutive failures. Should the goroutine have its own backoff on persistent failure?
3. **Mux split (US-22.8).** Worth doing now, or only if measured to be needed? The pod has limited CPU/memory; running two http.Server instances has marginal cost.
4. **Should `/v1/healthz` also gate on a basic agentd self-check** — e.g. "is the SSE tracker goroutine still alive and consuming events"? Or is "process responds to HTTP" sufficient? Argument for: catches silent deadlocks. Argument against: more complexity, more ways for the check itself to fail.
5. **Probe timeout values.** Kubelet liveness `TimeoutSeconds=5` is generous for the new `/v1/healthz` (which should be < 100ms). Should we tighten to 1s? Argument for: faster restart on real death. Argument against: marginal benefit.
