# Worklog: Epic 0 + Epic 1 (partial) — Fix Compile Errors + Create Shared CRD Types

**Date:** 2026-05-22
**Session:** Fix broken monorepo build, remove V1 warm pool/execution/file code, create shared CRD types package
**Status:** In Progress

---

## Objective

Get `go build ./...` passing by fixing 11 original compile errors, removing V1 warm pool/execution/file code, and establishing a proper shared CRD types package following kubebuilder conventions and SOLID principles.

---

## Work Completed

### 1. Epic 0: Fix Original Compile Errors
- Deleted `pkg/types/zz_generated.deepcopy.go` (6 errors from broken deepcopy generation)
- Removed `// +k8s:deepcopy-gen=package` from `pkg/types/doc.go`
- Fixed 5 webhook decoders: `*admission.Decoder` → `admission.Decoder` (pointer-to-interface bug)
- Fixed `controller/main.go` for controller-runtime v0.20.3: `MetricsBindAddress` → `Metrics: metricsserver.Options{}`, `Port` → `WebhookServer`, `common.LeaderElectionConfig` → direct `LeaseDuration`/`RenewDeadline`/`RetryPeriod` pointers
- Deleted duplicate `controller/internal/controller/setup.go`

### 2. Design Decision: Shared CRD Types Package (Option A)
- Rejected handwriting 300+ lines of manual deepcopy in `pkg/types/types.go` — would violate DRY, SRP, and k8s conventions
- Rejected keeping duplicate CRD types in two packages — violates SRP, causes type drift
- **Chose Option A:** Created `pkg/apis/llmsafespace/v1/` following standard kubebuilder pattern
- **TDD:** Wrote 8 tests FIRST, watched them fail, then implemented types + deepcopy
- Tests cover: scheme registration, DeepCopy isolation, DeepCopyObject, nil-safety, list types, group version constants

### 3. Remove V1 Warm Pool/Pod Code (US-1.3)
- Deleted 12 files: reconcilers, CRD types, deepcopy, webhooks, CRD YAMLs, mocks, validation
- Removed warm pool/pod from: `controller/internal/controller/controller.go`, `controller/main.go`, `controller/internal/resources/register.go`, `controller/internal/common/condition_adapter.go`, `controller/internal/common/pod_manager.go`, `controller/internal/common/utils.go`, `controller/internal/sandbox/controller.go`
- Removed warm pool/pod interfaces from `pkg/interfaces/kubernetes.go`
- Removed from `api/internal/interfaces/interfaces.go`: WarmPoolService, WarmPoolHandler, WarmPoolInterface type aliases
- Removed from `api/internal/services/services.go`: warm pool service creation, start/stop lifecycle

### 4. Remove V1 Execution/File Services (US-1.4)
- Deleted `api/internal/services/execution/`, `api/internal/services/file/`
- Deleted `pkg/kubernetes/kubernetes_operations.go` (716 lines of exec/file via pod exec)
- Removed execution/file service interfaces from `api/internal/interfaces/interfaces.go`
- Removed execution/file methods from `pkg/interfaces/kubernetes.go`
- Deleted stale test files: `pkg/kubernetes/tests/kubernetes_operations_test.go`, `mocks_test.go`, `test_helpers.go`
- Deleted all `mocks/kubernetes/*.go` files (need regeneration with new types)

### 5. Migrate pkg/kubernetes/ to Shared Types
- Rewrote `pkg/kubernetes/client_crds.go` to use `pkg/apis/llmsafespace/v1` instead of `pkg/types`
- Rewrote `pkg/kubernetes/informers.go` to use shared types, removed warm pool/pod informers

---

## Key Decisions

1. **Created `pkg/apis/llmsafespace/v1/`** — Standard kubebuilder pattern. Both controller and API layer import from here. One source of truth for CRD types. Generated deepcopy in one file.
2. **Manual deepcopy (not generated)** — The code generator isn't configured for this new package path. The manual implementation is correct and tested. Future story: add kubebuilder markers and wire up `make deepcopy`.
3. **`pkg/types/types.go` kept with CRD type stubs temporarily** — API services still use `pkg/types.Sandbox` etc. as DTOs. Full migration to separate concerns (CRD types in `pkg/apis/`, pure DTOs in `pkg/types`) is a follow-up story.
4. **Reverted manual deepcopy hack** — Initially wrote 300 lines of deepcopy into `pkg/types/types.go`, then realized this violated DRY and perpetuated the dual-ownership anti-pattern. Reverted with `git checkout`.

---

## Blockers

- **`api/internal/services/sandbox/sandbox_service.go`** (869 lines) needs rewrite: type mismatches between `pkg/types.Sandbox` (API DTOs) and `pkg/apis/llmsafespace/v1.Sandbox` (CRD types), plus removal of warm pod lookup, CoreV1 calls, and V1 service dependencies
- **`api/internal/services/database/database.go`** — Fixed (added UpdatedAt/Active/Role to User type)
- **`api/internal/middleware/metrics.go`** — Fixed (removed RecordExecution call)

---

## Tests Run

```
go test -timeout 30s -race -v ./pkg/apis/llmsafespace/v1/
=== RUN   TestSchemeRegistration (6 subtests) — PASS
=== RUN   TestSandboxDeepCopy — PASS
=== RUN   TestSandboxDeepCopyObject — PASS
=== RUN   TestSandboxListDeepCopy — PASS
=== RUN   TestSandboxNilSafeDeepCopy — PASS
=== RUN   TestSandboxProfileDeepCopy — PASS
=== RUN   TestRuntimeEnvironmentDeepCopy — PASS
=== RUN   TestGroupVersion — PASS
PASS

go build ./pkg/... ./controller/... — PASS (zero errors)
go build ./api/... — 3 packages remaining (database fixed, sandbox_service + middleware partially fixed)
```

---

## Next Steps

1. **Rewrite `api/internal/services/sandbox/sandbox_service.go`** — Remove V1 service fields (warmPoolService, fileService, execService), add conversion functions between `pkg/types.Sandbox` (API DTO) and `pkg/apis/llmsafespace/v1.Sandbox` (CRD type), remove warm pod lookup in CreateSandbox, remove CoreV1() calls (interface doesn't have it), fix RecordSandboxCreation call signature
2. **Delete `mocks/factory.go`** if it references deleted mock types
3. **Regenerate `mocks/kubernetes/*.go`** with new `pkg/apis/llmsafespace/v1` types
4. **Clean up `pkg/types/types.go`** — remove CRD types (Sandbox, WarmPool, WarmPod, RuntimeEnvironment, SandboxProfile and their specs/statuses/lists), keep only API DTOs (CreateSandboxRequest, User, SandboxMetadata, etc.)
5. **Run `go test -timeout 30s -race ./...`** to verify no regressions
6. **Write follow-up worklog**

---

## Files Modified

### Created
- `pkg/apis/llmsafespace/v1/types.go` — Shared CRD types (Sandbox, SandboxProfile, RuntimeEnvironment + lists + subtypes)
- `pkg/apis/llmsafespace/v1/deepcopy.go` — Manual DeepCopy for all CRD types
- `pkg/apis/llmsafespace/v1/types_test.go` — 8 TDD tests for shared types

### Deleted
- `pkg/types/zz_generated.deepcopy.go` — Broken generated deepcopy
- `controller/internal/controller/setup.go` — Duplicate of controller.go
- `controller/internal/warmpool/` — WarmPool reconciler
- `controller/internal/warmpod/` — WarmPod reconciler
- `controller/internal/resources/warmpool_types.go`, `warmpool_deepcopy.go`, `warmpool_webhook.go`
- `controller/internal/resources/warmpod_types.go`, `warmpod_deepcopy.go`, `warmpod_webhook.go`
- `api/internal/services/execution/` — Exec-based execution service
- `api/internal/services/file/` — File operation service
- `api/internal/services/warmpool/` — Warm pool service
- `api/internal/mocks/warmpool.go`, `execution.go`, `file.go`
- `api/internal/validation/warmpool.go`
- `pkg/crds/warmpool_crd.yaml`, `warmpod_crd.yaml`
- `pkg/kubernetes/kubernetes_operations.go` — V1 exec/file operations
- `pkg/kubernetes/tests/kubernetes_operations_test.go`, `mocks_test.go`, `test_helpers.go`
- `mocks/kubernetes/*.go` (all 7 files — need regeneration)

### Edited
- `pkg/types/doc.go` — Removed deepcopy-gen tag
- `pkg/types/types.go` — Added Message type, List types, User fields (UpdatedAt, Active, Role)
- `controller/main.go` — Fixed controller-runtime v0.20.3 API, removed warm pool/pod webhooks
- `controller/internal/resources/register.go` — Removed WarmPool/WarmPod scheme registration
- `controller/internal/resources/*_webhook.go` (5 files) — Fixed decoder pointer-to-interface
- `controller/internal/controller/controller.go` — Removed warm pool/pod reconciler setup
- `controller/internal/common/condition_adapter.go` — Removed warm pool condition functions
- `controller/internal/common/pod_manager.go` — Removed CreateWarmPodPod, RecyclePod
- `controller/internal/common/utils.go` — Removed FindWarmPodForSandbox
- `controller/internal/sandbox/controller.go` — Removed warm pod assignment logic
- `api/internal/interfaces/interfaces.go` — Removed V1 interfaces (Execution, File, WarmPool), updated Services interface
- `api/internal/services/services.go` — Removed V1 service creation, simplified lifecycle
- `pkg/interfaces/kubernetes.go` — Removed WarmPool/WarmPod/exec/file interfaces, migrated to shared types
- `pkg/kubernetes/client_crds.go` — Migrated to `pkg/apis/llmsafespace/v1` types, removed warm pool/pod CRUD
- `pkg/kubernetes/informers.go` — Migrated to shared types, removed warm pool/pod informers
- `api/internal/middleware/metrics.go` — Removed RecordExecution call
