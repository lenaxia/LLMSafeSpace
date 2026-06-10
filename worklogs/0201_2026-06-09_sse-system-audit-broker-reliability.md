# 0194 — SSE System Audit: Event Completeness & Broker Reliability

**Date:** 2026-06-09
**Session:** Full audit of the SSE event pipeline from opencode pod through API broker to frontend, cross-referenced against PR #76 (context-limit-ux-error-surfacing, reverted on main, pending re-land).

---

## Objective

Audit the SSE system for event completeness — ensure all SSE events emitted by opencode are surfaced to the frontend and none are silently swallowed. The audit covers every layer: opencode pod → workspace-agentd → API SSETracker → WorkspaceEventBroker/UserEventBroker → frontend SSE client → React event dispatch → streaming parser.

Secondary goal: determine how the proposed fixes interleave with PR #76, which adds retry banners, stream-interrupted detection, and error name mapping on the frontend.

---

## Assumptions (stated up front)

1. **Opencode emits 19 distinct SSE event types.** Validated from worklog 0069 (lines 172-186), `pkg/agent/opencode/dialect.go`, and test fixtures across `session_tracker_test.go`, `stream_events_test.go`, `e2e_test.go`, `ChatPage.sse.test.tsx`.
2. **The forwarding layer (`SSETracker.processEvent` → `onRawEvent`) forwards ALL event types.** Validated by reading `session_tracker.go:289-313` — `onRawEvent` is called for every successfully parsed event before any type-specific dispatch.
3. **`WorkspaceEventBroker` has no recovery mechanism when events are dropped.** Validated by reading `event_broker.go:93-98` — the `default` case in the non-blocking send silently discards the event with no logging, no metric, and no flag.
4. **`UserEventBroker` DOES have recovery (resync + missedEvent flag).** Validated by reading `event_broker_user.go:39-52` — `subscriber.send()` tracks `missedEvent` and prepends a `resync` event on the next successful delivery.
5. **The frontend `parseStreamEvent` handles text, reasoning, thinking, tool, tool_use, tool_call part types during streaming, but NOT tool_result or error.** Validated by reading `ChatPage.tsx:353-461` — no branch handles `partType === "tool_result"` or `partType === "error"`.
6. **PR #76 is `feat/context-limit-ux-error-surfacing` (pr/76, also `origin/feat/context-limit-ux-error-surfacing`), merged as `f8e1e3a3` then reverted as `17d366db`.** Validated by `git log origin/main --oneline -5` and `git diff origin/main...pr/76 --stat`.
7. **PR #76 does not touch the broker layer or `parseStreamEvent`.** Validated by `git diff origin/main...pr/76 --stat` — changes are to `ChatPage.tsx`, `useChatStream.ts`, `SessionRetryBanner.tsx`, `DiskUsageBar.tsx`, `types.ts`, and `cmd/workspace-agentd/main.go`.

---

## Architecture (as-found)

### Two SSE delivery streams

| Stream | Endpoint | Broker | Buffer | Recovery | Scope |
|---|---|---|---|---|---|
| Workspace session events | `GET /api/v1/workspaces/:id/session-events` | `WorkspaceEventBroker` | 16 events | **None** | Per-workspace |
| User workspace events | `GET /api/v1/events` | `UserEventBroker` (16 shards) | 128 events | **resync + missedEvent** | Per-user (all workspaces) |

### Complete event flow diagram

```
opencode pod
  │ GET /event (SSE, text/event-stream)
  │ Basic Auth: opencode / <password>
  │
  ▼
SSETracker.processEvent()                        [session_tracker.go:289]
  ├── onRawEvent(workspaceID, eventType, data)   [ALL event types]
  │     │
  │     ▼
  │   proxy.go: onRawEvent()                     [proxy.go:1177]
  │     ├── broker.Publish("opencode.event")     [proxy.go:1183-1187]
  │     │     → WorkspaceEventBroker              (16-event buffer, NO recovery)
  │     │     → StreamEvents() → browser          [proxy.go:396-431]
  │     │
  │     ├── persistTitleFromEvent()               [proxy.go:1190-1192]  (session.updated only)
  │     │
  │     └── emitNormalizedInputEvent()            [proxy.go:1195-1197]
  │           ├── agent.question                  (from question.asked)
  │           ├── agent.question.resolved         (from question.replied/rejected)
  │           ├── agent.permission                (from permission.asked, or auto-approve+swallow)
  │           └── agent.permission.resolved       (from permission.replied)
  │
  └── dispatchProperties()                       [session_tracker.go:316]
        ├── session.updated → handleSessionUpdated()  → billing/inference metrics
        └── session.status  → onSessionIdle/onSessionActive
              ├── session.status (synthesized)    → WorkspaceEventBroker → browser
              └── session.status (synthesized)    → UserEventBroker (not currently wired here)

Kubernetes CRD Watcher
  crd_watcher.go → onPhaseChange()
        └── userBroker.PublishToUser("workspace.phase")  → UserEventBroker → StreamUserEvents → browser

Browser frontend:
  useEventStream()   → /workspaces/:id/session-events  → handleSSEEvent → parseStreamEvent
  useUserEventStream() → /api/v1/events               → query invalidation
```

### Opencode SSE event types — complete catalog (19 types)

Validated from: worklog 0069 lines 172-186, `pkg/agent/opencode/dialect.go`, test fixtures.

| # | Event Type | Wire format example | Backend processing | Frontend handling |
|---|---|---|---|---|
| 1 | `server.connected` | `{"type":"server.connected","properties":{}}` | Forwarded only | Ignored |
| 2 | `server.heartbeat` | `{"type":"server.heartbeat","properties":{}}` | Forwarded only | Ignored |
| 3 | `message.created` | `{"type":"message.created","properties":{...}}` | Forwarded only | Ignored (via history re-fetch on idle) |
| 4 | `message.updated` | `{"type":"message.updated","properties":{...}}` | Forwarded only | Ignored (via history re-fetch on idle) |
| 5 | `message.error` | `{"type":"message.error","properties":{...}}` | Forwarded only | Ignored (via history re-fetch on idle) |
| 6 | `message.part.updated` | `{"type":"message.part.updated","properties":{"sessionID":"ses_...","part":{...}}}` | Forwarded only | **Streamed**: text, thinking, reasoning, tool, tool_use, tool_call |
| 7 | `message.part.delta` | `{"type":"message.part.delta","properties":{"sessionID":"ses_...","field":"text","delta":"..."}}` | Forwarded only | **Streamed**: incremental delta append |
| 8 | `session.created` | `{"type":"session.created","properties":{...}}` | Forwarded only | Ignored |
| 9 | `session.updated` | `{"type":"session.updated","properties":{"id":"ses_...","title":"...","tokens":{...},"model":{...}}}` | **Consumed**: billing metrics + title persistence + forwarded | Title update in sidebar |
| 10 | `session.status` (busy) | `{"type":"session.status","properties":{"sessionID":"ses_...","status":{"type":"busy"}}}` | **Consumed**: active session tracking + forwarded | session.status(busy) |
| 11 | `session.status` (idle) | `{"type":"session.status","properties":{"sessionID":"ses_...","status":{"type":"idle"}}}` | **Consumed**: idle callback + activity tracking + forwarded | session.status(idle) → reconcileOnIdle |
| 12 | `session.status` (retry) | `{"type":"session.status","properties":{"sessionID":"ses_...","status":{"type":"retry","attempt":2,"message":"...","next":...}}}` | **Consumed**: treated as busy + forwarded | PR #76: retry banner from opencode.event path |
| 13 | `session.error` | `{"type":"session.error","properties":{"sessionID":"ses_...","error":{...}}}` | Forwarded only | Error bubble in chat |
| 14 | `session.diff` | `{"type":"session.diff","properties":{"files":[...]}}` | Forwarded only | Ignored |
| 15 | `session.output` | `{"type":"session.output","properties":{...}}` | Forwarded only | Ignored |
| 16 | `question.asked` | `{"type":"question.asked","properties":{"id":"q_...","sessionID":"ses_...","questions":[...]}}` | Normalized → agent.question | Pending questions UI |
| 17 | `question.replied` | `{"type":"question.replied","properties":{"id":"q_..."}}` | Normalized → agent.question.resolved | Clear pending question |
| 18 | `question.rejected` | `{"type":"question.rejected","properties":{"id":"q_..."}}` | Normalized → agent.question.resolved | Clear pending question |
| 19 | `permission.asked` | `{"type":"permission.asked","properties":{"id":"p_...","sessionID":"ses_...","permission":"...","patterns":[...]}}` | Normalized → agent.permission (or auto-approve+swallow) | Pending permissions UI |
| 20 | `permission.replied` | `{"type":"permission.replied","properties":{"id":"p_..."}}` | Normalized → agent.permission.resolved | Clear pending permission |
| 21 | `ping` | `{"type":"ping","properties":{}}` | Forwarded only | Ignored |

Note: 21 rows because `session.status` has 3 subtypes (busy, idle, retry) and `question.*` has asked/replied/rejected. The distinct top-level `type` values are 19.

### Forwarding completeness: ALL events forwarded correctly

`session_tracker.go:289-313`:
```go
func (t *SSETracker) processEvent(workspaceID, data string) {
    // ... parse flat or nested format ...
    if t.onRawEvent != nil {
        t.onRawEvent(workspaceID, evt.Type, data)  // ← ALL types forwarded
    }
    t.dispatchProperties(workspaceID, evt.Type, evt.Properties)
}
```

`proxy.go:1177-1187`:
```go
func (h *ProxyHandler) onRawEvent(workspaceID, eventType, rawData string) {
    // ... parse rawData into generic interface{} ...
    h.broker.Publish(workspaceID, WorkspaceSSEEvent{
        Type:      "opencode.event",
        EventType: eventType,
        Data:      parsed,
    })
    // ... then type-specific processing ...
}
```

**Every opencode SSE event is published to the WorkspaceEventBroker as an `opencode.event` wrapper.** The type-specific processing in `emitNormalizedInputEvent` adds normalized events (`agent.question`, etc.) in addition to — not instead of — the raw forwarding. The only exception is auto-approved permissions, where the `agent.permission` event is intentionally suppressed and the auto-approve response is sent via goroutine instead (by design).

---

## Findings

### F1: WorkspaceEventBroker silently drops events with no recovery (HIGH)

**Location:** `api/internal/handlers/event_broker.go:93-98`

**Evidence:**
```go
for _, ch := range targets {
    select {
    case ch <- evt:
    default:
        // Subscriber is slow; drop event rather than block.
    }
}
```

The per-workspace broker uses a 16-event buffered channel with non-blocking sends. When a subscriber's channel is full (slow consumer — e.g., JavaScript main thread blocked, slow network, tab backgrounded), the event is silently dropped. No metric, no log, no flag.

**Contrast with UserEventBroker** (`event_broker_user.go:34-53`):
```go
func (s *subscriber) send(evt WorkspaceSSEEvent) {
    if s.closed.Load() { return }
    defer func() { _ = recover() }()
    if s.missedEvent.Load() {
        resync := WorkspaceSSEEvent{Type: "resync"}
        select {
        case s.ch <- resync:
            s.missedEvent.Store(false)
        default:
            return
        }
    }
    select {
    case s.ch <- evt:
    default:
        s.missedEvent.Store(true)  // ← FLAGGED for recovery
    }
}
```

The `UserEventBroker` has a `missedEvent` atomic flag per subscriber. When an event is dropped, the flag is set. On the next successful send, a `resync` event is prepended so the client knows to re-fetch state. The `WorkspaceEventBroker` has none of this.

**Critical scenario — session.status(idle) dropped:**

1. User sends a message → session goes `busy` → frontend disables send button
2. Agent responds → opencode emits `session.status(idle)` → `SSETracker` calls `onSessionIdle`
3. `onSessionIdle` publishes `session.status(idle)` to `WorkspaceEventBroker`
4. Subscriber channel is full (16 events buffered — could be a burst of `message.part.delta` events)
5. `session.status(idle)` is dropped silently
6. Frontend never receives `idle` → `serverBusy` stays `true` → send button stays disabled
7. User is stuck. Only recovery: page refresh.

**How often does this happen?** The 16-event buffer is small. During active streaming, `message.part.delta` events can arrive at 10-50/second. If the browser's main thread is blocked for >320ms (16 events / 50 events/sec), the buffer fills. This is realistic during:
- Heavy React re-renders (large message list)
- Browser DevTools open
- Mobile devices with slow JS execution
- Tab backgrounded (Chrome throttles timers, SSE processing slows)

**PR #76 interleave:** PR #76 adds a `streamTimedOut` banner that fires when the 60-second `IDLE_WAIT_TIMEOUT_MS` expires without an `session.status(idle)` SSE event AND `serverBusy` is false. This is a **symptom treatment** — it detects the stuck state after 60 seconds and shows a dismissable banner. The root cause (broker drop) remains. The user must manually dismiss and retry. Fixing F1 at the broker layer eliminates the root cause; the `streamTimedOut` banner remains valuable as defense-in-depth for non-broker failures (network partition, API pod restart).

**Proposed fix:**

Migrate `WorkspaceEventBroker` to use the same `subscriber` pattern as `UserEventBroker`:

1. Replace `map[chan WorkspaceSSEEvent]struct{}` with `map[uint64]*subscriber` where `subscriber` has `ch chan WorkspaceSSEEvent`, `missedEvent atomic.Bool`, `closed atomic.Bool`.
2. In `Publish`, use `subscriber.send()` which tracks `missedEvent` and prepends `resync` on recovery.
3. Add a `resync` handler in `StreamEvents()` that emits current workspace state (phase, active sessions) when a `resync` sentinel is received.
4. Add a `SubscribeWithReplay` variant that replays recent events (similar to `UserEventBroker.Replay`).

Alternatively, merge `WorkspaceEventBroker` into `UserEventBroker` as a workspace-scoped subscription mode, eliminating the code duplication. This is the cleaner approach since both brokers already use the same `WorkspaceSSEEvent` type and the `UserEventBroker`'s sharding and subscriber management are strictly more capable.

**Impact on existing code:**
- `StreamEvents()` in `proxy.go` — change from `ch := h.broker.Subscribe(workspaceID)` to `sub, err := h.broker.SubscribeWorkspace(workspaceID)` + handle `resync` events
- `StreamUserEvents()` — no change (already uses `UserEventBroker`)
- Heartbeat sentinel — needs to work through `subscriber.send()` instead of raw channel send
- Tests — `proxy_test.go` SSE tests need subscriber pattern update

**Estimated scope:** ~200 lines changed (broker removal, proxy.go subscription change, test updates). No frontend changes required — the frontend already handles unknown event types by ignoring them, and a `resync` event would trigger `handleSSEReconnect` → query invalidation.

---

### F2: `tool_result` and `error` part types not rendered during streaming (MEDIUM)

**Location:** `frontend/src/pages/ChatPage.tsx:353-461`

**Evidence:**

`parseStreamEvent` handles these `message.part.updated` part types:
- `reasoning` / `thinking` → thinking block (lines 373-394)
- `text` → text block with echo detection (lines 395-431)
- `tool` / `tool_use` / `tool_call` → tool call block (lines 432-458)
- `step-start` / `step-finish` → explicit no-op (line 460-461)

**NOT handled during streaming:**
- `tool_result` — rendered from history by `MessagePart.tsx:204-216` but never created as a `StreamPart` during live streaming
- `error` — rendered from history by `MessagePart.tsx:218-224` but never created as a `StreamPart` during live streaming

**Impact:** During streaming, if opencode emits a `message.part.updated` with `part.type="tool_result"` or `part.type="error"`, the part is silently ignored by `parseStreamEvent`. It only appears after `reconcileOnIdle` re-fetches the full message history from the server. This creates a visual gap during streaming where tool results and errors are invisible until the stream completes.

**PR #76 interleave:** PR #76 does not touch `parseStreamEvent`. No conflict. PR #76's error name mapping (for `session.error` events) is orthogonal — it handles session-level errors, not part-level errors within a streaming message.

**Proposed fix:**

Add `tool_result` and `error` handling to `parseStreamEvent`, similar to the existing `tool`/`tool_use`/`tool_call` handling:

```tsx
} else if (partType === "tool_result") {
  const text = typeof part.text === "string" ? part.text : "";
  setSseStreamParts((prev) => [...prev, { type: "tool_result", text }]);
  activePartTypeRef.current = null;
} else if (partType === "error") {
  const text = typeof part.text === "string" ? part.text : "Error";
  setSseStreamParts((prev) => [...prev, { type: "error", text }]);
  activePartTypeRef.current = null;
}
```

This requires adding `tool_result` and `error` to the `StreamPart` type union (defined near `ChatPage.tsx:275-283`):
```tsx
type StreamPart = {
  type: "text" | "thinking" | "tool" | "tool_result" | "error";
  text: string;
  toolState?: string;
  toolCallID?: string;
  toolInput?: unknown;
  toolOutput?: string;
};
```

`MessagePart.tsx` already renders both types correctly from history — no changes needed there. The streaming path just needs to create the same `StreamPart` shapes.

**Estimated scope:** ~20 lines changed in `ChatPage.tsx` (parseStreamEvent + StreamPart type). ~30 lines of new tests.

---

### F3: No metrics or logging for dropped broker events (MEDIUM)

**Location:** `api/internal/handlers/event_broker.go:96-98`

**Evidence:**
```go
default:
    // Subscriber is slow; drop event rather than block.
```

No Prometheus counter, no zap log, no structured field. Dropped events are completely invisible in production. The only way to diagnose "user stuck in busy" is to correlate browser network logs with API pod logs — but the API logs nothing about the drop.

**Contrast with `UserEventBroker`:** The `UserEventBroker` doesn't log drops either, but at least sets `missedEvent` so a `resync` is sent. Still no metric.

**Proposed fix:**

Add a Prometheus counter to both brokers:

```go
var brokerDroppedEvents = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_sse_broker_dropped_events_total",
        Help: "Events dropped because subscriber channel was full",
    },
    []string{"workspace_id", "event_type"},
)
```

Increment in the `default` case of both `WorkspaceEventBroker.Publish` and `subscriber.send`. Add a Debug-level log line with workspace ID, event type, and subscriber count.

Also add a gauge for active subscriber count per workspace:
```go
var brokerSubscriberCount = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "llmsafespace_sse_broker_subscribers",
        Help: "Active SSE subscribers per workspace",
    },
    []string{"workspace_id"},
}
```

This enables alerting on persistent drops and diagnosing whether the 16-event buffer is undersized for real-world traffic patterns.

**Estimated scope:** ~40 lines in `event_broker.go` + `event_broker_user.go`. Register in `app.go` metrics setup.

---

### F4: JSON marshal errors silently swallow events (LOW)

**Location:** `api/internal/handlers/proxy.go:417-419`, `stream_user_events.go:118-120,128-130`

**Evidence:**
```go
data, marshalErr := json.Marshal(evt)
if marshalErr != nil {
    continue  // ← silent drop
}
```

`WorkspaceSSEEvent.Data` is `interface{}`. If the data contains an unmarshalable type (e.g., a channel, a function, or a type with unexported fields that `json.Marshal` can't handle), the event is silently dropped. This should never happen in normal operation (the data came from `json.Unmarshal` of opencode's SSE output), but if it does, it produces an un diagnosable gap.

**Proposed fix:**

Log at Warn level with workspace ID, event type, and marshal error:

```go
data, marshalErr := json.Marshal(evt)
if marshalErr != nil {
    h.logger.Warn("SSE event marshal failed, dropping",
        "error", marshalErr,
        "workspaceID", workspaceID,
        "eventType", evt.Type,
    )
    continue
}
```

Same treatment in `StreamUserEvents` and `writeSSEEvent`.

**Estimated scope:** ~10 lines across 3 locations.

---

### F5: `session.status` with unknown subtypes silently ignored by dispatchProperties (LOW)

**Location:** `api/internal/handlers/session_tracker.go:334-376`

**Evidence:**

The switch statement handles `idle`, `busy`, and `retry`. Any future opencode status type would fall through the switch with no action. However, the event IS still forwarded via `onRawEvent` (line 310-311 fires before `dispatchProperties`), so the raw data reaches the frontend as `opencode.event`.

**Assessment:** Not a real bug. The forwarding is complete; the type-specific dispatch is additive. If opencode adds a new status type, the raw event is still delivered and the frontend can handle it from the `opencode.event` wrapper. PR #76 validates this pattern — it handles `retry` from the `opencode.event` path, not the synthesized `session.status` path.

**No fix needed.** Document as known design decision.

---

## Findings NOT bugs (intentional design)

### workspace-agentd drops non-session.status events

`cmd/workspace-agentd/main.go:358-391` — `sessionStatusTracker.processEvent` only processes `session.status` events and silently ignores all others (confirmed by test at `main_test.go:262-267`).

**Why this is correct:** workspace-agentd runs inside the pod and only needs to track session busy/idle state for `/v1/statusz` and `/v1/readyz`. All other event types are irrelevant to its purpose. The API service's `SSETracker` (separate component) handles the full event stream.

### Auto-approved permissions swallowed

`proxy.go:1250-1253` — when `shouldAutoApprovePermissions` returns true, `autoApprovePermission` is called in a goroutine and the function returns without publishing `agent.permission` to the broker.

**Why this is correct:** The permission is auto-resolved server-side. Showing the user a permission prompt that was already auto-approved would be a confusing flash. The `agent.permission.resolved` event is not published either, which is also correct — there was never a visible `agent.permission` to resolve.

### `workspace.phase` only on user stream (not workspace stream)

`proxy.go:899-908` — phase events are published to `userBroker` only, not `broker`.

**Why this is correct:** Per S28.4 (hard cutover design decision), workspace phase events are delivered exclusively via the user-scoped SSE stream (`/api/v1/events`). The workspace session stream (`/session-events`) carries session-level events only. The frontend `useUserEventStream` handles `workspace.phase` by invalidating React Query caches.

### Frontend ignores `message.created`, `message.updated`, `message.error`

These message-level events are received as `opencode.event` wrappers but not processed in `parseStreamEvent` (which only handles `message.part.updated` and `message.part.delta`).

**Why this is correct:** Message metadata (role, cost, tokens, finish status) is handled via the history re-fetch that `reconcileOnIdle` triggers after `session.status(idle)`. Processing these events during streaming would be redundant — the full message is already being reconstructed from part events.

### `step-start` / `step-finish` no-op in frontend

`ChatPage.tsx:460-461` — explicit no-op with comment.

**Why this is correct:** These opencode-internal step markers don't have a visual representation. They exist for opencode's internal orchestration and don't carry user-visible content.

---

## PR #76 Interleave Analysis

### PR #76 summary

PR #76 (`feat/context-limit-ux-error-surfacing`, branch `origin/feat/context-limit-ux-error-surfacing`, merged as `f8e1e3a3`, reverted as `17d366db`) adds:

| Change | File | Layer |
|---|---|---|
| Retry banner | `ChatPage.tsx` + `SessionRetryBanner.tsx` | Frontend rendering |
| Error name mapping (ContextOverflowError, MessageOutputLengthError, ProviderAuthError) | `ChatPage.tsx` | Frontend rendering |
| Stream interrupted banner | `ChatPage.tsx` + `useChatStream.ts` | Frontend symptom detection |
| Context bar "Unknown" when limit unreported | `DiskUsageBar.tsx` | Frontend rendering |
| Remove fabricated 128K×sessions fallback | `cmd/workspace-agentd/main.go` | Backend statusz |

### Finding-by-finding interleave

| My Finding | PR #76 Addressed? | Conflict? | Analysis |
|---|---|---|---|
| **F1: WorkspaceEventBroker drops without recovery** | Partial mitigation (symptom only) | **No conflict** | PR #76's `streamTimedOut` banner detects the stuck state after 60s timeout. This validates F1 is a real production problem. Root cause (broker drop) remains. Fixing F1 eliminates most triggers for the `streamTimedOut` banner; the banner becomes defense-in-depth for non-broker failures (network partition, API pod restart). |
| **F2: tool_result/error parts not streamed** | Not addressed | **No conflict** | PR #76 doesn't touch `parseStreamEvent`. |
| **F3: No metrics for dropped events** | Not addressed | **No conflict** | PR #76 doesn't touch broker code. |
| **F4: Marshal errors silently swallow events** | Not addressed | **No conflict** | PR #76 doesn't touch error handling in StreamEvents. |
| **F5: session.status unknown subtypes** | Validates current design | **No conflict** | PR #76 explicitly documents the dual-path architecture: proxy synthesizes string "busy" on the session.status channel for retry, while rich retry payload travels only via opencode.event. This confirms the forwarding is correct and no backend change is needed. |

### PR #76's own documented gaps (from its worklog `docs/0049`)

These survive alongside my findings and should be addressed separately:

1. **Path A overflow is still silent** — opencode's `isOverflow()` sets `needsCompaction=true` and returns `"compact"` with no `session.error` event. The user sees nothing before compaction starts. `ContextOverflowError` only fires on Path B (hard overflow, provider rejection).
2. **`model_enricher.go` doesn't read `max_input_tokens`** — even with LiteLLM now reporting it, agentd never parses it from the `/models` response. `ModelContextLimit()` still returns 0 for LiteLLM-proxied models.
3. **`SessionRetryBanner` has no tests** — `AtCapBanner` and `SuspendedBanner` both have test files.
4. **Error name mapping is untested inline code** — the mapping lives in `handleSSEEvent` with no unit tests.

### Conclusion: complementary, not conflicting

**All changes are complementary.** My findings (F1–F4) target the backend transport layer — ensuring events reliably reach the browser. PR #76 targets the frontend rendering layer — ensuring events that arrive are displayed usefully. Zero overlap, zero conflicts.

Recommended merge order:
1. F1 (broker resync) — eliminates the most common cause of stuck sessions
2. PR #76 re-land — adds retry/interrupted/error UX on top of reliable delivery
3. F2 (streaming tool_result/error) — closes the streaming rendering gap
4. F3 (metrics) — enables production monitoring
5. F4 (marshal error logging) — improves debuggability

---

## Assumptions validation evidence

| # | Assumption | Validated How |
|---|---|---|
| 1 | Opencode emits 19 distinct SSE event types | `worklogs/0069_2026-05-29_opencode-v1.2.27-api-validation.md:172-186` (live capture); `pkg/agent/opencode/dialect.go` (classification); test fixtures in `session_tracker_test.go`, `stream_events_test.go` |
| 2 | Forwarding layer forwards ALL event types | `session_tracker.go:289-313` — `onRawEvent` called at line 310-311 before any dispatch; `proxy.go:1177-1187` — `broker.Publish` at line 1183-1187 unconditionally |
| 3 | WorkspaceEventBroker has no recovery | `event_broker.go:93-98` — `default` case with no flag/metric/log |
| 4 | UserEventBroker has recovery | `event_broker_user.go:34-53` — `missedEvent` atomic + `resync` prepend |
| 5 | Frontend doesn't stream tool_result/error | `ChatPage.tsx:353-461` — exhaustive enumeration of handled part types; `MessagePart.tsx:204-224` confirms rendering exists for history path |
| 6 | PR #76 is context-limit-ux-error-surfacing | `git log origin/main --oneline -5` shows merge `f8e1e3a3` and revert `17d366db`; `git diff origin/main...pr/76 --stat` confirms files |
| 7 | PR #76 doesn't touch broker or parseStreamEvent | `git diff origin/main...pr/76 --stat` — 7 files, none are broker or parseStreamEvent |

---

## Tests to write (per finding)

### F1 tests

1. **TestWorkspaceEventBroker_DroppedEventSetsMissedFlag** — subscribe, fill buffer, publish one more, verify `missedEvent` flag is set
2. **TestWorkspaceEventBroker_ResyncPrependedOnRecovery** — set missed flag, publish event, verify subscriber receives resync first then the event
3. **TestWorkspaceEventBroker_ResyncOnSlowConsumer** — slow consumer that doesn't drain, publish burst, verify resync behavior
4. **TestStreamEvents_HandlesResyncEvent** — integration test: broker subscriber receives resync, StreamEvents writes `data: {"type":"resync"}\n\n` to response writer
5. **TestStreamEvents_InitialStateOnResync** — after resync, workspace phase and active sessions are re-emitted

### F2 tests

1. **TestParseStreamEvent_ToolResultPart** — `message.part.updated` with `part.type="tool_result"` creates `{type:"tool_result", text:...}` StreamPart
2. **TestParseStreamEvent_ErrorPart** — `message.part.updated` with `part.type="error"` creates `{type:"error", text:...}` StreamPart
3. **TestChatPage_SSE_ToolResultRendersDuringStream** — e2e: emit tool_result SSE event, verify it appears in chat without waiting for idle
4. **TestChatPage_SSE_ErrorPartRendersDuringStream** — e2e: emit error part SSE event, verify error bubble appears during streaming

### F3 tests

1. **TestWorkspaceEventBroker_DroppedEventMetricIncremented** — publish to full subscriber, verify Prometheus counter increments
2. **TestUserEventBroker_DroppedEventMetricIncremented** — same for user broker

### F4 tests

1. **TestStreamEvents_MarshalErrorLogged** — publish event with unmarshalable data, verify warn log emitted
2. **TestStreamUserEvents_MarshalErrorLogged** — same for user stream

---

## Next steps

1. **Implement F1** — migrate WorkspaceEventBroker to subscriber pattern with missedEvent + resync. This is the highest-impact change.
2. **Re-land PR #76** — after F1 is in place, PR #76's `streamTimedOut` banner becomes defense-in-depth rather than the primary recovery mechanism.
3. **Implement F2** — add `tool_result` and `error` streaming to `parseStreamEvent`.
4. **Implement F3** — add Prometheus counters for broker drops.
5. **Implement F4** — add warn-level logging for marshal failures.
6. **Address PR #76's documented gaps** — `model_enricher.go` context window parsing, SessionRetryBanner tests, error mapping tests.

---

## Files to modify

| Finding | Files |
|---|---|
| F1 | `api/internal/handlers/event_broker.go`, `api/internal/handlers/proxy.go` (StreamEvents), `api/internal/handlers/event_broker_test.go` (new), `api/internal/handlers/proxy_test.go` |
| F2 | `frontend/src/pages/ChatPage.tsx` (parseStreamEvent + StreamPart type), `frontend/src/pages/ChatPage.sse.test.tsx` |
| F3 | `api/internal/handlers/event_broker.go`, `api/internal/handlers/event_broker_user.go`, `api/internal/app/app.go` (metric registration) |
| F4 | `api/internal/handlers/proxy.go`, `api/internal/handlers/stream_user_events.go` |
