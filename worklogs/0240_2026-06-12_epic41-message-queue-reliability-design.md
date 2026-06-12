# Worklog: Epic 41 — Message Queue Reliability Design

**Date:** 2026-06-12
**Session:** Deep-dive investigation of queued message failures; root cause analysis; design document for Epic 41
**Status:** Complete

---

## Objective

The user reported that queued messages (messages typed while a session is busy) do not work correctly after the v3 message queue rewrite (PR #93, worklog 0232). Specifically:
1. Messages don't seem to send when the session goes idle
2. Messages don't show up in chat history when the queue is cleared

This session was to deep-dive the root cause, validate all assumptions against source code (including the opencode TUI source at `anomalyco/opencode`), determine the correct level of abstraction for the fix, and produce a complete design document.

---

## Work Completed

### 1. Repo hygiene and branch setup

- Checked out `main`, found divergence with origin (local had `9a98e6e3`, remote had `8a6f074f`). Resolved via `git pull --rebase`.
- Created feature branch `fix/message-queue-deep-dive` for investigation.

### 2. Full codebase exploration

Used parallel task agents to map:
- Frontend: `useMessageQueue.ts`, `ChatPage.tsx`, `QueueSection.tsx`, `useChatStream.ts`, `useEventStream.ts`, `messages.ts`
- Backend: `proxy.go`, `session_tracker.go`, `event_broker.go`, `agent_drain.go`
- SDK: All four SDKs (Go, TypeScript, Python, Java) — confirmed no client-side queue exists in any SDK

### 3. opencode source investigation

Sparse-cloned `anomalyco/opencode` at HEAD. Verified:
- `promptAsync` handler (`session.ts:309-327`): forks `promptSvc.prompt()` in **group scope** (not request scope), returns `HttpApiSchema.NoContent` (204). The fork uses `Effect.forkIn(scope, { startImmediately: true })`.
- `prompt()` function (`prompt.ts:1105-1124`): calls `createUserMessage` (persists user message to DB) BEFORE calling `loop()`.
- `loop()` → `state.ensureRunning(sessionID, onInterrupt, runLoop(sessionID))` (`prompt.ts:1407`).
- `ensureRunning()` (`runner.ts:115-138`): when state is `Running` or `ShellThenRun`, **discards the new work** and awaits the existing run. New work (`runLoop`) is never executed.
- Runner state transition (`runner.ts:70-81`): state set to `{ _tag: "Idle" }` synchronously via `SynchronizedRef.modify`, THEN `onIdle` effect runs (which calls `status.set(sessionID, { type: "idle" })` to emit the SSE event). So runner is `Idle` BEFORE the SSE event is emitted.
- `MessageID` schema (`schema.ts:10-13`): `Schema.String.check(Schema.isStartsWith("msg"))` — our `"msg_"` prefix passes.
- `PromptInput` (`prompt.ts:1594-1615`): includes `messageID: Schema.optional(MessageID)`. opencode DOES use our provided `messageID`.
- `TextPartInput` (`core/src/v1/session.ts:399-412`): accepts `{ type: "text", text: "..." }` — our format is compatible.

### 4. Root cause identification

**Bug 1 (primary, always reproducible):** `reconcileOnIdle` unconditionally calls `setSseStreamParts([])` and `setLocalMessages([])` after refetching history. This is triggered by the idle event from the PREVIOUS message, while the NEXT message (just sent by `queue.notifyIdle()`) is starting to produce streaming content. The streaming content from the queued message IS shown during the busy phase, but the SECOND `reconcileOnIdle` (when the queued message finishes) clears it, then history takes over — causing a visible blank frame and making the streaming phase invisible.

**Bug 2 (secondary, rare race):** If two concurrent `prompt_async` calls reach opencode while the runner fiber has not yet executed (Effect scheduler hasn't yielded between the fork and `ensureRunning`), one wins and the other's work is silently discarded. `createUserMessage` already ran for both (user messages are in DB), but only one gets an LLM response. Orphan user messages result.

**US-6.5 gap (pre-existing, correctness impact):** `workspaceConfig.workspaceID` is never populated (`proxy.go:49-53`). The `onSessionIdle` activity/sessionIndex recording branch (`proxy.go:1060`) is permanently dead — `cfg.workspaceID != ""` is always false. Activity tracking and session title persistence on idle have been silently broken since Epic 06.

### 5. Design decisions

After evaluating three approaches (targeted frontend fix, server-side queue, proxy-layer idempotency):

- **Rejected server-side queue:** Adds infra complexity (Redis or per-replica in-memory with known correctness gaps on restart/routing). Not justified by bug severity for a single-tenant workspace model.
- **Accepted targeted fixes at the correct abstraction level:**
  - Bug 1: Fix `reconcileOnIdle` to guard state-clear on `msgs.length > 0`
  - Bug 2: Add 409 guard in `SendPromptAsync` (converts silent drop to observable error); handle 409 as retryable-pending in frontend
  - US-6.5: Remove dead `wsConfig` lookup in `onSessionIdle`, restore correct activity tracking

### 6. Epic 41 design document

Wrote `design/stories/epic-41-message-queue-reliability/README.md` with:
- 23 validated assumptions (all cross-referenced to source)
- Full root cause analysis with confirmed sequence diagrams
- Architecture decision with alternatives considered
- 4 user stories (US-41.1 through US-41.4)
- 15 tests across both frontend and backend
- 4 failure modes with mitigations
- Implementation order recommendation

Updated `design/stories/README.md` to add Epic 41 to the V2 scope table.

---

## Key Decisions

1. **Client-side queue is correct for interactive sessions.** The TUI doesn't need a queue because it locks the input while busy. A browser can receive input at any time. The queue belongs in the client. A server-side queue adds failure modes (replica restart, multi-replica routing) that outweigh the benefit.

2. **The root cause of Bug 1 is not the race.** The 23-assumption validation confirmed that the runner IS `Idle` before the SSE event is emitted. Bug 1 is a sequencing issue in `reconcileOnIdle` — it clears state too eagerly. The fix is a one-line guard, not an architectural overhaul.

3. **409 for in-flight session converts silent drop to observable error.** This is the correct HTTP semantics (409 Conflict, not 429 Too Many Requests). It enables the frontend to retry correctly without user intervention.

4. **US-6.5 is a correctness gap, not cosmetic.** `activityTracker.Record` feeds into workspace suspension logic (prevents premature suspension of active workspaces). `sessionIndex.RecordMessage` feeds into the sidebar's last-activity display. Both have been silently broken since Epic 06.

5. **No unvalidated assumptions in the design.** Every assumption is backed by a code citation. This was a non-negotiable given the history of "should work" assumptions causing production bugs in this codebase (per README-LLM.md Rule 7).

---

## Blockers

None.

---

## Tests Run

No code changes made in this session — investigation and design only.

Ran existing tests to confirm baseline:
```
cd frontend && npx vitest run src/hooks/useMessageQueue.test.ts
# 29 tests passing

cd frontend && npx vitest run src/pages/ChatPage.queue.test.tsx
# 10 tests passing
```

---

## Next Steps

Implement Epic 41 in order:
1. **US-41.4** — `onSessionIdle` dead code fix (2 lines, highest correctness impact, no new tests required beyond regressions)
2. **US-41.2** — 409 guard in `SendPromptAsync` with tests
3. **US-41.1** — `reconcileOnIdle` guard in `ChatPage.tsx` with ChatPage queue tests
4. **US-41.3** — 409 handling in `drainOne` with `mark_pending` reducer action and tests
5. Open PR, push to `fix/message-queue-deep-dive`

Per README-LLM.md §0 (TDD): write tests first, run to confirm failure, then write implementation.

---

## Files Modified

| File | Change |
|------|--------|
| `design/stories/epic-41-message-queue-reliability/README.md` | Created — full Epic 41 design document |
| `design/stories/README.md` | Added Epic 41 to V2 scope table |
