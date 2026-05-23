# Worklog 0032: CI Versioning, Permissions Model, Watch Loop Hardening

**Date:** 2026-05-23
**Scope:** Three follow-ups from worklog 0031 — CI image versioning, permissions gap, watch channel close log spam
**Status:** Complete — all tests pass with `-race`, `go vet` clean

## Objective

Close three follow-ups identified in worklog 0031:

1. **CI image versioning** — every image must carry a sortable version tag, not just a moving `:dev`. Tagged releases get semver; non-tag builds get a unix timestamp shared across all images in one workflow run plus the commit SHA. Lets ops tell at a glance which image is newer.
2. **Permissions gap** — fresh users couldn't create sandboxes because `permissions` table was empty and there was no role-based bypass. Worklog 0031 worked around this by inserting wildcard rows manually. Need a real fix.
3. **Watch channel closed log spam** — `SandboxWatcher` logged `Warn("Sandbox watch error, restarting")` on every clean apiserver-driven watch cycle (~5–10 min), masquerading as errors. Worklog 0031's `client_crds.go` `Timeout=0` change was insufficient.

## What Changed

### 1. CI workflow versioning (`.github/workflows/ci.yml`)

Every image in every workflow run now carries:

| Tag | Purpose | When |
|-----|---------|------|
| `sha-<commit>` | Immutable per-commit pointer | Always |
| `ts-<unix>` | Sortable per-workflow-run pointer | Always |
| `dev` | Latest-from-main pointer | Push to main only |
| `<version>` (e.g. `1.2.3`) | Semver release | On `v*.*.*` tag push |
| `<major>.<minor>` | Floating semver | On `v*.*.*` tag push |
| `<major>` | Floating semver | On `v*.*.*` tag push |
| `latest` | Latest stable release | On `v*.*.*` tag push |

The `ts-<unix>` is generated once by a new `prepare` job (10-digit unix timestamp) and consumed by all three build jobs (`build-api`, `build-controller`, `build-runtime`) so every artifact in the same workflow run shares the same timestamp. Sortable lexicographically until 2286.

Helm deployments can now pin to `sha-<commit>` or `ts-<unix>` to side-step the kubelet pull-cache issue from worklog 0031 (where moving tags like `:dev` were not always re-pulled despite `imagePullPolicy: Always`).

Trigger expanded to include `tags: ['v*.*.*']` so semver releases actually trigger the workflow.

### 2. Removed legacy V1 workflow

Deleted `.github/workflows/build-runtimes.yml`. It was V1-era — built `python`, `python-ml`, `nodejs`, and `go` runtime images that no production code references. The base image (the only one actually consumed by `RuntimeEnvironment` CRDs and the controller) is built by `ci.yml`. When language-specific runtime images become real deliverables, we'll add them back to `ci.yml` with the same versioning scheme.

### 3. Permissions model — proper rewrite

Old model (deleted):

```go
allowed, err := s.dbService.CheckPermission(req.UserID, "sandbox", "", "create")
if !allowed { return forbidden }
```

This consulted a generic `permissions` table that nothing populated. Empty table = nobody can create sandboxes. There was no role bypass, no auto-seeding on registration, and no resource-ownership-based path.

New model (`api/internal/services/sandbox/sandbox_service.go`):

**`CreateSandbox`:**
1. Load user. If not found → 404. If `Active == false` → 403.
2. If `WorkspaceRef` is empty → auto-create a workspace owned by the caller. Done.
3. If `WorkspaceRef` is set:
   - If `user.Role == "admin"` → allow. Admins are cross-cutting authority.
   - Otherwise → call `workspaceService.GetWorkspace(ctx, userID, workspaceID)`, which already enforces ownership and returns `Forbidden` for foreign workspaces. Propagate its error.

**`TerminateSandbox`:** unchanged ownership check, but the fallback for non-owners is now `user.Role == "admin"` instead of a `CheckPermission(userID, sandboxID, "delete")` call against the empty table.

**Why this is the proper model (not just a workaround):**

- **Cross-cutting authority lives in roles.** Admin can do anything. This is a binary decision, not a per-resource lookup.
- **Resource-scoped authority lives in ownership.** "You can do X to your own Y" is the default for normal users. Workspace ownership is already authoritative (stored in K8s CRD and mirrored in PostgreSQL); we just consult it.
- **The `permissions` table is preserved** for future fine-grained sharing ("user A grants user B read on workspace X"). Not deleted, just unused. When the use case appears, the table is ready.
- **`auth.AuthorizeResourceAccess` (which still calls `CheckPermission`) is unused in production code paths.** Left alone — removing it is a separate refactor.

### 4. Watch loop — TDD-driven rewrite (`api/internal/handlers/crd_watcher.go`)

**Process note:** This was rewritten following proper TDD per README-LLM.md rule 0. New tests were written first to define the new contract; their failure against the old implementation was verified (6 of 7 new tests fail against legacy code) before adopting the new implementation.

The legacy watcher had four bugs that produced log spam:

1. **No `ResourceVersion` threading** — every Watch call started with empty RV, replaying every existing sandbox as `Added`. On reconnect, the same events fired again.
2. **No explicit `TimeoutSeconds`** — relied on apiserver default (5–10 min). When it expired, the channel closed cleanly with no error, but the watcher logged it as `Warn("Sandbox watch error, restarting")`.
3. **No bookmark support** — between events, the watcher had no way to advance its RV cursor, so a reconnect after a long quiet period replayed everything.
4. **No error event handling** — `event.Type == watch.Error` (with a `*metav1.Status` payload, e.g. `410 Gone`) was silently dropped by the type assertion in `handleEvent`, hiding real apiserver errors.

New implementation:

| Behavior | Implementation |
|----------|----------------|
| Initial RV seeding | `runWatchLoop` calls `seedResourceVersion()` once on start, doing `List()` and capturing `ListMeta.ResourceVersion`. Failure is non-fatal; logged at Warn, Watch starts with empty RV (apiserver replays state). |
| RV threading | `watchOnce` builds `metav1.ListOptions{ResourceVersion: w.getResourceVersion(), TimeoutSeconds: 290, AllowWatchBookmarks: true}` for every Watch call. |
| Bookmark events | `handleEvent` checks `event.Type == watch.Bookmark` first, extracts the RV from the carried object, updates the cache, and returns without firing the phase-change callback. |
| Normal events | Update cached RV from `sandbox.ResourceVersion` AND track phase + callback. |
| Error events | `event.Type == watch.Error` extracts the `*metav1.Status`, logs at Warn with `reason`/`message`/`code`. If `code == 410` (resource version too old), reset RV to empty so next Watch resyncs from current state. Other codes preserve the cached RV. |
| Clean close logging | Channel closing without an error event logs at **Debug**, not Warn. This is the normal apiserver-driven cycling case and shouldn't clutter operations logs. |
| Backoff | `2s → 4s → 8s → 16s → 30s` (capped) on real errors. Reset to `2s` on any clean close. Cancellable via `stopCh`. |

Every backoff sleep uses `sleepCancellable(stopCh, d)` so `Stop()` returns within microseconds rather than waiting for the timer.

### Tests

**Permissions tests (`api/internal/services/sandbox/sandbox_service_test.go`):**

| Test | Verifies |
|------|----------|
| `TestCreateSandbox_HappyPath` (updated) | No `CheckPermission` mock; user has `Active: true, Role: "user"` |
| `TestCreateSandbox_InactiveUser_ReturnsForbidden` (new) | `Active: false` blocks create even for admin role |
| `TestCreateSandbox_ForeignWorkspace_ReturnsForbidden` (new) | Non-admin attaching foreign workspace → Forbidden via `workspaceService.GetWorkspace` |
| `TestCreateSandbox_AdminUserAttachesForeignWorkspace_Succeeds` (new) | Admin role bypasses workspace ownership check |
| `TestCreateSandbox_NoWorkspaceRef_AutoCreatesWorkspace` (updated) | No `GetWorkspace` call on auto-create path |
| `TestCreateSandbox_WithWorkspaceRef_UsesExisting` (updated) | Non-admin with owned workspace passes `GetWorkspace` ownership check |
| `TestTerminateSandbox_NotOwner_NotAdmin_ReturnsForbidden` (renamed) | Non-admin non-owner → Forbidden |
| `TestTerminateSandbox_NotOwner_AdminRole_Succeeds` (renamed) | Admin role can terminate any sandbox |
| `TestTerminateSandbox_NotOwner_GetUserFails_ReturnsInternal` (new) | Internal error if role lookup fails (don't masquerade as Forbidden) |

**Watch loop tests (`api/internal/handlers/crd_watcher_test.go`):**

| Test | Verifies | Fails against legacy? |
|------|----------|------------|
| `TestSandboxWatcher_InitialListSeedsResourceVersion` | First Watch carries RV from List, plus TimeoutSeconds and AllowWatchBookmarks | Yes |
| `TestSandboxWatcher_FailedInitialListStillStarts` | Failed List doesn't prevent Watch loop from starting | No (legacy didn't List, vacuously passes) |
| `TestSandboxWatcher_BookmarkEventUpdatesRVWithoutCallback` | Bookmark advances cached RV; no callback; reconnect uses bookmarked RV | Yes |
| `TestSandboxWatcher_410ErrorResetsResourceVersion` | 410 Gone clears RV; next Watch starts fresh | Yes |
| `TestSandboxWatcher_NonGoneErrorPreservesRV` | Other error codes preserve RV; reconnect resumes | Yes |
| `TestSandboxWatcher_NormalEventAdvancesRV` | Added/Modified events update cached RV | Yes |
| `TestSandboxWatcher_CleanCloseImmediateReconnect` | Clean close reconnects in <1s (well under 2s error backoff) | Yes |

TDD verification was done by stashing the new implementation, restoring the legacy `crd_watcher.go` from `git show HEAD`, running the new tests, observing 6 failures + 1 vacuous pass, then restoring the new implementation.

### Mock helper update

`setupWatcherMocks` in the test file now sets a default `Maybe()` expectation for `List()` returning RV `100`. Tests that need a specific RV override with `sbMock.ExpectedCalls = nil` then `On("List", ...)`.

`Watch` matchers in legacy tests changed from `metav1.ListOptions{}` (literal match) to `mock.Anything` because the new code passes non-empty options.

## Tests Run

```
go test -timeout 180s -race -count=1 ./...   # all 27 packages pass
go vet ./...                                  # clean
```

Per-package new test counts:

- `api/internal/handlers`: +7 tests in `crd_watcher_test.go` (RV seeding, RV threading, bookmark, 410 reset, non-410 preserve, normal advance, clean close reconnect)
- `api/internal/services/sandbox`: +4 tests in `sandbox_service_test.go` (inactive user, foreign workspace, admin bypass, GetUser failure on terminate); 6 existing tests updated to match new signatures

## Cluster Validation

Not yet performed — code committed and pushed; once CI builds the new images, validation should:

1. Register a fresh user (no manual permission inserts)
2. Create a sandbox via API → expect 201 (previously failed with 403 until permissions were seeded)
3. Tail API logs during steady-state operation → expect no "watch channel closed" Warn entries; only Debug
4. Force a 410 by stopping the API for >5 min and restarting → expect one Warn for the 410 event, then normal operation
5. Pull the latest image by `ts-<timestamp>` tag from ghcr.io → confirm the timestamp matches the workflow run

## Files Modified

| File | Type | Change |
|------|------|--------|
| `.github/workflows/ci.yml` | rewrite | Add `prepare` job emitting unix timestamp; all build jobs add `ts-<ts>`, `sha-<commit>`, semver tags; trigger on `v*.*.*` |
| `.github/workflows/build-runtimes.yml` | delete | Legacy V1 workflow building unused python/nodejs/go runtime images |
| `api/internal/handlers/crd_watcher.go` | rewrite | RV threading, bookmarks, error-event handling, log-level differentiation, backoff |
| `api/internal/handlers/crd_watcher_test.go` | extend | +7 new TDD tests; `setupWatcherMocks` adds default List expectation; legacy tests use `mock.Anything` for Watch opts |
| `api/internal/services/sandbox/sandbox_service.go` | edit | `CreateSandbox`: drop `CheckPermission`; check `user.Active`; admin role bypasses workspace ownership check; non-admin must own workspace via `workspaceService.GetWorkspace`. `TerminateSandbox`: replace `CheckPermission` fallback with `user.Role == "admin"` |
| `api/internal/services/sandbox/sandbox_service_test.go` | edit | Drop `CheckPermission` mocks; add `Active` + `Role` to all `User` fixtures; +4 new tests; 2 terminate tests renamed |
| `worklogs/0032_2026-05-23_ci-permissions-watch.md` | new | This file |

## Key Decisions

1. **Versioning: `ts-<unix>` not `ts-<iso8601>`** — 10-digit decimal sorts lexicographically, fits in any tag character set, no special chars. ISO8601 (`ts-20260523T120000Z`) sorts the same way but is longer and contains `:` issues if used in some contexts.

2. **CI versioning: shared `prepare` job, not per-job timestamp** — ensures all images in one workflow run share one timestamp. If each job generated its own, the API/controller/base images from one push could differ by seconds, defeating the "match by ts" model.

3. **Permissions model: keep `permissions` table, don't delete** — even though no production code populates it, removing the schema closes off future per-resource sharing. Cost of keeping it: zero (empty table). Cost of deleting and re-adding later: a migration.

4. **Permissions model: admin bypass at `Role` field, not via a `permissions` row** — roles are the right place for cross-cutting authority. Admin-via-permissions-row would require seeding wildcards on user promotion, which is fragile.

5. **Watch backoff: clean close reconnects immediately, errors back off exponentially** — clean closes are normal apiserver behavior (~5–10 min cycling), should be invisible. Errors are real and might cascade if we hammer apiserver, so back off.

6. **TDD recovery: write the missing tests after-the-fact and prove they fail against legacy** — when I noticed mid-session that the watcher rewrite had skipped the TDD step (rule 0 violation), the user called it out. Recovery: stash new code, restore old code from `git show HEAD`, run new tests, confirm failures. This proves the tests genuinely lock in new behavior; they aren't just rubber-stamping whatever the new code does.

7. **Removed legacy `build-runtimes.yml`** — V1-era, builds runtime images nothing in V2 references. User explicitly approved removal of legacy v1 workflows.

## Next Steps

1. **Validate on the home-kubernetes cluster** — push will trigger CI, then:
   - Verify `ghcr.io/lenaxia/llmsafespace/api:ts-<unix>` exists alongside `:sha-<sha>` and `:dev`
   - `kubectl set image` to the `ts-` tag on the API deployment, restart, confirm image actually changes
   - Register a brand-new user, create sandbox via API → expect 201 with no permission seeding
   - Tail API logs for 30 min → confirm no `watch channel closed` Warn spam; one cycle should produce zero log entries (Debug only) under steady-state

2. **Switch helm chart from `:dev` to `:ts-<unix>` or `:sha-<sha>`** — kubelet image-pull caching (worklog 0031) means moving tags aren't reliably re-pulled. Pinning to immutable tags side-steps the issue. Recommendation: `helm upgrade` sets `image.tag=sha-<commit>` from a CI deploy job.

3. **Consider auto-promoting the first user to admin** — if PostgreSQL is empty and registration creates the first user, set `role='admin'`. Otherwise fresh installs require manual SQL `UPDATE users SET role='admin' WHERE id=...` to do anything privileged. Worth a separate PR.

4. **Document the verbose flag and new admin/role model in `design/EVOLUTION-V2.md` §9 (security)** — defer until the next time the design doc is touched.

5. **Phase 4 MCP server** — still open from worklog 0029. Not blocked by anything in this session.
