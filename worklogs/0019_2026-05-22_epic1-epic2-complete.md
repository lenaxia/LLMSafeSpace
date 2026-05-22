# Worklog 0019 — 2026-05-22 — Epic 1 + Epic 2 Complete

## Session goals

Complete all remaining Epic 1 (Foundation) and Epic 2 (Workspaces) user stories using a
delegated implement → skeptical-validate → fix-gaps → re-validate → commit loop for each story.

## Method

Each story followed a strict cycle:
1. Implementation agent reads README-LLM.md and EVOLUTION-V2.md, validates assumptions, writes
   tests first (TDD), implements, runs tests.
2. Skeptical validation agent reads README-LLM.md, re-reads every changed file, cross-checks
   against spec, **self-verifies every finding before reporting it** (quoted code + quoted rule).
3. Fix agent addresses every confirmed gap.
4. Re-validation repeats until zero gaps remain, then commit.

All validation findings were self-verified before being accepted. False positives that were
investigated and dismissed are recorded in each validation report under "Investigated — not
confirmed as gaps."

---

## Stories completed

### US-1.5 — Build Redact Binary
**Commit:** `353c9a5`

**Deliverables:**
- `pkg/redact/redact.go` — 16-pattern redaction library ported from k8s-mechanic §9.3
- `pkg/redact/redact_test.go` — table-driven tests, `TestNewRedactorFromFile` with temp-dir FS tests
- `cmd/redact/main.go` — stdin → redact → stdout, `-config` flag, fail-closed (exit 1) on error

**Key design decisions:**
- `sync.Once` caching for package-level `Redact()` — prevents repeated disk reads in hot paths
- `NewRedactorFromFile(path)` exported to eliminate `loadRedactor` duplication between pkg and cmd
- `errors.Is(err, os.ErrNotExist)` (not deprecated `os.IsNotExist`) for missing config
- All 16 patterns applied sequentially (not first-match); verified against §9.3 character-by-character

**Gaps found and fixed across 2 validation rounds:**
- MINOR-1: Tests restructured to table-driven format
- MINOR-2: Incorrect comment about pattern ordering removed
- MINOR-3: Duplicated `loadRedactor` logic extracted to `NewRedactorFromFile`
- MINOR-4: Double `"redact: "` prefix in errors (pkg was pre-prefixing before cmd added its own)
- MINOR-5: Added `TestNewRedactorFromFile` covering filesystem paths; removed stale deferral comment
- NEW-1 (round 2): `os.IsNotExist` → `errors.Is(err, os.ErrNotExist)`
- NEW-2 (round 2): `Redact()` re-reading disk on every call → `sync.Once` cache

---

### US-1.7 — Create Entrypoint Scripts
**Commit:** `3b6a19f`

**Deliverables:**
- `runtimes/base/tools/entrypoints/entrypoint-common.sh` — credential materialisation
- `runtimes/base/tools/entrypoints/entrypoint-opencode.sh` — opencode serve runner

**Key design decisions:**
- Both scripts use `#!/usr/bin/env bash` + `set -euo pipefail`
- No comments (Rule 4) — scripts are self-documenting
- `OPENCODE_SERVER_PASSWORD="$(cat /sandbox-cfg/password)"` quoted to prevent glob expansion

**Gaps found and fixed in 1 validation round:**
- MINOR-1: Unquoted `$(cat ...)` — glob expansion risk on password content

---

### US-1.8 — Rewrite Base Dockerfile
**Commit:** `6545cf2`

**Deliverables:**
- `runtimes/base/Dockerfile` — complete V2 rewrite
- `runtimes/base/tools/smoke-test.sh` — binary presence verification
- Deleted: `cleanup-pod`, `execution-tracker`, `health-check`, `sandbox-monitor` (V1 tools)

**Key design decisions:**
- Multi-stage build: `golang:1.23-bookworm AS redact-builder` → `debian:bookworm-slim@sha256:...`
- Base image digest-pinned: `sha256:40b107342c492725bc7aacbe93a49945f37a4143b8e1e87f2a6ff36f3f27a4ab`
- opencode SHA256-verified at build time using co-published `.sha256` release file
- `OPENCODE_VERSION=1.0.0` ARG with operator note; inert guard removed
- `/tmp` and `/home/sandbox` emptyDir requirement noted in single necessary comment
- `sandbox` user (uid 1000), `USER sandbox`, `WORKDIR /workspace`

**Gaps found and fixed:**
- C-1: Base image not digest-pinned
- M-1: `smoke-test.sh` missing as named artefact
- M-2: V1 tool files on disk (not copied into image but still present in repo)
- m-1, m-2, m-3: Banner comments, missing emptyDir doc, inert version guard

---

### US-2.1 — Define Workspace CRD
**Commit:** `3a00814`

**Deliverables:**
- `controller/internal/resources/workspace_types.go` — Workspace, WorkspaceList, WorkspaceSpec,
  WorkspaceStatus, WorkspacePhase constants, WorkspaceCondition, all nested types
- `controller/internal/resources/workspace_deepcopy.go` — manual DeepCopy for all types
- `controller/internal/resources/register.go` — Workspace + WorkspaceList added to scheme
- `pkg/crds/workspace_crd.yaml` — full OpenAPI v3 schema, status subresource, printer columns
- `controller/internal/resources/workspace_types_test.go` — 19 DeepCopy tests

**Key design decisions:**
- All lifecycle phases: Pending, Active, Suspending, Suspended, Resuming, Terminating, Terminated, Failed
- `+kubebuilder:subresource:status` and `+kubebuilder:resource:scope=Namespaced,shortName=ws`
- `WorkspaceStatus.LastActivityAt *metav1.Time` — updated by API, not controller (per §5.5a)
- Pointer fields (`Credentials`, `AutoSuspend`, `LastActivityAt`) nil-checked in DeepCopy

**Gaps found and fixed:** None — clean first pass (PASS verdict).
Three minor observations noted but all confirmed non-blocking.

---

### US-2.2 — Implement Workspace Reconciler
**Commit:** `fe69531`

**Deliverables:**
- `controller/internal/workspace/controller.go` — WorkspaceReconciler with full 8-phase state machine
- `controller/internal/workspace/controller_test.go` — 29 tests
- `controller/internal/controller/controller.go` — WorkspaceReconciler registered

**State machine:**
- Pending → creates PVC → requeues 5s → PVC bound check → Active (or Failed after 5 min timeout)
- Active → counts active sessions → if idle timeout exceeded → Suspending; else requeue at
  `lastActivity + idleTimeout*0.8` (not fixed 60s — scales with the configured timeout)
- Suspending → race condition check (re-read `lastActivityAt`; if recent activity → Active) →
  delete pods → update Sandbox CRDs status to Suspended → Suspended
- Suspended → TTL check → Terminating (or requeue for remaining TTL)
- Resuming → wait for all associated Sandbox CRDs to be Running → Active (requeue 5s if not ready)
- Terminating → delete PVC (NotFound ok) → delete Sandbox CRDs → remove finalizer → Terminated
- Deletion (deletionTimestamp) → same cleanup as Terminating → remove finalizer

**Key design decisions:**
- Finalizer: `"workspace.llmsafespace.dev/finalizer"`
- PVC creation returns `RequeueAfter: 5s` — does NOT optimistically jump to Active
- 80% requeue formula: `nextCheckAt = lastActivity.Add(idleTimeout * 0.8)` (spec §5.6)
- Sandbox CRD phase updated to `Suspended` during workspace suspension (MAJOR-1 fix)
- `SandboxStatus.Phase` kubebuilder enum extended to include Suspending/Suspended/Resuming
- `pkg/crds/sandbox_crd.yaml` enum updated to match

**Gaps found across 3 validation rounds:**
- MAJOR-1: Suspending did not update Sandbox CRD status → `updateSandboxesToSuspended` added
- MAJOR-2+3: No transition INTO Failed; 5-min timeout not implemented → PVC bound check added
- MINOR-1: Requeue formula used `remaining * 0.2` (20%) instead of `lastActivity + timeout * 0.8`
- MINOR-2: Resuming with zero sandboxes untested
- MINOR-3: Excessive comments removed
- NEW-1 (round 2): Sandbox enum missing Suspending/Suspended/Resuming
- NEW-2 (round 2): Newly-created PVC optimistically jumped to Active without waiting for ClaimBound

---

### US-2.3 — Implement Workspace API Service
**Commit:** `beed7ea`

**Deliverables:**
- `api/internal/services/workspace/workspace_service.go` — full CRUD + suspend/resume + credentials
- `api/internal/services/workspace/workspace_service_test.go` — 37 tests
- `api/internal/interfaces/interfaces.go` — WorkspaceService interface added
- `api/internal/mocks/workspace.go` — MockWorkspaceService
- `api/internal/server/router.go` — 9 workspace routes registered
- `api/internal/server/router_workspace_test.go` — route existence + auth enforcement tests
- `pkg/types/types.go` — CreateWorkspaceRequest, WorkspaceListResult, WorkspaceStatusResult,
  SetCredentialsRequest added
- `api/internal/services/sandbox/sandbox_service.go` — auto-create workspace when WorkspaceRef
  is empty; `workspaceService` dependency injected
- `pkg/types/types.go` — CreateSandboxRequest.WorkspaceRef added

**Key design decisions:**
- `verifyOwner()` called on every mutating and reading operation (owner check AC)
- Forbidden (not 404) returned when user does not own workspace
- Credentials stored as K8s Secret `workspace-creds-{workspaceID}` with owner reference to Workspace CRD
- `DeleteCredentials` ignores NotFound (idempotent)
- Auto-workspace name: `workspace-for-{userID}` — generated from user identity, not sandbox name
- Phase read from K8s CRD status only — never from PostgreSQL (§5.5a)

**Gaps found:**
- MAJOR-1: Auto-create workspace on sandbox creation without workspaceRef — entirely missing
- MINOR-2: Missing K8s error paths for Suspend/Resume/GetStatus
- MINOR-3: No route registration tests

---

### US-2.4 — Update Sandbox Reconciler for Workspaces
**Commit:** `63bdd0d`

**Deliverables:**
- `controller/internal/sandbox/controller.go` — workspace PVC mount, `workspace-setup` init
  container, `credential-setup` init container, password Secret creation, security context,
  emptyDir volumes, podIP status, Suspending/Resuming phase handlers
- `controller/internal/sandbox/controller_test.go` — 25 tests
- `controller/internal/resources/sandbox_types.go` — WorkspaceRef, PodIP, LastActivityAt fields
- `controller/internal/resources/sandbox_deepcopy.go` — LastActivityAt nil-checked DeepCopy

**Pod spec:**
- Workspace PVC mounted at `/workspace` (when `workspaceRef` set)
- `workspace-setup` init container: installs packages + runs initScript (when configured); runs
  before `credential-setup`
- `credential-setup` init container: copies `workspace-creds-{workspaceRef}` and
  `sandbox-pw-{name}` Secrets to `/sandbox-cfg/` emptyDir
- Main container: `readOnlyRootFilesystem`, `runAsNonRoot`, `allowPrivilegeEscalation: false`,
  `drop: ALL`
- EmptyDirs: `sandbox-cfg`, `tmp`, `sandbox-home`
- `/sandbox-cfg` mounted read-only in main container

**Key fix (CRITICAL):** `cred-secret` volume was mounted in init container but never added to
pod volumes — pod creation would have been rejected by Kubernetes. Both the volume and its mount
are now returned from `buildCredentialSetupInit` and appended to the pod spec.

**Gaps found across 2 validation rounds:**
- C-1: `cred-secret` volume missing from pod spec (runtime-breaking)
- M-1: `workspace-setup` init container completely absent
- M-2: No test for workspace-with-credentials path (would have caught C-1)
- M-3: No test for workspace-not-found error path
- N-1: `LastActivityAt` missing from SandboxStatus
- N-2: `handleSuspendingSandbox` did not clear PodName/PodNamespace
- N-3: Password Secret test did not verify owner reference
- NEW (round 2): 6 unnecessary comments + hardcoded fake CPU/memory resource values removed

---

### US-2.5 — Write V2 Database Migration
**Commit:** `3c4181d`

**Deliverables:**
- `api/migrations/000001_initial_schema.up.sql` — complete V2 schema (users with active/role,
  sandboxes with name/status/updated_at, sandbox_labels, api_keys, permissions; no warm_pools,
  no execution_history, no file_operations, no package_installations)
- `api/migrations/000001_initial_schema.down.sql` — reverse-order DROP statements
- `api/migrations/000002_workspaces.up.sql` — workspaces table + sandboxes.workspace_id FK
- `api/migrations/000002_workspaces.down.sql` — rollback
- `api/internal/services/database/database_test.go` — 15 new workspace DB tests

**Key design decisions:**
- `workspaces.id UUID PRIMARY KEY DEFAULT gen_random_uuid()` (spec type, not VARCHAR)
- `sandboxes.workspace_id UUID REFERENCES workspaces(id)` (matching type)
- `phase` column NOT in workspaces table (§5.5a: phase is CRD-owned, not PostgreSQL-owned)
- `phase` removed from all workspace queries in database.go and workspace_service.go
- `WorkspaceMetadata.Phase` and `WorkspaceUpdates.Phase` fields deleted from pkg/types/types.go
- `namespace DEFAULT 'default'` (not `'llmsafespace'` — matches what workspace_service sets)
- `TIMESTAMP WITH TIME ZONE` (better than spec's plain `TIMESTAMP`)
- `IF NOT EXISTS` guards on index

**Critical gap found:** `phase` column was added by the implementation agent despite the story's
explicit AC and §5.5a/§15 both prohibiting it. Required 3-layer fix: migration SQL + database.go
queries + workspace_service.go calls.

**Other gaps found:**
- M-1: UUID type deviation
- M-2: No DB tests for workspace CRUD methods
- NEW-1 (round 2): DeleteWorkspace never called `dbService.DeleteWorkspace` — DB record orphaned
- NEW-2 (round 2): Superfluous step-by-step comments in `database.New()` + "Fix:" annotation
- NEW-3 (round 2): `UpdateWorkspace` asymmetric nil-guard (early-exit then redundant check)

---

## Final state

| Metric | Value |
|---|---|
| Test packages | 24 (all passing) |
| Total tests | 411 |
| Test failures | 0 |
| Race conditions | 0 |
| Build errors | 0 |
| Epics complete | Epic 1 (3 stories), Epic 2 (5 stories) |
| Commits this session | 8 (`353c9a5` → `3c4181d`) |

## Next steps

Epic 3 — Proxy Sessions:
- US-3.1: Implement proxy handler (forward HTTP/SSE to `opencode serve :4096`)
- US-3.2: Add session proxy routes (`/sandboxes/{id}/sessions`, `/events`)
- US-3.3: Implement activity tracking (`lastActivityAt` patches on workspace)
