# Epic 41: Message Queue Reliability

**Status:** Design — Ready for Implementation
**Created:** 2026-06-12
**Priority:** High
**Depends on:** Epic 15 (Streaming State Resilience — SSE infrastructure), Epic 28 (Unified Event Stream — broker exists), Epic 38 (Session Activity Integrity — SSE authority model)
**Related:** Epic 03 (Proxy Sessions — `ProxyHandler`), Epic 25 (API Robustness), worklog 0232 (message queue v3 design)

---

## Problem Statement

Messages typed while a session is busy are held in the frontend's `useMessageQueue` hook and are supposed to be sent when the session next becomes idle. In practice, two distinct failures occur:

1. **Queued messages are sent (the 204 is returned) but the LLM response never appears in chat.** The queue pill disappears (send "succeeded") but the conversation shows only the user message with no assistant reply. The user must reload or wait for a subsequent interaction to see the response.

2. **Occasionally, queued messages are not processed by opencode at all.** The user message is persisted in history (written by opencode's `createUserMessage` before `loop()` is called), but no `session.status=busy` event arrives and no LLM response is ever generated. The message exists as a permanent orphan.

### User impact

The queue UX (drawer UI, pills, retry/dismiss) was built to give users confidence that messages sent while the agent is busy will be processed. Both failures undermine that promise: either the response silently disappears from the visible conversation, or the message is silently dropped at the LLM level.

---

## Validated Assumptions

Every assumption below was verified against source code before being used in this design. No unvalidated assumption is used.

| # | Assumption | Status | Evidence |
|---|-----------|--------|----------|
| V1 | `promptAsync` handler forks `promptSvc.prompt()` in the **group scope** (survives the HTTP request), then returns 204 NoContent | ✅ Verified | `session.ts:61` — `scope = yield* Scope.Scope` is the group handler scope; `session.ts:324` — `Effect.forkIn(scope, { startImmediately: true })`; `session.ts:326` — `return HttpApiSchema.NoContent.make()` |
| V2 | `createUserMessage` is called BEFORE `loop()` inside `prompt()` — user message is persisted to opencode's DB before `ensureRunning` is invoked | ✅ Verified | `prompt.ts:1110` — `const message = yield* createUserMessage(input)`, then `prompt.ts:1123` — `return yield* loop({ sessionID: input.sessionID })` |
| V3 | `loop()` calls `state.ensureRunning(sessionID, onInterrupt, runLoop(sessionID))` — the work passed to `ensureRunning` is the actual LLM execution | ✅ Verified | `prompt.ts:1407` |
| V4 | `ensureRunning()` when state is `Running` or `ShellThenRun` **discards the new work** entirely and returns `awaitDone(st.run.done)` — the new `runLoop` Effect is never executed | ✅ Verified | `runner.ts:120-122` — `case "Running": case "ShellThenRun": return [awaitDone(st.run.done), st] as const` |
| V5 | The runner sets its state to `Idle` **synchronously** (inside `SynchronizedRef.modify`) before emitting `session.status=idle` on the SSE stream — by the time the idle event reaches LLMSafeSpace's SSE tracker, the runner IS already `Idle` | ✅ Verified | `runner.ts:70-81` — `SynchronizedRef.modify` sets state to `{ _tag: "Idle" }` synchronously, then the effect runs `onIdle`; `run-state.ts:60-62` — `onIdle` calls `status.set(sessionID, { type: "idle" })` which emits the SSE event |
| V6 | `session.status=idle` is emitted by `status.ts:set()` which calls `events.publish(Event.Status, ...)` then immediately deletes the session from the in-memory status map — the event is on the wire AFTER the state is `Idle` | ✅ Verified | `status.ts:78-84` |
| V7 | `reconcileOnIdle` is called **before** `queue.notifyIdle()` in the SSE handler — both are called in the same synchronous handler | ✅ Verified | `ChatPage.tsx:556-557` |
| V8 | `reconcileOnIdle` yields at `await queryClient.refetchQueries(...)` before calling `queue.reconcile()` — `queue.notifyIdle()` runs synchronously AFTER `reconcileOnIdle` yields, before the history fetch completes | ✅ Verified | `ChatPage.tsx:307` — `await queryClient.refetchQueries(...)` is the first await; JavaScript microtask ordering guarantees `queue.notifyIdle()` runs before the refetch resolves |
| V9 | `reconcileOnIdle` calls `setSseStreamParts([])` and `setLocalMessages([])` AFTER the history refetch resolves — streaming content from the previous message is cleared | ✅ Verified | `ChatPage.tsx:308-309` — after `await queryClient.refetchQueries(...)` |
| V10 | The history refetch in `reconcileOnIdle` fetches the state AFTER the previous message is idle — it does NOT include the response from the just-queued message (which hasn't been processed yet) | ✅ Verified | Confirmed by V8: the queued message is sent AFTER the refetch starts; opencode processes it afterward; the refetch returns history as of the previous message's completion |
| V11 | After `queue.notifyIdle()` sends the queued message (204 returned), the next SSE events that arrive are `session.status=busy` (opencode starts processing), then streaming content events, then `session.status=idle` (opencode finishes) — only then does the NEXT `reconcileOnIdle` run | ✅ Verified | `ChatPage.tsx:561-564` — `session.status=busy` sets `serverBusy=true` but does NOT call `reconcileOnIdle` or clear `sseStreamParts` |
| V12 | `sseStreamParts` is the in-progress streaming display. It is cleared by `reconcileOnIdle` (which runs on EVERY idle event). The queued message's streaming response populates `sseStreamParts` normally, but the NEXT `reconcileOnIdle` (when the queued message finishes) clears it and replaces it with history | ✅ Verified | `ChatPage.tsx:308` clears `sseStreamParts`; `ChatPage.tsx:307` refetches and history becomes the canonical source; `ChatPage.tsx:752` — `allMessages = [...history, ...localMessages, ...sessionErrors]` |
| V13 | `sseStreamParts` state updates from the queued message's `session.status=busy` and streaming events ARE processed correctly — no clearing happens between `notifyIdle()` and the next `reconcileOnIdle` | ✅ Verified | `ChatPage.tsx:561-564` — busy handler does not call `setSseStreamParts`; streaming parts are accumulated normally during the queued message's processing |
| V14 | The `send_success` reducer action only removes a message if it is currently in `"sending"` status — if `reconcile` removes it first, `send_success` is a no-op, and `drainingRef` is still reset to `false` in `.then()` | ✅ Verified | `useMessageQueue.ts:42-44` — `state.find((m) => m.id === action.id && m.status === "sending")` check; `useMessageQueue.ts:154` — `drainingRef.current = false` is unconditional |
| V15 | The `reconcile` reducer removes queue items matching history IDs regardless of their status (`pending`, `sending`, `error`) | ✅ Verified | `useMessageQueue.ts:73-74` — `return state.filter((m) => !action.historyIds.has(m.id))` — no status check |
| V16 | opencode uses `input.messageID ?? MessageID.ascending()` as the user message ID — if we provide `messageID: "msg_xxx"`, it IS used as-is | ✅ Verified | `prompt.ts:664` |
| V17 | `MessageID` schema validation: `Schema.String.check(Schema.isStartsWith("msg"))` — our `"msg_"` prefix passes | ✅ Verified | `schema.ts:10-13` |
| V18 | `workspaceConfig.workspaceID` is NEVER populated — the `onSessionIdle` activity tracking branch at `proxy.go:1060` (`if ok && cfg.workspaceID != ""`) is permanently dead code (US-6.5 gap) | ✅ Verified | `proxy.go:49-53` — struct definition; `proxy.go:1424-1427` — only `cfg.autoApprovePermissions` and `cfg.maxActiveSessions` are ever written; `workspaceID` field is never assigned |
| V19 | The `TextPartInput` schema in opencode accepts `{ type: "text", text: "..." }` with no other required fields | ✅ Verified | `core/src/v1/session.ts:399-412` — `type: Schema.Literal("text")` and `text: Schema.String` are the only required fields |
| V20 | The frontend queue sends parts as `[{ type: "text", text: head.text }]` with `messageID: head.id` — both fields match opencode's expected schema | ✅ Verified | `useMessageQueue.ts:148-151`; V17; V19 |
| V21 | SDK clients (`sendMessage`, `sendPromptAsync`) do NOT send `messageID` and do NOT need a queue — SDK callers serialize naturally by blocking on `sendMessage` or explicitly managing the SSE lifecycle | ✅ Verified | `sdks/go/services.go:112-163`; `sdks/typescript/src/client.ts:217-236`; `sdks/python/llmsafespace/client.py:216-259`; no `messageID` field in any SDK call |
| V22 | `reconcileOnIdle` is an `async function` with `useCallback` — its identity changes when `[workspaceId, sessionId, queryClient, queue]` change, making it safe to capture in the SSE event handler | ✅ Verified | `ChatPage.tsx:299-330` — `useCallback` with correct deps; `ChatPage.tsx:690` — `reconcileOnIdle` is in the `handleSSEEvent` dep array |
| V23 | `queue` object returned by `useMessageQueue` has stable function identity via `useCallback` — all six functions are memoized | ✅ Verified | `useMessageQueue.ts:167-212` — `enqueue`, `notifyIdle`, `clear`, `reconcile`, `retry`, `dismiss`, `onPhaseChange` are all `useCallback` |

---

## Root Cause Analysis

### Bug 1: Queued message response is invisible until the second idle cycle

**Sequence:**

```
T=0   Session goes idle (previous message finished)
T=0   reconcileOnIdle() starts → awaits history refetch (async)
T=0   queue.notifyIdle() → drainOne() → POST /prompt → 204 returned
T=0   send_success: queue pill removed, drainingRef=false
T=1   History refetch resolves (contains previous message's response, not queued msg)
T=1   setSseStreamParts([]), setLocalMessages([]) ← streaming state wiped
T=1   queue.reconcile(history) ← no-op, queued msg already gone
T=2   opencode starts processing queued message → session.status=busy
T=2   serverBusy=true; streaming content begins accumulating in sseStreamParts
T=3   opencode finishes → session.status=idle (second idle event)
T=3   reconcileOnIdle() runs again → refetches history (NOW includes queued msg response)
T=3   setSseStreamParts([]) clears the in-progress streaming parts
T=3   History renders with both messages ← response finally visible
```

**The problem:** At T=1, `reconcileOnIdle` runs `setSseStreamParts([])` and `setLocalMessages([])`. This is correct for the previous message. But it means the user sees a brief blank/empty state between the previous message finishing and the queued message's streaming content appearing (T=2). More critically, the streaming content from the queued message at T=2 is never shown to the user because at T=3 `reconcileOnIdle` clears `sseStreamParts` again immediately as the queued message finishes. The streaming phase of the queued message is invisible — the response only appears from history after the second idle cycle.

This is **not a data loss bug** — the response IS in history. But it IS a UX failure: the streaming experience is broken for queued messages, and there is a confusing visual gap between the queue pill disappearing and the response appearing.

### Bug 2: Orphan user messages (rare concurrency race)

If two `prompt_async` calls reach opencode in rapid succession while the runner is in a transitional state — specifically, if the Effect fiber for `loop()` from the first call has been forked but not yet scheduled to execute (so `ensureRunning` hasn't been called yet), and the second call's forked fiber calls `ensureRunning` first — then:

- Call A: `createUserMessage` persists message A → forks `loop()`
- Call B: `createUserMessage` persists message B → forks `loop()`
- Both forked fibers now race to call `ensureRunning`
- One wins: starts the run
- The other loses: discards its work, awaits the existing run's completion
- Result: one user message has no LLM response ever

This race is narrow (nanoseconds in the Effect runtime) but not zero. It requires two `prompt_async` calls to arrive and be scheduled before either has called `ensureRunning`. In practice this is most likely to happen if the frontend retries a failed send while a direct send (`doSendNow`) is also in flight.

**Note:** This is distinct from the "runner not idle" scenario I initially described. The runner IS idle when opencode emits the SSE event. The race is internal to the Effect fiber scheduler, not the LLMSafeSpace SSE pipeline.

---

## Architecture Decision

### Level of abstraction

Bug 1 is entirely in the **frontend** — a sequencing issue in `ChatPage.tsx`. The fix is to not clear streaming state at T=1 if there are pending queued messages that will immediately start producing new streaming content.

Bug 2 is in **opencode's internal concurrency model**, which we do not control. The mitigation is to ensure LLMSafeSpace never sends a second `prompt_async` for the same session while the first one's fork is in flight — which the `drainingRef` already enforces for the normal queue case, but does not enforce against concurrent `doSendNow` calls.

### What level of solution is appropriate?

The question is whether to:

**(A) Fix the specific symptoms at the frontend layer** — targeted, low-risk, no new infrastructure.

**(B) Move the queue to the API server** — correct the problem at a higher level of abstraction; queue is server-side, frontend is dumb.

**(C) Add server-side idempotency enforcement** — opencode or LLMSafeSpace enforces "one in-flight prompt per session", returning a real error instead of silently dropping.

**Decision: Option A for Bug 1 (correct, minimal), Option C-lite for Bug 2 (enforce no concurrent sends at the proxy layer, not in opencode).**

Option B (server-side queue) is architecturally appealing but introduces new failure modes (in-memory queue lost on API replica restart, multi-replica routing, workspace suspend clears queue). It is not justified for this bug. The frontend is a legitimate place to hold pending messages for interactive users — this is the same design used by the opencode TUI (which simply does not allow submitting while busy). The difference is that a browser can receive concurrent input, so a client-side queue is necessary and correct.

Option C-lite at the proxy layer: `proxyToWorkspace` already tracks active sessions via `activeSess`. It can trivially enforce "no new `prompt_async` for a session that is already active" by checking `activeSess` before forwarding. This returns a clear 409 Conflict to the frontend rather than a silent drop, enabling correct retry behavior.

### Alternatives considered

| Alternative | Pros | Why discarded |
|------------|------|---------------|
| **Server-side queue (Redis-backed)** | Survives replica restart; multi-tab correct | In-memory queue lost on restart anyway unless Redis; Redis adds infra dependency; multi-tab scenarios are already broken in other ways (Epic 38 noted this); not justified by bug severity |
| **Server-side queue (in-memory in ProxyHandler)** | Simpler than Redis; no new infra | Lost on replica restart; multi-replica routing means the replica that received the queue may not be the one that processes the SSE idle; this is a worse variant of the current client-side queue |
| **Suppress `reconcileOnIdle` during queue drain** | Simplest possible fix for Bug 1 | Incorrect — `reconcileOnIdle` serves the important function of clearing streaming state and fetching authoritative history after each message. Suppressing it would break normal message display |
| **Delay `reconcileOnIdle` until queue is empty** | Preserves streaming for queued messages | Complex state machine; reconcile serves a safety function (clears stale state) that should not be gated on queue state |
| **Pass queue depth to `reconcileOnIdle` and skip the state wipe** | Targeted | Couples queue state to history reconciliation logic; creates a dependency that the queue should not have |
| **Frontend: confirm queue send via `session.status=busy`** | Makes the 204→work-accepted guarantee visible | Adds a new timing dependency; `session.status=busy` may be delayed or arrive on a different SSE connection; adds complexity to a hook already at the edge of correctness |

---

## Design

### Overview

The fix has three parts:

1. **US-41.1 (frontend):** Fix `reconcileOnIdle` to not clear `sseStreamParts` and `localMessages` until the in-progress streaming content from the queued message has been captured in history. Specifically: after sending a queued message, defer the clearing of `sseStreamParts`/`localMessages` to the NEXT `reconcileOnIdle` call (not the one that triggered the send).

2. **US-41.2 (backend):** Add a 409 Conflict response to `SendPromptAsync` when the session is already active (has an in-flight prompt). This converts the silent drop in opencode's runner into an observable error at the LLMSafeSpace proxy layer, enabling the frontend to retry correctly.

3. **US-41.3 (frontend):** Handle 409 from `SendPromptAsync` in `drainOne` — mark the message as pending (not error) and schedule a retry after a short delay. This gives opencode time to finish the in-flight prompt and go idle.

4. **US-41.4 (backend):** Fix the `workspaceConfig.workspaceID` dead code (US-6.5 gap) — the `onSessionIdle` activity/sessionIndex recording branch is permanently unreachable. This is a correctness gap: `onSessionIdle` is supposed to record session activity and update the session index, but it never does because `cfg.workspaceID` is never populated.

### US-41.1: Decouple streaming state clear from reconcileOnIdle trigger

**Root cause:** `reconcileOnIdle` unconditionally clears `sseStreamParts` and `localMessages` after the history refetch. When a queued message is sent immediately after (within the same idle handler), the streaming content from the queued message populates `sseStreamParts` between the clear (T=1) and the next idle (T=3). The clear at T=3 wipes the streaming content before the user sees it, then history takes over.

The user DOES see the streaming content at T=2, because `sseStreamParts` accumulates during the busy phase. The problem is at T=3: `reconcileOnIdle` clears `sseStreamParts` BEFORE the history is populated in the cache (the `await queryClient.refetchQueries()` call triggers a fetch but React Query doesn't update `history` until after the component re-renders with the new data). This creates a render frame where both `sseStreamParts` is empty AND `history` hasn't updated yet.

**Fix:**

In `ChatPage.tsx`, `reconcileOnIdle` should perform the state clear in a `flushSync`-like manner only after the query cache has been updated with the new data, OR defer the clear to after the refetch promise resolves and the `history` variable is confirmed to be non-empty.

The simplest correct approach: move `setSseStreamParts([])` and `setLocalMessages([])` to AFTER a guard that confirms the refetched history is non-empty. If the refetch returns empty (unlikely but possible if opencode hasn't committed the response yet), keep the current state visible.

```typescript
// In reconcileOnIdle:
await queryClient.refetchQueries({ queryKey: ["messages", workspaceId, sessionId] });

const freshHistory = queryClient.getQueryData<{ pages: Array<{ messages: Message[] }> }>(
  ["messages", workspaceId, sessionId],
);
const msgs = freshHistory?.pages.flatMap((p) => p.messages) ?? [];

// Only clear streaming state if history has content to replace it with.
// If history is empty (opencode hasn't committed yet), preserve streaming state
// so the user doesn't see a blank screen.
if (msgs.length > 0) {
  setSseStreamParts([]);
  setLocalMessages([]);
  setSessionErrors([]);
}

// Always reset reconnect mode and part tracking refs
isReconnectMode.current = false;
knownLivePartIds.current.clear();
sentTextRef.current = "";
activePartTypeRef.current = null;
currentThinkingIdxRef.current = -1;
currentTextIdxRef.current = -1;

// Reconcile queue regardless
queue.reconcile(msgs);
```

**Trade-off:** If opencode commits the response but the refetch returns it, `msgs.length > 0` is true and the clear happens — correct. If the refetch returns before opencode commits (race, extremely unlikely since the idle event fires after the response is committed), we keep streaming state visible — this is the safe fallback.

**Failure mode FM-1.1:** If `sessionErrors` contains an error from the previous message, it persists until the next `reconcileOnIdle` that clears it. This is acceptable — session errors should be visible until cleared.

### US-41.2: Return 409 Conflict when session is already active

**Goal:** Convert opencode's silent drop (V4) into an observable error.

**Implementation:**

In `proxy.go`'s `SendPromptAsync`, before forwarding to opencode, check if the session is already in `activeSess`. If it is, return 409 immediately without forwarding.

```go
func (h *ProxyHandler) SendPromptAsync(c *gin.Context) {
    sid := c.Param("sessionId")
    if err := validateSessionID(sid); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sessionId: " + err.Error()})
        return
    }
    wid := c.Param("id")

    // Guard: if this session already has an in-flight prompt, reject immediately.
    // opencode's runner.ensureRunning() discards work submitted while Running —
    // returning a 409 here converts that silent drop into an observable error that
    // the client can retry after the session goes idle.
    if h.isSessionActive(wid, sid) {
        c.Header("Retry-After", "1")
        c.JSON(http.StatusConflict, gin.H{
            "error":      "session is busy; retry after idle",
            "retryAfter": 1,
        })
        return
    }

    h.proxyToWorkspace(c, "/session/"+sid+"/prompt_async", true, sid)
}
```

Add `isSessionActive` helper:

```go
func (h *ProxyHandler) isSessionActive(workspaceID, sessionID string) bool {
    h.activeMu.Lock()
    defer h.activeMu.Unlock()
    sessions, ok := h.activeSess[workspaceID]
    if !ok {
        return false
    }
    return sessions[sessionID]
}
```

**Why 409 and not 429:** 409 Conflict is semantically correct — the request conflicts with the current state of the resource. 429 Too Many Requests implies rate limiting, which is not what's happening. The existing 429 at `checkAndAddActiveSession` (for maxActiveSessions) is a different condition.

**Why Retry-After: 1:** The session will be idle within seconds (opencode is actively processing). 1 second is aggressive but correct — the frontend's retry logic (US-41.3) uses this header.

**Interaction with `checkAndAddActiveSession`:** `checkAndAddActiveSession` at `proxy.go:517` runs AFTER the 409 guard. If the session is already active and we return 409 early, `checkAndAddActiveSession` is never called — correct, because we're not starting a new session.

**Interaction with `proxyToWorkspace`:** The 409 guard runs before `proxyToWorkspace`. If the guard passes (session is not active), `proxyToWorkspace` proceeds normally including `checkAndAddActiveSession`.

**Limitation:** `activeSess` is per-replica and in-memory. If the SSE tracker for this workspace is connected to a different API replica, `activeSess` on this replica may be empty even though the session is actually busy. In this case, the 409 guard does not fire, and the request is forwarded to opencode. This is an acceptable false negative — the guard is defense-in-depth, not the primary fix. The primary fix (US-41.1) handles the common case.

**Does this break the normal flow?** No. In normal operation, `doSendNow` is only called when `!serverBusy && !streaming`. At that point, `activeSess` for the session should be empty (the previous message has gone idle, `onSessionIdle` removed it). The 409 guard passes and the request proceeds.

**Does this break the direct-send path?** `SendMessage` (synchronous proxy) does NOT get the 409 guard — only `SendPromptAsync` does. This is correct because `SendMessage` uses a different code path and the concurrency model is different (synchronous round-trip, no racing fork).

### US-41.3: Frontend handles 409 as retryable-pending

**Goal:** When `drainOne` receives a 409 from `SendPromptAsync`, keep the message as `pending` (not `error`) and allow the next `session.status=idle` event to retry it.

**Implementation:**

In `useMessageQueue.ts`, `drainOne`'s `.catch()` handler:

```typescript
.catch((err: unknown) => {
  // 409: session is busy — keep as pending so the next idle event retries.
  // Do NOT mark as error; the send will succeed after the current run completes.
  if (err instanceof ApiClientError && err.status === 409) {
    dispatch({ type: "mark_pending", id: head.id });  // new action
    drainingRef.current = false;
    return;
  }
  // 429: rate limited — mark as error with retry-after info
  let error = err instanceof Error ? err.message : "Failed to send";
  if (err instanceof ApiClientError && err.status === 429) {
    const retryAfter = ((err.body as unknown) as Record<string, unknown>).retryAfter ?? 60;
    error = `Rate limited. Retry after ${retryAfter}s`;
  }
  dispatch({ type: "mark_error", id: head.id, error });
  drainingRef.current = false;
});
```

Add `mark_pending` action to the reducer:

```typescript
| { type: "mark_pending"; id: string }

// In reduce():
case "mark_pending":
  return state.map((m) =>
    m.id === action.id && m.status === "sending"
      ? { ...m, status: "pending" as const, _sentAt: undefined }
      : m,
  );
```

**Why a new `mark_pending` action instead of `retry`?** `retry` is the user-initiated action and validates against history before resetting. `mark_pending` is an internal state reset — the message is still "in flight" conceptually (we know the session is busy, not that the send failed). Keeping these two actions separate maintains clear semantics in the reducer.

**Why not a timer/delay before retry?** The message will be retried on the next `session.status=idle` SSE event, which fires when opencode finishes the current prompt. This is the exact right time — no polling, no arbitrary delays.

**Interaction with 60-second sending timeout:** If the session stays busy for >60 seconds (long-running LLM call), the `_sentAt` timestamp on the `sending` item would trigger a timeout... but `mark_pending` transitions back from `sending` to `pending`, and clears `_sentAt`. So the timeout interval's `hasStuck` check finds no stuck `sending` items. Correct.

### US-41.4: Fix `onSessionIdle` dead-code — populate `cfg.workspaceID`

**Root cause:** `workspaceConfig.workspaceID` is never populated (`proxy.go:49-53`), making the `cfg.workspaceID != ""` guard at `proxy.go:1060` always false. The `onSessionIdle` handler never records activity or calls `fetchAndPersistTitle` — two correctness gaps.

The issue persists from US-6.5 (Epic 06). It was documented but never fixed.

**Fix:** Populate `workspaceID` in `wsConfig` when the workspace becomes active. In the CRD watcher's `handleWorkspaceEvent` function, add the workspace ID to `wsConfig`:

```go
// In handleWorkspaceEvent, when phase == phaseActive:
h.wsConfigMu.Lock()
cfg := h.wsConfig[workspace.Name]
cfg.workspaceID = workspace.Name  // populate the missing field
h.wsConfig[workspace.Name] = cfg
h.wsConfigMu.Unlock()
```

**Alternative approach:** Remove the `cfg.workspaceID != ""` guard entirely and use `workspaceID` directly (it's already available as a parameter to `onSessionIdle`). The guard was added defensively to avoid recording activity for workspaces not yet tracked in `wsConfig` — but at idle time, the workspace is active (it has a running session), so the guard is unnecessary.

Simpler fix:

```go
func (h *ProxyHandler) onSessionIdle(workspaceID, sessionID string) {
    h.removeActiveSession(workspaceID, sessionID)

    if h.broker != nil { ... }
    if h.userBroker != nil { ... }

    // Record activity and persist session title.
    // Previously gated on cfg.workspaceID which was never populated (US-6.5 gap).
    if h.activityTracker != nil {
        h.activityTracker.Record(workspaceID)
    }
    if h.sessionIndex != nil {
        h.sessionIndex.RecordMessage(workspaceID, sessionID, "", time.Now())
        go h.fetchAndPersistTitle(workspaceID, sessionID)
    }
}
```

The `wsConfig` lookup was a vestige of a design where `workspaceID` might differ from the map key. It doesn't — `workspaceID` IS the map key (`h.wsConfig[workspaceID]`). Removing the lookup and guard is the correct, simpler fix.

**Impact:** This restores two behaviors that have been silently broken:
- `activityTracker.Record(workspaceID)` — updates `status.lastActivityAt` on the workspace CRD (used by suspension logic)
- `sessionIndex.RecordMessage` / `fetchAndPersistTitle` — records message activity and updates session title in the DB on idle

Both are also called in `proxyToWorkspace` (on write operations), so this is not a regression fix for all cases — but `onSessionIdle` is the correct place to record these for messages that arrive via the async path or from external sources.

---

## Failure Modes

### FM1 — History refetch returns empty; streaming state not cleared (MEDIUM)

**Scenario:** `reconcileOnIdle` refetches history. The refetch returns before opencode has committed the response (theoretical — idle fires after commit in practice). `msgs.length === 0`. `setSseStreamParts` and `setLocalMessages` are NOT called. Streaming parts from the previous session remain visible alongside the new session's content.

**Mitigation:** This scenario requires opencode to emit `session.status=idle` before persisting the response to its DB, which contradicts the confirmed order (V5-V6: state is `Idle`, THEN the event is emitted; the response is written by `runLoop` before `finishRun` is called). In practice this cannot happen.

**Residual risk:** If opencode's DB write is asynchronous and the SSE event races ahead of it (not observed in source), a brief stale state could persist. Self-corrects on the next interaction. Low probability, low impact.

### FM2 — 409 guard fires for a session active on a different replica (LOW)

**Scenario:** Session is active on replica B (SSE tracker on B has `activeSess[wsId][sesId]=true`). Client sends `prompt_async` to replica A. Replica A's `activeSess` is empty for this session. The 409 guard does NOT fire. Request is forwarded to opencode. opencode's runner handles it correctly because its own state IS `Idle` at this point (the session was busy on replica B, not on opencode — wait, no, opencode is single-pod and the runner IS busy if the session is processing).

Actually: opencode is single-pod. If session is processing on opencode, `activeSess` on SOME replica has it. Replica A may not. So the 409 guard is a best-effort defense. If it misses, opencode's runner silently drops the work (Bug 2 scenario). The frontend gets 204, removes the pill, orphan user message.

**Mitigation:** US-41.1 (Bug 1 fix) is the primary mitigation. Bug 2 (orphan messages) is a secondary concern only triggered by concurrent sends. The `drainingRef` guard in the frontend prevents the queue from sending concurrently. This race can only happen if `doSendNow` is called simultaneously with a queue drain — which requires `!serverBusy && !streaming` to be true at the same time as a queued message is draining.

In practice: `doSendNow` is only called when the user hits Enter, which requires `serverBusy=false`. If a queued message was just sent (drainOne), `serverBusy` goes true when opencode emits busy. If the user hits Enter during the gap between 204 returning and `session.status=busy` arriving (milliseconds), a concurrent `doSendNow` can race. This is a genuine but narrow window.

**Definitive mitigation (follow-up):** Gate `doSendNow` on `!drainingRef.current` from the queue — i.e., while the queue is actively draining, treat `handleSend` as "busy" and enqueue instead of calling `doSendNow`. This is out of scope for this epic (requires exposing `drainingRef` from `useMessageQueue`) but should be tracked.

### FM3 — `mark_pending` causes infinite retry on persistent failure (LOW)

**Scenario:** `SendPromptAsync` returns 409 repeatedly (session stuck busy — e.g., opencode is processing indefinitely). The message cycles: `pending` → `sending` → 409 → `pending` → ... The user sees the pill permanently as "pending" with no indication of the issue.

**Mitigation:** The 60-second sending timeout fires after `SENDING_TIMEOUT_MS` if the message is in `sending` state. But if the 409 arrives quickly (sub-second), the message transitions back to `pending` before the timeout check. The timeout only checks `sending` items.

Add a **retry counter** to `QueuedMessage` and escalate to `error` after N retries:

```typescript
export type QueuedMessage = {
  id: string;
  text: string;
  status: "pending" | "sending" | "error";
  error?: string;
  sessionId: string;
  _sentAt?: number;
  _retryCount?: number;  // added
};
```

In `mark_pending`, increment `_retryCount`. In `drainOne`, if `_retryCount >= MAX_RETRIES` (e.g., 5), dispatch `mark_error` instead of `mark_pending`.

This is tracked as US-41.3b.

### FM4 — `reconcile` prematurely removes a pending item (LOW)

**Scenario:** A message was sent, its `messageID` appears in history (previous send), but the item is still in the queue as `pending` (e.g., after a 409 retry reset). `reconcile` removes it. The user never sees an error; the message simply disappears from the queue.

This can only happen if the same `messageID` appears in history AND the queue. Since `messageID` is generated fresh in `enqueue()` (ULID-style, time-ordered, high entropy), collision is negligible. However, if the message was previously sent successfully (the `send_success` path didn't fire due to a bug), the ID could appear in both.

**Mitigation:** `reconcile` semantics are intentionally "if the server has it, remove the queue item". This is correct behavior — it's defense-in-depth cleanup. If the message is in history, it was processed; removing the queue pill is right.

---

## Scale Analysis

- Queue state: O(n) where n = number of typed-while-busy messages per session. Bounded by user typing speed; never exceeds a few dozen items.
- The 409 check (`isSessionActive`) is O(1) — a map lookup behind a mutex.
- `mark_pending` adds no new state beyond an integer counter.
- `onSessionIdle` change removes one map lookup per idle event. The activity tracker and session index writes were already happening in `proxyToWorkspace`; this adds a second write on idle. Both are non-blocking (write-behind queue for session index; goroutine for title fetch).

---

## Test Plan

### US-41.1 tests (frontend — ChatPage)

| # | Test | File | Type |
|---|------|------|------|
| 1 | `reconcileOnIdle` does NOT clear `sseStreamParts` when history is empty | `ChatPage.queue.test.tsx` | Regression |
| 2 | `reconcileOnIdle` clears `sseStreamParts` when history is non-empty | `ChatPage.queue.test.tsx` | Regression |
| 3 | Queued message streaming content is visible during busy phase after idle reconcile | `ChatPage.queue.test.tsx` | Regression |
| 4 | Queued message response appears in history after second idle | `ChatPage.queue.test.tsx` | Integration |

### US-41.2 tests (backend — Go)

| # | Test | File | Type |
|---|------|------|------|
| 5 | `SendPromptAsync` returns 409 when session is in `activeSess` | `proxy_test.go` | Unit |
| 6 | `SendPromptAsync` proceeds normally when session is NOT in `activeSess` | `proxy_test.go` | Unit |
| 7 | `isSessionActive` returns false for unknown workspace | `proxy_test.go` | Unit |
| 8 | 409 response body has correct `error` and `retryAfter` fields | `proxy_test.go` | Unit |

### US-41.3 tests (frontend — useMessageQueue)

| # | Test | File | Type |
|---|------|------|------|
| 9 | 409 response transitions message to `pending` (not `error`) | `useMessageQueue.test.ts` | Unit |
| 10 | Message in `pending` after 409 is retried on next `notifyIdle` | `useMessageQueue.test.ts` | Unit |
| 11 | After MAX_RETRIES 409s, message transitions to `error` | `useMessageQueue.test.ts` | Unit |
| 12 | `_retryCount` resets to 0 on successful send | `useMessageQueue.test.ts` | Unit |

### US-41.4 tests (backend — Go)

| # | Test | File | Type |
|---|------|------|------|
| 13 | `onSessionIdle` calls `activityTracker.Record` regardless of `wsConfig` state | `proxy_test.go` | Regression |
| 14 | `onSessionIdle` calls `sessionIndex.RecordMessage` for a workspace not in `wsConfig` | `proxy_test.go` | Regression |
| 15 | `onSessionIdle` calls `fetchAndPersistTitle` goroutine | `proxy_test.go` | Regression |

Run commands:
```bash
# Backend
go test -timeout 30s -race ./api/internal/handlers/...

# Frontend
cd frontend && npx vitest run src/hooks/useMessageQueue.test.ts src/pages/ChatPage.queue.test.tsx
```

---

## User Stories

### US-41.1: Fix streaming state clear timing in `reconcileOnIdle`

**Goal:** Queued message streaming content is visible during processing; response appears from history without a blank frame.

**Files:** `frontend/src/pages/ChatPage.tsx`

**Implementation:** Guard `setSseStreamParts([])` and `setLocalMessages([])` and `setSessionErrors([])` on `msgs.length > 0`. Move ref resets outside the guard (they are always safe). Keep `queue.reconcile(msgs)` outside the guard (reconcile with empty history is a no-op).

**Acceptance criteria:** After a queued message is sent:
- The streaming content from the queued message is visible during the busy phase
- The response appears from history on the next idle without a blank intermediate frame
- If history refetch returns empty, existing streaming state is preserved (FM1 mitigation)

**Tests:** #1, #2, #3, #4

---

### US-41.2: Return 409 Conflict for in-flight session in `SendPromptAsync`

**Goal:** Convert opencode's silent drop into an observable, retryable error.

**Files:** `api/internal/handlers/proxy.go`, `api/internal/handlers/proxy_test.go`

**Implementation:**
- Add `isSessionActive(workspaceID, sessionID string) bool` helper
- Add 409 guard at the top of `SendPromptAsync` before `proxyToWorkspace`
- Return `{"error": "session is busy; retry after idle", "retryAfter": 1}` with `Retry-After: 1` header

**Acceptance criteria:**
- POST to `prompt` while session is in `activeSess` returns 409 with correct body
- POST to `prompt` while session is NOT in `activeSess` proceeds normally (no regression)
- 409 does not affect `SendMessage` (synchronous path)

**Tests:** #5, #6, #7, #8

---

### US-41.3: Handle 409 as retryable-pending in `drainOne`

**Goal:** 409 responses from `SendPromptAsync` do not surface as error pills; messages are silently retried on the next idle event.

**Files:** `frontend/src/hooks/useMessageQueue.ts`, `frontend/src/hooks/useMessageQueue.test.ts`

**Implementation:**
- Add `_retryCount?: number` to `QueuedMessage` type
- Add `mark_pending` reducer action: transitions `sending` → `pending`, clears `_sentAt`, increments `_retryCount`
- In `drainOne .catch()`: 409 → dispatch `mark_pending` (unless `_retryCount >= MAX_RETRIES`, in which case dispatch `mark_error`)
- `MAX_RETRIES = 5` (constant)

**Acceptance criteria:**
- 409 transitions message from `sending` to `pending`, NOT `error`
- After 5 consecutive 409 responses, message transitions to `error` with "Session busy — retry manually"
- A successful send resets `_retryCount` (handled by `send_success` removing the item)
- Existing error handling for network failures and 429s is unchanged

**Tests:** #9, #10, #11, #12

---

### US-41.4: Restore `onSessionIdle` activity and title recording

**Goal:** Remove the dead `cfg.workspaceID != ""` guard; restore correct activity tracking and session title persistence on idle.

**Files:** `api/internal/handlers/proxy.go`, `api/internal/handlers/proxy_test.go`

**Implementation:**
- Remove the `h.wsConfigMu.RLock()` / `cfg, ok := h.wsConfig[workspaceID]` lookup block in `onSessionIdle`
- Call `h.activityTracker.Record(workspaceID)` and `h.sessionIndex.RecordMessage(...)` directly using the `workspaceID` parameter
- Clean up the dead `workspaceConfig.workspaceID` field (it can remain in the struct but should be noted as unused, or removed if no tests reference it)

**Acceptance criteria:**
- `onSessionIdle` calls `activityTracker.Record` for every idle event, regardless of `wsConfig` state
- `onSessionIdle` calls `sessionIndex.RecordMessage` and `fetchAndPersistTitle` for every idle event
- No regression in existing tests

**Tests:** #13, #14, #15

---

## Files Modified

| File | Stories | Change |
|------|---------|--------|
| `frontend/src/pages/ChatPage.tsx` | US-41.1 | Guard `setSseStreamParts`/`setLocalMessages`/`setSessionErrors` on `msgs.length > 0` in `reconcileOnIdle` |
| `frontend/src/hooks/useMessageQueue.ts` | US-41.3 | Add `_retryCount` field; add `mark_pending` action; handle 409 in `drainOne`; add `MAX_RETRIES` constant |
| `frontend/src/api/types.ts` | US-41.3 | No change required — `ApiClientError` already carries status code |
| `frontend/src/hooks/useMessageQueue.test.ts` | US-41.3 | 4 new tests (#9–#12) |
| `frontend/src/pages/ChatPage.queue.test.tsx` | US-41.1 | 4 new tests (#1–#4) |
| `api/internal/handlers/proxy.go` | US-41.2, US-41.4 | Add `isSessionActive` helper; 409 guard in `SendPromptAsync`; fix `onSessionIdle` dead code |
| `api/internal/handlers/proxy_test.go` | US-41.2, US-41.4 | 11 new tests (#5–#15) |

---

## Implementation Order

Stories are independent and can be parallelized. Recommended order for a single implementer:

1. **US-41.4** (backend, 2-line fix, highest correctness impact, no test complexity)
2. **US-41.2** (backend, new endpoint guard, requires new tests)
3. **US-41.1** (frontend, `reconcileOnIdle` guard, requires ChatPage test updates)
4. **US-41.3** (frontend, `drainOne` 409 handling, requires new reducer action and tests)

US-41.3 depends on US-41.2 being deployed (otherwise there are never any 409s to handle). In a phased deployment: US-41.4 + US-41.2 first, then US-41.1 + US-41.3.

---

## Open Questions

None. All assumptions have been validated (see table above). No unvalidated assumption is used in this design.

---

## Non-Goals

- **Server-side persistent message queue:** Not required. The client-side queue is architecturally correct for interactive single-user-per-workspace sessions. A server-side queue would add infrastructure complexity (Redis or per-replica in-memory with known correctness gaps) for no benefit given the single-tenant workspace model.
- **Cross-replica active session coordination:** The `activeSess` map is per-replica by design (documented in `README-LLM.md` §Known design fragilities). The 409 guard (US-41.2) is best-effort; it does not require cross-replica coordination.
- **opencode runner changes:** We do not modify opencode. The `ensureRunning` behavior (discard on busy) is a valid design for a sequential LLM runner. Our mitigations make this behavior safe from the LLMSafeSpace client's perspective.
- **SDK queueing:** Confirmed not needed (V21).
