## feat(epic-55): pending-input state as first-class session-activity facet

### Problem

The sidebar `?` indicator (pending question/permission) only inconsistently replaced the blue busy spinner. When a session had a pending input while busy, the spinner almost always shadowed the `?`.

### Root Cause

Two compounding defects:

1. **Render precedence in the view** — `Sidebar.tsx:813-821` evaluated `isBusy` before `showPending`. A session awaiting input is *by definition still busy* (no `session.status=idle` fires mid-turn), so the spinner always won. The existing tests never caught this because all cases used `status: "idle"` + `mockIsSessionBusy=false`.

2. **Event-sourcing asymmetry** — `busy`/`unread` are facets owned by `SessionActivityProvider` (cross-workspace user stream + REST seed). `pending` was sourced from the workspace-scoped stream via `ChatPage` (view-scoped), so a question on a non-viewed workspace had no path to the sidebar.

### Changes

**US-55.1 — Precedence (frontend):**
- Added `resolveSessionStatus` + `useSessionStatus` hook with total-order precedence: `pending_input > busy > unread > idle`
- Replaced `Sidebar.tsx` render cascade with status→icon `switch` — precedence cannot be re-broken by a future view edit

**US-55.2 — Dual-publish (backend):**
- Added `publishWorkspaceAndUserEvent` helper that dual-publishes to both workspace and user streams
- Added `RequestID` field to `WorkspaceSSEEvent` (D10) so the provider routes without parsing `Data`
- Replaced 6 `publishWorkspaceEvent` calls for input events

**US-55.3 — Anti-entropy + ownership (both):**
- `emitPendingInputRequests` emits `agent.input.snapshot_complete` marker via `defer` (D9) — fires unconditionally, even on timeout
- `snapshotUserWorkspaces` fans out pending fetches for Active workspaces on connect/reconnect
- Provider's `onEvent` now handles input events (was previously silent-dropping them — Finding 1)
- Per-workspace staging buffer + marker commit: clears ghost `?`s without flicker

**US-55.4 — Regression guards:**
- Forgotten-publish guard (table-driven, 4 input event types)
- JSON round-trip contract test for `request_id`/`session_id`/`workspace_id`

### Design docs

- `design/stories/epic-55-pending-input-state/README.md` — full epic with 10 design decisions (D1–D10), validated contracts, 3 residual races
- `design/stories/epic-55-pending-input-state/US-55.1–55.4-*.md` — story specs

### Robustness

Parity with `busy`'s profile: eventually consistent, bounded staleness, self-heals on reconnect. Three residual races (R1 snapshot ordering, R2 owner-lookup gap, R3 force-restart orphans) documented with self-heal bounds.

### Verification

- Frontend: 1270 tests pass, typecheck clean, lint clean
- Backend: all handler tests pass (77s), vet clean
- 16 files changed, +2028/-23

### Checklist

- [x] Tests added (28 new: 16 frontend, 12 backend)
- [x] Worklog created (`worklogs/NNNN_2026-06-24_epic-55-pending-input-state-facet.md`)
- [x] No regression in existing tests
- [ ] CI green (pending push)
