# Worklog: ChatPage Duplicate Messages Fix (post-idle reconcile)

**Date:** 2026-05-29
**Session:** Follow-up to 0078 — bug surfaced during live cluster validation
**Status:** Complete

---

## Objective

After deploying the refresh-aborts-LLM fix (0078) and testing on
safespace.thekao.cloud, the user reported a separate issue: every completed
user/assistant turn rendered TWICE in the chat window. Reproduce, root-cause,
fix with TDD, deploy.

---

## Assumptions Stated and Validated (Rule 7)

| # | Assumption | Validation method | Result |
|---|------------|-------------------|--------|
| A1 | The opencode session contains only one copy of each message (no server-side duplication) | `kubectl exec ... curl localhost:4096/session/<sid>/message` | ✅ TRUE — opencode returned 9 distinct messages, none duplicated |
| A2 | The frontend duplicates by merging `localMessages` and `history` without dedup | Read `ChatPage.tsx:384` | ✅ TRUE — `const allMessages = [...(history ?? []), ...localMessages];` with no dedup logic |
| A3 | `reconcileOnIdle` refetches history but does not clear `localMessages` | Read `ChatPage.tsx:120-134` | ✅ TRUE — sets `setSseStreamParts([])` but never touches `localMessages` |
| A4 | `localMessages` is only cleared on `sessionId` change | grep for `setLocalMessages\(\[\]` | ✅ TRUE — only cleared at line 31 in the `useEffect` keyed on `[sessionId]` |

---

## Root Cause

`ChatPage.tsx` maintains two stores of messages:

1. **`history`** — react-query cache for `["messages", workspaceId, sessionId]`, fetched on mount and refetched by `reconcileOnIdle`
2. **`localMessages`** — local React state populated optimistically on send (line 411 user msg, line 429 assistant msg via `send()` callback)

The render uses both: `allMessages = [...history, ...localMessages]`.

During an in-flight send, `localMessages` provides instant feedback while
`history` is still empty. When `session.status idle` SSE arrives,
`reconcileOnIdle` refetches `history` — but the new history contains the
same messages now persisted in opencode. `localMessages` is never cleared,
so every completed turn renders twice (history + localMessages).

This compounded across multiple sends: a session with N completed turns
produced 2N rendered messages.

---

## Fix (TDD)

**Test written first** (`frontend/src/pages/ChatPage.sse.test.tsx`):

```ts
it("REGRESSION: idle event triggers reconcile that does NOT cause duplicate localMessage rendering", async () => {
  // Mock getHistory to return empty initially, then [user, assistant] after reconcile
  // Send a message → localMessages gets user+assistant
  // Drive session.status idle SSE event → reconcileOnIdle runs, refetches history
  // Assert ChatView's `messages` prop has exactly 2 entries, not 4
});
```

Verified the test FAILED against current code: rendered messages had 4
entries (`msg-user-real`, `msg-asst-real`, `local-<timestamp>` user,
`local-<timestamp>` assistant).

**Fix applied** (`frontend/src/pages/ChatPage.tsx`):

```ts
// In reconcileOnIdle, after history refetch:
setSseStreamParts([]);
setLocalMessages([]);  // ← NEW: history is now authoritative
```

The catch path is unchanged — if the history fetch fails, `localMessages`
is preserved so the user doesn't lose context.

Test passes. The 84 ChatPage-related tests + 500 total frontend tests all green.

---

## Why this approach

| Option | Trade-off | Chosen? |
|--------|-----------|---------|
| Clear `localMessages` in `reconcileOnIdle` | Simple, matches Epic 15 intent (history is authoritative once idle) | ✅ Yes |
| Dedupe by ID matching | Fragile — `localMessages` IDs are `local-<ts>`, history has opencode IDs; no reliable cross-reference | No |
| Dedupe by content fingerprint in render | False positives — two identical messages would dedupe incorrectly; expensive on every render | No |

Per user direction, Option 1 (Recommended) was selected.

---

## Validation

| Command | Result |
|---------|--------|
| `npx vitest run src/pages/ChatPage.sse.test.tsx -t "REGRESSION"` | passing in 262ms |
| `npx vitest run src/pages/ChatPage.*.test.tsx` | 84/84 passing |
| `npx vitest run` (full frontend suite) | 500/500 passing (was 499, +1 new test) |
| `npm run build` | clean |
| `npm run lint` (touched files only) | clean |

---

## Files Modified

| File | Change |
|------|--------|
| `frontend/src/pages/ChatPage.tsx` | Added `setLocalMessages([])` to `reconcileOnIdle` happy path (lines 124-129); kept catch path unchanged so history fetch failures don't lose user context |
| `frontend/src/pages/ChatPage.sse.test.tsx` | Extended ChatView mock with `data-messages` attr; added regression test that drives `session.status idle` SSE event and asserts no duplicate rendering |

Untouched:
- `frontend/src/api/events.ts` — refresh-abort fix from 0078 still in place
- `frontend/src/hooks/useChatStream.ts` — race fixes from 0078 still in place
- All other tests

---

## Next Steps

1. CI: monitor build of new commit
2. Deploy: `helm upgrade ... --set frontend.image.tag=sha-<new>`
3. Live retest: send 2-3 prompts in a session, refresh after one completes, confirm no duplicate rendering
4. Confirm with user that the duplicate UX issue is gone

---

## Related

- Worklog 0078 (refresh-abort fix) — surfaced this issue during live validation
- Epic 15 (streaming reconnect) — design intent: history is authoritative once idle. This bug violated that intent by leaving stale `localMessages` around.
