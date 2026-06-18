# Frontend Busy/Loading Indicator — Consistency Analysis

**Date:** 2026-06-16
**Status:** Analysis / proposal only — no code changes
**Scope:** `frontend/` — busy & loading indicator mechanisms, their state sources, and consistency

---

## 1. Objective

Answer the user's question with evidence: *can the "bouncing dot" busy indicator and the "blue circle" busy indicator share one mechanism? Is the blue circle robust/reliable? Are there other consistency improvements worth making?* Document at the right level of abstraction. Propose nothing that has not been validated by reading the code.

Per README-LLM.md Rule 7 (Assumptions: State, Then Validate) and Rule 11 (Adversarial Self-Review), every claim below is backed by a `file:line` reference. No guesswork.

---

## 2. Terminology — the two indicators the user named

There is no component literally called "bouncing dot" or "blue circle". Mapping the user's description to actual code:

| User's name | Actual component | File | Glyph | Colour |
|---|---|---|---|---|
| "bouncing dot" | `StreamingIndicator` | `frontend/src/components/chat/StreamingIndicator.tsx:1` | 3 dots, `animate-bounce`, staggered 0/150/300 ms | `bg-muted-foreground` (NOT blue) |
| "blue circle" | inline `Loader2` (lucide icon, `animate-spin`) in the Sidebar | `frontend/src/components/layout/Sidebar.tsx:371` and `:792` | spinning circle | `text-blue-500` |

The "blue circle" is a `lucide-react` `Loader2` icon rendered inline with an ad-hoc `text-blue-500` class — it is **not** the shared `Spinner` component (`components/ui/Spinner.tsx`), which uses `text-muted-foreground`.

There are, in fact, **three conceptually distinct states** these glyphs express, and the codebase blurs them:

| Concept | Meaning | Canonical state source |
|---|---|---|
| **A. Agent working** ("busy") | The opencode agent has an in-flight turn on a session | `session.status === "busy"` (SSE) |
| **B. Local send in flight** | The browser has POSTed a message, awaiting first SSE confirmation | `useChatStream.localStreaming` |
| **C. UI/data loading** | Generic data fetch / route load / mutation pending | React Query `isPending`/`isLoading` |

The bouncing dot (A+B) and the blue circle (A) both ultimately reflect concept **A**, which is the heart of the user's question.

---

## 3. Validated architecture — how "busy" is computed today

There are **two independent derivations** of the same `session.status` event, fed by **two independent SSE connections**.

### 3.1 Sidebar blue circle — `SessionActivityProvider`

- Mounts once at the app root: `components/layout/AppShell.tsx:46`.
- Opens **SSE connection #1** to `GET /api/v1/events` (user-scoped, all workspaces): `hooks/useUserEventStream.ts:47`.
- State store: `busySessions: Map<sessionId, workspaceId>` (`providers/SessionActivityProvider.tsx:24`).
- Sources of truth, in priority order:
  1. **SSE** `session.status === "busy"|"idle"` (`SessionActivityProvider.tsx:181-249`) — sole authority once seen.
  2. **REST seed** from the `["sessions", wsId]` React Query cache: a session with `status === "active"` is treated as busy (`SessionActivityProvider.tsx:78`). Seeded once per workspace, guarded by `seededRef` so later REST refetches cannot clobber SSE-tracked state (`SessionActivityProvider.tsx:35`, `:64-94`).
- Reset on user-stream reconnect: `busySessions` cleared, `seededRef` cleared (`SessionActivityProvider.tsx:167-171`).
- Consumed by: `useIsSessionBusy`, `useWorkspaceBusyCount` (`SessionActivityProvider.tsx:281`, `:291`, `:407`). Rendered as the blue spinner at `Sidebar.tsx:792` (per session) and `Sidebar.tsx:371` (collapsed workspace busy-count).

### 3.2 Chat bouncing dots — `ChatPage` + `useChatStream`

- Mounts in `ChatPage`. Opens **SSE connection #2** to `GET /api/v1/workspaces/{id}/session-events` (workspace-scoped): `hooks/useEventStream.ts:24`.
- State store: local React state `serverBusy` (`pages/ChatPage.tsx:205`) + `sseHasDrivenBusy` ref (`ChatPage.tsx:207`).
- Sources of truth:
  1. **SSE** `session.status` for the **current session only** (`ChatPage.tsx:549-568`): `busy` → `setServerBusy(true)`, `idle` → `setServerBusy(false)`.
  2. **REST seed** from `["workspace-status", wsId]` → `status.sessions[].status === "busy"` (`ChatPage.tsx:204`, `:251-255`), gated on `!sseHasDrivenBusy` so SSE wins once it has fired.
- Reset on session change (`ChatPage.tsx:49-51`) and on workspace-stream reconnect (`ChatPage.tsx:700-706`).
- The chat-level flag is `streaming = localStreaming || serverBusy` (`hooks/useChatStream.ts:138`). `localStreaming` (concept B) is set true on `send()` (`useChatStream.ts:46`) and cleared on idle-SSE/timeout/finally (`useChatStream.ts:118-121`).
- `serverBusy` is also read by `useChatStream`'s 60 s timeout to distinguish "interrupted connection" from "legitimately slow response" (`useChatStream.ts:97`, via `serverBusyRef` `:20-21`).
- Rendered as the bouncing dots at `ChatView.tsx:70` (`{streaming && <StreamingIndicator />}`).

### 3.3 The key redundancy

Both derivations consume the **same `session.status` SSE event** for the current session, but over **two separate TCP connections** delivered to **two separate state stores**:

```
                          ┌── SSE #1: /events ──────────────► SessionActivityProvider.busySessions
                          │        (user-scoped)                       │
opencode session.status  │                                            ▼
        event ───────────┤                                   Sidebar blue circle (all sessions)
                          │
                          └── SSE #2: /workspaces/:id/session-events ► ChatPage.serverBusy
                                   (workspace-scoped)                     │
                                                                          ▼
                                                            Chat bouncing dots (current session only)
                                                            (OR'd with localStreaming)
```

For the active session the two indicators are driven by the same logical event but arrive via independent connections with independent delivery timing, independent REST-seed sources, and independent reconnect-reset behaviour.

---

## 4. Findings

Each finding is rated **Severity** (impact if unfixed) and **Confidence** (how thoroughly validated).

### F1 — Dual derivation of the same "busy" signal [Severity: Medium | Confidence: High]

`serverBusy` in `ChatPage` and `busySessions` in `SessionActivityProvider` both reduce the same `session.status` SSE events for the current session. Evidence: §3.2 vs §3.3.

Observable consequences:
- **Timing divergence.** The two SSE connections deliver the same event independently. A busy→idle transition can momentarily show in one indicator before the other (brief flicker). Both self-correct within one event, so this is cosmetic, not a correctness bug.
- **Reconnect asymmetry.** Sidebar clears busy on user-stream reconnect (`SessionActivityProvider.tsx:167`); chat clears on workspace-stream reconnect (`ChatPage.tsx:702`). If only one stream drops, the two indicators can disagree until both reconnect.
- **Maintenance hazard.** Two derivations must be kept in sync. The `sseHasDrivenBusy` gating in `ChatPage` (`ChatPage.tsx:206`, `:252`, `:553`, `:564`, `:702`) exists precisely to mirror race-handling that `SessionActivityProvider` already solves with `seededRef` (`SessionActivityProvider.tsx:35`, `:64`). Parallel logic for the same problem.

### F2 — "Blue circle" robustness is adequate; its fragility is the dual mechanism, not the indicator itself [Severity: Low | Confidence: High]

The provider's busy tracking is reasonably robust:
- REST seed once, then SSE-authoritative via `seededRef` (`SessionActivityProvider.tsx:35`, `:64-94`).
- Workspace suspend/terminal phases clear busy for that workspace (`SessionActivityProvider.tsx:252-273`).
- Cross-session counting via `workspaceBusyCount` iterates the map (`SessionActivityProvider.tsx:291-300`) — O(sessions), trivially small.

The realistic robustness gap is **F1's reconnect asymmetry**, not the indicator logic. The indicator itself will not get "stuck busy": an `idle` SSE event, a workspace suspend, or a stream reconnect (which re-seeds from REST) all clear it.

### F3 — `Spinner` primitive exists but is bypassed by 7 inline `Loader2` usages [Severity: Medium | Confidence: High]

`components/ui/Spinner.tsx` is the intended shared loading primitive (typed, sized, `aria-label="Loading"`). It is used for route/page/data loads (`router.tsx:20`, `:27`; `ChatPage.tsx:872`, `:941`; `org-admin/OrgAdminLayout.tsx:34`; `settings/RelaySetupWizard.tsx:109`).

But `Loader2` from `lucide-react` is used **directly** in 7 places with ad-hoc styling, conflating concepts A and C:

| Location | Class | Concept expressed |
|---|---|---|
| `Sidebar.tsx:161` | `h-4 w-4 animate-spin` (inherited colour) | C (create-workspace mutation) |
| `Sidebar.tsx:358` | `h-3 w-3 animate-spin text-muted-foreground` | C (workspace activating) |
| `Sidebar.tsx:371` | `h-3 w-3 animate-spin text-blue-500` | **A** (busy session count) |
| `Sidebar.tsx:384` | `h-3 w-3 animate-spin` (inherited) | C (creating session) |
| `Sidebar.tsx:792` | `h-3.5 w-3.5 animate-spin text-blue-500` | **A** (session busy) |
| `chat/MessageList.tsx:127` | `h-4 w-4 animate-spin text-muted-foreground` | C (loading older messages) |
| `chat/SessionRetryBanner.tsx:46` | `RefreshCw ... animate-spin` (2 s duration) | A-adjacent (retry in progress) |

Issues:
- **Concept A ("busy") and concept C ("loading") use the same spinning-circle glyph**, distinguished only by colour (`blue-500` vs `muted-foreground`). A user cannot tell "the agent is thinking" from "the UI is fetching" by shape — only by hue, which is not accessible (colour-only signal).
- **Inconsistent colour for the same concept.** Busy is `blue-500` in the sidebar but the chat's equivalent (bouncing dots) is `muted-foreground`. Two visual languages for one idea.
- **No `aria-label` on inline `Loader2`.** The `Spinner` component sets `aria-label="Loading"` (`Spinner.tsx:17`); the inline `Loader2` usages do not. Screen readers get nothing for the busy spinners. (The `Spinner.test.tsx:9` test asserts the label; no equivalent guard exists for inline usages.)
- **No `aria-label` on `StreamingIndicator`** either (`StreamingIndicator.tsx` has none). Design doc §… (`design/0026 …` line 783) calls for `aria-live="polite"` on streaming responses; the indicator itself is not announced.

### F4 — Dead code cluster: a parallel (unused) session-list implementation [Severity: Medium | Confidence: High]

Three files form an unused parallel implementation, superseded by the tree-based `WorkspaceSessionList` defined inline in `Sidebar.tsx:432`:

| File | Status | Evidence |
|---|---|---|
| `components/session/SessionItem.tsx` | Dead | only imported by `SessionList.tsx` + its own test. Uses REST `session.status === "active"` → static `bg-blue-500` dot (`SessionItem.tsx:29-31`) — a THIRD way to show "busy". |
| `components/session/SessionList.tsx` | Dead | only imported by `workspace/WorkspaceSessionList.tsx` + its own test. |
| `components/workspace/WorkspaceSessionList.tsx` | Dead | imported by **nothing** (grep for `workspace/WorkspaceSessionList` returns no production import). |

This is the classic "100% LLM-developed" hazard the user flagged: a newer tree implementation was built alongside the old flat-list implementation, and the old one was never removed. Beyond dead code, `SessionItem.tsx:29` encodes yet another busy representation (`session.status === "active"`) that could mislead a future maintainer about the canonical signal.

### F5 — Dead code: `api/events.ts` BroadcastChannel SSE client (never wired) [Severity: Medium | Confidence: High]

`api/events.ts` (`createEventStream`) implements a `BroadcastChannel`-multiplexed SSE client with leader election — exactly the cross-tab scheme described in `design/0026_2026-05-23_frontend.md` §10.3 (line 730). It is **not used by any production code**: the only importers are test files (`api/events.test.ts` and `useChatStream.test.ts:13`). The live streams use `lib/sseConnection.ts` (`createSSEConnection`, raw `fetch` reader loop), opened independently per tab.

Consequences:
- **Design divergence.** The documented cross-tab SSE multiplexing (one leader tab holds the connection; others subscribe via `BroadcastChannel`) was never wired up / was removed. Each browser tab opens **two** SSE connections (user stream + workspace stream). With the opencode-pod connection budget discussed in `design/0026` §10.3, a user with N tabs consumes 2N connections to a single sandbox pod.
- **Stale test.** `api/events.test.ts` tests dead code, giving false coverage signal. Also `useChatStream.test.ts:241` references a `registerTabCloseAbort` that was "removed in fix for refresh-abort bug" — confirming `api/events.ts` was partially dismantled but the file and its leader-election logic were left behind.

### F6 — Status vocabulary inconsistency: `"active"` vs `"busy"` for the same concept [Severity: Low | Confidence: High]

The same fact ("the agent has an in-flight turn") is reported under two different strings depending on the endpoint:

| Source | Field | Value meaning "busy" | Evidence |
|---|---|---|---|
| SSE event | `SessionStatusEvent.status` | `"busy"` | `api/types.ts:198` (`status: "idle" \| "busy"`) |
| `GET /workspaces/:id/status` | `AgentSessionInfo.status` | `"busy"` | `api/types.ts:117` (`// "idle" \| "busy"`); used at `ChatPage.tsx:253` |
| `GET /workspaces/:id/sessions` | `SessionListItem.status` | `"active"` | type is bare `string` (`api/types.ts:71`); provider treats `"active"` as busy (`SessionActivityProvider.tsx:78`, `:196`) |

`SessionActivityProvider` silently translates SSE `"busy"` → cache `"active"` (`SessionActivityProvider.tsx:196`), bridging the two vocabularies. This is a latent trap: any future code reading `SessionListItem.status` must "know" that `"active"` means busy. `SessionListItem.status` is not even documented as an enum (`api/types.ts:71`).

### F7 — `localStreaming` is genuinely distinct and must not be unified away [Severity: n/a (design constraint) | Confidence: High]

Important counter-finding so the proposal does not over-reach: concept B (`localStreaming`, the optimistic "send in flight" state) is **not** redundant with concept A. It is set the instant the user clicks Send, before any SSE `busy` arrives (`useChatStream.ts:46`), and it drives the immediate UI feedback (bounce starts before the server confirms). It must remain local to `useChatStream`. Any unification concerns **only** the `serverBusy` term of `streaming = localStreaming || serverBusy`.

---

## 5. Proposals

Each proposal states the problem it solves, the abstraction level, the complexity, the validation gates required before implementation, and what it deliberately does NOT do (to avoid over-engineering, per README-LLM.md Rule 4 "Not over-engineered").

### P1 — Make the chat consume `SessionActivityProvider` for server-side busy (the unification the user asked for) [Priority: High]

**Problem solved:** F1 (dual derivation), and directly answers "have the bouncing dot feed off the same mechanism as the blue circle".

**Change:** In `ChatPage`, replace the local `serverBusy`/`sseHasDrivenBusy` machinery with the provider's value:

```
// before
const [serverBusy, setServerBusy] = useState(false);
const sseHasDrivenBusy = useRef(false);
// + status-poll sync effect (ChatPage.tsx:251-255)
// + setServerBusy branches in handleSSEEvent (ChatPage.tsx:553-567)
// + resets at ChatPage.tsx:49-51, :702

// after
const isSessionBusy = useIsSessionBusy(sessionId ?? "");
// streaming = localStreaming || isSessionBusy
```

The bouncing dots (`ChatView.tsx:70`) and the sidebar blue circle (`Sidebar.tsx:792`) would then read the **same** `busySessions` map → same mechanism, by construction.

**Right level of abstraction:** this is the canonical single-source-of-truth pattern. `SessionActivityProvider` already aggregates REST seed + SSE for **all** sessions and already handles the REST-vs-SSE race (`seededRef`) that `ChatPage` reimplements (`sseHasDrivenBusy`). Consolidating removes code rather than adding abstraction.

**Complexity:** Low. Net deletion of state + effects in `ChatPage`; the `useChatStream` API stays the same (it already accepts `serverBusy` as a parameter — `useChatStream.ts:10` — so it becomes `useChatStream(activeWorkspaceId, sessionId, isSessionBusy)`).

**Validation gates (MUST resolve before implementing — these are the reasons this is "document for now", not "do it now"):**

1. **REST-seed parity.** ChatPage seeds from `["workspace-status", wsId]` (`status === "busy"`); the provider seeds from `["sessions", wsId]` (`status === "active"`). Different endpoints, different cache-population timing. **Must verify** that mounting into a busy session (the reconnect-mode path, `ChatPage.tsx:280-285`) still works when busy comes from the provider's seed. The provider only seeds once `["sessions", wsId]` is in the cache; `ChatPage` triggers that fetch (`ChatPage.tsx:92`) — confirm ordering.
2. **`useChatStream` timeout correctness.** `useChatStream` reads `serverBusy` via a ref to decide interrupted-vs-slow at 60 s (`useChatStream.ts:20-21`, `:97`). `useIsSessionBusy` is a hook returning a derived boolean; the ref-mirror pattern must be preserved (a `useEffect` syncing `serverBusyRef.current = isSessionBusy`, exactly as today). Trivial, but must not be dropped.
3. **Reconnect semantics.** Provider clears busy on user-stream reconnect (`SessionActivityProvider.tsx:167`); ChatPage additionally re-polls workspace-status on workspace-stream reconnect (`ChatPage.tsx:700-706`). After unification, the `handleSSEReconnect` still needs to invalidate `["workspace-status"]` (for phase, not busy). Confirm no busy-state path is lost.
4. **`isReconnectMode` derivation** (`ChatPage.tsx:280-285`) keys off `serverBusy`; update to key off `isSessionBusy`.
5. **Test impact.** `ChatPage.*.test.tsx` mock `useWorkspaceBusyCount` but the SSE busy path is tested directly via `handleSSEEvent` (`ChatPage.reconnect.test.tsx`, `ChatPage.sse.test.tsx`). Those tests assert `serverBusy`-driven behaviour through the captured SSE handler; they will need rework to assert via the provider instead. The `integration/session-activity.test.tsx` suite already covers provider→UI wiring and would gain the chat as a consumer.

**Does NOT do:** does not merge the two SSE connections (see P4), does not touch `localStreaming` (F7), does not introduce a new abstraction layer.

### P2 — Consolidate loading/busy primitives: one `Spinner` (loading) + one `BusyIndicator` (agent working) [Priority: Medium]

**Problem solved:** F3 (concept A vs C blurred; inaccessible colour-only signalling; inline `Loader2` proliferation).

**Change:**
- Keep `Spinner` (`components/ui/Spinner.tsx`) as the **only** primitive for concept C (data/route/mutation loading). Replace the 4 inline `Loader2` "loading" usages (`Sidebar.tsx:161`, `:358`, `:384`; `MessageList.tsx:127`) with `<Spinner size="sm" />`.
- Introduce a small `BusyIndicator` component (or extend `Spinner` with a `tone` prop) for concept A, owning the **shape** that distinguishes "agent working" from "loading". Today concept A is rendered as a blue spinner (sidebar) AND bouncing dots (chat) — two shapes. Pick one canonical shape for "agent working" and use it in both places, OR explicitly decide the chat uses a richer indicator (dots) while the sidebar uses a compact one (spinner) — but make that an intentional, documented decision rather than accidental divergence.
- Add `aria-label`/`aria-live` to both primitives (the `Spinner` has it; `StreamingIndicator` and inline `Loader2` do not). This satisfies the accessibility intent in `design/0026` line 783.

**Right level of abstraction:** a UI-primitive layer with two clearly-named, single-purpose components. This is the idiomatic React/Tailwind pattern (the codebase already has `components/ui/` with `Spinner`, `Button`, etc.). It does not invent a framework.

**Complexity:** Low–Medium. Mostly find-and-replace plus one new tiny component. No state changes.

**Validation gates:**
1. Confirm the colour choice for "busy" is intentional (blue) vs "loading" (muted). If busy should also be muted for consistency, the sidebar blue-500 is itself the inconsistency to fix — **ask the user**; do not assume.
2. Sidebar tests assert `.animate-spin.text-blue-500` directly (`Sidebar.test.tsx:290`, `:342`). If the canonical busy shape/colour changes, update those assertions to target the new primitive (e.g. `data-busy` attribute) rather than a colour class — more robust.

**Does NOT do:** does not change when busy shows (that is P1's job), only how it renders.

### P3 — Delete the dead code (F4 + F5) [Priority: Medium]

**Problem solved:** F4 (parallel session-list cluster), F5 (dead BroadcastChannel SSE). Both are pure tech-debt removal (README-LLM.md Rule 5: Zero Technical Debt).

**Change:** remove `components/session/SessionItem.tsx`, `components/session/SessionList.tsx`, `components/workspace/WorkspaceSessionList.tsx`, `api/events.ts`, and their co-located tests (`SessionItem.test.tsx`, `SessionList.test.tsx`, `api/events.test.ts`). Confirm `components/session/` still has `RenameSessionDialog` (it does — used by `Sidebar.tsx:10`).

**Right level of abstraction:** deletion. The lowest-complexity, highest-clarity change.

**Complexity:** Trivial. Pure removal.

**Validation gates:**
1. Re-run the full grep for any dynamic import / barrel re-export of these symbols before deleting (the `components/ui/index.ts` barrel should be checked for any analogous barrel in `components/session/` — none found, but re-confirm at implementation time).
2. Run `npm run typecheck && npm run test && npm run lint` after deletion (per README-LLM.md "After completing work").

**Does NOT do:** does not address the design divergence from `design/0026` §10.3 (BroadcastChannel cross-tab multiplexing) — that is a separate decision (P4).

### P4 — Decide explicitly on cross-tab SSE multiplexing (deferred — document only) [Priority: Low]

**Problem surfaced:** F5's design divergence. The documented BroadcastChannel leader-election scheme (`design/0026` §10.3) is not implemented; each tab opens 2 SSE connections.

**This is deliberately a "decide, don't necessarily build" item.** Cross-tab multiplexing adds real complexity (leader election, heartbeat, failover) for a benefit (connection-count savings) that only matters for power users with many tabs against a single sandbox. It may not be worth the complexity today (Rule 4: "Not over-engineered").

**Validated constraint (see `0038_2026-06-16_frontend-broader-findings.md` §1):** the two SSE connections **cannot be merged**. The user stream (`/events`) and the workspace stream (`/workspaces/:id/session-events`) carry genuinely different event types — confirmed from both frontend consumers and the backend `PublishToUser` vs `broker.Publish` call sites in `api/internal/handlers/proxy_events.go`. The workspace stream's `opencode.event` / `queue.update` / `agent.*` events are essential to the chat UX and are not delivered on the user stream. So the remedy here is **cross-tab multiplexing per stream**, not stream merging.

The right action is:
- Either re-implement the documented cross-tab multiplexing properly (non-trivial; needs its own design + TDD; applies to **each** of the two streams independently), **or**
- Update `design/0026` §10.3 to record that the scheme was deliberately not built and why, so the doc and code stop disagreeing.

**Recommendation:** update the doc to match reality now (zero risk), and treat true cross-tab multiplexing as a deferred follow-up only if connection pressure is observed. **Do not** implement it as part of this consistency pass.

---

## 6. Recommended ordering

1. **P3** (delete dead code) — zero-risk, immediately reduces confusion about which session-list is canonical and removes a third "busy" representation (`SessionItem.tsx:29`).
2. **P1** (unify busy mechanism) — the core of the user's ask. Needs the §P1 validation gates passed first.
3. **P2** (primitive consolidation) — best done after P1 so the "busy" concept has a single state source before unifying its rendering.
4. **P4** (doc update) — independent; can happen anytime.

Each is independently mergeable. None depends on the others being merged first (P1 and P2 are cleanly separable; P3 is a prerequisite only insofar as it removes a misleading parallel implementation that could distract a reviewer of P1/P2).

---

## 7. Assumptions stated and validation status

| # | Assumption | Status | Evidence |
|---|---|---|---|
| A1 | There are exactly two live SSE connections: `/events` and `/workspaces/:id/session-events`. | **Validated** | `useUserEventStream.ts:47`, `useEventStream.ts:24` |
| A2 | Both streams carry `session.status` events for the current session. | **Validated** | `SessionActivityProvider.tsx:181`; `ChatPage.tsx:549` |
| A3 | `serverBusy` and `busySessions` reduce the same underlying event for the current session. | **Validated** | §3.2 vs §3.3 |
| A4 | `SessionItem`/`SessionList`/`workspace/WorkspaceSessionList` are unused in production. | **Validated** | grep returns no production importer of `workspace/WorkspaceSessionList`; the other two are imported only by that file + tests |
| A5 | `api/events.ts` (`createEventStream`) is unused in production. | **Validated** | only importers are `api/events.test.ts` and `useChatStream.test.ts:13` (both test files); no production importer |
| A6 | `localStreaming` is distinct from server busy and must remain. | **Validated** | `useChatStream.ts:46` (set on send, before any SSE) |
| A7 | The blue spinner's `text-blue-500` colour is an intentional design choice. | **NOT validated** | no comment, no design-doc spec for the *colour* (design doc §9.5 line 696 says "blue" for the session dot, but the spinner colour is not separately specified). Flagged for user confirmation in P2. |
| A8 | Mounting into a busy session via the provider's REST seed works equivalently to ChatPage's status-poll seed. | **NOT validated** | different endpoints/timing (see P1 gate 1). Must be verified before P1. |

A7 and A8 are the two items that cannot be confirmed by reading alone and are explicitly flagged as pre-implementation gates.

---

## 8. What this analysis deliberately did NOT do

- **No code changes** (user said "just document for now").
- **Did not assume the two indicators are buggy.** They are not stuck-busy; the provider's reset paths (SSE idle, workspace phase, reconnect re-seed) all clear them. The issue is duplication/consistency, not a live defect.
- **Did not propose merging the two SSE connections.** They carry different event types (the workspace stream carries `opencode.event` rich streaming — `message.part.delta`, etc. — that the user stream does not). Merging them is a larger architectural change outside this consistency pass.
- **Did not propose a new state-management library or context.** `SessionActivityProvider` already exists and is the right home; P1 consolidates *into* it.
- **Did not score the existing code against the PR rubric.** This is analysis, not a PR review.

---

## 9. Files examined (evidence base)

- `frontend/src/components/chat/StreamingIndicator.tsx`
- `frontend/src/components/ui/Spinner.tsx`
- `frontend/src/components/layout/Sidebar.tsx` (esp. `:340-390`, `:688-789`)
- `frontend/src/components/layout/AppShell.tsx`
- `frontend/src/components/session/SessionItem.tsx`
- `frontend/src/components/session/SessionList.tsx` (via grep)
- `frontend/src/components/workspace/WorkspaceSessionList.tsx`
- `frontend/src/components/chat/ChatView.tsx`
- `frontend/src/pages/ChatPage.tsx` (esp. `:38-112`, `:195-300`, `:540-710`, `:920-985`)
- `frontend/src/hooks/useChatStream.ts`
- `frontend/src/hooks/useEventStream.ts`
- `frontend/src/hooks/useUserEventStream.ts`
- `frontend/src/hooks/useWorkspaces.ts`
- `frontend/src/providers/SessionActivityProvider.tsx`
- `frontend/src/lib/sseConnection.ts`
- `frontend/src/api/events.ts`
- `frontend/src/api/workspaces.ts`
- `frontend/src/api/types.ts`
- `frontend/src/api/contract-fixtures.json`
- `design/0026_2026-05-23_frontend.md` (§9.5, §10.3, line 783)
