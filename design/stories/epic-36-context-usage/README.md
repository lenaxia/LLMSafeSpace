# Epic 36: Fix Context Usage Calculation

**Status:** Design
**Created:** 2026-06-10
**Priority:** High
**Depends on:** Epic 22 (agentd health/isolation)
**Related:** docs/0049 (context limit UX — fixed the total, not the used)

---

## Problem Statement

The context usage bar shows **cumulative tokens** (everything ever consumed across all turns) instead of **current context window size** (prompt tokens from the last LLM call).

### Root cause

`cmd/workspace-agentd/main.go:701`:
```go
totalTokens += s.Tokens.Input + s.Tokens.Output + s.Tokens.Reasoning
```

These tokens come from `GET /session`, which returns cumulative session totals (additive via SQL `column + value` in opencode's `projector.ts:97-109`). A session with 5 turns consuming 50K input + 30K output shows 80K "used", but the current context is ~15K.

### What we want

Current context size = raw prompt tokens from the last LLM call = `tokens.input + tokens.cache.read + tokens.cache.write` on the last assistant message (opencode overwrites per step, not accumulates — `processor.ts:718`, `message-updater.ts:216`).

---

## Validated Assumptions

Each assumption was checked against the actual codebase before being used in the design.

| # | Assumption | Status | Evidence |
|---|---|---|---|
| V1 | `session.next.step.ended` SSE event carries `tokens.input + tokens.cache.read + tokens.cache.write` = raw prompt size | ✅ Validated | opencode `publish-llm-event.ts:17-27` maps `nonCachedInputTokens`, `cacheReadInputTokens`, `cacheWriteInputTokens` from LLM Usage. `session.ts:384-425` stores `tokens.input = rawInputTokens - cacheRead - cacheWrite`. Prompt size = `tokens.input + cache.read + cache.write`. |
| V2 | `session.next.step.ended` fires on the instance SSE stream (`/event`) which agentd subscribes to | ✅ Validated | agentd `main.go:322` connects to `getAgentAddr()+"/event"`. opencode emits `session.next.step.ended` as a flat SSE event with type field. |
| V3 | Last assistant message tokens = last step's tokens (overwritten, not accumulated) | ✅ Validated | `processor.ts:718`: `ctx.assistantMessage.tokens = usage.tokens` (overwrite `=`). `message-updater.ts:216`: `draft.tokens = event.data.tokens` (overwrite `=`). |
| V4 | opencode supports multiple concurrent SSE connections with fan-out | ✅ Validated | `event.ts:31`: per-connection `Queue.unbounded`. `event.ts:630`: `listeners.push()` — no limit. Each connection gets full event copy. |
| V5 | SSE reconnection has a gap (events lost during disconnect) | ✅ Validated | `main.go:316`: 5-minute hard timeout. `main.go:289-296`: 2s minimum backoff. Status map preserved across reconnects but events during gap are lost. |
| V6 | V1 `GET /session/:id/message?limit=N` returns most recent N messages in chronological order | ✅ Validated | V1 handler at `session.ts:104-143`: when `limit > 0`, calls `MessageV2.page({limit})` which queries `ORDER BY seq DESC LIMIT N+1`, then `.reverse()`. Returns newest N in chronological order. |
| V7 | `omitempty` on `ContextUsed`/`ContextTotal` means zero values are omitted from JSON | ✅ Validated | CRD: `workspace_types.go:278-279`, API: `types.go:470-471`, Frontend: `types.ts:111-112`. All use `omitempty`/`?`. |
| V8 | CRD context values are never cleared — persist across pod restarts | ✅ Validated | `health.go:284-287`: only writes when `status.Context != nil`. No code path sets to 0. Values persist indefinitely. |
| V9 | agentd does NOT have Redis connectivity | ✅ Validated | Zero Redis imports in `cmd/workspace-agentd/`. No Redis env vars in `pod_builder.go`. Network policy `workspace-network-policy.yaml` blocks Redis egress. `redis_cache.go` used only by API server. |

### Assumption that was WRONG

| # | Original claim | Reality | Impact |
|---|---|---|---|
| ~~A1~~ | agentd has Redis connectivity | **No Redis access**. No imports, no env vars, network policy blocks egress. | **Phase 2 (Redis event bus) is not feasible without infra changes.** Adding Redis to sandbox pods is a security boundary change — requires its own epic. Removed from this epic. |

---

## Failure Modes

### FM1 — SSE gap on agentd restart (HIGH)

**Scenario:** agentd restarts. contextTracker's in-memory map is empty. Until SSE reconnects and a new `session.next.step.ended` fires, statusz returns 0 context for all sessions.

**Mitigation:** Background `fillGaps()` goroutine fetches message history for sessions without SSE data. Runs every 30s. First fill completes within 30s of agentd start. statusz shows 0 until fill completes — accurate (we don't know yet).

**Residual risk:** 0-30s window after agentd start where context shows nothing. Frontend already handles `contextUsed=undefined` (no bar rendered). Acceptable.

### FM2 — SSE gap during reconnection (MEDIUM)

**Scenario:** SSE connection drops (5-min timeout, network blip). During 2-30s reconnection gap, `session.next.step.ended` events are lost. contextTracker retains stale values.

**Mitigation:** Values from last successful event are preserved in the map. Stale is better than wrong (cumulative). On reconnect, next `session.next.step.ended` updates to current value. Max staleness: 5min + 30s reconnection + time until next LLM call.

**Residual risk:** If a session was compacted during the gap, context bar shows pre-compaction value until next LLM call. Acceptable — compaction is fast and next call updates quickly.

### FM3 — Cold-start message fetch fails (LOW)

**Scenario:** `fetchSessionPromptTokens` calls opencode's message API and gets an error (opencode busy, timeout).

**Mitigation:** Returns 0, logs warning. Retries on next `fillGaps` tick (30s). Graceful degradation.

**Residual risk:** Extended period of 0 context. Acceptable — context bar is informational, not safety-critical.

### FM4 — Context values persist across pod recreation (MEDIUM)

**Scenario:** Workspace pod is recreated (upgrade, eviction). New pod has different sessions. CRD still has context values from old pod's sessions.

**Existing behavior:** This already happens with the current cumulative calculation. Not a regression.

**Mitigation (future):** CRD context values should be cleared on phase transitions that recreate the pod. This is a separate concern — tracked in open questions.

### FM5 — `session.next.step.ended` not emitted by all providers (LOW)

**Scenario:** Some LLM providers may not return usage data. opencode's `Usage` fields are all `Schema.optional`. If the provider returns no usage, `step-finish` has `usage: undefined`.

**Mitigation:** `getUsage()` returns tokens with all zeros. contextTracker stores 0 for that session. Frontend shows 0/total. Not ideal but not wrong.

**Residual risk:** Context bar shows 0 for providers that don't report usage. No way to distinguish "provider doesn't report" from "session just started". Acceptable.

---

## Design Critique

The following issues were identified during self-review and addressed in the implementation plan.

### W1 — fillGaps unbounded latency (CRITICAL) — FIXED

If 5 sessions need cold-start data, `fillGaps` makes 5 sequential HTTP calls (5s timeout each = 25s worst case). The 30s ticker fires again before the first iteration finishes, starting a concurrent second iteration. Two goroutines race to fetch the same sessions, doubling load on opencode.

**Fix:** Add `sync.Mutex` to prevent concurrent iterations. Add per-iteration deadline (20s). Skip remaining sessions if deadline exceeded. See S36.2 implementation.

### W2 — fillGaps violates SRP on sessionStatusTracker (MEDIUM) — FIXED

The tracker's responsibility is "track session state from SSE events". `fillGaps` is cold-start HTTP data hydration — a different responsibility. Conflating SSE event handling with HTTP backfill makes the tracker harder to test and reason about.

**Fix:** `fillGaps` becomes a standalone function. It writes to the tracker via `setPromptTokens` but lifecycle and HTTP logic live outside. See S36.2 implementation.

### W3 — processEvent doubles parse cost for high-frequency events (MEDIUM) — FIXED

Current `processEvent` early-returns for non-`session.status` events after one JSON unmarshal. The refactored version must parse the envelope for all events to determine the type. For high-frequency events (`message.part.updated` on every streamed token), this doubles parse cost.

**Fix:** Parse flat envelope first (cheap — just `{"type":"..."}`). Only attempt nested parse if flat fails. Skip immediately if type matches no handler. Add debug log for unknown types (throttled). See S36.1 implementation.

### W4 — sessionId undefined on initial load (LOW) — ACKNOWLEDGED

`ChatPage.tsx:33`: `sessionId` from `useParams()` is undefined when first navigating to a workspace. Auto-create fires. Until session exists, `contextUsed` is undefined, no bar rendered. Correct behavior. Explicitly tested in S36.4.

### W5 — fetchSessionPromptTokens uses 5s timeout (LOW) — FIXED

`doRequest` uses `c.client` with `Timeout: 5s`. For 20 messages with large tool outputs, this may timeout. Would cause false cold-start failures.

**Fix:** Use context with 10s deadline for message history fetches. See S36.2 implementation.

### W6 — Stale promptTokens on session deletion (LOW) — ACKNOWLEDGED

Same limitation as existing `statuses` map. `prune` only runs on cache miss. Stale entries linger briefly. Not a regression. Acceptable.

---

## Design

### Architecture

```
opencode (step-finish)
  │ SSE: session.next.step.ended
  │ { sessionID, tokens: { input, output, reasoning, cache: { read, write } } }
  ▼
agentd sessionStatusTracker (in-memory)
  │ promptTokens[sessionID] = input + cache.read + cache.write
  │
  ├──► statusz handler reads from tracker
  │         │
  │         ├──► SessionInfo.ContextUsed (per-session) ──┐
  │         └──► ContextUsage.TotalTokens (model limit)   │
  │                                                       │
  │         ▼                                             │
  │     Controller scrapes every 60s → CRD status         │
  │         │                                             │
  │         ▼                                             │
  │     API reads CRD → frontend WorkspaceStatus          │
  │         │                                             │
  │         ▼                                             │
  │     ChatPage finds active session → DiskUsageBar      │
  │     contextUsed = sessions[activeID].contextUsed  ◄───┘
  │     contextTotal = contextTotal (model limit, same for all)
  │
  └──► fillGaps (background, 30s cadence)
         fetches message history for sessions without SSE data
```

No new infrastructure. No Redis. No PG. Fixes the bug using existing data paths.

### SSE connection strategy

**Decision: Extend the existing `sessionStatusTracker`, not a separate struct.**

Rationale (SOLID analysis):
- **Single Responsibility**: The tracker's responsibility is "track session state from SSE events". Prompt tokens are session state. Same responsibility.
- **Open/Closed**: Adding a new event type to `processEvent` is an extension, not a modification of existing behavior. The existing `session.status` handling is untouched.
- Adding a separate `contextTracker` with its own SSE connection would double the fan-out load on opencode (V4 — opencode supports it, but V5 — reconnection gaps become twice as likely, and Epic 22 established that opencode is sensitive to backpressure).

**Implementation:** Refactor `processEvent` from handling only `session.status` to dispatching on event type. Add a `session.next.step.ended` case. Add `promptTokens map[string]int64` to the existing struct.

### Per-session context — the only correct number

**Decision: DiskUsageBar shows the active session's context usage. Top-level aggregate is retained for CRD backward compat only.**

Rationale:
- Sessions have independent context windows. The context bar is shown on the ChatPage, which is always viewing a specific session. The bar should show **that session's** context usage, not an aggregate across all sessions.
- `max(promptTokens across sessions)` is not a meaningful number for any session.
- The frontend already knows which session is active (ChatPage is session-scoped). It looks up the active session's `contextUsed` from the `sessions` array in `WorkspaceStatus`.
- Top-level `ContextUsage{UsedTokens, TotalTokens}` is retained in statusz and CRD for backward compat (other consumers may read it), but the frontend **stops using it**. `TotalTokens` (the model's context limit) is still read from the top-level field since it's the same for all sessions.
- Per-session `ContextUsed` on `SessionInfo` flows through statusz → controller → CRD → API → frontend. All 6 layers must carry it.

### Cold-start: background fillGaps

**Decision: Background goroutine, not inline in statusz.**

Rationale:
- Epic 22's purpose was eliminating slow opencode calls from statusz. Adding `fetchSessionPromptTokens` inline would regress Epic 22.
- 30s cadence is acceptable. First fill within 30s of agentd start.
- Uses V1 `GET /session/:id/message?limit=20` — fetches 20 most recent messages, iterates in reverse to find last assistant message with tokens.

---

## Implementation Plan

### S36.1 — Extend sessionStatusTracker with prompt tokens (~1.5pt)

**Files:** `cmd/workspace-agentd/main.go`

**Changes to `sessionStatusTracker`:**
- Add `promptTokens map[string]int64` field (initialized in constructor)
- Add `mu` is already shared — reuse same mutex for both maps
- Refactor `processEvent` to handle multiple event types (W3 fix — cheap early skip for unknown types):
  ```go
  func (t *sessionStatusTracker) processEvent(data string) {
      // Parse flat envelope first (cheap). Only try nested if flat fails.
      var evt struct {
          Type       string          `json:"type"`
          Properties json.RawMessage `json:"properties"`
      }
      if json.Unmarshal([]byte(data), &evt) != nil {
          return
      }
      // Early skip for unknown types — avoids double parse for high-frequency events (W3).
      switch evt.Type {
      case "session.status":
          // current handler, unchanged
          t.handleSessionStatus(evt.Properties)
      case "session.next.step.ended":
          t.handleStepEnded(evt.Properties)
      default:
          // Not an event we handle. Log at debug level (throttled) for discoverability (W5).
          return
      }
  }
  ```

  Note: The nested envelope format is only used by the global SSE endpoint (`/global/event`). agentd connects to the instance endpoint (`/event`), which always uses flat format. The existing nested-format code path is retained as a fallback by keeping the original `handleSessionStatus` implementation — it already handles both formats internally. No change needed for the nested path.
- New `handleStepEnded` method:
  ```go
  func (t *sessionStatusTracker) handleStepEnded(props json.RawMessage) {
      var data struct {
          SessionID string `json:"sessionID"`
          Tokens    *struct {
              Input     int64 `json:"input"`
              Output    int64 `json:"output"`
              Reasoning int64 `json:"reasoning"`
              Cache     struct {
                  Read  int64 `json:"read"`
                  Write int64 `json:"write"`
              } `json:"cache"`
          } `json:"tokens"`
      }
      if json.Unmarshal(props, &data) != nil || data.SessionID == "" || data.Tokens == nil {
          return
      }
      promptTokens := data.Tokens.Input + data.Tokens.Cache.Read + data.Tokens.Cache.Write
      t.mu.Lock()
      t.promptTokens[data.SessionID] = promptTokens
      t.mu.Unlock()
  }
  ```
- Add `getPromptTokens(sessionID string) int64` (RLock, return 0 if missing)
- Add `hasPromptTokens(sessionID string) bool` (RLock, check existence)
- Update `prune` to also clean `promptTokens` map
- Refactor `handleSessionStatus` from existing inline code (no behavior change)

**TDD cycle:**
1. Write `TestProcessEvent_StepEnded_CapturesPromptTokens`
2. Write `TestProcessEvent_StepEnded_MissingTokensIgnored`
3. Write `TestProcessEvent_StepEnded_EmptySessionIDIgnored`
4. Write `TestGetPromptTokens_NoData_ReturnsZero`
5. Write `TestPrune_RemovesPromptTokens`
6. Write `TestProcessEvent_SessionStatus_UnchangedBehavior` (regression)
7. Implement, verify all pass

### S36.2 — Cold-start background fillGaps (~1pt)

**Files:** `cmd/workspace-agentd/main.go`

**New method on `OpenCodeClient`:**

V1 `GET /session/:id/message?limit=20` returns `Array<{info: {role, tokens}, parts}>` (validated — `WithParts` at `v1/session.ts:491`). The `info` field contains the `User` or `Assistant` message data. Only `Assistant` messages have a `tokens` field. The response is in chronological order with the most recent messages when `limit` is specified (V1 handler calls `MessageV2.page` which queries `ORDER BY seq DESC LIMIT N+1` then `.reverse()`).

```go
func (c *OpenCodeClient) fetchSessionPromptTokens(ctx context.Context, sessionID string) int64 {
    // Use longer timeout than default 5s — message payloads can be large (W5).
    fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    resp, err := c.doRequest(fetchCtx, "/session/"+sessionID+"/message?limit=20")
    if err != nil { return 0 }
    defer resp.Body.Close()

    var messages []struct {
        Info struct {
            Role   string `json:"role"`
            Tokens *struct {
                Input int64 `json:"input"`
                Cache struct {
                    Read  int64 `json:"read"`
                    Write int64 `json:"write"`
                } `json:"cache"`
            } `json:"tokens"`
        } `json:"info"`
    }
    if json.NewDecoder(resp.Body).Decode(&messages) != nil { return 0 }

    // V1 returns chronological order with limit. Iterate reverse for newest.
    for i := len(messages) - 1; i >= 0; i-- {
        if messages[i].Info.Role == "assistant" && messages[i].Info.Tokens != nil {
            return messages[i].Info.Tokens.Input + messages[i].Info.Tokens.Cache.Read + messages[i].Info.Tokens.Cache.Write
        }
    }
    return 0
}
```

**Why limit=20 is sufficient:** The endpoint returns **messages** (user/assistant pairs), not steps. 25 LLM calls in one assistant turn = 1 assistant message with 25 step-finish parts. The assistant message's tokens field contains the last step's tokens (overwritten per step — V3). So `limit=20` returns up to 20 messages (~10 turns) — the last assistant message is always among them.

**Standalone background function (W2 fix — not on sessionStatusTracker):**

```go
type fillGapsState struct {
    mu         sync.Mutex
    running    bool
}

func fillGaps(ctx context.Context, client *OpenCodeClient, tracker *sessionStatusTracker, sessions func() []agentd.SessionInfo, state *fillGapsState) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            runFill(ctx, client, tracker, sessions, state)
        }
    }
}

func runFill(ctx context.Context, client *OpenCodeClient, tracker *sessionStatusTracker, sessions func() []agentd.SessionInfo, state *fillGapsState) {
    // Prevent concurrent iterations (W1 fix).
    state.mu.Lock()
    if state.running {
        state.mu.Unlock()
        return
    }
    state.running = true
    state.mu.Unlock()
    defer func() { state.running = false }()

    // Per-iteration deadline (W1 fix). Skip remaining sessions if exceeded.
    iterCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()

    for _, s := range sessions() {
        if tracker.hasPromptTokens(s.ID) {
            continue
        }
        select {
        case <-iterCtx.Done():
            return
        default:
        }
        if tokens := client.fetchSessionPromptTokens(iterCtx, s.ID); tokens > 0 {
            tracker.setPromptTokens(s.ID, tokens)
        }
    }
}
```

Started in `main()` alongside existing `go sseTracker.subscribe(...)`:
```go
fillState := &fillGapsState{}
go fillGaps(context.Background(), client, sseTracker, func() []agentd.SessionInfo {
    cache.mu.Lock()
    sessions := cache.sessions
    cache.mu.Unlock()
    return sessions
}, fillState)
```

Key design decisions:
- **Standalone function, not method on tracker** (W2) — tracker owns data, fillGaps owns HTTP lifecycle.
- **`fillGapsState` mutex prevents concurrent iterations** (W1) — if previous iteration is still running, skip this tick.
- **20s per-iteration deadline** (W1) — won't exceed one ticker period.
- **Context checked between sessions** (W1) — stops mid-iteration if deadline exceeded.
- **10s per-fetch timeout** (W5) — separate from the default 5s client timeout.

**TDD cycle:**
1. Write `TestFetchSessionPromptTokens_AssistantWithTokens`
2. Write `TestFetchSessionPromptTokens_NoAssistant_ReturnsZero`
3. Write `TestFetchSessionPromptTokens_APIError_ReturnsZero`
4. Write `TestFillGaps_SkipsKnownSessions`
5. Write `TestFillGaps_FillsUnknownSessions`
6. Write `TestFillGaps_SkipsIfAlreadyRunning` — concurrent invocation blocked
7. Write `TestFillGaps_StopsOnDeadline` — doesn't exceed 20s
8. Implement, verify all pass

### S36.3 — Fix statusz handler + thread per-session context through all layers (~1.5pt)

**Files:** `cmd/workspace-agentd/main.go`, `pkg/agentd/types.go`, `pkg/apis/llmsafespace/v1/workspace_types.go`, `controller/internal/workspace/health.go`, `pkg/types/types.go`, `api/internal/services/workspace/workspace_service.go`, `frontend/src/api/types.ts`

Per-session context must flow through 6 layers to reach the frontend. Each layer needs a new field:

| Layer | Type | File | Change |
|---|---|---|---|
| agentd | `SessionInfo` | `pkg/agentd/types.go` | Add `ContextUsed int64` |
| CRD | `AgentSessionStatus` | `pkg/apis/llmsafespace/v1/workspace_types.go` | Add `ContextUsed int64` |
| Controller | `enrichAgentStatus` | `controller/internal/workspace/health.go` | Copy `s.ContextUsed` |
| API types | `SessionStatusItem` | `pkg/types/types.go` | Add `ContextUsed int64` |
| API service | `GetWorkspaceStatus` | `api/internal/services/workspace/workspace_service.go` | Copy `s.ContextUsed` |
| Frontend | `AgentSessionInfo` | `frontend/src/api/types.ts` | Add `contextUsed?: number` |

**Layer 1 — agentd types:**
```go
type SessionInfo struct {
    ID          string         `json:"id"`
    Title       string         `json:"title,omitempty"`
    Status      string         `json:"status"`
    Tokens      *SessionTokens `json:"tokens,omitempty"`
    Model       string         `json:"model,omitempty"`
    ContextUsed int64          `json:"contextUsed,omitempty"`
}
```

**Layer 2 — CRD types:**
```go
type AgentSessionStatus struct {
    ID          string `json:"id"`
    Title       string `json:"title,omitempty"`
    Status      string `json:"status"`
    ContextUsed int64  `json:"contextUsed,omitempty"`
}
```

**Layer 3 — Controller (health.go:248-251):**
```go
// Before:
sessions[i] = v1.AgentSessionStatus{ID: s.ID, Title: s.Title, Status: s.Status}
// After:
sessions[i] = v1.AgentSessionStatus{ID: s.ID, Title: s.Title, Status: s.Status, ContextUsed: s.ContextUsed}
```

**Layer 4 — API types:**
```go
type SessionStatusItem struct {
    ID          string `json:"id"`
    Title       string `json:"title,omitempty"`
    Status      string `json:"status"`
    ContextUsed int64  `json:"contextUsed,omitempty"`
}
```

**Layer 5 — API service (workspace_service.go:581-584):**
```go
// Before:
result.Sessions = append(result.Sessions, types.SessionStatusItem{
    ID: s.ID, Title: s.Title, Status: s.Status,
})
// After:
result.Sessions = append(result.Sessions, types.SessionStatusItem{
    ID: s.ID, Title: s.Title, Status: s.Status, ContextUsed: s.ContextUsed,
})
```

**Layer 6 — Frontend:**
```typescript
export interface AgentSessionInfo {
  id: string;
  title?: string;
  status: string;
  contextUsed?: number;
}
```

**Statusz handler change — replace cumulative loop:**

Before (lines 697-706):
```go
var totalTokens int64
for _, s := range sessions {
    if s.Tokens != nil {
        totalTokens += s.Tokens.Input + s.Tokens.Output + s.Tokens.Reasoning
    }
    ...
}
```

After:
```go
var modelID string
for i, s := range sessions {
    if pt := sseTracker.getPromptTokens(s.ID); pt > 0 {
        sessions[i].ContextUsed = pt
    }
    if modelID == "" && s.Model != "" {
        modelID = s.Model
    }
}
// Top-level TotalTokens = model context limit (same for all sessions).
// UsedTokens is no longer meaningful as an aggregate — frontend uses
// per-session ContextUsed instead. Set UsedTokens=0 to signal this.
contextLimit := client.ModelContextLimit(r.Context(), modelID, "")
if len(sessions) > 0 {
    contextUsage = &agentd.ContextUsage{
        UsedTokens:  0, // per-session ContextUsed is the source of truth
        TotalTokens: contextLimit,
    }
}
```

Note: `ContextUsage.UsedTokens` is set to 0 (which means omitted via `omitempty`). The frontend no longer reads it — it uses the active session's `contextUsed` instead. `TotalTokens` (model limit) is still read from the top-level field.

**TDD cycle:**
1. Write `TestStatuszEndpoint_ContextUsage_PromptTokens` — verify per-session and aggregate
2. Write `TestStatuszEndpoint_ContextUsage_ColdStart` — no SSE data → 0
3. Write `TestStatuszEndpoint_ContextUsage_EmptySessions` — no context field
4. Write `TestStatuszEndpoint_ContextUsage_MultipleSessions` — max is aggregate
5. Write `TestCheckAgentHealth_ThreadsContextUsed` — controller copies per-session field to CRD
6. Write `TestGetWorkspaceStatus_IncludesSessionContextUsed` — API returns per-session field
7. Implement all layers, verify all pass

### S36.4 — Frontend: use active session's context (~1pt)

**Files:** `frontend/src/pages/ChatPage.tsx`, `frontend/src/components/workspace/DiskUsageBar.tsx`

Today, ChatPage passes `status?.contextUsed` and `status?.contextTotal` to DiskUsageBar — the top-level aggregate. This must change to use the active session's `contextUsed`:

```typescript
// ChatPage.tsx — find the active session
const activeSession = status?.sessions?.find(s => s.id === activeSessionID)
const contextUsed = activeSession?.contextUsed
const contextTotal = status?.contextTotal // model limit, same for all sessions

<DiskUsageBar
  diskUsedBytes={status?.diskUsedBytes}
  diskTotalBytes={status?.diskTotalBytes}
  memoryUsedBytes={status?.memoryUsedBytes}
  memoryTotalBytes={status?.memoryTotalBytes}
  contextUsed={contextUsed}
  contextTotal={contextTotal}
/>
```

When `contextUsed` drops by >50% from previous render (compaction):
- Show "Context compacted" label that fades after 3s
- Track previous value via `useRef`
- Animate progress bar transition (CSS transition already in place)

**TDD cycle:**
1. Write `TestDiskUsageBar_ActiveSession_ContextUsed` — shows active session's context
2. Write `TestDiskUsageBar_ActiveSession_NoContextUsed` — no data yet, no bar
3. Write `TestDiskUsageBar_CompactionIndicator_ShownOnDrop`
4. Write `TestDiskUsageBar_CompactionIndicator_NotShownOnNormalChange`
5. Write `TestDiskUsageBar_CompactionIndicator_FadesAfter3s`
6. Implement

### S36.5 — Frontend: per-session context in session list (~0.5pt)

**Files:** `frontend/src/components/workspace/DiskUsageBar.tsx`, `frontend/src/api/types.ts`

When multiple sessions exist, show a compact context indicator next to each session in the sidebar or session list. This is a small label (e.g. "50K / 200K") rather than a full progress bar. Uses the same `contextUsed` field from `AgentSessionInfo`.

The main DiskUsageBar on ChatPage always shows the active session (S36.4). This story adds per-session indicators in the sidebar.

**TDD cycle:**
1. Write `TestSessionList_ContextIndicator_SingleSession`
2. Write `TestSessionList_ContextIndicator_MultipleSessions`
3. Implement

---

## Test Plan

### Unit Tests

| Test | What it validates | File |
|---|---|---|
| `TestProcessEvent_StepEnded_CapturesPromptTokens` | Prompt tokens stored correctly from SSE event | `main_test.go` |
| `TestProcessEvent_StepEnded_MissingTokensIgnored` | Graceful on nil tokens | `main_test.go` |
| `TestProcessEvent_StepEnded_EmptySessionIDIgnored` | Graceful on missing sessionID | `main_test.go` |
| `TestProcessEvent_StepEnded_NestedFormat` | Works with nested SSE envelope | `main_test.go` |
| `TestGetPromptTokens_NoData_ReturnsZero` | Zero when no event received | `main_test.go` |
| `TestGetPromptTokens_ExistingData_ReturnsValue` | Value preserved after set | `main_test.go` |
| `TestPrune_RemovesPromptTokens` | Stale entries cleaned | `main_test.go` |
| `TestProcessEvent_SessionStatus_UnchangedBehavior` | Regression: existing status tracking still works | `main_test.go` |
| `TestFetchSessionPromptTokens_AssistantWithTokens` | Happy path: last assistant found | `main_test.go` |
| `TestFetchSessionPromptTokens_NoAssistant_ReturnsZero` | Only user messages | `main_test.go` |
| `TestFetchSessionPromptTokens_APIError_ReturnsZero` | opencode returns error | `main_test.go` |
| `TestFetchSessionPromptTokens_InvalidJSON_ReturnsZero` | Malformed response | `main_test.go` |
| `TestFillGaps_SkipsKnownSessions` | No re-fetch for sessions with SSE data | `main_test.go` |
| `TestFillGaps_FillsUnknownSessions` | Fetches and stores for sessions without data | `main_test.go` |
| `TestFillGaps_SkipsIfAlreadyRunning` | Concurrent invocation blocked by mutex (W1) | `main_test.go` |
| `TestFillGaps_StopsOnDeadline` | Returns before 20s deadline (W1) | `main_test.go` |

### Integration Tests

| Test | What it validates | File |
|---|---|---|
| `TestStatuszEndpoint_ContextUsage_PromptTokens` | E2E: SSE event → statusz returns correct per-session values | `main_test.go` |
| `TestStatuszEndpoint_ContextUsage_ColdStart` | No SSE data → fillGaps → statusz shows values | `main_test.go` |
| `TestStatuszEndpoint_ContextUsage_EmptySessions` | No sessions → no context field | `main_test.go` |
| `TestStatuszEndpoint_ContextUsage_UsedTokensIsZero` | Top-level UsedTokens=0, per-session ContextUsed set | `main_test.go` |
| `TestStatuszEndpoint_OldFieldsUnchanged` | Regression: disk, memory, CPU, sessions still correct | `main_test.go` |

### E2E Tests (controller → CRD → API → frontend)

| Test | What it validates | How |
|---|---|---|
| CRD context values reflect prompt tokens | Controller scrapes statusz → CRD has non-cumulative values | Check CRD status after statusz returns prompt tokens |
| API returns per-session context | `GET /workspaces/:id/status` includes `contextUsed` per session | Check API response when CRD has per-session data |
| Frontend shows non-cumulative context | DiskUsageBar shows current context, not cumulative | Manual verification (no E2E framework) |

### Regression Tests

| Existing test | Must still pass | Why |
|---|---|---|
| `TestListSessions_HappyPath` | ✅ | ListSessions unchanged |
| `TestListSessions_EmptyList` | ✅ | ListSessions unchanged |
| `TestStatuszEndpoint_IncludesSessionsAndDisk` | ✅ | Disk/memory/CPU paths unchanged |
| `TestSessionStatusTracker_SetAndGet` | ✅ | Status tracking unchanged |
| `TestSessionStatusTracker_ProcessEvent_Flat` | ✅ | session.status still handled |
| `TestSessionStatusTracker_ProcessEvent_Nested` | ✅ | Nested format still handled |
| `TestSessionStatusTracker_MergesIntoCachedState` | ✅ | SSE→cache merge unchanged |
| `TestCheckAgentHealth_PopulatesSessions` | ✅ | Controller session mapping updated but test still passes |
| `TestCheckAgentHealth_SessionsWithoutTitles` | ✅ | Controller session mapping updated but test still passes |
| `TestCheckAgentHealth_SetsActiveSessions` | ✅ | ActiveSessions logic unchanged |
| `All workspace_service_test.go tests` | ✅ | API reads CRD, no structural change |
| `All DiskUsageBar.test.tsx tests` | ✅ | Existing rendering unchanged |

### CRD DeepCopy regeneration

`AgentSessionStatus` gains a new field. The controller uses `k8s.io/code-generator` for `DeepCopy` — must run `make deepcopy` after changing `workspace_types.go` and verify the generated code compiles.

---

## Acceptance Criteria

- [ ] Context usage bar shows the **active session's** current context size, not cumulative tokens and not a cross-session aggregate
- [ ] Per-session `contextUsed` flows through all 6 layers: agentd → CRD → controller → API types → API service → frontend
- [ ] Top-level `ContextUsage.UsedTokens` set to 0 (no longer used by frontend)
- [ ] Top-level `ContextUsage.TotalTokens` = model context limit (still used by frontend)
- [ ] Cold-start: sessions without SSE data get context from message history within 30s
- [ ] No regression to Epic 22 (no extra opencode calls in statusz hot path)
- [ ] No regression to existing session status tracking
- [ ] Compaction indicator shown when context drops >50%
- [ ] Per-session context indicators visible in session list/sidebar
- [ ] CRD DeepCopy regenerated and compiles
- [ ] All existing tests pass
- [ ] TDD discipline followed — tests written before implementation

---

## Scope Exclusions (deferred to future epics)

| Item | Why deferred |
|---|---|
| Redis event bus (real-time push) | Requires adding Redis connectivity to sandbox pods. Security boundary change. Separate epic. |
| PG table for context data | Only valuable with Redis event bus. |
| SSE `workspace.context_usage` event | Only valuable with real-time push path. |
| CRD context value cleanup on pod recreation | Separate concern. Tracked in open questions. |
| `model_enricher.go` reading `max_input_tokens` | Already tracked in docs/0049 as a known gap. |

---

## Open Questions

| # | Question | Resolution |
|---|---|---|
| Q1 | Should CRD `contextUsed`/`contextTotal` be cleared when the pod is recreated? | Recommend yes — clear on phase transitions that recreate the pod. Separate PR. |
| Q2 | `fillGaps` fetches limit=20 messages. Is 20 sufficient? | Most sessions have <20 messages between agentd restarts. If a session has >20, we may miss the last assistant message. Increase to 50 if needed after testing. |
| Q3 | Should the controller poll interval (60s) be reduced for faster context updates? | Not in this epic. 60s is acceptable for a context bar. Reducing it would increase load. Real-time push (future epic) is the right solution. |
| Q4 | Should top-level `ContextUsage.UsedTokens` be removed from the CRD entirely, or kept at 0 for backward compat? | Keep at 0 for now. Removing CRD fields requires a migration. Tracked as design debt. |

---

## Dependency Graph

```
S36.1 (extend tracker: handle step-ended, store prompt tokens)
  └── S36.2 (fillGaps: background cold-start)
        └── S36.3 (statusz: per-session contextUsed + thread through all 6 layers)
              ├── S36.4 (frontend: active session contextUsed + compaction indicator)
              └── S36.5 (frontend: per-session indicators in sidebar)
```

Total estimated effort: ~4pt

---

## Design Debt

- **No real-time context updates** — 60s polling interval. Context bar can be stale for up to 60s. Acceptable for informational display.
- **No per-session context limit** — all sessions report against the same model's context limit. If sessions use different models, the total is wrong for some. Requires per-session model tracking.
- **Top-level `ContextUsage.UsedTokens` is unused** — retained in CRD for backward compat but set to 0. Any external consumer reading it will see no data. Should be removed in a future cleanup.
