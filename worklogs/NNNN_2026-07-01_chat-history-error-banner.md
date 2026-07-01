# Worklog: chat-history diagnostic banner (frontend)

**Date:** 2026-07-01
**Session:** #490 — chat page silently rendered empty state on message-history 5xx (no signal to user that backend was broken). Companion to server-side observability in #488/#489. This is Workstream B2.

**Status:** Complete (post-review-iteration)

---

## Objective

Turn "chat window is blank" from an ambiguous signal (could be "no messages yet" or "backend broken") into an unambiguous diagnostic surface that:
- Announces failure to screen readers (`role="alert"`).
- Gives the user an actionable Retry.
- Exposes opencode's `err_XXXXXXXX` ref to operators (in an expandable Details block) so they can jump directly from the browser to the workspace pod's opencode log.

Companion to #488/#489 (server-side metric + log). Together the incident-recovery path becomes: user reports → operator sees banner → copies ref → greps opencode logs → root cause.

---

## Work Completed

### Audit — no duplication

- `ApiClientError` (`api/client.ts:4`) — already surfaces `status` and `body`. No new error class needed.
- `useToast` provider — established but wrong fit here. A user staring at a blank chat needs the error IN the chat area, not dismissed in a corner. Would also miss the ref/status details that make this useful for operators.
- Sibling banners in ChatPage.tsx (`chatError` at 978, `streamTimedOut` at 971, `SessionRetryBanner` at 968) — reuse the exact visual language: `border-destructive/50`, `bg-destructive/10`, `text-destructive`.
- `EnrichChatErrorBody` (`api/internal/handlers/proxy_chat_enrichment.go`) — server-side allowlist that promotes `ref`, `message` to top level on POST /prompt. Confirms the two shapes the frontend must handle.

Decision: three new files, no new abstractions or providers.

### Validated assumptions

1. **Two error-body shapes exist.** Validated by reading `proxy_handlers.go:154-157` (GET history passes opencode's raw envelope through verbatim: `{ name: "UnknownError", data: { message, ref } }`) and `proxy_chat_enrichment.go` (POST prompt runs the allowlist, promoting `message` and `ref` to top level). Both are real in production.
2. **`ApiClientError.super(body.error)` produces an empty string when `body.error` is absent.** Verified via `new Error(undefined).message === ""`. The reviewer's initial claim of `"undefined"` (string) was slightly wrong — real behavior is empty. My defense-in-depth `!== "undefined"` check remains as guard against subclasses that stringify.
3. **`useInfiniteQuery` exposes `isError`, `error`, and `refetch`.** Validated in `useMessageHistory.ts` — the underlying react-query hook has all three; ChatPage just wasn't destructuring them.

### Root fix

Three files:

- `frontend/src/api/opencodeRef.ts` — `extractOpencodeRef` + `extractOpencodeMessage` (new in review round). Both pull from `body.field` OR `body.data.field`, prefer top-level, reject empty strings / non-strings / arrays. Never throw.
- `frontend/src/components/chat/ChatHistoryErrorBanner.tsx` — `role="alert"` inline banner. Visual language reused from siblings. Details block with HTTP status, message (via extractOpencodeMessage → body.error → err.message → placeholder), and ref (when present). Retry action.
- `frontend/src/pages/ChatPage.tsx` — destructures `isError`, `error`, `refetch` from `useMessageHistory`; renders banner immediately after the sibling chatError banner.

### Tests (TDD'd, adversarially validated at each layer)

- `opencodeRef.test.ts` — 6 unit tests for the helper (both shapes, both fields, prefers top-level, empty-string handling, non-object handling, array rejection).
- `ChatHistoryErrorBanner.test.tsx` — 7 tests. Now uses **production-real** fixture shapes (raw envelope for #486 GET-history case, flat allowlisted for POST-prompt case, API-own error shape for 503). Includes an explicit negative assertion that the literal empty string / "undefined" never renders. New regression-guard test for the "no top-level error, no opencode message" pathological case falls through to the "Unknown error" placeholder.
- `ChatPage.historyError.test.tsx` — 4 integration tests. Fixture updated to real production shape (no synthetic top-level `error` field). Assertion added that opencode's `data.message` renders in the banner details (was the exact test-fidelity gap the first review found).

Adversarial validation: commented out `extractOpencodeMessage` in the banner's fallback chain. 3 tests failed as expected — proving the extraction step is what makes the fix work, not a happy accident. Restored — green.

### Review-feedback iteration

First review returned REQUEST CHANGES with 3 findings, all correct:

1. **Message extraction bug for GET-history shape.** My banner used `error.body?.error` then `error.message`, but the raw opencode envelope has neither — the message is at `body.data.message`. Result would have been an empty line in the banner Details.

2. **Test fidelity bug.** My fixtures injected synthetic top-level `error` fields that don't exist in production. Assertions passed artificially. Fixed by rewriting all fixtures to the real shapes.

3. **Missing worklog.** Added this file.

The fix landed a new `extractOpencodeMessage` helper (symmetric with `extractOpencodeRef`), refactored the banner's message-fallback chain to try it first, and rewrote every fixture to the production shape.

---

## Key Decisions

- **Extract both helpers into `opencodeRef.ts`** (renamed conceptually — the file's role is "opencode error-envelope field extraction"). Sharing one private `extractOpencodeField(body, field)` implementation guarantees the two extractors stay in lockstep on edge cases (empty strings, arrays, nested vs top-level precedence).
- **Fall-through chain: opencode message → API error field → err.message → placeholder.** Order matters — for #486 shapes the top item wins; for API-native 503s the second wins; for network errors the third wins; for pathological cases the placeholder wins. Every step has a test.
- **Empty-string check via truthy `&&`, not explicit length check.** JavaScript's `""` is falsy — `error.message && ...` correctly short-circuits without extra code. The `!== "undefined"` clause is defense-in-depth documented in the comment.
- **`role="alert"` is required for accessibility.** Confirmed by the reviewer's a11y-role test that I initially had; kept and expanded in the fixture-fidelity refactor.
- **Do NOT sanitize opencode messages before rendering.** The message is displayed in a `<details>` block that users have to click to see — it's an operator-facing surface, not a user-visible chatty message. Sanitization would risk stripping useful details.
- **Retry button wires to react-query's `refetch()`, not a page reload.** The transient case (pod restart, rate-limit, transient DNS) resolves within seconds; page reload would lose composer state and scroll position.

---

## Blockers

None.

---

## Tests Run

- `npx vitest run src/api/opencodeRef.test.ts` — 6 helper tests, green.
- `npx vitest run src/components/chat/ChatHistoryErrorBanner.test.tsx` — 7 banner-in-isolation tests, green.
- `npx vitest run src/pages/ChatPage.historyError.test.tsx` — 4 integration tests, green.
- `npx vitest run` — full suite: 1277/1277 pass (was 1257 before this PR series, net +20 from banner + helper + integration).
- `npx tsc --noEmit` — clean.
- Adversarial: replaced `extractOpencodeMessage` with `undefined` in banner fallback chain — 3 tests failed. Restored — green.

---

## Next Steps

- Deploy to home-kubernetes once #491 merges. Runtime-base image tag bump; workspace pods pick up the new frontend on their next refresh (or automatically on hard-reload for browser sessions).
- No follow-up features. This PR closes the LLMSafeSpaces#490 gap end-to-end.

---

## Files Modified

- `frontend/src/api/opencodeRef.ts` — new file. `extractOpencodeRef` + `extractOpencodeMessage` + shared private `extractOpencodeField`. First revision had only `extractOpencodeRef`; message extraction added in review round.
- `frontend/src/api/opencodeRef.test.ts` — new file. 6 helper unit tests.
- `frontend/src/components/chat/ChatHistoryErrorBanner.tsx` — new file. Banner component.
- `frontend/src/components/chat/ChatHistoryErrorBanner.test.tsx` — new file. 7 banner unit tests. Fixtures rewritten in review round to real production shapes.
- `frontend/src/pages/ChatPage.tsx` — destructures isError/error/refetch from useMessageHistory; renders the banner.
- `frontend/src/pages/ChatPage.historyError.test.tsx` — new file. 4 integration tests. Fixture updated in review round to real production shape.
- `worklogs/NNNN_2026-07-01_chat-history-error-banner.md` (this file).
