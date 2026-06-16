# Worklog: Stuck sessions after opencode OOMKill — clear stale activeSess on SSE reconnect

**Date:** 2026-06-16
**Session:** Diagnosed and fixed permanently-stuck sessions after pod OOMKill
**Status:** Complete

---

## Objective

Two production sessions reported "session is busy; retry after idle" indefinitely:
- `ses_13076538bffeYtLrhoZ2ccRM1E` (workspace `a847faa5-19b4-463d-a434-1ce473a16f93`)
- `ses_130c14344ffeVF52UQ6QGPmB0P` (workspace `8154ae86-d7b7-4f53-b046-d8d3b462b972`)

Frontend showed "server busy" with no recovery path other than manual workspace pod restart or full API rollout.

---

## Work Completed

### Investigation

Reproduced from API logs and direct workspace pod inspection:

1. **The 409 path:** Frontend POST to `/api/v1/workspaces/:id/sessions/:sid/prompt` returned `409 Conflict` with body `{"error":"session is busy; retry after idle","retryAfter":1}`.
2. **Source of the 409:** `proxy_handlers.go:78` checks `h.isSessionActive(wid, sid)` which reads `proxy.go:67`'s `activeSess` per-replica in-memory map.
3. **opencode said idle:** Direct query to opencode `/session` returned the session with no `time.completed` on the last assistant message (truly orphaned), but agentd's `/v1/statusz` reported `status: "idle"`. CRD also reported `idle`. So opencode and agentd agreed the session was idle.
4. **API server disagreed:** The API's `activeSess` map still contained `ws-1 → {ses_xyz: true}`. POST returned 409 from this stale entry.

### Timeline (workspace `a847faa5`, session `ses_13076538bffeYtLrhoZ2ccRM1E`)

- **08:46:35 UTC**: Session was actively running a `go build` tool call. opencode emitted `session.status=busy`. API's `onSessionActive` added the session to `activeSess`.
- **08:47:44 UTC**: Pod OOMKilled (exit code 137, memory limit 2 GiB exceeded). 16 concurrent sessions accumulated ~1.6M context tokens.
- **No idle event emitted**: opencode died with the assistant turn mid-stream. The `session.status=idle` event was never sent.
- **Pod restarted ~11h later, agentd recovered**: Agentd's `abortStaleSessionsAfterStart` aborted the orphaned message in opencode's SQLite, but the API's `activeSess` map still had the stale entry.
- **API's SSE reconnect ran `reconcileStrandedQueues`**: This existing function only fixed sessions with messages in the Redis queue (`proxy_events.go:549`: `if err != nil || n == 0 { continue }`). Sessions with empty queues were skipped — including this one.
- **Result**: Stale `activeSess` entry persisted indefinitely. Every POST returned 409.

### Root cause

`reconcileStrandedQueues` was scoped narrowly to fix one specific class of bug (SSE reconnect missing an idle event for queued messages). The broader bug — the `activeSess` map can become stale for any reason and is never reconciled against opencode's actual state — was not addressed.

The `activeSess` map is an in-memory per-replica view that must be kept in sync with opencode via SSE events. Any time opencode dies between emitting `session.status=busy` and `session.status=idle` (OOM, SIGTERM, crash), the map enters a stale state with no automatic recovery.

### Fix

Renamed `reconcileStrandedQueues` → `reconcileSessionState` and broadened its responsibility to reconcile two distinct classes of state drift on every SSE reconnect:

1. **Stale `activeSess` entries** (NEW): For any session reported as `idle` by opencode's `/v1/statusz` but `active` in our local `activeSess` map, remove the stale entry and publish `session.status=idle` to connected clients so their UI updates.
2. **Stranded queues** (existing): For any session with messages in the Redis queue but `status=idle` per opencode, trigger `onSessionIdle` to drain.

Both classes are reconciled in the same loop over `statusz.Sessions`. The two are independent: a session can have a stale `activeSess` entry without a queue, and vice versa. The fix runs both blocks for each idle session.

### Recovery semantics

The fix triggers on every SSE reconnect, which happens:
- When the SSE stream's idle timeout fires (5 minutes per `tracker.go:20`)
- When the SSE connection drops (network, agentd restart, opencode restart)
- When the API replica restarts and the watcher reseeds

Worst case recovery: ~5 minutes from the bug condition to next reconnect cycle.

### Tests written

Three new tests in `proxy_queue_drain_miss_test.go`:

| Test | What it proves |
|------|----------------|
| `TestReconcileSessionState_ClearsStaleActiveSess` | Reproduces today's incident: mix of stale-active and legitimately-busy sessions; verifies stale entry is cleared while active one remains |
| `TestReconcileSessionState_NoStalenessNoOp` | Guards against accidentally clearing legitimately-active sessions when no staleness exists |
| `TestReconcileSessionState_PublishesIdleEventOnStaleClear` | Verifies that clearing a stale entry publishes `session.status=idle` to subscribers (added in response to PR review) |

The third test was added in response to automated PR review noting the SSE event publication had no test coverage. Used the existing broker subscription pattern from `TestReconcileStrandedQueues_Non200Statusz`.

### Why not move activeSess to Redis now

The proper fix is Epic 45 (Multi-Replica State Consistency): move `activeSess`, `pwCache`, `wsConfig`, etc. from per-replica in-memory maps to Valkey. That work is ~12 days. This worklog's fix is a quick patch to stop the bleeding while Epic 45 is in design.

The Redis migration will eliminate this class of bug entirely (cluster-wide consistency, automatic TTL expiration). Until then, this reconcile-on-reconnect approach is the minimum-viable fix.

---

## Files Changed

- `api/internal/handlers/proxy_events.go` — Renamed `reconcileStrandedQueues` to `reconcileSessionState`, added stale-activeSess cleanup logic
- `api/internal/handlers/proxy_lifecycle.go` — Updated SSE callback wire-up
- `api/internal/handlers/proxy_queue_drain_miss_test.go` — Updated existing test references, added 3 new tests

---

## Lessons Learned

1. **Per-replica in-memory state is inherently fragile**: Without a reconciliation loop against an authoritative source, drift accumulates. Today the source of truth was opencode (queryable via `/v1/statusz`); the bug was that we only used it for one narrow purpose.

2. **Function naming reflects scope**: `reconcileStrandedQueues` told us what the function did *initially*. When responsibilities grew, the name should have grown with them. The rename to `reconcileSessionState` makes the broader scope clear.

3. **Test coverage requires asserting on side effects, not just state changes**: The first version of `TestReconcileSessionState_ClearsStaleActiveSess` only asserted on `isSessionActive` (the state). The PR reviewer correctly flagged that the SSE event publication (a side effect) had no test. The fix: subscribe to the broker before triggering reconcile, assert the expected event arrives.

4. **Quick fixes should reference the proper fix**: Epic 45 is the right long-term solution. This worklog explicitly calls that out so future readers don't think this reconcile pattern is the intended design.

---

## Related

- **Epic 44** (Session Reliability & Transparency): broader work on session lifecycle observability, includes OOM detection and memory pressure warnings.
- **Epic 45** (Multi-Replica State Consistency): proper fix — externalize per-replica state to Valkey, eliminating this class of bug entirely.
- **PR #197**: This worklog's code change.
