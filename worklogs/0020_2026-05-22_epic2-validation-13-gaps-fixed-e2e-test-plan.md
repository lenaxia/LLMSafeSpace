# Worklog 0020: Epic 2 Skeptical Validation — 13 Gaps Fixed + E2E Test Plan

**Date**: 2026-05-22
**Scope**: Skeptical review of all Epic 2 code, fix gaps, establish e2e test coverage

---

## Gaps Found and Fixed

### CRITICAL (would break at runtime)

| # | Gap | Root Cause | Fix |
|---|-----|-----------|-----|
| GAP-1 | `v1.SandboxSpec` missing `WorkspaceRef` | API types diverged from controller types | Added field + deepcopy |
| GAP-2 | `v1.SandboxStatus` missing `PodIP`, `LastActivityAt` | Same divergence | Added fields + deepcopy |
| GAP-4 | `buildCRDFromRequest` never set `Spec.WorkspaceRef` | Only used as label, not spec field | Set `WorkspaceRef: workspaceID` in spec |

### HIGH (functional gaps)

| # | Gap | Root Cause | Fix |
|---|-----|-----------|-----|
| GAP-3 | API CRD types missing Packages, NetworkAccess, InitScript, Credentials, ObservedGeneration | Only subset of controller types defined | Added all missing fields to `v1.WorkspaceSpec`/`WorkspaceStatus` |
| GAP-5 | `SandboxMetadata` had no `WorkspaceID`, DB never wrote `workspace_id` | Migration added FK column but code never used it | Added field to struct, INSERT/SELECT/Scan in all 3 DB methods |

### MEDIUM (correctness issues)

| # | Gap | Fix |
|---|-----|-----|
| GAP-6 | Resuming workspace with Suspended sandbox CRDs: sandbox reconciler ignores `Suspended` phase, so sandboxes stuck forever | `handleResuming` now transitions `Suspended` sandboxes to `Resuming` |
| GAP-7 | Suspend/Resume didn't validate current phase | Added phase checks: Suspend requires Active/Resuming, Resume requires Suspended |
| GAP-8 | `buildWorkspaceSetupScript` hardcoded `pip install` for all runtimes | Added runtime-aware package install (pip/npm/go install) |
| GAP-9 | TTL used `lastActivityAt` instead of actual suspend entry time | Added `SuspendedAt` timestamp, set on suspend, used for TTL calculation |

### LOW (deferred)

| # | Gap | Decision |
|---|-----|---------|
| GAP-11 | API directly sets controller-owned `status.phase` | Acceptable for V1; works because controller reads and acts on it |
| GAP-12 | No credential validation on SetCredentials | Deferred to V2; fail-fast when agent uses them |
| GAP-13 | No JSON validation on credential config | Deferred; garbage in, garbage out is acceptable |

---

## E2E Test Plan

### What "E2E" means here

We cannot run a real Kubernetes cluster in unit tests. Our "e2e" tests exercise the **seams between components** using the same fake client and mock infrastructure the controller tests already use. Each test validates that:

1. The API layer produces CRDs that the controller can consume (type alignment)
2. The controller reads CRD fields the API actually sets (field wiring)
3. Cross-reconciler interactions work correctly (workspace ↔ sandbox)
4. Data flows through the entire stack: API → CRD → Controller → Status → API response

### Test Categories

#### Category A: API → Controller Type Alignment (the class of bug GAP-1/2/3 was)

These tests verify that when the API creates a CRD using `pkg/apis/llmsafespace/v1` types, the controller `controller/internal/resources` types can round-trip the same data. Since they're different packages, we test via the `controller-runtime` fake client which uses the resources types.

| ID | Test Name | What it verifies |
|----|-----------|-----------------|
| A1 | `TestE2E_SandboxCRD_WorkspaceRef_RoundTrip` | API sets `Spec.WorkspaceRef`, controller reads it, mounts PVC |
| A2 | `TestE2E_SandboxCRD_PodIP_RoundTrip` | Controller sets `Status.PodIP`, API `convertCRDToAPI` reads it |
| A3 | `TestE2E_WorkspaceCRD_SuspendedAt_RoundTrip` | Controller sets `Status.SuspendedAt`, workspace API reads it for TTL |
| A4 | `TestE2E_WorkspaceCRD_FullSpec_RoundTrip` | Workspace with Packages, AutoSuspend, NetworkAccess, InitScript, Credentials |

#### Category B: Cross-Reconciler Integration (workspace ↔ sandbox)

These tests exercise the interaction between the workspace reconciler and the sandbox reconciler using a shared fake client.

| ID | Test Name | What it verifies |
|----|-----------|-----------------|
| B1 | `TestE2E_SuspendWorkspace_SuspendsSandboxCRDs` | Workspace Suspending → sets sandbox phases to Suspended |
| B2 | `TestE2E_ResumeWorkspace_TransitionsSuspendedSandboxes` | Workspace Resuming → transitions Suspended sandboxes to Resuming, then sandbox reconciler creates pod |
| B3 | `TestE2E_SuspendResumeCycle_PVCPreserved` | After suspend/resume, PVC still exists and workspace returns to Active |
| B4 | `TestE2E_TTLAfterSuspended_UsesSuspendedAt` | TTL calculated from `SuspendedAt`, not `LastActivityAt` |

#### Category C: API Service ↔ Controller CRD Seam

These tests use the API service's `buildWorkspaceCRD` / `buildCRDFromRequest` helpers and verify the resulting CRD is consumable by the controller.

| ID | Test Name | What it verifies |
|----|-----------|-----------------|
| C1 | `TestE2E_APICreatesSandbox_WorkspaceRefSetAndConsumed` | `buildCRDFromRequest` sets WorkspaceRef; controller `buildSandboxPodWithContext` reads it |
| C2 | `TestE2E_APICreatesWorkspace_ControllerCreatesPVC` | `buildWorkspaceCRD` output triggers workspace reconciler to create PVC |
| C3 | `TestE2E_APICredentials_CredSecretMountedInPod` | `SetCredentials` creates Secret; sandbox reconciler mounts it as volume |

#### Category D: Phase Validation (GAP-7 fix verification)

| ID | Test Name | What it verifies |
|----|-----------|-----------------|
| D1 | `TestE2E_SuspendWorkspace_OnlyActiveOrResumingAllowed` | Rejects suspend of Suspended/Terminated/Failed/Pending |
| D2 | `TestE2E_ResumeWorkspace_OnlySuspendedAllowed` | Rejects resume of Active/Pending/Terminated/Failed |

#### Category E: Runtime-Aware Setup Script (GAP-8 fix verification)

| ID | Test Name | What it verifies |
|----|-----------|-----------------|
| E1 | `TestE2E_SetupScript_PythonRuntime_UsesPip` | Python packages → `pip install` |
| E2 | `TestE2E_SetupScript_NodejsRuntime_UsesNpm` | Node.js packages → `npm install` |
| E3 | `TestE2E_SetupScript_GoRuntime_UsesGoInstall` | Go packages → `go install` |

### Test Location

All e2e tests live in a new package: `tests/e2e/` at the repository root. This is separate from unit tests to make them easy to identify and run independently.

### Implementation Strategy

1. Shared test helpers in `tests/e2e/helpers.go`:
   - `newTestScheme()` — scheme with both resources and corev1 registered
   - `newFakeClient(scheme, objs...)` — fake client with status subresources for Workspace + Sandbox
   - Factory functions for creating well-formed Workspace and Sandbox CRDs

2. Each test file imports both:
   - `controller/internal/resources` (controller types)
   - `pkg/apis/llmsafespace/v1` (API types)
   
   This ensures both type systems can round-trip data.

3. Category B tests create both reconcilers against the same fake client to test cross-reconciler interaction.

### Missing E2E Tests (identified gaps)

The following scenarios had **zero** test coverage before this work:

1. **API CRD → Controller consumption** — No test verified that CRDs produced by the API layer (`v1.Sandbox`, `v1.Workspace`) were consumable by the controller layer (`resources.Sandbox`, `resources.Workspace`). This was GAP-1/2/3.

2. **Cross-reconciler workspace↔sandbox** — No test exercised the workspace reconciler's sandbox CRD mutations (setting phase to Suspended, transitioning back on resume). Workspace tests mocked sandboxes by hand; sandbox tests didn't test workspace interactions.

3. **Suspend/resume state machine** — No test verified invalid phase transitions were rejected. The API service tests had one test each for happy path; no negative phase tests.

4. **TTL timer correctness** — The existing TTL test used `LastActivityAt` which coincidentally passed. No test verified the timer starts from the actual suspend time.

5. **Runtime-aware package install** — No test for non-Python runtimes. The setup script was only tested implicitly through the Python path.

---

## Files Changed

| File | Change |
|------|--------|
| `pkg/apis/llmsafespace/v1/types.go` | Added `WorkspaceRef` to SandboxSpec, `PodIP`/`LastActivityAt` to SandboxStatus, `NetworkAccess`/`Packages`/`InitScript`/`Credentials` to WorkspaceSpec, `SuspendedAt`/`ObservedGeneration` to WorkspaceStatus, plus supporting types |
| `pkg/apis/llmsafespace/v1/deepcopy.go` | DeepCopy for all new pointer/slice fields |
| `api/internal/services/sandbox/sandbox_service.go` | Set `WorkspaceRef` in `buildCRDFromRequest`, read `PodIP` in `convertCRDToAPI` |
| `api/internal/services/workspace/workspace_service.go` | Phase validation on Suspend/Resume |
| `pkg/types/types.go` | Added `WorkspaceID` to `SandboxMetadata` |
| `api/internal/services/database/database.go` | Read/write `workspace_id` in `CreateSandbox`, `GetSandbox`, `ListSandboxes` |
| `api/internal/services/database/database_test.go` | Updated SQL mock expectations for 8th column |
| `controller/internal/sandbox/controller.go` | Runtime-aware `buildWorkspaceSetupScript` |
| `controller/internal/workspace/controller.go` | `handleResuming` transitions stuck sandbox CRDs, sets `SuspendedAt`, clears on resume, uses `SuspendedAt` for TTL |
| `controller/internal/resources/workspace_types.go` | `SuspendedAt` field in WorkspaceStatus |
| `controller/internal/resources/workspace_deepcopy.go` | DeepCopy for `SuspendedAt` |

## Test Results

- **Before fixes**: 411 tests, 0 failures (but end-to-end broken)
- **After all fixes + e2e tests**: 463 tests, 0 failures

### New test breakdown (52 new tests)

| Package | Tests Added | Categories |
|---------|------------|------------|
| `controller/internal/resources` | 9 | JSON round-trip M1/M2/M3 (the critical safety net) |
| `controller/internal/workspace` | 15 | Full-flow M4/M5 + unhappy paths (9) |
| `controller/internal/sandbox` | 8 | Runtime scripts E1-E4 + unhappy paths (6) + M8 credential naming |
| `api/internal/services/sandbox` | 6 | API→Controller C1-C3 + M6 convertCRDToAPI |
| `api/internal/services/workspace` | 4 | Phase validation D1/D2 + create workspace C2 |
| `api/internal/services/database` | 0 (fixes only) | Updated SQL mocks for workspace_id column |

### What each category catches

- **JSON round-trip (M1/M2/M3)**: Would have caught GAP-1/2/3 before they shipped. Any future type divergence between `pkg/apis/llmsafespace/v1` and `controller/internal/resources` will break these tests.
- **Full-flow (M4/M5)**: Exercises multi-reconcile suspend→resume cycle against a single fake client, verifying phase transitions propagate correctly.
- **Unhappy paths**: PVC timeout → Failed, race condition revert, partial sandbox readiness, workspace deletion cascade, TTL not-expired guard, missing workspace ref, pod disappearance, timeout exceeded, suspend clears pod fields.
- **convertCRDToAPI (M6)**: Verifies all controller-set status fields (PodIP, Phase, PodName) are mapped to the API DTO.
- **Credential naming (M8)**: Verifies the `workspace-creds-{name}` convention is consistent between sandbox reconciler and API.

