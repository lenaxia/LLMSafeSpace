# Worklog: Refresh Compute — resync workspace CRD with current platform defaults

**Date:** 2026-06-29
**Session:** Added a "Refresh Compute" action for workspaces across the API, MCP server, client SDKs, and frontend, so a long-lived workspace can pick up current platform defaults (resources, security level, storage class, max active sessions) and the latest runtime image version.
**Status:** Complete

---

## Objective

Provide a user-triggerable way to "refresh compute" for a workspace. A workspace CRD that has lived a long time can drift from the platform's current config — its image version may be stale, or its CPU/memory requests may no longer match the platform default. Refresh resyncs the CRD with the current defaults and rebuilds the pod. It must be reachable as: a REST endpoint, an SDK method (Go/TypeScript/Python), an MCP tool, and a frontend action (left-nav workspace kebab).

---

## Assumptions (stated + validated)

1. **The runtime image is resolved by the controller at pod-build time from `spec.runtime` via `resolveRuntimeImage`** (RuntimeEnvironment CR lookup), NOT stored as a literal image on the Workspace CRD. → Validated in `controller/internal/workspace/runtime_resolver.go:26` and `phase_active.go:66-83`. **Consequence:** bumping `spec.restartGeneration` is sufficient to make the rebuilt pod pick up a newly-published image version; refresh must NOT rewrite `spec.runtime` (that would silently change the user's chosen language). Refresh re-applies *defaults*, not the user's runtime choice.
2. **A `restartGeneration` bump triggers a full pod rebuild (delete → Creating) in all non-terminal phases**, and the rebuilt pod is constructed from the current `spec`. → Validated: `phase_active.go:66` (Active deletes pod → Creating), `phase_creating.go:35` (Creating clears backoff), `recovery.go:27` (Failed → Pending). So writing refreshed `spec.Resources` in the same Update as the generation bump guarantees the new pod uses the new resource requests.
3. **`applyWorkspaceDefaults` only fills empty fields** (idempotent, preserves explicit values) — unsuitable for refresh, which must *overwrite*. → Validated at `workspace_service.go:931-1005`. Added a separate `reapplyComputeDefaults` with overwrite semantics.
4. **Instance settings that are strings have a clean "configured" signal (non-empty); `GetInt` keys use 0 = not configured (schema default 0).** Only these are safe to force-overwrite. Booleans (auto-suspend, network ingress) cannot distinguish "configured false" from "not configured" (schema default false), so refresh deliberately does NOT touch auto-suspend/TTL/network to avoid clobbering a deliberate user value with a schema default. → Validated in `pkg/settings/registry.go:36-48`.
5. **The TS SDK treated HTTP 202 the same as 204 (discard body)** (`client.ts:127`), which would discard the refresh response body. → Validated. 204 means no body by definition; 202 *may* carry a payload (RFC 7231 §6.3.3). Fixed to read the body and return `undefined` only when it is actually empty (preserving the `void` contract for suspend/restart).

---

## Work Completed

### Backend — API service (`api/internal/services/workspace/`)
- New `RefreshWorkspaceCompute(ctx, userID, workspaceID) (*types.RefreshWorkspaceResult, error)` (`workspace_service.go`): verifies owner, rejects Terminating/Terminated (conflict, same as restart), re-applies compute defaults, bumps `spec.restartGeneration`, writes via `Update` (never touches status — the controller owns status). Idempotent at the spec layer.
- New `reapplyComputeDefaults` helper: overwrites `spec.Resources.{CPU,Memory}`, `spec.SecurityLevel`, `spec.Storage.StorageClassName`, `spec.MaxActiveSessions` to current instance settings, but only when the platform default is explicitly configured (non-empty / > 0).
- New `RefreshWorkspaceResult{ RestartGeneration int64 }` in `pkg/types/workspace.go`.
- TDD: `workspace_refresh_test.go` — happy path (resources/security/storage/maxActive overwritten + generation bumped), nil-resources initialized, no-settings (generation bump only, user values preserved), suspended (allowed), wrong owner (forbidden), terminal phases (rejected), K8s get/update failures.

### Backend — wiring
- Added `RefreshWorkspaceCompute` to the `WorkspaceService` interface (`api/internal/interfaces/interfaces.go`) + the mock (`api/internal/mocks/workspace.go`).
- Registered `POST /api/v1/workspaces/:id/refresh-compute` in the router (`api/internal/server/router.go`) → 202 Accepted + JSON body. Session-ID context propagation matches restart/activate.
- Router e2e: added the route to the table-driven existence/auth tests + dedicated success (202 + `restartGeneration`) and error-propagation tests (`router_workspace_test.go`).

### MCP server (`pkg/mcp/`)
- New `workspace_refresh_compute` tool (`server.go`) + handler returning a human-readable result including the bumped generation.
- Added `RefreshWorkspace` to the `APIClient` interface + `HTTPClient` impl (`client.go`) hitting `/api/v1/workspaces/{id}/refresh-compute`, plus `RefreshWorkspaceResp`.
- Tests: handler happy-path / missing-id / API-error (`server_test.go`); HTTP client happy-path / API-error (`client_test.go`); integration tool-count bumped 14→15 (`integration_test.go`).

### SDKs
- **openapi.yaml**: new `/workspaces/{id}/refresh-compute` (operationId `refreshWorkspaceCompute`) + `RefreshWorkspaceResult` schema.
- **Go SDK**: `WorkspacesService.RefreshCompute` + `RefreshWorkspaceResult` type + client test.
- **TypeScript SDK**: `workspaces.refreshCompute` + `RefreshWorkspaceResult` type; fixed 202 body handling so the response parses; added a client test.
- **Python SDK**: `refresh_compute` on both sync (`client.py`) and async (`async_client.py`) clients.
- **Canary MCP** (`sdks/canary/mcp/main.go`): fixed pre-existing stale tool count (11 → reality 15) and added `workspace_refresh_compute` + the 3 question/permission tools to the expected list.

### Frontend
- `workspacesApi.refreshCompute` (`api/workspaces.ts`).
- Left-nav workspace kebab item "Refresh compute" (`Sidebar.tsx`) with a `refreshComputeMutation` that invalidates workspace/status queries; shows "Refreshing compute…" + disables while pending. Wired via new `onRefreshCompute`/`refreshingCompute` props on `WorkspaceGroup`.
- Test: asserts `workspacesApi.refreshCompute("ws-1")` is called when the kebab item is clicked (`Sidebar.test.tsx`).

---

## Key Decisions

- **Refresh reuses `restartGeneration` (no new CRD field).** A dedicated `refreshGeneration` would require a CRD schema change, deepcopy regen, webhook, and controller handling for no functional gain — the controller already rebuilds the pod on a restartGeneration bump, and the refreshed spec is written atomically in the same Update. Simplest correct design.
- **Refresh does NOT change `spec.runtime`.** The image *version* drift is fixed by the pod rebuild (controller re-resolves `spec.runtime` → RuntimeEnvironment image). Changing `spec.runtime` would silently swap a user's chosen language, which is not "refresh" — it's "recreate". Out of scope and unsafe.
- **Overwrite scope limited to clean-signal settings.** Only fields whose "configured" state is unambiguous (non-empty string / >0 int) are force-overwritten. Auto-suspend, TTL, and network access are deliberately left alone to avoid replacing deliberate user values with schema defaults when the platform has no opinion.
- **202 Accepted for the endpoint** (async semantics: "refresh initiated, pod rebuilding") with an informative JSON body, consistent with the declarative nature of the controller-driven rebuild.

---

## Blockers

None.

---

## Pre-existing failures found (NOT caused by this change — surfaced, not silently dismissed)

1. **`api/internal/app` test build break** (`secrets_wiring_test.go:308`): `NewPodBootstrapHandlerFromClientset` gained new params (`bootstrapInjector, bootstrapWorkspaceLookup, *prompt.Service`) in commit `7b615aff` (agent-customization feature) but this unrelated test was not updated. The non-test `app` package builds fine; my router change is unaffected. Out of scope for refresh-compute (it is the agent-customization feature's incomplete test wiring).
2. **`pkg/repolint TestLive_Migrations_NoCollisionsOrGaps`**: reports a phantom gap (versions 2–45 "missing", max 47) — a pre-existing false-positive in the migration-sequence checker, unrelated to this work (I added no migrations).

Both were verified as independent of refresh-compute (my changes touch none of those files). Flagging for a follow-up rather than fixing inline to avoid expanding scope into an unrelated subsystem.

---

## Tests Run

- `go test -race ./api/internal/services/workspace/` — PASS (incl. 8 new RefreshWorkspaceCompute tests)
- `go test -race ./api/internal/server/` — PASS (incl. new refresh-compute route tests)
- `go test -race ./pkg/mcp/` — PASS (incl. new tool/client/integration tests)
- `go build ./...` — PASS (full root module)
- `go vet` + `gofmt -l` on all changed Go files — clean
- Go SDK (`sdks/go`): `go build ./... && go test ./...` — PASS
- Canary (`sdks/canary/mcp`): `go build . && go vet .` — PASS
- TS SDK (`sdks/typescript`): `tsc --noEmit` clean; `vitest run tests/client.test.ts` — PASS (13 tests, incl. new refreshCompute)
- Frontend: `tsc --noEmit` clean; `eslint` on changed files clean; `vitest run` (api + layout) — 168 PASS (incl. new Sidebar refresh test); `contract.test.ts` — 9 PASS
- `go test -short ./...` (root) — 63 packages PASS; only the two pre-existing unrelated failures above.

---

## Next Steps

- Follow up on the two pre-existing failures (agent-customization `secrets_wiring_test.go` signature drift; repolint migration-gap false positive) as a separate change.
- Consider a `restart` entry in `openapi.yaml` (it exists in all SDKs but is missing from the spec — noticed while adding refresh-compute; left out of scope here).
- Optional: surface "image updated to X" in the refresh result once the controller reports the new `status.imageTag` after rebuild (currently the response carries only the generation).

---

## Files Modified

- `pkg/types/workspace.go`
- `api/internal/interfaces/interfaces.go`
- `api/internal/mocks/workspace.go`
- `api/internal/services/workspace/workspace_service.go`
- `api/internal/services/workspace/workspace_refresh_test.go` (new)
- `api/internal/server/router.go`
- `api/internal/server/router_workspace_test.go`
- `pkg/mcp/client.go`
- `pkg/mcp/server.go`
- `pkg/mcp/server_test.go`
- `pkg/mcp/client_test.go`
- `pkg/mcp/integration_test.go`
- `sdks/openapi.yaml`
- `sdks/go/services.go`
- `sdks/go/types.go`
- `sdks/typescript/src/client.ts`
- `sdks/typescript/src/types.ts`
- `sdks/typescript/tests/client.test.ts`
- `sdks/python/llmsafespaces/client.py`
- `sdks/python/llmsafespaces/async_client.py`
- `sdks/canary/mcp/main.go`
- `frontend/src/api/workspaces.ts`
- `frontend/src/components/layout/Sidebar.tsx`
- `frontend/src/components/layout/Sidebar.test.tsx`
