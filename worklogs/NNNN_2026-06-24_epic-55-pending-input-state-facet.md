# Worklog: Epic 55 — Pending-Input State as a First-Class Session-Activity Facet

**Date:** 2026-06-24
**Session:** Designed, documented, and implemented Epic 55: fixing the sidebar `?` indicator (pending question/permission) inconsistently replacing the blue busy spinner, making pending a first-class session-activity facet on parity with busy/unread.
**Status:** Complete

---

## Objective

The sidebar's amber `?` (pending input indicator) only inconsistently replaced the blue busy spinner. Diagnose the root cause, design the correct long-term architecture, document as an epic, and implement.

---

## Work Completed

### Investigation and Root Cause (spike)

Identified two compounding defects:

1. **Render precedence in the view** — `Sidebar.tsx:813-821` evaluated `isBusy` before `showPending`. A session awaiting input is *by definition still busy* (no `session.status=idle` fires mid-turn), so the spinner always shadowed the `?`. The existing tests (`Sidebar.test.tsx:413-463`) never caught this because all cases used `status: "idle"` + `mockIsSessionBusy=false`.

2. **Event-sourcing asymmetry** — `busy`/`unread` are owned by `SessionActivityProvider` (cross-workspace user stream + REST seed). `pending` was owned by `ChatPage` (workspace-scoped stream only), making it structurally view-scoped: a question on a non-viewed workspace had no path to the sidebar. Backend: `session.status` and `agent_died` dual-publish to both streams; `agent.question`/`agent.permission`/`*.resolved` published to workspace stream only (`emitNormalizedInputEvent`).

### Design Iterations (stress-tested)

Multiple design iterations were stress-tested adversarially:
- Initial DB-persistence proposal rejected — no anti-entropy mechanism; the pod restarts with empty pending (in-memory), so a DB column would drift indefinitely (ghost `?`).
- Pod-authoritative model selected: the pod is the sole source of truth via `QuestionListPath`/`PermissionListPath`. The existing `emitPendingInputRequests` primitive is the anti-entropy; extending `snapshotUserWorkspaces` to fan it out across the user's Active workspaces on connect is the reconciliation.
- Per-workspace marker commit (D9) designed to prevent global wipe flicker: `emitPendingInputRequests` emits `agent.input.snapshot_complete` via `defer`; provider stages events per-workspace and commits atomically on the marker.
- `RequestID` field (D10) added to `WorkspaceSSEEvent` so the provider routes without parsing `Data`.
- Robustness bar set at parity with `busy`: eventually consistent, bounded staleness, self-heals on reconnect. Three residual races (R1 snapshot/owner, R2 owner gap, R3 force-restart orphans) documented with self-heal bounds.

### Epic Documentation (design/stories/epic-55-pending-input-state/)

Created complete epic documentation following the Epic 16/37 format:
- `README.md` — problem, spike output (validated contracts with evidence), 10 design decisions (D1–D10), architecture/data-flow diagrams, 3 residual races, success criteria
- `US-55.1-precedence-status-function.md` — `useSessionStatus` + `pending > busy` precedence
- `US-55.2-dual-publish-input-events.md` — input events to user stream + D10 RequestID
- `US-55.3-snapshot-pending-anti-entropy.md` — snapshot fan-out + marker commit + provider onEvent handling
- `US-55.4-tests.md` — 56-case test plan

### US-55.1: Precedence Status Function (frontend-only)

- Added `SessionDisplayStatus` type (`pending_input | busy | unread | idle`) and `resolveSessionStatus` pure function with total-order precedence: `pending_input > busy > unread > idle`.
- Added `useSessionStatus` hook combining `useIsSessionBusy`, `useIsSessionUnread`, `useIsSessionPendingAction`.
- Replaced `Sidebar.tsx` `SessionTreeRow` render cascade with status→icon `switch`. Core precedence lives in the state layer; the view cannot silently re-shadow.
- Depth gate (D6) preserved as explicit view-layer concern: subtask rows render `busy` even when pending; parent shows `?` via `pendingIndicatorIds` bubble-up.

### US-55.2: Dual-Publish Input Events (backend)

- Added `RequestID string` field to `WorkspaceSSEEvent` (`sse_event.go`) — D10.
- Added `publishWorkspaceAndUserEvent` helper to `proxy_lifecycle.go` — publishes to both workspace stream (active-view) and user stream (cross-workspace, replay-buffered). User-stream copy carries `WorkspaceID`.
- Replaced 6 `publishWorkspaceEvent` calls across `emitNormalizedInputEvent` (4 sites) and `emitPendingInputRequests` (2 sites) with `publishWorkspaceAndUserEvent`.
- Set `SessionID` + `RequestID` top-level on all 4 input event types.

### US-55.3: Snapshot Anti-Entropy + Provider Reconcile (both)

**Backend:**
- Added `defer` marker emission (`agent.input.snapshot_complete`) to `emitPendingInputRequests` — fires unconditionally, even on timeout/error (D9). Published via `PublishToUser` directly (user stream only — `ChatPage` ignores unknown types).
- Extended `snapshotUserWorkspaces` (`stream_user_events.go`) to fan out `emitPendingInputRequests` for each Active workspace on connect/reconnect. Bounded by ≤10 active workspaces (scale constraint).

**Frontend (Finding 1 fix):**
- Added input-event handling to provider's `onEvent` for `agent.question`/`agent.permission`/`*.resolved` — the core wiring that makes the provider the authoritative owner (was previously missing entirely; events were silently dropped).
- Implemented D9 staging buffer + marker commit: `stagingRef` (per-workspace `Map<requestId, sessionId>`) + `committedWsRef` (workspaces whose marker arrived). Events for uncommitted workspaces are staged + applied optimistically; marker commit authoritatively replaces the workspace's pending set (clears ghosts, preserves live entries, no flicker).
- Modified `onReconnect` to NOT wipe `pendingActions` (D9 — no flicker). Resets staging state instead.

### US-55.4: Regression Guards

- Forgotten-publish guard: table-driven test enumerating all 4 input event types, asserting each reaches `PublishToUser` with `WorkspaceID`, `SessionID`, `RequestID`. Fails with a descriptive message if a future event type is workspace-only.
- JSON round-trip contract test: verifies `request_id`, `session_id`, `workspace_id` survive JSON marshaling with the keys the frontend expects.

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| **D1: `pending_input > busy` precedence in state layer** | A waiting agent is busy *because* it is pending; pending is actionable and wins. Lives in `resolveSessionStatus`, not the view's render cascade. |
| **D2: Pod-authoritative (no DB persistence)** | The pod is the live source of truth; a DB column would be a second source with no anti-entropy. `context_used`/`hasUnread` analogies are false (those have no live authority). |
| **D3: REST-authoritative within pod-authoritative frame** | Like `unread`, not `busy`. Pending is long-lived and resolves by external action whose event may be missed. Snapshot is the correctness backstop; dual-publish is freshness. |
| **D9: Per-workspace marker commit (no global wipe)** | `busy` can wipe globally (rebuilds synchronously from cache). Pending rebuilds via N async pod fetches — global wipe would flicker. Marker stages + commits per-workspace atomically. |
| **D10: Top-level `RequestID` on `WorkspaceSSEEvent`** | Provider needs `request_id` without parsing `Data` (avoids coupling state layer to agent event shapes). 1-line struct addition; makes control events uniform. |
| **Trim enum to 4 members (Rule 4)** | `dead`/`errored` arms deferred — no workspace-phase hook exists. 4 reachable states form a complete total order. |
| **Marker published via `PublishToUser` only** | Marker is a provider signal, not an input event. `ChatPage` ignores unknown types, so workspace-stream delivery is unnecessary noise. |
| **Staging buffer as `Map<requestId, sessionId>`** | `Set<{sessionId, requestId}>` fails on `delete` (reference equality). Map keyed by requestId gives O(1) add/remove. |

---

## Blockers

None.

---

## Tests Run

| Command | Outcome |
|---------|---------|
| `npx vitest run` (frontend) | 1270 tests pass (0 regressions) |
| `npx tsc --noEmit` (frontend typecheck) | Clean |
| `npm run lint` (frontend eslint) | Clean |
| `go test -timeout 120s -race ./api/internal/handlers/` | All pass (77s) |
| `go vet ./api/internal/handlers/ ./api/internal/types/` | Clean |
| `go build ./...` | Clean |

---

## Next Steps

1. **Commit and open a PR** — all three stories (US-55.1/55.2/55.3) + regression guards are ready. Branch prefix: `fix/` or `feat/` (e.g. `fix/sidebar-pending-input-precedence`).
2. **Update the epic docs** if the PR review surfaces findings — the design docs at `design/stories/epic-55-pending-input-state/` should reflect the as-built implementation.
3. **Integration test for cross-workspace delivery** — the unit tests verify each side (backend publish, frontend consume). A full integration test (backend SSE → provider → sidebar) through the existing `ChatPage.input.test.tsx` patterns would close the wiring verification, but the risk is low given the JSON round-trip contract test and the forgotten-publish guard.
4. **Consider depth-gate UX change separately** — D6 preserves the current `depth===0` behavior (subtask pending shows spinner, not `?`). If the product wants `?` on subtask rows, that's a separate UX decision, not an architecture change.

---

## Files Modified

### Design docs (created)
- `design/stories/epic-55-pending-input-state/README.md`
- `design/stories/epic-55-pending-input-state/US-55.1-precedence-status-function.md`
- `design/stories/epic-55-pending-input-state/US-55.2-dual-publish-input-events.md`
- `design/stories/epic-55-pending-input-state/US-55.3-snapshot-pending-anti-entropy.md`
- `design/stories/epic-55-pending-input-state/US-55.4-tests.md`

### Frontend (modified)
- `frontend/src/providers/SessionActivityProvider.tsx` — `resolveSessionStatus`, `useSessionStatus`, input-event handling in `onEvent`, D9 staging buffer + marker commit, `onReconnect` modification
- `frontend/src/providers/SessionActivityProvider.test.tsx` — 16 new tests (7 precedence, 9 input handling + staging/commit)
- `frontend/src/components/layout/Sidebar.tsx` — replaced render cascade with `useSessionStatus` + status→icon mapping
- `frontend/src/components/layout/Sidebar.test.tsx` — F7 test (pending while busy), mock update for `useSessionStatus`

### Backend (modified)
- `api/internal/types/sse_event.go` — `RequestID string` field (D10)
- `api/internal/handlers/proxy_lifecycle.go` — `publishWorkspaceAndUserEvent` helper
- `api/internal/handlers/proxy_events.go` — 4 `publishWorkspaceEvent` → `publishWorkspaceAndUserEvent` + `SessionID`/`RequestID` top-level
- `api/internal/handlers/proxy_input.go` — 2 `publishWorkspaceEvent` → `publishWorkspaceAndUserEvent` + `SessionID`/`RequestID` + `defer` marker emission (D9)
- `api/internal/handlers/stream_user_events.go` — `snapshotUserWorkspaces` pending fan-out for Active workspaces
- `api/internal/handlers/proxy_input_test.go` — 12 new tests (7 dual-publish, 3 marker/fan-out, 2 regression guards)
