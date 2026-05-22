# Worklog 0004 — 2026-05-22 — Validation: typed ListSandboxes, remove all V1 warm pool dead code

## Session goals

Validate that the work from session 3 (worklog 0003) was done correctly and completely. Systematic scan for:
- Remaining V1 warm pool / warm pod references in production code
- Type safety violations (`map[string]interface{}` in domain interfaces/services)
- Interface vs implementation mismatches
- Test count and pass rate

## Issues found during validation

| # | Location | Issue | Severity |
|---|----------|-------|----------|
| 1 | `api/internal/interfaces/interfaces.go:90` | `SandboxService.ListSandboxes` returns `[]map[string]interface{}` — violates type safety rule | High |
| 2 | `api/internal/services/sandbox/sandbox_service.go` | `ListSandboxes` impl built `[]map[string]interface{}` rows | High |
| 3 | `pkg/types/types.go` | `WarmPod`, `WarmPodSpec`, `WarmPodStatus`, `WarmPool`, `WarmPoolSpec`, `WarmPoolStatus`, `WarmPodReference` types still present | High |
| 4 | `pkg/types/types.go:167` | `SandboxStatus.WarmPodRef *WarmPodReference` still present | High |
| 5 | `pkg/types/types.go:381` | `CreateSandboxRequest.UseWarmPool bool` still present | High |
| 6 | `pkg/types/types.go` | `WarmPoolList`, `WarmPodList` still present | High |
| 7 | `controller/internal/metrics/metrics.go` | `WarmPoolSizeGauge`, `WarmPoolUtilizationGauge`, `WarmPoolAssignmentDurationSeconds`, `WarmPoolRecycleTotal` Prometheus metrics still registered | High |
| 8 | `controller/internal/metrics/metrics.go:41` | `SandboxStartupDurationSeconds` label `warm_pod_used` — V1 concept | Medium |
| 9 | `controller/internal/common/metrics.go` | `WarmPodsAvailable`, `WarmPodsAssigned`, `WarmPoolHitRatio`, `WarmPodRecycleCount`, `WarmPodTTLExceededCount` still registered; `SandboxCreationDuration` still had `warm_pool_used` label | High |
| 10 | `controller/internal/common/constants.go` | `WarmPoolFinalizer`, `WarmPodFinalizer`, `AnnotationWarmPodID`, `AnnotationPoolName`, `AnnotationRecyclable`, `AnnotationRecycleCount`, `AnnotationLastRecycled`, `AnnotationLastSandbox`, `LabelWarmPodID`, `LabelPoolName`, `ComponentWarmPool`, `ComponentWarmPod`, `ConditionPoolReady`, `ConditionScalingUp/Down`, `ReasonPoolReady/NotReady`, `ReasonScalingUp/Down`, `WarmPodPhase*` constants still present | High |
| 11 | `api/internal/errors/errors.go:192` | `IsWarmPoolNotFoundError` function — dead code | Medium |
| 12 | `api/internal/docs/swagger.go:37` | WarmPools Swagger tag still present | Low |
| 13 | `api/internal/services/database/database.go:586` | `case "warmpool"` in `CheckResourceOwnership` switch — references deleted table | Medium |

## Fixes applied

### 1. Typed `ListSandboxes` return

Added to `pkg/types/types.go`:
```go
type SandboxListResult struct {
    Items      []SandboxListItem
    Pagination *PaginationMetadata
}

type SandboxListItem struct {
    // DB fields: ID, UserID, Runtime, CreatedAt, UpdatedAt, Status, Name, Labels
    // Live k8s (best-effort): Phase, StartTime, CPUUsage, MemoryUsage
}
```

Updated `api/internal/interfaces/interfaces.go`:
```go
ListSandboxes(...) (*types.SandboxListResult, error)
```

Rewrote `sandbox_service.go:ListSandboxes` to populate `SandboxListItem` structs directly. Removed all `map[string]interface{}` row building. Sort now operates on `[]SandboxListItem` using `CreatedAt` directly — no type assertion needed.

Updated `sandbox_service_test.go` list tests to use `result.Items[i].ID` instead of `results[i]["id"]`.

### 2. Remove WarmPod/WarmPool types from `pkg/types/types.go`

Removed:
- `WarmPodReference` struct
- `WarmPod` / `WarmPodSpec` / `WarmPodStatus`
- `WarmPool` / `WarmPoolSpec` / `WarmPoolStatus`
- `WarmPoolList` / `WarmPodList`
- `SandboxStatus.WarmPodRef`
- `CreateSandboxRequest.UseWarmPool`

### 3. Remove warm pool metrics from controller

**`controller/internal/metrics/metrics.go`:**
- Removed `WarmPoolSizeGauge`, `WarmPoolUtilizationGauge`, `WarmPoolAssignmentDurationSeconds`, `WarmPoolRecycleTotal`
- Removed their `prometheus.MustRegister` calls
- Fixed `SandboxStartupDurationSeconds` label: `["runtime", "warm_pod_used"]` → `["runtime"]`

**`controller/internal/common/metrics.go`:**
- Removed `WarmPodsAvailable`, `WarmPodsAssigned`, `WarmPoolHitRatio`, `WarmPodRecycleCount`, `WarmPodTTLExceededCount`
- Removed their `metrics.Registry.MustRegister` calls
- Fixed `SandboxCreationDuration` label: `["runtime", "security_level", "warm_pool_used"]` → `["runtime", "security_level"]`

### 4. Remove warm pool constants from controller

**`controller/internal/common/constants.go`:** removed all warm pool/pod finalizers, annotation keys, label keys, component values, condition types/reasons, and phase values. Kept only sandbox-relevant constants.

### 5. Remove dead code in api

- `api/internal/errors/errors.go`: deleted `IsWarmPoolNotFoundError`
- `api/internal/docs/swagger.go`: deleted WarmPools tag
- `api/internal/services/database/database.go`: deleted `case "warmpool"` from `CheckResourceOwnership`

## Build and test status after fixes

All packages build cleanly. Full test suite:

| Package | Tests | Result |
|---------|-------|--------|
| `api/internal/config` | 2 | PASS |
| `api/internal/logger` | 3 | PASS |
| `api/internal/middleware/tests` | 40 | PASS |
| `api/internal/services/auth` | 10 | PASS |
| `api/internal/services/cache` | 6 | PASS |
| `api/internal/services/database` | 11 | PASS |
| `api/internal/services/metrics` | 10 | PASS |
| `api/internal/services/sandbox` | 39 | PASS |
| `api/internal/utilities` | 2 | PASS |
| `mocks/kubernetes` | 31 | PASS |
| `pkg/apis/llmsafespace/v1` | 8 | PASS |
| `pkg/kubernetes` | 0 | PASS |
| `pkg/logger` | 2 | PASS |

**Total: 164 tests, 0 failures, 13/13 packages.**

## Known non-critical items (separate stories)

`DatabaseService` and `CacheService` interfaces still have `map[string]interface{}` in `UpdateUser`, `UpdateSandbox`, `GetSession`, `SetSession`. These are legacy DB update patterns not touched by the sandbox service rewrite. Replacing with typed update/session structs is a separate refactoring story.

## Commits

- `be2d9f7` — Epic 0+1+2: fix compile errors, TDD mocks/kubernetes, rewrite sandbox service
- `7c50139` — Validation fixes: typed ListSandboxes, remove all V1 warm pool dead code

## Next steps

- US-1.5: Build `redact` binary for log sanitization
- US-1.7: Create entrypoint scripts for runtime containers
- US-1.8: Rewrite base Dockerfile
- Epic 2: Workspace CRD (replaces warm pools)
- Separate story: replace `map[string]interface{}` in `UpdateUser`, `UpdateSandbox`, `GetSession`, `SetSession` with typed structs
