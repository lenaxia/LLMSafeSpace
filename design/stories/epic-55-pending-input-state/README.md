# Epic 55: Pending-Input State as a First-Class Session-Activity Facet

**Status:** Planning
**Depends On:** Epic 16 (agent input requests — question/permission feature exists), Epic 37 (session activity provider — `busy`/`unread` facets and the provider exist), Epic 28 (unified event stream — `UserEventBroker`)

---

## Problem Statement

When a session has a pending question or permission request, the sidebar's `?` (amber pulsing `HelpCircle`) only inconsistently replaces the blue busy spinner (`Loader2`). The `?` should win — a waiting agent is busy *because* it is pending, and pending is actionable — but it frequently does not appear.

The inconsistency is a symptom of two compounding defects, not one:

1. **Precedence lives in the view's render cascade, where it can be silently shadowed.** `Sidebar.tsx:813-821` evaluates `isBusy` before `showPending`. A session awaiting input is *by definition still busy* (opencode never emits `session.status=idle` mid-turn; verified `proxy_events.go:113-173`). So `isBusy` is almost always true simultaneously with `showPending`, and the spinner wins. The `?` only slips through during transient windows where the two state maps disagree.

2. **The two indicators are sourced asymmetrically.** `busy` and `unread` are facets owned by `SessionActivityProvider`, sourced from the cross-workspace user SSE stream + REST seed. `pending` is sourced from the workspace-scoped SSE stream via `ChatPage.tsx:688,699` — and `ChatPage` is only mounted for the viewed workspace. So the provider's pending set is structurally view-scoped: a question popping on a non-viewed workspace has no path to the sidebar at all.

The fix makes `pending_input` a first-class session-activity facet, on structural parity with `busy` and `unread`, with precedence encoded as a pure function in the state layer where it cannot rot.

---

## Scope Boundary

This epic concerns **session-activity state for the sidebar indicator**, not the agent-input feature itself (Epic 16) or the in-chat prompt cards (which remain legitimately view-local state in `ChatPage`). It sits in the lineage of Epic 37 (`busy`/`unread` facets) and Epic 39 (integrity hardening of that layer).

---

## Scale Constraints

Inherited from Epic 37; re-verified. These bound the performance profile and close the last open design cost (US-55.3 fan-out).

| Constraint | Value | Source |
|---|---|---|
| Max workspaces per user | ~20 (default list limit) | `workspace_service.go:419` |
| Max active workspaces per user | 10 (configurable 1–50) | `pkg/settings/schema.go:81` — `maxActiveWorkspacesPerUser` |
| Max sessions per workspace | 5 (configurable 1–20) | `pkg/settings/schema.go:82` — `defaultMaxActiveSessions` |

**Design implication:** The US-55.3 anti-entropy fan-out (≤10 pod fetches on user-stream connect) is trivially bounded. No parallelism limiting or degradation logic required beyond the existing per-fetch timeout (`stream_user_events.go:221-237` already times out a k8s list; the pending fan-out reuses that budget pattern).

---

## Spike Output (Validated Contracts)

The research for this epic is complete — the contracts below were validated against live code during design and drive the architecture. This is the record required by README-LLM Rule 7 (State, Then Validate).

### Root cause (verified)

| # | Finding | Verified At | Result |
|---|---|---|---|
| F1 | The render cascade checks `isBusy` before `showPending` | `Sidebar.tsx:813-821` | **Confirmed.** Spinner shadows the `?` whenever both are true. |
| F2 | `showPending` requires `depth === 0`; `isBusy` does not | `Sidebar.tsx:737-738` | **Confirmed.** Subtask pending questions show a spinner on the child row; `?` only on the parent via bubble-up (`Sidebar.tsx:513-526`). |
| F3 | `session.status` dual-publishes to both streams | `proxy_events.go:116-172` (`onSessionIdle`/`onSessionActive`) | **Confirmed.** Control event: workspace stream + user stream (replay-buffered). |
| F4 | `agent.question`/`agent.permission`/`*.resolved` publish to workspace stream **only** | `proxy_events.go:245-301` (`emitNormalizedInputEvent`) | **Confirmed.** Mis-classified: low-frequency control events stranded on the high-volume content stream. |
| F5 | `agent_died` dual-publishes with an explicit comment explaining replay on reconnect | `proxy_events.go:210-231` | **Confirmed.** The rationale applies identically to pending input events. |
| F6 | Token stream (`opencode.event`) is workspace-only | `proxy_events.go:190` | **Confirmed.** The two-stream split is principled (control vs. content); the replay ring is not threatened by adding 4 control event types. |
| F7 | The existing tests for the pending indicator never exercise the "pending *while* busy" case | `Sidebar.test.tsx:413-463` | **Confirmed.** All cases use `status: "idle"` + `mockIsSessionBusy=false`. The precedence bug went uncaught. |
| F8 | `pendingActions` is populated by `ChatPage`, not the provider; `ChatPage` is view-scoped | `ChatPage.tsx:688,699`; provider `addPendingAction` at `SessionActivityProvider.tsx:330-342` | **Confirmed.** Ownership inversion: global UI state sourced from a view component. |

### Authority model (verified — pod is the source of truth)

| # | Assumption | Verified At | Result |
|---|---|---|---|
| A1 | opencode exposes the authoritative pending set via `QuestionListPath`/`PermissionListPath` | `proxy_input.go:110-133` (`emitPendingInputRequests` fetches both) | **Confirmed.** The pod cannot be wrong about whether it is waiting. |
| A2 | An anti-entropy primitive already exists: `emitPendingInputRequests` re-fetches from the pod on workspace-stream open | `proxy_stream.go:76` | **Confirmed.** It is scoped to the viewed workspace only — the reason the bug exists. |
| A3 | A fan-out primitive already exists: `snapshotUserWorkspaces` lists all the user's workspaces on user-stream connect | `stream_user_events.go:189-260` | **Confirmed.** Iterates workspaces, filters to known phases, emits phase events. Extending it to also fetch pending per Active workspace is the reconciliation path. |
| A4 | `busy`'s reconcile is authoritative-replacement (wipe + reseed), not merge | `SessionActivityProvider.tsx:168-171` (`onReconnect` wipes `busySessions`) + `seedBusy` (lines 64-94) replaces from REST | **Confirmed.** Pending's reconcile is also authoritative-replacement, but the *mechanism* differs (D9): busy can wipe globally because `seedBusy` rebuilds synchronously from the React Query cache; pending rebuilds via N async pod fetches, so a global wipe would flicker. Instead, per-workspace marker commit replaces each workspace's pending set atomically as its snapshot completes. |
| A5 | `PublishToUser` appends to a 128-entry per-user replay ring and assigns `EventID` | `eventbroker/broker.go:20` (`replayBufferSize`), `user_broker.go:129-146` | **Confirmed.** Recoverable via `Replay()` for short gaps. |
| A6 | `PublishToWorkspace` has no replay buffer | `user_broker.go:148-158` | **Confirmed.** Workspace stream is fire-and-forget; reconnect re-fetches via `emitPendingInputRequests`. |
| A7 | `workspace.phase` publishes to user stream **only** (not dual) | `proxy_events.go:32` | **Confirmed.** The control/content split is per-sink ("which consumers need this?"), not "control = dual." A `publishControlEvent` helper that always dual-publishes would encode a false taxonomy. |

### Robustness ceiling (verified — matches `busy`, not provably zero-staleness)

| # | Residual race | Bounded window | Shared with `busy`? | Self-heals? |
|---|---|---|---|---|
| R1 | Snapshot→publish ordering: pod fetch at T returns `{Q1}`; live `.resolved` at T+ε removes Q1; snapshot then re-adds Q1 | fetch latency (~tens of ms) | Yes (REST seed at T vs. idle at T+ε) | on next reconnect |
| R2 | `WorkspaceOwner` lookup gap after API restart: owner unrecorded → dual-publish dropped to workspace-only | until watcher records owner | Yes (`onAgentDied` has the same dependency, `proxy_events.go:223`) | on next reconnect |
| R3 | Force-restart orphans (mitigated, not dissolved): `maybeRestart` force-restarts after `maxDefer` while a question is open | until next reconnect | No — but `maybeRestart` defers while sessions are busy (`secrets.go:202-264`), so the common path blocks restart until idle | on next reconnect (pod's in-memory pending is empty after restart; opencode does not persist questions) |

---

## Design Decisions

### D1: `pending_input > busy` precedence, encoded in the state layer

A session awaiting input is busy *because* it is pending; pending is the actionable facet and wins. The precedence is a total order over a single `SessionDisplayStatus` enum, exposed as `useSessionStatus(id)` — a pure function the view consumes. The view becomes a `switch(status)` and physically cannot re-introduce the shadowing bug. This is the systematic fix; the render-cascade swap is its consequence.

### D2: Pod-authoritative sourcing (not DB-persisted)

The pod is the sole source of truth; nothing is persisted to PostgreSQL. Rationale (validated against a rejected alternative):

- A DB-persisted `pending` column would be a *second* source of truth with **no anti-entropy mechanism**. The pod restarts with empty pending (in-memory in opencode); the DB row would drift indefinitely — ghost `?` forever. Persistence *amplifies* the cleanup gap rather than hiding it.
- The analogy to `context_used` (`session_index` column) and `hasUnread` is false: those have **no live authority** (the DB *is* the only record). Pending has a live authority (the pod) that exposes `QuestionListPath`/`PermissionListPath`.
- The anti-entropy primitive already exists (`emitPendingInputRequests`); extending it to user-stream scope (US-55.3) is the reconciliation. No new table, no new write path, no new desync class.

### D3: REST-authoritative within the pod-authoritative frame

Within "the pod is the source of truth," the provider treats pending as **REST-authoritative like `unread`**, not event-authoritative like `busy`. Pending is long-lived (a question can stay open for hours) and resolves by an external user action whose event the client may miss (reconnect outside the replay window, dropped events, cross-replica re-routing). An event-authoritative model goes stale silently in those cases. So:

- **Snapshot on connect/reconnect** (US-55.3) is the correctness backstop (anti-entropy).
- **Dual-publish live deltas** (US-55.2) is the freshness mechanism (delivery of post-connect events).

Both are required; they fail open in each other's gap. Conflating them was the error in earlier design iterations.

### D4: Delivery and anti-entropy have different jobs

| | Job | When | Catches |
|---|---|---|---|
| Live delta (US-55.2) | delivery | on event | questions that pop *after* connect |
| Snapshot (US-55.3) | anti-entropy | on connect/reconnect | events missed before connect, dropped, replay-window misses |

### D5: `ChatPage` keeps view-local prompt-card state; stops being the sidebar authority

`ChatPage` retains `setPendingQuestions`/`setPendingPermissions` — legitimately view-local (which prompt card to render in the active chat). Its calls into the provider's `addPendingAction`/`removePendingAction` become **optimistic updates** for responsiveness (mirroring `clearedRef` for unread), not the source of truth. The provider's pending reconcile (per-workspace marker commit — D9) is authoritative.

### D6: Depth gate is a deliberate view-layer decision, not an emergent side effect

Moving precedence into per-session `useSessionStatus` would make a pending subtask show `?` on its own row (the `depth===0` gate on `showPending` currently prevents this). This epic **preserves the current UX**: subtask rows render `busy` regardless of resolved status; only `pendingIndicatorIds` (the bubble-up set) flips the parent. The depth gate stays a view-layer concern. Showing `?` on subtask rows is a separate UX decision, deferred.

### D7: No `publishControlEvent` helper (Rule 4)

An earlier iteration proposed a `publishControlEvent` helper that always dual-publishes. Rejected: it encodes a false taxonomy (D-A7 — `workspace.phase` is control and user-*only*; the decision is per-sink, not "control = dual"). The dual-publish sites are few (4 input events + 3 existing) and the per-sink decision is clearer written inline. A comment + regression test (US-55.4: "every sidebar-relevant control event reaches `PublishToUser`") prevents the forgotten-publish bug class better than a leaky abstraction.

### D8: Robustness bar = `busy`'s profile, explicitly

This epic does not claim provable zero-staleness. It claims parity with the shipped `busy`/`unread` path: eventually consistent with bounded staleness, reconciled on reconnect. If `busy`'s robustness is acceptable for this codebase (it is shipped, and the reported bug was never about `busy`), then `pending`'s is too. A stricter anti-entropy (periodic re-snapshot every N seconds) is an explicit non-goal unless a future finding shows `busy` is also insufficient.

### D9: Per-workspace marker commit (no global wipe, no flicker)

`busy` can safely wipe on reconnect because `seedBusy` rebuilds synchronously from the in-memory React Query cache (`SessionActivityProvider.tsx:64-94`). Pending's rebuild depends on N async pod fetches — a global wipe would blank all `?`s until the slowest fetch completes (visible flicker). Instead, `emitPendingInputRequests` emits an `agent.input.snapshot_complete` marker per workspace (unconditionally, via `defer`, even on timeout) to the **user stream only**. The provider stages snapshot events per-workspace and commits atomically on the marker, replacing `pendingActions` for that workspace's sessions only. Each workspace's `?` updates independently when its snapshot completes; no global blank.

### D10: Top-level `RequestID` field on `WorkspaceSSEEvent`

The provider needs `request_id` to call `addPendingAction`/`removePendingAction`. Parsing it from `Data` (`QuestionRequest.ID` / `.resolved`'s `Data.request_id`) would couple the state layer to the agent's event shape. Add `RequestID string` to `WorkspaceSSEEvent` (`sse_event.go`), set at each publish site. `SessionID` already exists top-level. This makes control events uniform — `session.status` carries `SessionID`; input events now carry `SessionID` + `RequestID`. The `Data` payload is unchanged for workspace-stream consumers.

---

## Architecture

### Resolved state model

```ts
type SessionDisplayStatus =
  | "pending_input" | "busy" | "unread" | "idle";

// precedence (total order): pending_input > busy > unread > idle
// exposed as useSessionStatus(id) — pure function, state-layer owned.
// (dead/errored arms deferred until a workspace-phase hook is available;
//  the 4 reachable states still form a total order.)
```

### Data Flow — Real-time pending (post-connect)

```
opencode emits question.asked for session S in workspace W (user not viewing W)
  │
  ├── SSETracker.onRawEvent("W", "question.asked", raw)
  │     └── emitNormalizedInputEvent("W", ...)                       — proxy_events.go:233
  │           ├── (auto-approve guard at proxy_events.go:276 — skip if headless)
  │           ├── publishWorkspaceAndUserEvent("W", {Type:"agent.question", SessionID:S, RequestID:req.id, Data:req})
  │           │     ├── PublishToWorkspace (workspace stream → ChatPage in-chat card)
  │           │     └── PublishToUser (user stream → provider)                      — NEW (US-55.2)
  │
  └── User-scoped SSE → useUserEventStream.onEvent → SessionActivityProvider
        └── onEvent: agent.question handler → addPendingAction("W", S, req.id)      — NEW (US-55.3, Finding 1)
        └── Sidebar: useSessionStatus(S) === "pending_input" → HelpCircle (US-55.1)
```

### Data Flow — Anti-entropy (connect/reconnect)

```
User opens/reloads page, or user SSE reconnects
  │
  ├── useUserEventStream opens GET /api/v1/events
  │
  └── snapshotUserWorkspaces (stream_user_events.go:189)
        ├── list user's workspaces (existing)
        ├── for each Active workspace W:                                       — NEW (US-55.3)
        │     go emitPendingInputRequests("W")
        │       ├── fetchFromPod(QuestionListPath) + fetchFromPod(PermissionListPath)
        │       ├── publishWorkspaceAndUserEvent("W", {Type:"agent.question", SessionID, RequestID, Data}) for each
        │       └── defer: PublishToUser({Type:"agent.input.snapshot_complete"})  — D9 marker (user stream only)
        │
        └── Provider: NO global wipe (D9). Stages events per-workspace; commits atomically on each W's marker.
```

### Provider pending reconcile (per-workspace marker — D9)

```
// SessionActivityProvider — pending reconcile.
// UNLIKE reconcileUnread (reads hasUnread from REST cache) and seedBusy (reads
// status from REST), pending reconcile has NO REST source (D2). Its sole
// authoritative input is the snapshot events + marker delivered via the user
// stream (pod truth).
//
// D9: NO global wipe on reconnect (unlike busySessions). Instead:
//   onReconnect: clear stagingRef + snapshotCommittedWsRef. Do NOT touch pendingActions.
//   onEvent agent.question/.permission (W uncommitted): stage in stagingRef[W].
//   onEvent *.resolved: unstage from stagingRef[W] + removePendingAction (clears stale).
//   onEvent agent.input.snapshot_complete(W): commit stagingRef[W] → replace
//     pendingActions for W's sessions. Mark W committed. Future events go live.
//
// This prevents the global flicker that a synchronous wipe would cause (the
// rebuild is async via N pod fetches). Each workspace's ? updates independently
// when its snapshot completes.
```

---

## Stories

| Story | Title | Layer | Priority | Depends On |
|-------|-------|-------|----------|------------|
| [US-55.1](US-55.1-precedence-status-function.md) | `useSessionStatus` + `pending > busy` precedence in the state layer | Frontend | Critical | — |
| [US-55.2](US-55.2-dual-publish-input-events.md) | Dual-publish `agent.question`/`agent.permission`/`*.resolved` to the user stream | Backend | Critical | — |
| [US-55.3](US-55.3-snapshot-pending-anti-entropy.md) | Extend `snapshotUserWorkspaces` to fan out `emitPendingInputRequests`; provider input-event handling + per-workspace marker commit | Both | Critical | US-55.2 |
| [US-55.4](US-55.4-tests.md) | Tests: precedence-while-busy, cross-workspace delivery, reconnect anti-entropy, e2e | Both | Critical | US-55.1–55.3 |

### Dependency Graph

```
US-55.1 (precedence) ────────────────────────────────┐
                                                     │
US-55.2 (dual-publish) ──── US-55.3 (snapshot+reconcile) ── US-55.4 (tests)
```

### Implementation Order

```
Phase 1 (frontend-only, fixes the reported symptom, no sourcing change):
  US-55.1  ← useSessionStatus + switch in Sidebar

Phase 2 (backend, additive, enables cross-workspace freshness):
  US-55.2  ← PublishToUser in emitNormalizedInputEvent (4 sites)

Phase 3 (anti-entropy + ownership correction):
  US-55.3  ← snapshotUserWorkspaces fan-out + provider onEvent input handler + per-workspace marker commit (D9)

Phase 4 (tests):
  US-55.4  ← the missing coverage that let the bug ship
```

**US-55.1 ships safely alone** and fixes the reported inconsistency for the viewed workspace. US-55.2 + US-55.3 are the systematic completion (cross-workspace visibility + reload/cold-start parity) and correct the ownership inversion. No phase regresses: US-55.3 depends on US-55.2, and US-55.2 alone (without US-55.3) degrades gracefully (provider falls back to `ChatPage`-sourced pending until reconnect seeds via snapshot).

---

## Non-Goals

- **In-chat prompt cards** — `ChatPage`'s `setPendingQuestions`/`setPendingPermissions` remain view-local. This epic concerns only the sidebar indicator's state.
- **`?` on subtask rows** — preserving the current `depth===0` UX (D6). A subtask pending shows a spinner on its own row; `?` on the parent via bubble-up. Changing this is a separate UX decision.
- **Periodic re-snapshot anti-entropy** — the reconnect cadence (every page load + every SSE drop) plus the replay buffer is the bar `busy`/`unread` ship at (D8). A polling re-snapshot is out of scope unless `busy` is later shown insufficient.
- **DB persistence of pending** — explicitly rejected (D2). The pod is the sole source of truth.
- **`publishControlEvent` abstraction** — explicitly rejected (D7). The per-sink decision is clearer inline.
- **Strict consistency / zero-staleness** — explicitly out of scope (D8).

---

## Known Limitations

1. **R3 (force-restart orphans).** A credential-reload force-restart (`secrets.go:232`) while a question is open orphans it; the provider shows a ghost `?` for a non-viewed workspace until the next user-stream reconnect (when the pod reports empty pending). The common path (session-aware restart deferral, `secrets.go:202-264`) blocks restart while sessions are busy. This is mitigated, not dissolved — and is no worse than the residual races `busy` ships with.

2. **R1/R2 (snapshot/owner races).** Bounded to tens of ms / until-watcher-catch-up respectively; self-heal on reconnect. Shared with `busy`.

3. **Pending freshness for non-viewed workspaces between reconnects.** Requires US-55.2 (dual-publish) to be live; without it, non-viewed workspace pending is only as fresh as the last reconnect.

4. **Pod-timeout clears pending (behavior change).** On snapshot timeout (pod unreachable), `emitPendingInputRequests` emits the marker via `defer` with empty staging → the provider commits empty → `pendingActions[W]` is cleared. This is defensible: if the pod is unreachable, the user cannot answer via the proxy either, and the `?` would be un-actionable. It is a behavior change from today (where the `?` persists), but an improvement: a stale `?` pointing at an unreachable pod is worse than no `?`. Self-heals on the next reconnect when the pod is reachable again.

---

## Success Criteria

1. A pending question/permission on the **viewed** workspace shows the amber `?` even while the session is busy (the reported bug is fixed).
2. A pending question/permission on a **non-viewed** workspace shows the `?` in the sidebar (cross-workspace visibility).
3. Page refresh while a question is pending on any workspace shows the `?` on reload (anti-entropy via snapshot + marker).
4. The `?` clears promptly when the question is resolved (`agent.question.resolved` delivered live).
5. No regression: existing busy spinner, unread pulse, and in-chat prompt cards behave identically.
6. Core precedence (`pending > busy`) cannot be re-broken by a future render-layer edit (it lives in `resolveSessionStatus`, consumed via `switch`).
7. The "pending while busy" case is covered by automated tests (F7 — the gap that let the bug ship).
8. No visible flicker on reconnect — per-workspace marker commit (D9), not global wipe.
9. The provider's `onEvent` handles all input event types — no silent drops (Finding 1 regression guard).
