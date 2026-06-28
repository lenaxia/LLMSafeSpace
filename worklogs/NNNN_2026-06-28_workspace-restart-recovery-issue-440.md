# Worklog: Workspace agent restart recovery (issue 440)

**Date:** 2026-06-28
**Session:** Investigate and fix issue 440 — "Adding credentials to a running workspace silently aborts in-flight sessions"
**Status:** Complete

---

## Objective

Resolve issue 440: a credential add to a running workspace with an active session produced a silent hang. The user's chat became unresponsive with no recovery path and no explanation.

---

## Investigation

### Validated against source (opencode v1.15.12, the pinned version)

1. **The issue's root-cause diagnosis is false.** It claims `abortStaleSessions` kills in-flight sessions because "opencode persists session state in SQLite... the in-flight session is left with a busy flag." Verified at opencode v1.15.12: the `session` table (`packages/opencode/src/session/session.sql.ts:14-58`) has **no** busy/status/state column. Session busy state is an in-memory `Map` (`packages/opencode/src/session/run-state.ts:27-43`), wiped on every process start.
2. **`abortStaleSessions` is a no-op on every idle session.** `run-state.ts:62-75` at v1.15.12: `if (!existing || !existing.busy) { status.set(...idle); return }`. The `aborted=N` count in the issue's pod logs counts 2xx POST responses, not interrupted sessions. Sessions survive opencode restarts fine: `message-v2.ts:847-858` synthesizes a tool_result for any orphaned tool_use, and the prompt handler has no busy gate (`handlers/session.ts:286-301`).
3. **The restart is load-bearing and correct.** opencode freezes provider credentials and the SDK client in `InstanceState` at process start and never invalidates them; env is frozen at fork. We cannot modify opencode (constraint). So the restart is a fixed fact, not a design choice to optimize away.
4. **The real defect is outage visibility and recovery, not session death.** An in-place opencode restart creates a ~5–10s window where the pod IP is unchanged but opencode is dead. The proxy returns 503+Retry-After on the dead socket (already exists, `api/internal/handlers/proxy.go:377-390`), bufferable writes are already buffered (`proxy.go:304-375`), SSE EOF already emits `agent_died` (US-44.1), and the frontend already auto-reconnects. The gaps were: (A) the frontend dropped the message on 503 instead of retrying, and (B) SSE reconnect did not resync the transcript from authoritative history.
5. **Level correction.** The initial design proposed a multi-component subsystem (RestartCoordinator, readyz-pull, three new SSE events, CRD condition, SDK helper, MCP error mapping). Disproved the load-bearing assumption: agentd's readyz is a debounced liveness probe (3 failures × 5s — `healthz_cache.go:14-18`), never trips during a ~10s restart. The whole subsystem was over-built on an unvalidated signal. The correct level is: remove the harmful code, and close the two frontend gaps on signals that already exist.
6. **Generic, not credential-specific.** opencode restarts for many reasons (OOM, crash, relay injection, credential reload) that all produce the same `agent_died` + 503 signals. Fixing the recovery path closes the entire class, not just 440's trigger.

### Adversarial review of the false diagnosis (Rule 7)

`stale_sessions.go:10-17` documents a mechanism that does not exist. Keeping no-op code built on a disproved assumption violates Rule 5 ("Zero Technical Debt") and Rule 7 ("failed validations are findings"). The `abortStaleSessionsAfterStart` onStart callback also imposes a 30s health-check stall because `healthCheckURL` points at opencode (port 4096, serves SPA + requires basic auth) instead of agentd's readyz (port 4098). Removing both is unconditionally correct.

---

## Work Completed

### Backend (agentd) — remove false-premise code + fix misconfigured probe

- **Deleted `abortStaleSessions` and `abortStaleSessionsAfterStart`** (`cmd/workspace-agentd/stale_sessions.go`). They solved a non-problem (no busy flag in SQLite) and imposed a 30s health-check stall on every restart via a probe that can never succeed.
- **Removed the `onStart` wiring** in `startManagedProcess` (`cmd/workspace-agentd/main.go`). The `onStart` field on `managedProcess` is retained for future use; the supervisor still fires it (no-op when nil).
- **Fixed `healthCheckURL`** (`cmd/workspace-agentd/managed_process.go`): `http://localhost:4096/v1/readyz` → agentd's own `http://localhost:4098/v1/readyz` with the Bearer admin token. Port 4096 is opencode (serves SPA HTML + requires basic auth on every endpoint), so the probe has always failed. The post-restart health probe (`healthProbeAfterRestart`) now actually succeeds when opencode is healthy.

### Frontend — close the two recovery gaps

- **A: 503 with `retryAfter` now retries the send.** `frontend/src/hooks/useChatStream.ts` previously caught only 429 (rate-cap) and dropped every other status with `setError("Failed to send message")` — including the workspace-restart 503. Added a 503 branch mirroring the 429 path: read `retryAfter`, set a transient banner state, retry the send after the delay. Bounded to a small max-attempt count so the loop cannot spin forever.
- **B: SSE reconnect now resyncs the transcript.** `handleSSEReconnect` in `frontend/src/pages/ChatPage.tsx` previously only invalidated workspace-status and refreshed the queue — it did not reconcile the message transcript. opencode history is authoritative (validated #2 above), so reconnect now calls the existing `reconcileOnIdle()` to refetch history and clear stale local state. Idempotent by design.
- **Softened the `agent_died` banner copy** since reconnect now actively recovers: "Agent is restarting — reconnecting…" replaces the passive "will recover or show as idle shortly" wording.

---

## Key Decisions

1. **Remove rather than gate `abortStaleSessions`.** Per Rule 5. If opencode ever persists busy state (it doesn't at v1.15.12), the removal is trivially reverted. Keeping no-op code "in case" is the exact anti-pattern Rule 5 forbids.
2. **Mirror the existing 429 retry path for 503 rather than introducing a new component.** The 429 path is already tested and understood; a new "RestartCoordinator" would duplicate a signal the connection error already provides.
3. **Reuse `reconcileOnIdle` on SSE reconnect rather than writing new resync logic.** It already refetches authoritative history and clears stale local state. opencode is the source of truth; the resync is idempotent.
4. **No new SSE events, no CRD condition, no SDK/MCP contract changes.** `agent_died` + 503+Retry-After are sufficient signals. The fix's value exceeds the triggering issue (it covers OOM/crash/relay restarts for free), which is the signature of correct abstraction.

---

## Assumptions (validated, Rule 7)

| # | Assumption | Validation |
|---|---|---|
| S1 | opencode 1.15.12 cannot hot-reload credentials without restart | Read `provider/provider.ts` InstanceState build path; provider cache never invalidated, env frozen at fork |
| S2 | opencode session history survives a hard restart intact | Read `message-v2.ts:847-858`; orphaned tool_use gets a synthesized tool_result |
| S3 | A new prompt after an interrupted turn works correctly | Read `handlers/session.ts:286-301`; no `assertNotBusy` on prompt path |
| S4 | `abortStaleSessions` is a no-op on every idle session | Read `run-state.ts:62-75` at v1.15.12 tag |
| S5 | agentd (port 4098) stays alive across in-place opencode restarts | agentd is the supervisor; it SIGTERMs opencode, not itself |
| S6 (disproved) | agentd readyz reflects opencode liveness during restart | **FALSE.** `healthz_cache.go:14-18`: 3-failure × 5s debounce never trips during a ~10s restart. readyz is unfit as a reactive restart signal; design does not depend on it. |
| S7 | `agent_died` SSE event is emitted on stream EOF | US-44.1, `proxy_terminal_events_test.go` |
| S8 | The request buffer handles bufferable writes across the restart window | `proxy.go:304-375`, tested |
| S9 | The API is the initiator of credential-reload restarts | `reloadSecretsHandler` → `proc.restart()` |

---

## Adversarial Review (Rule 11)

- **Could the 503 retry loop spin forever?** Bounded to a small max-attempt count (mirrors the spirit of the existing `streamTimedOut` cap). After exhaustion, surfaces a hard error.
- **Does removing `onStart` lose a needed hook?** `onStart` currently only runs `abortStaleSessionsAfterStart` (`main.go:159-161`). The hook field stays on `managedProcess` for future use; nil is a documented no-op.
- **Is `agent_died` reliable enough to drive recovery?** Emitted on SSE EOF (US-44.1, tested). The one accepted false-positive (clean SSE close) triggers a harmless idempotent reconnect+resync.
- **Non-bufferable SSE cut mid-token?** Irrecoverable for that stream (tokens already sent are kept in opencode history; the turn is partial-but-persisted). The reconciled history surfaces the partial turn. Acceptable — matches opencode's own interruption semantics.

---

## Testing

- Backend: existing `stale_sessions_test.go` removed alongside the source; `managed_process_test.go` updated for the new `healthCheckURL` default.
- Frontend: `useChatStream.test.ts` extended with a 503-retry case; `ChatPage.reconnect.test.tsx` extended to assert `reconcileOnIdle` fires on reconnect.
- Go and frontend typecheck/lint/test run green.

---

## References

- Issue 440 — original report (false diagnosis: "abortStaleSessions kills sessions")
- `cmd/workspace-agentd/stale_sessions.go` — removed (false premise)
- `cmd/workspace-agentd/managed_process.go:107` — `healthCheckURL` fix
- `api/internal/handlers/proxy.go:377-390` — existing 503+Retry-After on dead socket (unchanged)
- `frontend/src/hooks/useChatStream.ts:108-115` — 503 retry branch added
- `frontend/src/pages/ChatPage.tsx:692-697` — `handleSSEReconnect` now calls `reconcileOnIdle`
- Worklogs 0030, 0033 — prior silent-assumption-drift bugs in this area (cited in README-LLM.md §7)
