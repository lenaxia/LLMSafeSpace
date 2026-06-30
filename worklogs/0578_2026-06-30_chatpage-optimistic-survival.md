# Worklog: ChatPage optimistic user-bubble survival across reconcile (#447)

**Date:** 2026-06-30
**Session:** Fix open issue #447 — the just-sent user message bubble vanished from chat when `reconcileOnIdle` fired during the eventual-consistency window before opencode persisted the new message.
**Status:** Complete

---

## Objective

Resolve #447 (bug(frontend): just-sent user message bubble vanishes after send when reconcileOnIdle wipes localMessages). Two production triggers: an SSE reconnect mid-send, or a premature `session.status=idle`, both of which call `reconcileOnIdle`, which cleared `localMessages` unconditionally whenever refetched history had any messages. When that history was stale (opencode hadn't persisted the just-sent message yet), the optimistic user bubble was wiped with no compensation and never re-rendered.

---

## Work Completed

- **Root-cause fix** in `frontend/src/pages/ChatPage.tsx`:
  - Added a module-level `messageIdentityKey(m: Message)` helper that returns `${role}|${textContent}`, where textContent is the concatenation of string `text` fields across the message's parts.
  - In `reconcileOnIdle`, replaced the unconditional `setLocalMessages([])` with a content-aware filter: build a `Set` of identity keys from the refetched history and keep only optimistic local messages whose key is NOT present — i.e. the server hasn't demonstrably caught up yet. `setSseStreamParts([])` is left unchanged (clearing streaming state when history is authoritative is the intended US-15.5 behavior; this bug is about the user bubble, not the assistant stream).
- **Regression tests** (`frontend/src/pages/ChatPage.optimistic-survival.test.tsx`, 376 lines, 3 tests) — taken from the issue's investigation branch `investigate/missing-sent-user-message` (commit `b74d004b`), which defines the contract: two bug repros (SSE reconnect mid-send; premature idle) asserting the just-sent bubble survives against stale history, plus a control asserting no duplicate when history has caught up. Removed one unused import (`workspacesApi`) flagged by `tsc --noEmit`.

A prior `/fix` bot run (issue comment) had proposed this exact Fix Direction #1 but its push failed (auth error), so no PR existed; this recreates and lands it.

---

## Key Decisions

1. **`(role, text)` key, no time bucket.** The issue suggested `(role, text, secondsBucket(createdAt))`, but `transformHistory` (`api/messages.ts:50-52`) sets `createdAt: undefined` when opencode omits `info.time.created`. Including a time bucket would make the optimistic message (which always has `createdAt`) never match a server message lacking it — breaking the control/duplicate case. `(role, text)` is the simplest key that satisfies all acceptance criteria. Validated against the test mocks.
2. **Known limitation documented.** Two consecutive identical messages collide on this key; when one lands in history both clear. Extremely rare, minimal visual impact (identical messages); explicitly accepted in #447. Noted in the helper's doc comment.
3. **`messageIdentityKey` excludes `id`.** Optimistic ids are `local-N`; server ids are opencode UUIDs — they never match, so id is useless as a match key and is deliberately excluded.
4. **Single chokepoint fix.** Both triggers (handleSSEReconnect → reconcileOnIdle, and `session.status=idle` → reconcileOnIdle) route through the one function, so the single filter change fixes both. Verified by the two BUG REPRO tests.

---

## Blockers

None.

---

## Tests Run

- `npx vitest run src/pages/ChatPage.optimistic-survival.test.tsx` — 3/3 PASS (was 2 fail / 1 pass before the fix).
- `npx vitest run src/pages/ChatPage` — 12 files, 172 tests PASS (no regressions in sse/reconnect/activate suites; covers the acceptance-criteria regression tests at `ChatPage.sse.test.tsx:226`, `:282` and `ChatPage.reconnect.test.tsx:984`).
- `npm run typecheck` (`tsc --noEmit`) — PASS (after removing the unused import).
- `npx eslint src/pages/ChatPage.tsx src/pages/ChatPage.optimistic-survival.test.tsx` — PASS.

---

## Next Steps

- After merge: no live-cluster step required (frontend-only). Monitor for any follow-up if the consecutive-identical-message edge case is ever reported.
- Continue issue burn-down. Remaining open items are epics/large (#127, #43, #42, #41, #40, #38, #429) or blocked (#366 on US-50.2, #454 on a maintainer decision) or complex (#388, #281, #85). None are currently "straightforward" without further input.

---

## Files Modified

- `frontend/src/pages/ChatPage.tsx` — added `messageIdentityKey`; changed `reconcileOnIdle` wipe to content-aware filter.
- `frontend/src/pages/ChatPage.optimistic-survival.test.tsx` — new regression tests (from investigation branch, unused import removed).
- `worklogs/0578_2026-06-30_chatpage-optimistic-survival.md` — this worklog.
