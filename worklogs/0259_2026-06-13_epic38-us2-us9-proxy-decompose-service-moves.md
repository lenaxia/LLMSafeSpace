# Worklog: US-38.2 + US-38.9 — Decompose ProxyHandler, Move Services Out of Handlers

**Date:** 2026-06-13
**Session:** Epic 38 architectural remediation, stories 38.2 (proxy decomposition) and 38.9 (service moves)
**Status:** Complete

---

## Objective

Split the 1558-line `proxy.go` into focused files within the same package (no behavior change), and relocate service types (activity tracker, event brokers, SSE tracker, workspace watcher) out of the `handlers` package into their own `services/` sub-packages. Address reviewer findings from PR #148.

---

## Work Completed

### US-38.2: Decompose ProxyHandler

Extracted methods from `proxy.go` into focused files (same `handlers` package):
- `proxy_connections.go`: connection & session tracking, password cache
- `proxy_events.go`: SSE event callbacks (onPhaseChange, onSessionIdle, onRawEvent, etc.)
- `proxy_permissions.go`: auto-approve permissions & workspace config
- `proxy_session_index.go`: session-index persistence
- `proxy_handlers.go`: CRUD HTTP handlers (CreateSession, ListSessions, etc.)
- `proxy_stream.go`: StreamEvents SSE handler
- `proxy_lifecycle.go`: Start/Stop, getters/setters
- `proxy_helpers.go`: pure utility functions

`proxy.go` retains core orchestration: structs, constructor, `proxyToWorkspace`, `doProxy`.

### US-38.9: Move Service Types Out of Handlers

Relocated service types from `handlers` into dedicated `services/` sub-packages:
- `handlers/activity.go` → `services/activity/tracker.go` (package `activity`)
- `handlers/event_broker.go` → `services/eventbroker/broker.go` (package `eventbroker`)
- `handlers/event_broker_user.go` → `services/eventbroker/user_broker.go`
- `handlers/session_tracker.go` → `services/sse/tracker.go` (package `sse`)
- `handlers/crd_watcher.go` → `services/workspace/watcher.go` (package `workspace`)

Renamed types for clarity:
- `WorkspaceWatcher` → `workspace.Watcher`
- `SSETracker` → `sse.Tracker`
- `NewWorkspaceEventBroker` → `eventbroker.NewWorkspaceEventBroker`

### Rebase onto Updated Dependencies (#146, #147)

This branch was originally based on an older main + older dependency commits. Rebuilt cleanly on top of the force-pushed dependency branches:
- Reset to `origin/refactor/epic-38-us7-us8-dead-code-dual-patterns` (PR #146 with review fixes)
- Cherry-picked US-38.11 k8s client fix + review fix from PR #147
- Cherry-picked US-38.2 and US-38.9

Resolved conflicts arising from dependency changes that post-dated the original branch:
- `proxy.go`: re-added `stripPatchParts`/`filterOutPatch`/`messageEnvelope` (removed by old US-38.7 but present in new base); added `workspace.VersionSyncCallback` reference; restored `SetVersionSyncCallback`/`SetMeteringService`/`GetWorkspaceOwner`/`GetAllKnownPhases` methods
- `proxy_events.go`: restored metering lifecycle event recording in `onPhaseChange`; un-nested `sessionIndex.RecordMessage` from `activityTracker != nil` guard in `onSessionIdle`
- `watcher.go`: merged `VersionSyncCallback` type + `onVersionSync`/`knownImageTags` fields (from base) with `WorkspaceOwnerTracker` interface + `Watcher` rename (from US-38.9)
- `version_sync_test.go`: moved from `handlers` to `workspace` package; updated mock signatures for new k8s interface (`LlmsafespaceV1` returns 2 values, `Get`/`List` take context)
- Fixed stale mock signatures in `proxy_test.go` (2-arg `Get` → 3-arg with `mock.Anything` for context)

### Review Fixes

1. **Import ordering** (`proxy_stream.go`, `stream_user_events.go`): reordered so `eventbroker` (services) precedes `apitypes` (types) alphabetically within the import group.

2. **Unexported ActivityTracker fields** (`tracker.go`): changed all exported fields to unexported — `Mu`→`mu`, `Activity`→`activity`, `LastFlush`→`lastFlush`, `K8sClient`→`k8sClient`, `Logger`→`logger`, `Namespace`→`namespace`, `StopCh`→`stopCh`, `StopOnce`→`stopOnce`, `Done`→`done`. Updated all references in `tracker.go` and `tracker_test.go`.

---

## Verification

- `go build ./...` — passes
- `go test ./api/... -timeout 120s` — all 21 packages pass
- `goimports -l` on `proxy_stream.go` and `stream_user_events.go` — no issues
