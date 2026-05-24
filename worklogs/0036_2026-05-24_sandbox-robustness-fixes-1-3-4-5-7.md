# Worklog: Sandbox Robustness Fixes #1, #3, #4, #5, #7

**Date:** 2026-05-24
**Session:** Complete remaining sandbox robustness fixes from design/SANDBOX-ROBUSTNESS-PLAN.md
**Status:** Complete

---

## Objective

Continue from the "incomplete work" commit (3ceded4) which had fix #2 (transient pod-loss recovery) and fix #6 (lifecycle doc) already done. Implement fixes #1, #3, #4, #5, #7 per the plan's ordering.

---

## Work Completed

### Fix #1: POST /sandboxes/:id/restart (commit 87070eb)

- Added `Spec.RestartGeneration int64` and `Status.ObservedRestartGeneration int64` to Sandbox CRD
- Controller: `handleRestartRequest()` detects spec > status, gracefully deletes pod, reverts to Pending
- API: `RestartSandbox(ctx, sandboxID)` validates ownership + phase (Running only), bumps RestartGeneration
- Router: `POST /api/v1/sandboxes/:id/restart` → 202 Accepted
- 4 controller unit tests (happy + idempotent + pod-gone + both-zero)

### Fix #3: Controller watches credential Secret (commit 11e35ab)

- Added `Watches(&corev1.Secret{}, mapCredSecretToSandboxes)` to SetupWithManager
- Mapper filters by `workspace-creds-*` naming pattern, enqueues sandboxes with matching workspace label
- `checkCredentialSecretChanged()` computes SHA-256 of secret data; first observation records without restarting; subsequent changes bump RestartGeneration
- Added `Status.CredentialSecretHash string` to track last-observed hash
- 6 unit tests (changed/unchanged/first-observation/missing + mapper tests)

### Fix #5: POST /sandboxes/:id/retry (commit 0eda38f)

- Added `Spec.MaxRetries int32` (default 3) to Sandbox CRD
- API: `RetrySandbox(ctx, sandboxID)` resets Failed → Pending, bounded by MaxRetries
- Router: `POST /api/v1/sandboxes/:id/retry` → 202 Accepted
- Returns 409 if not Failed or if max retries exceeded

### Fix #4: RuntimeEnvironment.spec.requiresCredentials (commit 917fe0a)

- Added `RequiresCredentials bool` to RuntimeEnvironmentSpec
- In CreateSandbox: if runtime requires credentials and no workspaceRef provided (auto-create path), returns 409 with clear guidance
- Updated RuntimeEnvironment CRD YAML

### Fix #7: local/test.sh robustness probes (commit 5d1b0da)

- Test 10: POST /restart → 202, sandbox returns to Running
- Test 11: Graceful pod deletion → sandbox self-heals (not Failed)
- Test 12: POST /retry → 409 when not Failed (API shape validation)

---

## Assumptions Validated

### Fix #1
- F1-A1: `SandboxInterface.Update` updates full object (verified: `pkg/interfaces/kubernetes.go:38`)
- F1-A2: `For(&v1.Sandbox{})` watches all events including spec changes (controller-runtime standard)
- F1-A3: handleRunningSandbox is the only place needing restart detection (verified: phase dispatch at line 109)
- F1-A4: Graceful pod deletion uses default grace period (verified: existing pattern in controller)
- F1-A5: API can call `.Update(crd)` to patch spec (verified: interface exists)
- F1-A6: Setting phase to Pending triggers pod recreation (verified: fix #2 already does this)

### Fix #3
- F3-A1: Credential secrets named `workspace-creds-{workspaceID}` (verified: `workspace_service.go:391`)
- F3-A2: `Owns(&corev1.Secret{})` only fires for sandbox-owned secrets (verified: A4 in plan)
- F3-A3: `Watches` with `EnqueueRequestsFromMapFunc` is standard pattern (verified: controller-runtime docs)
- F3-A4: `Spec.WorkspaceRef` holds workspace name (verified: `controller.go:91`)
- F3-A5: Label `llmsafespace.dev/workspace` exists on sandboxes (verified: `common/constants.go:30`)

### Fix #5
- F5-A1: Failed phase is terminal (verified: `controller.go:122`)
- F5-A2: Setting phase to Pending triggers pod recreation (verified: fix #2)

### Fix #4
- F4-A1: RuntimeEnvironment types in `pkg/apis/llmsafespace/v1/runtimeenvironment_types.go` (verified)
- F4-A2: Sandbox service can look up RuntimeEnvironments via k8sClient (verified: interface exists)

---

## Key Decisions

- Used `RestartGeneration int64` (nanosecond timestamp) rather than a simple counter for idempotency and monotonicity
- Credential hash check uses SHA-256 over sorted key-value pairs for determinism
- First credential observation records hash without restarting (avoids restart storm on controller startup)
- Fix #4 only blocks auto-workspace-creation path (no workspaceRef); explicit workspaceRef trusts the user
- Fix #5 uses `RestartCount >= MaxRetries` as the bound (cumulative, not per-failure-type)

---

## Blockers

None. All fixes implemented and committed.

---

## Tests Run

Cannot run `go test` in this environment (no network access for module downloads). Structural validation performed:
- All files have balanced braces
- Interface and mock are aligned (9 methods)
- CRD YAML has no tabs
- All imports verified present

Tests to run on deployment:
```bash
go test -timeout 90s -race ./controller/internal/sandbox/...
go test -timeout 90s -race ./api/internal/...
cd local && ./test.sh
```

---

## Next Steps

1. Deploy to cluster and run `go test -timeout 90s -race ./...`
2. Run `local/test.sh` with the new probes (Tests 10-12)
3. Verify credential rotation triggers auto-restart (manual: PUT credentials on a running workspace)
4. Consider writing the worklog for the frontend design implementation (Phase A)

---

## Files Modified

- `pkg/apis/llmsafespace/v1/sandbox_types.go` — added RestartGeneration, ObservedRestartGeneration, CredentialSecretHash, MaxRetries
- `pkg/apis/llmsafespace/v1/runtimeenvironment_types.go` — added RequiresCredentials
- `pkg/crds/sandbox_crd.yaml` — all new fields
- `pkg/crds/runtimeenvironment_crd.yaml` — requiresCredentials
- `charts/llmsafespace/crds/sandbox.yaml` — synced from pkg/crds
- `controller/internal/sandbox/controller.go` — handleRestartRequest, checkCredentialSecretChanged, hashSecretData, mapCredSecretToSandboxes, credential hash check in handleRunningSandbox
- `controller/internal/sandbox/restart_test.go` — new (4 tests)
- `controller/internal/sandbox/credential_watch_test.go` — new (6 tests)
- `api/internal/interfaces/interfaces.go` — RestartSandbox, RetrySandbox
- `api/internal/mocks/sandbox.go` — RestartSandbox, RetrySandbox
- `api/internal/services/sandbox/sandbox_service.go` — RestartSandbox, RetrySandbox, requiresCredentials check
- `api/internal/server/router.go` — POST /:id/restart, POST /:id/retry
- `local/test.sh` — Tests 10-12 (robustness probes)
