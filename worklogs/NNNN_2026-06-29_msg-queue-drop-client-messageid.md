# Worklog: Queued messages silently dropped — back out client-supplied messageID

**Date:** 2026-06-29
**Session:** Investigate "queued messages sent but agent did not continue" in session ses_0eb5c012effeDnxHIesQFFXUxz (https://chat.safespaces.dev/chat/6117de76-…/ses_0eb5c012effeDnxHIesQFFXUxz)
**Status:** Complete

---

## Objective

After deploying worklog 0569's fix (`fix(msgqueue): match opencode message-ID format`), the user reported the inverse symptom: queued follow-up messages were delivered to opencode but the agent never produced a response. Investigate, find the root cause, fix.

---

## Investigation

Inspected the workspace pod's `opencode.db` and opencode debug log for the affected session.

### Timeline

- 18:29:06 — initial `/prompt` "test, give me a multi-call response …" → opencode generates user `msg_f14a4669e` and assistant `msg_f14a466c7`.
- 18:29:09 — agent loop steps 1→2 produce assistant `msg_f14a4724d` with `finish=stop`. Session goes idle.
- 18:29:12–15 — user enqueues 3 follow-ups while busy: "test 123", "hi", "how are you?" → queue IDs `msg_f14a38518`, `msg_f14a38b83`, `msg_f14a3943b`.
- 18:29:15 — drain runs three `POST /prompt_async` calls. Each user message lands in `opencode.db` but **no assistant message follows**.

Opencode log shows, for each drained prompt:

```
step=0 loop
exiting loop
```

The loop exited at step 0 without invoking the LLM.

### Root cause

Opencode's `session.prompt` loop-exit predicate (`packages/opencode/src/session/prompt.ts`):

```ts
lastAssistant?.finish && !["tool-calls"].includes(lastAssistant.finish)
  && !hasToolCalls && lastUser.id < lastAssistant.id
```

At step 0 of each drained prompt:

- `lastAssistant` = `msg_f14a4724d` (previous turn, `finish=stop`)
- `lastUser` = the just-inserted queued message, e.g. `msg_f14a38518`
- `lastUser.id < lastAssistant.id`? **YES**, because `f14a3` lex-sorts below `f14a4`.

The predicate fires immediately. Opencode treats the queued user message as **already-responded-to history** and exits without prompting the model.

Decoding the embedded timestamps confirms: queued IDs encode ~60 seconds earlier than the previous-turn assistant ID. That's worklog 0569's `idClockSafetyMs = 60_000` backdate.

### The flaw in worklog 0569

The loop-exit predicate requires the user ID to land *between* two opencode-generated IDs:

- **Above** the previous-turn assistant ID (so the loop sees a new turn, not historical)
- **Below** the next-turn assistant ID opencode is about to create (so the loop continues past step 0)

Worklog 0569 optimized aggressively for the second bound (60s backdate beats opencode's clock by miles) but broke the first. The new IDs sort below the previous-turn assistant when the previous turn finished moments ago.

Both bugs trace to the same architectural mistake: **shipping a client-supplied messageID to opencode's `prompt_async`** at all. Opencode's loop-exit invariant depends on monotonicity of IDs *opencode itself generates*. Any externally-supplied ID risks landing on the wrong side of either bound.

### Validation against source

- Confirmed loop-exit predicate at `packages/opencode/src/session/prompt.ts` (anomalyco/opencode commit `2538c0d08`). The predicate is `lastUser.id < lastAssistant.id` and the comparison is raw string `<`.
- Confirmed user-message ID assignment at the same file: `id: input.messageID ?? MessageID.ascending()`. Caller-supplied IDs are used as-is; otherwise opencode generates its own monotonic ID.
- Verified API↔workspace pod clock skew on this cluster: 3ms (sample: 1782758050836 vs 1782758050833). The 60s safety buffer was orders of magnitude larger than needed.
- Verified frontend queue tracking does NOT correlate queue IDs with history IDs. `useMessageQueue.ts` tracks queue items by the ID returned from `POST /queue`; `ChatPage.reconcileOnIdle` refetches history and queue independently. So we can stop shipping the queue ID to opencode without changing any frontend behavior.

---

## Work Completed

### `api/internal/handlers/proxy_events.go`

- Changed `promptRequestBody.MessageID` JSON tag from `"messageID"` to `"messageID,omitempty"`.
- Cleared `MessageID` in `sendQueuedToOpencode`'s request body — opencode now generates the user-message ID itself via `MessageID.ascending()`.
- Documented why on the struct definition (cite both failure modes: role-flip if user ID above the new assistant; silent drop if user ID below the previous assistant).

### `api/internal/services/msgqueue/service.go`

- Removed `idClockSafetyMs` constant and the 60s timestamp backdate.
- Rewrote the file-level comment to reflect the new design: the opencode-format ID generator stays only because the queue ID surfaces in the frontend queue UI alongside opencode-generated history IDs, and matching the shape keeps temporal ordering coherent there. The queue ID is no longer shipped to opencode.

### `api/internal/handlers/proxy_queue_test.go`

- Rewrote the integration assertion in `TestEnqueueMessage_DrainsWhenIdle`: instead of asserting that the queue ID reaches opencode with a specific format, now assert that the `prompt_async` body contains **no** `messageID` key (raw body string check + parsed struct check).
- Captures the raw request body so we can verify field absence, not just empty string.

### `api/internal/services/msgqueue/service_test.go`

- Removed `TestEnqueue_IDSortsBeforeOpencodeAssistantID` and `TestEnqueue_IDSurvivesClockSkew` — these tests pinned the now-obsolete invariant that queue IDs must lex-sort below opencode IDs. The invariant doesn't apply because we no longer ship the queue ID to opencode.
- Removed the test-only helpers `simulateOpencodeAscendingMessageID` and `opencodeAscendingIDAt` (no remaining callers).
- Added `TestEnqueue_IDsAreTemporallyOrdered` to verify the property the frontend actually relies on: successive Enqueue calls produce IDs that lex-sort in creation order (same-ms ties allowed; cross-ms must be ordered).
- Kept `TestEnqueue_IDFormatMatchesOpencode` (pins the on-the-wire shape so frontend code that may parse the prefix keeps working) and `TestEnqueue_LegacyUUIDFormatWouldRegress` (archaeological — pins the original failure mode for posterity).

---

## Key Decisions

- **Drop the client-supplied messageID entirely**, picked over (a) reading opencode's session message list to compute a strictly-greater ID before enqueue, or (b) just removing the 60s backdate and keeping counter=0 ordering. Picked drop because:
  - It's the only fix that eliminates the lex-ordering trap permanently — both reported failure modes (role-flip from worklog 0569; silent-drop from this worklog) trace to the same source.
  - Opencode's monotonic `MessageID.ascending()` is the only ID source guaranteed to satisfy opencode's own loop-exit invariant; deferring to it is correct.
  - No frontend behavior depends on the queue ID matching the opencode user-message ID.
  - Lowest moving-parts: no extra API call, no clock-skew math, no future-skew math, no double-bounded math.
- **Kept the opencode-format generator for queue IDs.** The queue ID surfaces in `useMessageQueue.ts` state and may be rendered/sorted alongside opencode-generated history IDs in the frontend. Matching the shape keeps temporal ordering coherent there (this was the lesson of worklog 0555). The generator is otherwise unused beyond Redis storage.
- **No backwards compatibility for in-flight `msg_q_*` or 60s-backdated IDs.** TTL is 24h; production queue was empty when checked. Per project convention, we don't carry compat shims for transient queue state.
- **Did not file upstream.** Opencode's contract is "supply a messageID at your own risk; it must satisfy our loop-exit invariant." We were violating that contract. Worth a doc note in the opencode SDK if `messageID` is ever surfaced to external integrators, but out of scope.

---

## Validation

- `go test -timeout 60s -race ./api/internal/services/msgqueue/ ./api/internal/handlers/` — all relevant tests pass.
- `go build ./...` — clean.
- `go vet ./api/internal/services/msgqueue/ ./api/internal/handlers/` — clean.
- Traced opencode source at the pinned version (anomalyco/opencode commit `2538c0d08`) to confirm:
  - `input.messageID ?? MessageID.ascending()` honors caller IDs and falls back to opencode's monotonic generator.
  - The loop-exit predicate exact form (`lastUser.id < lastAssistant.id` under string comparison).

---

## Files Touched

- `api/internal/handlers/proxy_events.go` — drop messageID from prompt_async body; add omitempty and explanatory comment.
- `api/internal/handlers/proxy_queue_test.go` — invert the integration assertion to verify messageID is absent on the wire.
- `api/internal/services/msgqueue/service.go` — remove 60s backdate; rewrite file-level comment.
- `api/internal/services/msgqueue/service_test.go` — remove obsolete lex-ordering tests; add temporal-ordering test; trim unused imports.

---

## Follow-ups (not in scope)

- The opencode-format generator in `msgqueue/service.go` is now justified only by the frontend queue-display sorting concern. If we ever stop displaying queue IDs alongside opencode IDs (or move queue rendering into a separate list), the generator could be simplified to a plain ULID or even a UUID — at the cost of breaking temporal sort in the queue UI. Not changing now; the cost of the current generator is low.
- An end-to-end test that exercises the full drain path through a fake opencode that implements the loop-exit predicate would catch future regressions in this class. The current `TestEnqueueMessage_DrainsWhenIdle` only verifies the wire shape, not the consequence. Out of scope here; would be its own design.
