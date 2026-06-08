# 0184 — Auto-abort stuck question/permission sessions on reconnect

**Date:** 2026-06-08
**Status:** Complete

---

## Problem

When opencode restarts while a `question` or `permission` tool is pending (observed: relay injector kills opencode to apply config — session `ses_15a84243fff` on workspace `1aa87aec`), the question is lost from opencode's in-memory queue. On page refresh:

- `/question` (GET) returns `[]` — `emitPendingInputRequests` finds nothing to replay
- Session message history still shows the tool as `type=tool_use, toolState=running, text="question: GitHub auth required"`
- `session.status=idle` was never fired — the session is permanently busy from the broker's perspective
- The user sees the old question UI but can never answer it — opencode is no longer waiting

Root cause confirmed via opencode logs: opencode restarted at `04:25:52` (relay injector config reload). The question was in-flight during the previous opencode process. SQLite persisted the message/part state but the live question queue is in-memory only — new process, empty queue.

---

## Fix

`ChatPage.tsx`: added a `useEffect` that fires in reconnect mode (session busy on page load) after history has loaded. Checks:

1. `isReconnectMode.current === true` — we're loading into an already-busy session
2. History has loaded and the last assistant message has a `tool_use` part with `toolState === "running"` and `text` starting with `"question"` or `"permission"`
3. `pendingQuestions.length === 0 && pendingPermissions.length === 0` — the question was NOT re-emitted by `emitPendingInputRequests` via SSE (i.e. opencode has lost it)

If all three conditions are met: auto-call `workspacesApi.abortSession`, set `sessionWasInterrupted = true`, call `reconcileOnIdle()` to refresh history.

A yellow banner is displayed: "⚠ Session was interrupted while waiting for your input. You can continue in this session or start a new one." with a Dismiss button.

The abort is skipped if the question IS still live (SSE replayed it) — ensuring we don't abort answerable questions.

State (`hasAutoAbortedRef`, `sessionWasInterrupted`) is reset on session change.

---

## Tests

4 new tests in `ChatPage.reconnect.test.tsx`:

- `auto-aborts and shows interrupted banner when reconnecting to session stuck on question tool`
- `auto-aborts when last tool is permission (not just question)`
- `does NOT abort when question is re-emitted via SSE (still answerable)`
- `abort failure still shows interrupted banner and reconciles history`

---

## Assumptions Validated

| Assumption | Validation |
|---|---|
| `/question` returns `[]` when opencode restarts with pending question | Confirmed: `kubectl exec ... curl http://localhost:4096/question` returned `[]` after relay injector restart |
| `toolState === "running"` is the correct field to check | Confirmed: `transformHistory` in `messages.ts:37` maps `state.status` → `toolState` |
| `tool` field (tool name) maps to `text` field in `MessagePart` | Confirmed: `messages.ts:35-40` maps `part.tool` → `text` as `"toolName"` or `"toolName: title"` |
| `emitPendingInputRequests` fires synchronously enough that `pendingQuestions` is populated before auto-abort effect runs | Validated: SSE events from `emitPendingInputRequests` go through `handleSSEEvent` → `setPendingQuestions` before the `useEffect` re-runs; the guard prevents false aborts |
