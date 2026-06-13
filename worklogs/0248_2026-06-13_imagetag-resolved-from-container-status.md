# Worklog: Fix imageTag stale display after Helm upgrade

**Date:** 2026-06-13
**Session:** Operational incident — workspace UI showed sha-c318bbcc after upgrade to ts-1781332002
**Status:** Complete — PR #138

---

## Objective

After deploying PRs #135 and #136 via `helm upgrade`, workspace cards in the UI continued
showing `image: sha-c318bbcc` instead of `ts-1781332002`. Hard-refresh did not fix it.
Root-cause three compounding bugs and fix them with TDD.

---

## Root Cause Investigation

**Observed:** DB had `sha-c318bbcc` for three workspaces; CRD `status.imageTag` had `ts-1781332002`.

**Bug 1 — Controller reads wrong pod field:**
`imageTagFromPod()` read `pod.Spec.Containers[0].Image` (the requested tag at scheduling
time). During a rolling upgrade the pod spec still carries the old tag while the new image
is being pulled. The controller wrote the old tag into `status.ImageTag` during the
`Creating→Active` transition before the pod had finished pulling the new image.

**Bug 2 — DB sync only triggered by HTTP poll:**
`SyncWorkspaceVersionInfo` was a lazy side-effect inside `GetWorkspaceStatus`. The DB only
updated when a browser tab had that workspace open (30s poll). Workspaces not currently
open kept stale values indefinitely across upgrades — the three affected workspaces
(`1d6d8407`, `beb76c43`, `0ced8d69`) had nobody visiting their ChatPage.

**Bug 3 — Unconditional UPDATE clobbers agent_version:**
Found during adversarial review of Fix 2. The watcher cannot source `agentVersion` (not in
the CRD status), so passing `""` to `SyncWorkspaceVersionInfo` would have overwritten
existing `agent_version` values on every imageTag sync via the old unconditional
`UPDATE workspaces SET image_tag=$1, agent_version=$2`.

---

## Work Completed

### Fix 1 — `controller/internal/workspace/reconciler.go`

Rewrote `imageTagFromPod` to read `ContainerStatuses[0].ImageID` (resolved after pull)
with fallback to `Spec.Containers[0].Image` only when `ImageID` carries no tag.

Extracted two helpers:
- `tagFromImageID(imageID string) string` — strips `@sha256:` digest suffix, guards
  against registry-port colons (`registry.local:5000/img`) via `lastColon > lastSlash`,
  returns `""` for digest-only and bare-sha256 formats.
- `tagFromSpecImage(image string) string` — same colon guard, returns full ref if no tag.

Handles all real-world container runtime formats:
| Format | Source | Result |
|--------|--------|--------|
| `ghcr.io/org/img:ts-123@sha256:<hex>` | docker tag+digest | `ts-123` |
| `ghcr.io/org/img:ts-123` | tag only | `ts-123` |
| `ghcr.io/org/img@sha256:<hex>` | containerd digest-only | fallback to spec |
| `sha256:<hex>` | bare digest | fallback to spec |
| `""` | not yet started | fallback to spec |
| `registry.local:5000/org/img:ts-123` | private registry with port | `ts-123` |

### Fix 2 — `api/internal/handlers/crd_watcher.go`, `proxy.go`, `app.go`

Added `VersionSyncCallback func(workspaceID, imageTag, agentVersion string)` type and
`SetVersionSyncCallback` method to `WorkspaceWatcher`.

`handleEvent` now tracks `knownImageTags` map (parallel to `knownPhases`) and fires the
callback whenever `imageTag` is non-empty and changed — regardless of whether a phase
transition occurred. This covers:
- `Creating/Resuming → Active`: controller writes `imageTag` at the same time as phase
- `Active → Active` with updated `imageTag`: future in-place image refreshes
- Any other event type where the controller updates `imageTag`

`seedResourceVersion` fires the callback for already-Active workspaces with a non-empty
`imageTag` — covers the API-restart case where no phase transition will occur.

Added `SetVersionSyncCallback` to `ProxyHandler` (stored as `versionSyncCb` field), wired
into the `WorkspaceWatcher` during `Start()`.

In `app.go`: `proxyHandler.SetVersionSyncCallback(func(...) { dbSvc.SyncWorkspaceVersionInfo(ctx, ...) })`.

### Fix 3 — `api/internal/services/database/database.go`

`SyncWorkspaceVersionInfo` now uses conditional `UPDATE` queries via a switch on which
fields are non-empty. Passing `agentVersion=""` no longer overwrites an existing value.
Three query variants: both fields, imageTag-only, agentVersion-only.

---

## Key Decisions

1. **`VersionSyncCallback` fires on imageTag change, not just Active phase** — the phase
   transition fires exactly when the controller writes `imageTag`, so they coincide in
   practice. But tracking `knownImageTags` separately makes the intent explicit and handles
   future cases where `imageTag` could change without a phase transition.

2. **Watcher calls callback synchronously** — the DB `UPDATE` is fast (~1ms) and the watch
   goroutine is not on any critical path. If the DB is unavailable the error is logged and
   swallowed; the watch loop continues. Acceptable tradeoff at current scale.

3. **`knownImageTags` seeded before first watch event** — `seedResourceVersion` populates
   `knownImageTags` alongside `knownPhases`, so the first `Modified` watch event for a
   workspace with an unchanged `imageTag` does not fire a redundant DB write.

4. **`SetVersionSyncCallback` must be called before `Start()`** — follows the same
   convention as `SetUserBroker`. Documented on the method. No lock needed: `Start()` calls
   `go w.runWatchLoop()` which establishes a happens-before edge.

---

## Adversarial Findings

| # | Finding | Severity | Fix |
|---|---------|----------|-----|
| F1 | Watcher passes `agentVersion=""` to `SyncWorkspaceVersionInfo`; old code would overwrite existing value | HIGH | Conditional UPDATE (Fix 3) |
| F2 | Missing test for registry-host-port colon in ImageID (`registry.local:5000/org/img:ts-123`) | LOW | Test added |

---

## Tests Written (TDD)

All tests written before implementation. Red confirmed before green.

**`controller/internal/workspace/image_tag_test.go`** — 12 cases:
- tag+digest, digest-only→fallback, tag-only, empty ImageID→fallback, bare digest→fallback
- no ContainerStatuses→fallback, nil pod, spec no tag, `latest`, hyphen/number tags, `sha-` prefix
- registry host with port (`registry.local:5000/org/img:ts-123`)

**`api/internal/handlers/version_sync_test.go`** — 7 cases:
- Creating→Active fires, Resuming→Active fires, empty imageTag no-op
- imageTag unchanged → no additional call (count stays at 1)
- seed fires for Active workspaces only (not Suspended, not no-tag)
- nil callback safe (no panic), Active→Active imageTag change fires

**`api/internal/services/database/sync_version_test.go`** — 5 cases:
- both fields, imageTag-only (agent_version NOT in SET clause), agentVersion-only
- both empty → no-op (no SQL executed), empty workspaceID → no-op

---

## Assumptions Stated and Validated

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | `ContainerStatuses` is populated when pod is `Running` + `allContainersReady` | Verified: Kubernetes guarantees ContainerStatuses for all running containers |
| A2 | Workspace pods are single-container | Verified: all pod builders in controller use a single workspace container |
| A3 | `tagFromImageID` `lastColon > lastSlash` guard correctly handles port in registry host | Verified: test added for `registry.local:5000/org/img:ts-123@sha256:abc` |
| A4 | `SyncWorkspaceVersionInfo` with imageTag-only does not affect agentVersion column | Verified: `sync_version_test.go` checks exact SQL — `agent_version` absent from SET |

---

## Files Modified

| File | Change |
|------|--------|
| `controller/internal/workspace/reconciler.go` | Rewrote `imageTagFromPod`; added `tagFromImageID`, `tagFromSpecImage` |
| `controller/internal/workspace/image_tag_test.go` | 12 new tests (new file) |
| `api/internal/handlers/crd_watcher.go` | Added `VersionSyncCallback` type, `knownImageTags` tracking, `SetVersionSyncCallback`, seed sync, handleEvent imageTag change detection |
| `api/internal/handlers/proxy.go` | Added `versionSyncCb` field, `SetVersionSyncCallback` method, wiring in `Start()` |
| `api/internal/handlers/version_sync_test.go` | 7 new tests (new file) |
| `api/internal/app/app.go` | Wire `SetVersionSyncCallback` to `dbSvc.SyncWorkspaceVersionInfo` |
| `api/internal/services/database/database.go` | Conditional UPDATE in `SyncWorkspaceVersionInfo` |
| `api/internal/services/database/sync_version_test.go` | 5 new tests (new file) |

---

## Blockers

None.

---

## Next Steps

None — all bugs fixed and tested. The three workspaces with stale `sha-c318bbcc` were
patched directly in the DB during the incident (`UPDATE workspaces SET image_tag = 'ts-1781332002'`).
Future upgrades will not require manual DB patching.
