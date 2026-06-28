# Worklog: Message history pagination — fix #440

**Date:** 2026-06-28
**Session:** Investigated and fixed #440 — long sessions in the chat UI show only the tail of the conversation with no way to reach earlier messages.
**Status:** Complete

---

## Objective

Reproduce, with tests, the production failure mode in session
`ses_0f01dd6f1ffe8awjS68zzWTjI5` where the user observed:

- The first user message of a long session was not visible at the top of the chat.
- There was no "Load earlier messages" button.
- Scrolling to the top of the rendered list revealed nothing further.

Then fix it.

---

## Assumptions (stated and validated)

| Assumption | Validation |
|---|---|
| opencode's `/session/{id}/message` returns the full message array, oldest-first, with no pagination | Verified by curling the running opencode pod in cluster (`workspace-510e910a-...`); 84 messages returned in one shot for the offending session, oldest at index 0 |
| The frontend's `useMessageHistory` was already wired for `X-Next-Cursor` paging | Confirmed in `frontend/src/api/messages.ts:80-87` and `frontend/src/hooks/useMessageHistory.ts:26` |
| The current `GetHistory` handler was a dumb pass-through | Confirmed at `api/internal/handlers/proxy_handlers.go:88-95` (pre-fix) — no `limit`/`before` handling, no header emission |
| opencode message IDs are stable per session and lexically-comparable when zero-padded | Verified by inspecting raw `info.id` shape (`msg_<base62>`); IDs are not lexically sortable by creation time, so cursoring must search the array, not sort lexically |
| `WorkspaceListItem.orgId?` is present on main | Confirmed at `frontend/src/api/types.ts` |

---

## Work Completed

### 1. Tests written first (TDD red phase)

All tests below were authored before any implementation and confirmed to fail
against the pre-fix code.

- **`api/internal/handlers/proxy_history_pagination_test.go`** — 8 server-side
  contract tests:
  - First page returns the newest N (default 50), oldest-first within the page,
    with `X-Next-Cursor` pointing at the oldest id of the returned page.
  - `?limit=` validation (default 50, max 200, reject 0/negative/non-numeric).
  - `?before=<cursor>` returns messages strictly older than the cursor.
  - Unknown cursor returns an empty page (defensive).
  - Server filters non-displayable messages (system role, empty-parts) before
    counting against the limit — prevents jumpy page sizes.
  - Upstream non-JSON/error responses surface as 5xx (not as opaque pass-through
    that would crash `transformHistory` on the frontend).
  - `?limit` / `?before` are stripped from the forwarded query so opencode
    doesn't see them.

- **`api/internal/handlers/proxy_history_e2e_test.go`** — 2 full-flow tests
  driving the entire gin router with a fake opencode backend, replicating
  the 84-message production session and walking pagination end-to-end.

- **`frontend/src/api/messages.pagination.test.ts`** — 4 wire-contract tests
  for `messagesApi.getHistoryPage`, asserting the `?limit`/`?before` request
  shape and the `X-Next-Cursor` response header parsing.

- **`frontend/src/hooks/useMessageHistory.pagination.test.tsx`** — 3 hook-level
  tests including a regression test that names the production bug explicitly:
  "when server never sets nextCursor, hasNextPage stays false even with many
  messages".

- **`frontend/src/components/chat/MessageList.test.tsx`** — 4 new tests in a
  "load-earlier discoverability (#440)" suite: button is inside the scroll
  container, is the first interactive element, has an accessible label, and
  its anchor is `sticky top-0` so it remains visible as the user scrolls.

### 2. Server-side pagination implementation

Rewrote `GetHistory` in `api/internal/handlers/proxy_handlers.go`:

- Reads `?limit` (default 50, capped at 200) and `?before` (opaque message id).
- Fetches the full upstream history via a new `fetchUpstreamHistory` helper
  that uses the existing `httpClient` directly (rather than the streaming
  `proxyToWorkspace` path, which doesn't allow body inspection).
- Filters non-displayable messages (role ∉ {user, assistant} or no displayable
  parts) server-side via `messageIsDisplayable`.
- Slices the displayable array: if `before` is set, returns up to `limit`
  messages strictly older than the cursor; otherwise returns the newest
  `limit`. Result is oldest-first within the page (matches the frontend's
  existing chronological-sort hook).
- Sets `X-Next-Cursor` to the OLDEST id in the returned page when more older
  messages exist. Header is absent when there's nothing further.
- Preserves the opencode message schema in the response body (raw
  `json.RawMessage` per element) so `transformHistory` on the frontend keeps
  working unchanged.

Added `stripPaginationQuery` to remove `limit`/`before` from the forwarded
query string before any (defensive) future passes to opencode. Mirrors the
existing `stripVerboseQuery` pattern.

The upstream body cap is 16 MiB to bound memory; the request is bounded by
the existing per-workspace connection semaphore.

### 3. MessageList UX cue

Wrapped the existing "Load earlier messages" button in a sticky anchor
(`sticky top-0 z-10`) inside the scroll container. On a fresh load of a long
session, the message list still auto-scrolls to the bottom (existing
behavior), but as soon as the user scrolls up, the sticky button slides into
view at the top of the viewport — making the "more history exists" affordance
discoverable. Adds an explicit `aria-label` for assistive tech.

### 4. Cascading test updates

Three pre-existing tests were updated because their fixtures encoded the OLD
contract (GET history is an opaque pass-through):

- `proxy_test.go` — `newTestEnv` default backend, `TestProxy_EndpointMapping`,
  and `TestProxy_E2E_FullFlow` now return a valid JSON array for GET
  `/session/{id}/message` (because that's what opencode actually returns and
  what the paginated handler expects to decode).
- `proxy_filter_test.go` — `TestProxy_StripPreservesNonJSONResponses` was
  inverted: it now asserts the correct (new) behavior — non-JSON-array
  upstream bodies surface as 502, never as opaque pass-through that would
  crash `transformHistory`. Old behavior was a footgun, not a feature.

---

## Key Decisions

- **Cursor semantics: opaque message id, not base64-encoded timestamp tuple.**
  opencode message ids are monotonic per session, and the cursor only needs
  to be reproducible — not parseable. Simpler.
- **Filter server-side, not client-side.** Otherwise a `limit=50` page that
  contained 5 system messages would render as a 45-message page, and users
  would see jumpy page sizes. Doing it server-side keeps the contract clean:
  `limit=N` means `N displayable messages`.
- **Preserve opencode's message JSON shape verbatim.** The frontend's
  `transformHistory` already understands it; rewriting would be churn.
- **502 (not 200 + opaque bytes) on malformed upstream body.** The
  `TestProxy_StripPreservesNonJSONResponses` inversion is intentional: an
  HTML error page served with status 200 from upstream used to silently corrupt
  the frontend's JSON parse. Surfacing it as 5xx lets the user see a real
  error and retry.
- **Keep auto-scroll-to-bottom on fresh load.** Most users want to see the
  most recent message first; the sticky button makes older history
  discoverable without changing the default scroll position.

---

## Adversarial Self-Review

- **What if opencode reorders messages?** They don't (verified empirically),
  but my code walks the array in upstream order — no sorting assumption beyond
  "opencode returns oldest-first". Documented.
- **What if a malicious client sends `?limit=99999999`?** Capped at 200 server-side. Verified by `TestGetHistory_LimitCappedAtMax`.
- **What if the connection semaphore deadlocks under GET+POST concurrency?**
  Same acquire/release pattern as the existing proxy path; the slot is released
  via `defer` even on error.
- **What if `?before=` references a non-existent or non-displayable message?**
  Returns empty page, no cursor. Verified by `TestGetHistory_BeforeCursor_NotFound_ReturnsEmpty`.
- **What if the upstream body is enormous?** 16 MiB cap with surfaced 502.
- **Could the sticky button conflict with the existing "new messages" divider?**
  No — they're in different positions in the DOM (button is BEFORE the message
  map; divider is INSIDE it at `dividerIndex`). Verified by an existing
  regression test (`MessageList.test.tsx > pagination with divider regression`).

---

## Files Touched

- `api/internal/handlers/proxy_handlers.go` (new pagination logic + helpers)
- `api/internal/handlers/proxy_history_pagination_test.go` (new)
- `api/internal/handlers/proxy_history_e2e_test.go` (new)
- `api/internal/handlers/proxy_test.go` (default backend now returns array for GET /message)
- `api/internal/handlers/proxy_filter_test.go` (inverted non-JSON test)
- `frontend/src/components/chat/MessageList.tsx` (sticky load-earlier anchor)
- `frontend/src/components/chat/MessageList.test.tsx` (new discoverability tests)
- `frontend/src/api/messages.pagination.test.ts` (new wire-contract tests)
- `frontend/src/hooks/useMessageHistory.pagination.test.tsx` (new hook tests)

## Verification

- `cd api && go test -timeout 180s -count=1 ./internal/handlers/` — passes
- `cd api && go build ./...` — clean
- `cd api && gofmt -l . && go vet ./...` — clean
- `cd frontend && npx vitest run` — 1226/1226 passing
- `cd frontend && npx tsc --noEmit` — clean
