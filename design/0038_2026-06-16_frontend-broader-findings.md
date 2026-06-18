# Frontend — Broader Findings (Beyond Busy Indicators)

**Date:** 2026-06-16
**Status:** Analysis / proposal only — no code changes
**Scope:** `frontend/` — issues found during the broader pass requested after `0037_2026-06-16_frontend-busy-indicator-consistency-analysis.md`
**Companion to:** `design/0037_2026-06-16_frontend-busy-indicator-consistency-analysis.md`

---

## 0. Method

Per README-LLM.md Rule 7 (Assumptions: State, Then Validate) and Rule 11 (Adversarial Self-Review), every finding below was validated by reading code (frontend and, where relevant, the Go backend) before being asserted. Where a subagent helped gather evidence, the evidence was re-checked against the actual files. Findings that could not be substantiated were dropped; two candidate concerns were refuted and are recorded as false alarms in §6 so future reviewers don't re-litigate them.

Severity scale: **High** = correctness/reliability/real cost; **Medium** = maintainability / measurable but bounded cost; **Low** = polish.

---

## 1. The two SSE connections are genuinely necessary (informs `0037` P4)

`0037` §3.3 noted that each tab opens two SSE connections and flagged this as a "design divergence" worth a doc update. Validating the *event-type divergence* claim against the backend:

- **User stream** (`GET /api/v1/events`, consumed by `useUserEventStream`): the backend's `UserEventBroker.PublishToUser` is called from **exactly 3 production sites**, all in `api/internal/handlers/proxy_events.go:32,126,165`. It emits only `workspace.phase`, `session.status=idle`, `session.status=busy` (+ internally-generated `resync`).
- **Workspace stream** (`GET /api/v1/workspaces/:id/session-events`, consumed by `useEventStream`): the backend's `WorkspaceEventBroker.Publish` additionally carries `opencode.event` (which wraps `message.part.delta`, `message.part.updated`, `session.next.step.ended`, `session.error`, `session.updated`), `queue.update`, `agent.question`, `agent.question.resolved`, `agent.permission`, `agent.permission.resolved` (`api/internal/handlers/proxy_events.go:185,224,234,254,265,368,481`).
- Frontend consumers confirm the asymmetry: `ChatPage.handleSSEEvent` branches on 8 outer `event.type` values + 4 `oe.event_type` sub-values (`ChatPage.tsx:545-696`); `SessionActivityProvider` branches on only `session.status` and `workspace.phase` (`SessionActivityProvider.tsx:181,252`).

**Conclusion: CONFIRMED.** The two streams carry genuinely different event types. The dual-connection design is **not** an accidental redundancy — the workspace stream's `opencode.event`/`queue.update`/`agent.*` events are essential to the chat UX and are not delivered on the user stream.

**Implication for `0037` P4:** the recommendation stands but should be sharpened. You cannot merge the two streams without either (a) forwarding the rich event types to the user stream (backend change; fan-out cost: every user gets every workspace's `message.part.delta`), or (b) keeping both connections and only doing cross-tab multiplexing per-stream. The right framing is: **the doc (`design/0026` §10.3) describes cross-tab multiplexing that was never built; the per-tab dual-connection count is the real cost, and multiplexing (not merging) is the only viable remedy.** Update `0037` P4 accordingly.

---

## 2. `wsLog` is unconditionally enabled in production [Severity: High | Confidence: High]

`frontend/src/lib/wsLog.ts` defines `wsLog(event, workspaceId, extra)` which **always** calls `console.log` — there is no env / build flag gate (`wsLog.ts:38-44`). It is called from **21 production sites** (grep across `src`, excluding tests): every SSE connect/disconnect/read-timeout/reconnect, every status fetch, every UI activate click.

Consequences:
- **Production noise.** Every user's browser console fills with `[ws-timing] ...` lines on every reconnect storm. For a tool aimed at AI-agent workloads (where users *will* open DevTools), this is user-visible noise and unprofessional.
- **Performance.** `console.log` with string interpolation + `new Date().toISOString()` + `perfNow()` runs on every SSE event under load. Each `message.part.delta` does not log, but every `session.status`, every reconnect, every status-fetch does. Bounded but non-trivial.
- **Information leak (minor).** The logs include `ws=<workspaceId-slice-8>` and ISO timestamps — useful for support but arguably PII-adjacent in a multi-tenant context. Not a secret, but worth a conscious decision.

**Proposal P5 — Gate `wsLog` behind an explicit debug flag.** Replace the body with something like:

```ts
const enabled = (() => {
  try { return localStorage.getItem("lsp.debug.ws") === "1"; } catch { return false; }
})();
export function wsLog(...): void { if (!enabled) return; ... }
```

Or use Vite's `import.meta.env.DEV` for a build-time gate (no runtime cost in production). The localStorage approach has the advantage of being toggled per-support-session without a redeploy.

**Right level of abstraction:** one-line gate in one file. No new abstraction. Idiomatic for this kind of diagnostic logger.

**Complexity:** Trivial.

**Validation gates:** confirm with the user whether the `[ws-timing]` logs are intentionally always-on for support diagnostics (if so, the gate should default-on in dev and default-off in prod, documented). Do not assume.

---

## 3. `MessageBubble` runs one `setInterval` per message — for a clock-time display that doesn't need it [Severity: Medium | Confidence: High]

`MessageBubble.tsx:54` calls `useNow()` (60 s `setInterval`, `useNow.ts:14-17`). `MessageBubble` is rendered once per message in the list. With N visible messages that is N independent 60-second intervals.

The interval is used only to format `message.createdAt` via `formatTimestamp(message.createdAt!, now)` (`MessageBubble.tsx:101`). Reading `formatTimestamp` (`MessageBubble.tsx:40-49`): it returns either a **clock time** (`toLocaleTimeString`, e.g. "14:32") or a **date** (`toLocaleDateString`, e.g. "Jun 16"). It **never** renders a relative time ("5m ago"). A clock-time or date display does not change within a session — the value is stable the moment `createdAt` is known.

So:
- **The interval is wasted work.** It ticks every 60 s, recomputes `now`, recomputes `formatTimestamp`, and produces the identical string 100% of the time (unless the message lands exactly on a midnight boundary, in which case the date flips once).
- **It defeats `memo`.** `MessageList.tsx:22` wraps `MessageBubble` in `memo()`, but `useNow()` causes each instance to re-render every 60 s regardless of prop changes. With 100 visible messages, that is 100 re-renders/minute purely for timestamps that don't change.
- The other `useNow` consumers are fine: `Sidebar.tsx:695` (session-list relative times via `formatRelativeTime` — legitimately relative) and the dead `SessionItem.tsx:16` (will be removed by `0037` P3).

**Proposal P6 — Remove `useNow()` from `MessageBubble`.** Pass the formatted timestamp string as a prop from the parent, or compute it inline at render with no interval. Since the format is clock-time/date (not relative), no live updating is needed.

If relative timestamps are ever desired on messages, lift `useNow` to a single ancestor (e.g. `MessageList`) and pass `now` down as a prop — one interval for the whole list, not N.

**Right level of abstraction:** removes an unnecessary hook; leans harder on the existing `memo()` boundary. Simpler, not more complex.

**Complexity:** Trivial.

**Validation gates:** confirm there is no hidden requirement for live-updating message timestamps (there is not, per the format function — but flag for user).

---

## 4. `SessionActivityProvider` does `queryCache.getAll()` on every sessions event [Severity: Medium | Confidence: High]

`SessionActivityProvider.tsx:56-165` subscribes to the React Query cache and, on every `updated`/`added` event whose key starts with `"sessions"`, runs **both** `seedBusy()` and `reconcileUnread()`. Both functions call `queryCache.getAll()` (lines 67, 113) and walk the entire cache, even though the changed query is available on `event.query`.

Validated amplifier: `ChatPage.tsx:550` calls `queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] })` on **every** `session.status` SSE event (busy or idle). Each invalidate → background refetch → `"updated"` event → full `getAll()` walk × 2 functions. The provider's own `setQueryData` calls (`SessionActivityProvider.tsx:193,224,241`) each emit another `"updated"` event, so a single status change can run the subscriber 2–3×.

Cost shape (validated): with ~5 workspaces × ~20 sessions the cache holds ~216 queries + ~100 sessions; per event that is ~300 iterations, runs 2–3× per status change, at several status changes/sec under load → thousands of iterations/sec. Not catastrophic, but it is **O(E·(Q+S))** where the changed query would give O(1).

`seedBusy` is partly mitigated by its `seededRef` early-exit (`SessionActivityProvider.tsx:71`); `reconcileUnread` has **no** early-exit and walks every session in every sessions-query on every event.

**Proposal P7 — Use `event.query` instead of `queryCache.getAll()`.** The subscriber already filters by key prefix (`:157`); replace `getAll()` with the specific changed query's data (`event.query.state.data`). This turns the per-event cost from O(Q+S) to O(sessions-in-one-workspace).

```
// before
const unsubscribe = queryCache.subscribe((event) => {
  if (event.type === "updated" || event.type === "added") {
    const key = event.query.queryKey;
    if (Array.isArray(key) && key[0] === "sessions") {
      seedBusy();        // walks getAll()
      reconcileUnread(); // walks getAll()
    }
  }
});

// after: pass the changed query (or its wsId) into seed/reconcile so they
// touch only that workspace's sessions.
```

**Right level of abstraction:** uses the data React Query already hands you. Removes a hidden global-scan anti-pattern. No new abstraction.

**Complexity:** Low–Medium. The `seedBusy`/`reconcileUnread` functions currently iterate all queries by design (they were written to handle initial-seed across many workspaces). Splitting "initial seed across all" from "incremental update for one query" is the natural refactor. Add tests for the incremental path (the existing provider tests cover the global behaviour).

**Validation gates:**
1. The initial seed genuinely needs to scan all sessions-queries once (because on mount, several workspaces' sessions lists may already be cached). Preserve an explicit "seed all on mount" path; only the *incremental* subscriber needs to use `event.query`.
2. Re-run `providers/SessionActivityProvider.test.tsx` (the existing 1000+ line suite) after refactor — it covers exactly these reconciliation paths.

---

## 5. `contextBySessionRef` + `contextVersion` counter is a non-idiomatic workaround [Severity: Medium | Confidence: High]

Validated by direct reading of `ChatPage.tsx:209-239`:

- `contextBySessionRef` is a `Map<sessionId, number>` updated via `.set()` on each `session.next.step.ended` SSE event (`ChatPage.tsx:628`).
- Reactivity is forced via a separate `contextVersion` counter (`ChatPage.tsx:213`) bumped on each update (`:629`).
- The read site (`ChatPage.tsx:218-223`) is an IIFE that does `void contextVersion;` (`:219`) then reads `contextBySessionRef.current.get(sessionId ?? "")`.
- **The map only ever serves the current session** — the only `.get()` is for `sessionId`. Multi-session storage is unused.

Three problems:
1. **`void contextVersion;` is a lint-silencing trick, not a reactivity mechanism.** The author's comment ("consumed to trigger re-evaluation when SSE updates the ref") is technically inaccurate — the IIFE re-runs on every render regardless; reactivity comes from `setContextVersion` causing the render. The `void` line just keeps `noUnusedLocals` happy.
2. **The Map shape is unjustified.** A scalar `useState<number | undefined>` for the realtime value would be both simpler and correct.
3. **The comment misleads future maintainers** into thinking the ref is required for correctness.

This is the **only** instance of the "ref + version counter" anti-pattern in `src` (validated by searching for `setVersion`, `forceUpdate`, `useReducer(x => x+1, 0)`, `[, force]`, `void …Version`). The `compactionDetected` block nearby (`ChatPage.tsx:228-239`) is **not** the same anti-pattern — it uses a legitimate "previous value" ref plus real state.

**Related — the 50% compaction heuristic is undocumented.** `ChatPage.tsx:230-239` fires `setCompactionDetected(true)` when `contextUsed` drops >50%. No comment justifies the 50% threshold. It can both false-positive (any turn whose `prompt_tokens` happens to halve vs. the prior step — e.g. a big tool output followed by a short reply, or a provider switch that changes cache-read accounting) and false-negative (compaction from 100K→60K won't fire). The tests (`ChatPage.context.test.tsx:170,194`) only pin the boundary, not the rationale.

**Proposal P8 — Replace the ref+counter with scalar state; document or reconsider the 50% threshold.**

```
// before
const contextBySessionRef = useRef<Map<string, number>>(new Map());
const [contextVersion, setContextVersion] = useState(0);
// ... setContextVersion(v => v+1) and void contextVersion

// after
const [realtimeContextUsed, setRealtimeContextUsed] = useState<number | undefined>(undefined);
// on session.next.step.ended for the current session: setRealtimeContextUsed(promptTokens)
// on session change: setRealtimeContextUsed(undefined)
```

For the compaction threshold: either (a) add a comment citing why 50% is the right signal (e.g. opencode's compaction typically retains ~25-30%, so 50% is a conservative trigger), or (b) ask the user whether the compaction banner is even carrying its weight — it may be noise.

**Right level of abstraction:** replaces a workaround with the idiomatic primitive. Reduces code.

**Complexity:** Trivial for the state change. The compaction-threshold question is a product decision, not engineering.

**Validation gates:** the multi-session Map *might* have been intended to support a future "show context for hovered session" feature — confirm with the user that no such feature is planned before collapsing to a scalar. (If it is planned, keep the Map but drive it via `useState<Map>` with immutable updates, still dropping the version counter.)

---

## 6. False alarms (validated, dismissed with rationale)

Recorded so future reviewers don't re-litigate.

### 6.1 "Concurrent writers to `["sessions"]` cache lose updates" — FALSE ALARM

Validated: all 5 production `setQueryData` call sites on the `["sessions", wsId]` key use the **functional-updater form** `(old) => old.map(...)`:
- `SessionActivityProvider.tsx:193,224,241`
- `ChatPage.tsx:587`
- `useSessionTitle.ts:53`

React Query v5 applies functional updaters synchronously against `query.state.data` inside the cache; JS is single-threaded; back-to-back updaters compose correctly. No direct-replacement writes exist in production (only in `*.test.tsx` fixtures).

The closest real concern is **optimistic write vs. background refetch**: `ChatPage.tsx:550` invalidates on every `session.status` SSE event, and the subsequent refetch can overwrite a concurrent optimistic `setQueryData` if the backend hasn't persisted yet. This is **by design** — busy/unread truth lives in `busySessions`/`pendingUnread` React state, not the cache; the cache is treated as best-effort. Transient flicker of e.g. a session title is possible during the refetch window but self-corrects. Not a bug; worth a one-line comment near `ChatPage.tsx:550` documenting the acceptable transient.

### 6.2 "Dual `useQuery(["workspaces"])` in ChatPage is wasteful" — MINOR, NOT A BUG

`ChatPage.tsx:63-76` has two `useQuery({ queryKey: ["workspaces"], select: ... })` calls — one selecting `workspaceName`, one selecting `activeWorkspaceData`. React Query dedupes the underlying fetch (one network request, two subscriptions). Each `select` runs on every cache notification but the work is trivial (an `Array.find`). This is idiomatic React Query usage of `select` for derived views; merging them into one hook with two derivations would be micro-optimisation with no measurable benefit. Not worth changing.

---

## 7. Inventory of hardcoded magic numbers (for the record)

Not proposals — just an inventory so they are visible. Each is a candidate for a named constant if the surrounding area is touched for other reasons.

| Value | Location | Meaning |
|---|---|---|
| `60_000` | `useChatStream.ts:8` (`IDLE_WAIT_TIMEOUT_MS`) | Wait for idle SSE before falling back to getHistory. Named. |
| `35_000` | `useEventStream.ts:7`, `useUserEventStream.ts:9` | SSE read timeout (must exceed backend 25 s heartbeat). Named locally; duplicated across two files. |
| `30_000` / `3_000` | `useWorkspaces.ts:48` | Workspace-status poll intervals (Active / transitioning). Inline. |
| `2_000` / `30_000` / `1_000` | `sseConnection.ts:15-16`, `useEventStream.ts:5-6`, `useUserEventStream.ts:7-8` | SSE reconnect backoff bounds. Duplicated across files with slight inconsistencies (min reconnect is 2000 in `useEventStream`, 1000 in `useUserEventStream` and `sseConnection` default). |
| `1_000` | `ChatPage.tsx:102` | markSessionSeen debounce. Inline. |
| `2_000` | `MessageBubble.tsx:62` | "Copied" indicator timeout. Inline. |
| `0.5` | `ChatPage.tsx:233` | Compaction detection threshold. Inline, undocumented (see §5). |
| `60_000` | `useNow.ts:14` | Default tick. Documented. |
| `2_000` / `5_000` / `15_000` | `api/events.ts:10-12` | BroadcastChannel heartbeat/leader timeouts — **in dead code** (removed via `0037` P3). |

**Observation:** the SSE backoff constants are defined independently in `sseConnection.ts`, `useEventStream.ts`, and `useUserEventStream.ts` with **inconsistent values** (min reconnect 1000 vs 2000). If `sseConnection.ts` already exposes `DEFAULT_MIN_RECONNECT_MS` etc. as module constants, the two hooks should import them rather than redefining. Minor, but a real consistency gap.

---

## 8. Summary of new proposals

| ID | Problem | Severity | Complexity | Dependencies |
|---|---|---|---|---|
| **P5** | `wsLog` always-on in prod | High | Trivial | Confirm intent with user |
| **P6** | Per-message `useNow` interval for a static timestamp | Medium | Trivial | None |
| **P7** | `queryCache.getAll()` on every sessions event | Medium | Low–Med | Add incremental-path tests |
| **P8** | `contextBySessionRef`+counter workaround; undocumented 50% threshold | Medium | Trivial (state) / product (threshold) | Confirm no planned multi-session feature |
| (update) | Amend `0037` P4: streams cannot be merged; only multiplexed | — | — | None |

P5 and P6 are the highest value-to-effort ratio: both are near-trivial, both remove real per-event/per-message cost, and both improve the user-facing experience (console noise; render performance).

---

## 9. What this pass deliberately did NOT cover

To stay disciplined about scope and not boil the ocean:
- **Did not audit the chat streaming reconcile/reconnect-mode logic** (`ChatPage.tsx:276-365`, `knownLivePartIds`, auto-abort of stuck sessions). This is genuinely complex but is also the most-tested area (worklogs 0300s, `ChatPage.reconnect.test.tsx`). It deserves its own focused pass with runtime validation, not a side-quest here.
- **Did not audit the message-queue subsystem** (`useMessageQueue`). Same reason — substantial enough to warrant its own analysis.
- **Did not assess Tailwind/styling consistency** beyond the indicator colours covered in `0037` P2.
- **Did not run the app.** All findings are static-analysis-based. Runtime profiling would sharpen P3/P6/P7 (could confirm or refute the perceived cost) but is out of scope for a documentation pass.

---

## 10. Files examined (evidence base, beyond those in 0037)

- `frontend/src/lib/wsLog.ts`
- `frontend/src/hooks/useNow.ts`
- `frontend/src/components/chat/MessageBubble.tsx` (esp. `:40-49`, `:54`, `:101`)
- `frontend/src/components/chat/MessageList.tsx:22`
- `frontend/src/providers/SessionActivityProvider.tsx` (esp. `:56-165`)
- `frontend/src/pages/ChatPage.tsx` (esp. `:209-239`, `:550`, `:587`)
- `frontend/src/hooks/useEventStream.ts`, `useUserEventStream.ts`, `useChatStream.ts`, `useWorkspaces.ts`, `useSessionTitle.ts`
- `frontend/src/lib/sseConnection.ts`
- `api/internal/handlers/proxy_events.go` (backend SSE publishing — `:34,130,171,185,224,234,254,265,374,481`)
- `api/internal/handlers/stream_user_events.go:45-69`
- `api/internal/handlers/proxy_stream.go:20-58`
- `api/internal/services/eventbroker/broker.go`, `user_broker.go`
