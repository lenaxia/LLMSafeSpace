# Worklog 0200 — Epic 36: Fix Context Usage Calculation

**Date:** 2026-06-10
**Session:** Design, validation, and partial implementation of Epic 36 — fixing the context usage bar from cumulative to current prompt tokens
**Status:** In Progress

---

## Objective

The context usage bar in the frontend was showing **cumulative tokens** (everything ever consumed across all turns in all sessions) instead of **current context window size** (prompt tokens from the last LLM call). Fix the calculation end-to-end and thread per-session context data through all 6 layers to the frontend.

---

## Work Completed

### Design (epic-36)

Created `design/stories/epic-36-context-usage/README.md` — full epic design document. Multiple iterations with assumptions validated against actual code before being accepted.

**Key validated assumptions:**
- `session.next.step.ended` SSE event carries `tokens.input + tokens.cache.read + tokens.cache.write` = raw prompt size (`publish-llm-event.ts:17-27`, `session.ts:384-425`)
- Last assistant message tokens are overwritten per step, not accumulated (`processor.ts:718`, `message-updater.ts:216`) — the last message's tokens correctly represent current context
- agentd does NOT have Redis connectivity — zero imports, no env vars injected, network policy blocks Redis egress from sandbox pods. This invalidated the Redis event bus design (Phases 2-4 deferred to a future infra epic)
- V1 `GET /session/:id/message` returns `Array<{info:{role,tokens}, parts}>` — the `info` field wraps the message data (parsing code in original design was wrong: was parsing `{role, tokens}` at the top level)
- V1 endpoint is NOT deprecated — opencode's own frontend uses V1 exclusively for all session operations

**Key design decisions:**
- `DiskUsageBar` shows the **active session's** `contextUsed` (looked up by `sessionId` from URL params in ChatPage), not a cross-session aggregate. `max(promptTokens)` is not a meaningful number for any session.
- Top-level `ContextUsage.UsedTokens` set to 0 — frontend stops reading it. `TotalTokens` (model limit) retained as the same for all sessions.
- `fillGaps` is a **standalone function**, not a method on `sessionStatusTracker` — SRP: tracker owns SSE event data, fillGaps owns HTTP backfill lifecycle.
- Single SSE connection — no second connection for context tracking, avoids doubling fan-out load on opencode (Epic 22 established opencode is sensitive to backpressure).
- `processEvent` dispatches on type first, early-skips unknown event types to avoid double-parse overhead on high-frequency events (`message.part.updated` fires on every streamed token).

**Failure modes documented and mitigated:**
- FM1 (SSE gap on restart) — fillGaps background goroutine, 30s cadence
- FM2 (SSE reconnection gap) — stale values preserved, updated on next step-finish
- FM3 (cold-start message fetch fails) — returns 0, retries on next tick
- FM4 (CRD values persist across pod recreation) — existing behavior, separate concern
- FM5 (provider doesn't report usage) — shows 0, indistinguishable from new session

**Design weaknesses found and fixed:**
- W1 (fillGaps unbounded latency): concurrent iteration prevention via `fillGapsState` mutex + 20s per-iteration deadline
- W2 (SRP violation): fillGaps extracted to standalone function
- W3 (double parse cost): early type dispatch before envelope parsing
- W5 (5s timeout too short): dedicated 10s context per message fetch

### Implementation (S36.1 — TDD ✅)

**`cmd/workspace-agentd/main.go`:**
- `sessionStatusTracker` gains `promptTokens map[string]int64`
- `processEvent` refactored: parses flat envelope first, early-skips unknown types, dispatches to `handleSessionStatus` (unchanged) or new `handleStepEnded`
- `handleStepEnded` captures `session.next.step.ended` events: computes `tokens.input + tokens.cache.read + tokens.cache.write`, stores in `promptTokens` map
- `prune` cleans both `statuses` and `promptTokens` maps
- New methods: `setPromptTokens`, `getPromptTokens`, `hasPromptTokens`

Tests written first (7 new + 9 regression, all pass):
- `TestSessionStatusTracker_ProcessEvent_StepEnded_CapturesPromptTokens`
- `TestSessionStatusTracker_ProcessEvent_StepEnded_MissingTokensIgnored`
- `TestSessionStatusTracker_ProcessEvent_StepEnded_EmptySessionIDIgnored`
- `TestSessionStatusTracker_ProcessEvent_StepEnded_NestedFormat`
- `TestSessionStatusTracker_GetPromptTokens_NoData_ReturnsZero`
- `TestSessionStatusTracker_GetPromptTokens_ExistingData_ReturnsValue`
- `TestSessionStatusTracker_HasPromptTokens`
- `TestSessionStatusTracker_Prune_RemovesPromptTokens`
- `TestSessionStatusTracker_ProcessEvent_SessionStatus_UnchangedBehavior`

### Implementation (S36.2 — TDD ✅)

**`cmd/workspace-agentd/main.go`:**
- `fetchSessionPromptTokens` on `OpenCodeClient`: calls `GET /session/:id/message?limit=20`, parses `[]struct{Info struct{Role string; Tokens *struct{...}} json:"info"}` (validated response shape), iterates reverse for last assistant message, returns `input + cache.read + cache.write`. Uses 10s context (separate from default 5s client timeout).
- `fillGapsState` struct: mutex + running flag prevents concurrent iterations
- `runFill` standalone function: checks mutex, 20s iteration deadline, context check between sessions, calls `fetchSessionPromptTokens` for sessions without SSE data
- `fillGaps` standalone function: 30s ticker, calls `runFill` each tick, exits on context cancellation
- Wired in `main()` alongside `sseTracker.subscribe`

Tests written first (7 new, all pass):
- `TestFetchSessionPromptTokens_AssistantWithTokens`
- `TestFetchSessionPromptTokens_NoAssistant_ReturnsZero`
- `TestFetchSessionPromptTokens_APIError_ReturnsZero`
- `TestFetchSessionPromptTokens_InvalidJSON_ReturnsZero`
- `TestFillGaps_SkipsKnownSessions`
- `TestFillGaps_FillsUnknownSessions`
- `TestFillGaps_SkipsIfAlreadyRunning`

### Implementation (S36.3 — partial, TDD incomplete ⚠️)

All 6 layers modified but **integration tests not yet written**. This is a TDD violation — implementation was done before tests. Flagged for immediate fix.

**Changes made:**
- `pkg/agentd/types.go` — `SessionInfo.ContextUsed int64 json:"contextUsed,omitempty"`
- `cmd/workspace-agentd/main.go` — statusz handler uses `sseTracker.getPromptTokens(s.ID)` per session, sets `sessions[i].ContextUsed`, top-level `ContextUsage.UsedTokens=0`
- `pkg/apis/llmsafespace/v1/workspace_types.go` — `AgentSessionStatus.ContextUsed int64`
- `controller/internal/workspace/health.go` — copies `s.ContextUsed` into `AgentSessionStatus`
- `pkg/types/types.go` — `SessionStatusItem.ContextUsed int64`
- `api/internal/services/workspace/workspace_service.go` — copies `s.ContextUsed`
- `frontend/src/api/types.ts` — `AgentSessionInfo.contextUsed?: number`

**Note:** DeepCopy regeneration skipped — `AgentSessionStatus` only contains value types (`string`, `int64`); `copy(*out, *in)` in the generated slice copy handles the new field automatically without code change.

All 104 agentd tests pass. 0 regressions.

---

## Remaining Work

- **S36.3 integration tests** (BLOCKED — TDD violation): must write before proceeding
  - `TestStatuszEndpoint_ContextUsage_PerSessionContextUsed` — SSE event → per-session ContextUsed in statusz
  - `TestStatuszEndpoint_ContextUsage_EmptySessions` — no context field when no sessions
  - `TestStatuszEndpoint_ContextUsage_ColdStart` — no SSE data → 0 per session
  - `TestStatuszEndpoint_OldFieldsUnchanged` — disk/memory/CPU unchanged
  - `TestCheckAgentHealth_ThreadsContextUsed` — controller copies ContextUsed to CRD
  - `TestGetWorkspaceStatus_IncludesSessionContextUsed` — API returns ContextUsed
- **S36.4** — Frontend: ChatPage uses `sessions.find(s => s.id === sessionId)?.contextUsed` instead of top-level `contextUsed`; compaction indicator when contextUsed drops >50%
- **S36.5** — Per-session context indicators in sidebar/session list

---

## Blockers

None. TDD gap in S36.3 is self-inflicted and being fixed immediately.

---

## Key Decisions

| Decision | Rationale |
|---|---|
| No Redis in this epic | agentd has no Redis access — no imports, no env vars, network policy blocks egress. Redis event bus is a future infra epic. |
| fillGaps standalone (not on tracker) | SRP: tracker handles SSE events, fillGaps handles HTTP backfill. |
| Top-level UsedTokens = 0 | max(promptTokens) is meaningless; frontend uses per-session ContextUsed from sessions array keyed by active sessionId. |
| limit=20 for message history | Returns messages (not steps) — 25 LLM calls = 1 assistant message. 20 messages covers ~10 turns, always includes the last assistant. |
| 10s timeout for message fetch | Default 5s client timeout too short for large message payloads. |

---

## Files Changed

- `design/stories/epic-36-context-usage/README.md` — epic design (new)
- `cmd/workspace-agentd/main.go` — S36.1, S36.2, S36.3 agentd changes
- `cmd/workspace-agentd/main_test.go` — 14 new tests (S36.1, S36.2) + 4 partial statusz integration tests
- `pkg/agentd/types.go` — `SessionInfo.ContextUsed`
- `pkg/apis/llmsafespace/v1/workspace_types.go` — `AgentSessionStatus.ContextUsed`
- `controller/internal/workspace/health.go` — copy ContextUsed into CRD
- `pkg/types/types.go` — `SessionStatusItem.ContextUsed`
- `api/internal/services/workspace/workspace_service.go` — copy ContextUsed in GetWorkspaceStatus
- `frontend/src/api/types.ts` — `AgentSessionInfo.contextUsed`
