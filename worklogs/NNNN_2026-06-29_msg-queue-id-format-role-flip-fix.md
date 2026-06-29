# Worklog: Queued message ID format breaks opencode's agent loop (role-flip)

**Date:** 2026-06-29
**Session:** Investigate "role keeps flipping" report in chat session ses_0ed760478ffeQVPJGD5iEvRRmu — LLM appears to talk to itself after a queued message drains
**Status:** Complete

---

## Objective

The user sent two prompts in chat https://chat.safespaces.dev/chat/9eca2c9a-…/ses_0ed760478ffeQVPJGD5iEvRRmu. The second was queued because the session was busy. After it drained, the assistant produced 11 consecutive turns with no user input between them, increasingly confused, "talking to itself with role flips." Same behavior never observed in the opencode TUI. Find the cause and fix.

---

## Investigation

Inspected the workspace pod's `opencode.db` (path `/workspace/.local/opencode/opencode.db`). Key DB findings for the affected session:

- 3 user messages and 13 assistant messages total.
- The first two user messages have native opencode IDs (`msg_f128...`); the third has the API-server queue-generated ID `msg_q_884a9b62-8a2f-47b2-80cc-3ae8eebdcecf`.
- After the queued user message, **11 consecutive assistant messages** were generated, all carrying `parentID: msg_q_884a9b62-...`. No user messages between them. The DB role labels are correct — no actual role flipping.

Opencode's log for the session shows `step=0` through `step=10` of the `session.prompt` loop running on that single user prompt before the user clicked abort. The agent loop never exited.

### Root cause

Opencode's `session.prompt` loop exit condition, decompiled from `/usr/local/bin/opencode` v1.15.12 (the pinned version):

```js
if (Q?.finish && !["tool-calls"].includes(Q.finish) && !yA && bA.id < Q.id) {
  yield* kA.info("exiting loop"); break
}
```

Where `bA` is the latest user message and `Q` the latest assistant message. The exit predicate `bA.id < Q.id` is **lexicographic string comparison** on the IDs. Opencode assumes its own monotonically-increasing `Identifier.ascending` IDs satisfy this naturally.

Our queue service generated user message IDs as `"msg_q_" + uuid.NewString()` (`api/internal/services/msgqueue/service.go:45`). The third user message's ID was `msg_q_884a9b62-...`. Compare character by character to the first assistant reply `msg_f128cb848001qefJU6L6hgzhao`: position 4 is `'q'` (0x71) vs `'f'` (0x66). `'q' > 'f'`, so `msg_q_884... > msg_f128cb848...` — the predicate is **false**, the loop never exits.

Each iteration `aA > 1` triggers opencode's per-iteration prompt machinery, which wraps the user text in:

```
<system-reminder>
The user sent the following message:
<original text>

Please address this message and continue with your tasks.
</system-reminder>
```

The model sees a "new" user message every iteration, with its own previous response appended to history. After a few turns GLM-5.2 (the configured model) starts producing increasingly broken replies — "Your statement is accurate," "Correct on the DeepSeek API point," "Role flip again," etc. None of those user statements ever existed.

### Why the TUI is unaffected

`packages/opencode/src/session/session.ts` calls `MessageID.ascending()` when no client-supplied ID is provided. The TUI never supplies `messageID`. Its IDs follow opencode's own format starting with `msg_f...` (currently) and naturally satisfy the loop invariant. The bug only fires when an external client supplies an out-of-spec messageID.

### Validation against source

Confirmed opencode's exact ID layout in source: `packages/opencode/src/id/id.ts:51-70`:

```
"msg_" + 12 hex chars + 14 base62 chars
```

The 12 hex chars encode the low 48 bits of `(timestamp_ms * 0x1000 + counter)` big-endian. Counter starts at 0 each new millisecond and is `++`'d before encoding, so the minimum used counter at any ms is 1.

### Pre-existing breadcrumbs

Worklog 0555 already identified this lex-ordering issue at the **frontend** layer ("Queued messages rendered after all native messages on page reload") and fixed it by switching from `id.localeCompare` to a `createdAt` sort. Worklog 0564 noted "opencode message IDs are stable per session... IDs are not lexically sortable by creation time." Neither realized that opencode's own internal agent loop depends on the same lex-ordering assumption, so caller-supplied IDs that violate it break correctness, not just sort order.

---

## Work Completed

### `api/internal/services/msgqueue/service.go`

Replaced the legacy `Enqueue` ID scheme with a faithful port of opencode's `Identifier.ascending("message")` algorithm:

- New `generateOpencodeMessageID()` produces `"msg_" + 12 hex chars + 14 base62 chars` matching opencode's exact byte layout.
- **Counter held at 0** (opencode's minimum used counter is 1), so on a same-ms tie our hex prefix is strictly less than opencode's.
- **Timestamp backdated by 60 seconds** (`idClockSafetyMs`) to absorb any clock skew between the API pod and the workspace pod. K8s same-cluster NTP drift is typically <100ms but the cost of overshooting is purely cosmetic (decoded ID time appears 60s earlier than real time; nothing depends on decoded ID time — actual creation time lives in `QueuedMessage.EnqueuedAt`).
- Random suffix drawn from `crypto/rand` over opencode's full 62-char alphabet.
- Removed the `github.com/google/uuid` import from this file (still used by 18 other files in the repo).

### `api/internal/services/msgqueue/service_test.go`

Added three tests covering the failure mode and the fix invariants:

- **`TestEnqueue_IDFormatMatchesOpencode`** — pins the exact ID layout (length 30; `msg_` prefix; 12 lowercase-hex; 14 base62).
- **`TestEnqueue_IDSortsBeforeOpencodeAssistantID`** — runs 200 iterations of (Enqueue, drain, simulate opencode's worst-case minimum-counter assistant ID at the same instant) and asserts the lex-ordering invariant holds. This is the direct regression test for the 2026-06-29 incident.
- **`TestEnqueue_IDSurvivesClockSkew`** — sub-tests with simulated opencode clocks running 0ms, 10ms, 100ms, 1s, 10s, and 50s behind ours, asserting the invariant holds in each.
- **`TestEnqueue_LegacyUUIDFormatWouldRegress`** — frozen failure case: shows the old `"msg_q_" + UUID` scheme lex-sorts above the real opencode IDs captured from the incident. If anyone reintroduces the old scheme this test catches it.

Authored `simulateOpencodeAscendingMessageID` / `opencodeAscendingIDAt` test helpers as a faithful Go port of opencode's `id.ts:create`. Limited to the test file; production code does not need an opencode-format generator beyond `generateOpencodeMessageID`.

---

## Key Decisions

- **Match opencode's format exactly, don't try to be clever with prefixes.** The user picked this option out of three alternatives. The two rejected options were: (1) keep `msg_q_` prefix but change the alphabet to start with a digit, which fixes the immediate symptom but breaks again if opencode ever changes its alphabet; (2) drop the client-supplied messageID entirely and let opencode generate one, which breaks the queue↔history correlation that the frontend's queue-pill rendering depends on (epic-41 V20).
- **Counter held at 0, timestamp backdated 60s.** The counter trick alone protects against same-ms ties from a perfectly-synchronized opencode clock. The timestamp backdate handles real-world clock skew. Combined, the invariant is double-defended.
- **No backwards compatibility for legacy `msg_q_*` messages.** Per direction during this session. Production Redis was inspected (`KEYS llmsafespaces:msgqueue:*` returned empty) so there are no stranded messages to migrate. The 24h queue TTL eats any in-flight legacy entries enqueued after this worklog but before deployment.
- **Did not file an upstream opencode bug.** The behavior is consistent with opencode's design contract — its IDs are documented as `Identifier.ascending` and the loop exit assumes that property. The defect was on our side for shipping IDs that violated the contract. Worth a future doc note in the opencode SDK type comments if `messageID` is exposed in any client-facing surface, but out of scope here.

---

## Validation

- `go test -timeout 60s -race -count=3 ./api/internal/services/msgqueue/` — all 22 tests pass, including the 4 new ones.
- `go test -timeout 60s -count=1 -run 'Queue|Drain' ./api/internal/handlers/` — all queue/drain handler tests still pass.
- `go build ./...` — clean.
- `go vet ./api/internal/services/msgqueue/ ./api/internal/handlers/` — clean.

---

## Files Touched

- `api/internal/services/msgqueue/service.go` — replaced ID generator; removed `uuid` import; added 60s clock-skew safety constant; documented the loop-exit invariant.
- `api/internal/services/msgqueue/service_test.go` — added 4 regression tests + opencode ID simulator helpers.

---

## Follow-ups (not in scope)

- The frontend distinguishes queued vs. native messages by data source (queue endpoint vs. message-history endpoint), not by ID prefix. Verified in worklog 0555. No frontend change needed.
- The opencode loop bug is upstream and we cannot fix it. If `messageID` is ever surfaced in the opencode SDK as a documented field, a contract note ("must lex-sort below opencode-generated message IDs") would help future integrators avoid the same trap. Out of scope here.
