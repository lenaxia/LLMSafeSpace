# US-1.3: Remove WarmPool and WarmPod

**Epic:** 1 - Foundation
**Priority:** Critical
**Depends on:** US-1.1, US-1.2

## User Story

As a developer, I want warm pool code removed from the codebase, so that V2 architecture is clean and doesn't maintain dead code.

## Acceptance Criteria

- [ ] All warm pool/warm pod code deleted
- [ ] `go build ./...` succeeds after removal
- [ ] No references to WarmPool or WarmPod remain in the codebase (except migration history)

## Technical Details

**CRD type ownership note (see design §P.2):** WarmPool/WarmPod types exist in BOTH `controller/internal/resources/` (authoritative CRD) and `pkg/types/types.go` (API transfer objects). Both must be removed.

**Delete these files/directories:**

| Path | What |
|------|------|
| `controller/internal/warmpool/` | WarmPool reconciler |
| `controller/internal/warmpod/` | WarmPod reconciler |
| `controller/internal/resources/warmpool_types.go` | WarmPool CRD types |
| `controller/internal/resources/warmpod_types.go` | WarmPod CRD types |
| `controller/internal/resources/warmpool_deepcopy.go` | Generated deepcopy |
| `controller/internal/resources/warmpod_deepcopy.go` | Generated deepcopy |
| `controller/internal/resources/warmpool_webhook.go` | Validator |
| `controller/internal/resources/warmpod_webhook.go` | Validator |
| `pkg/crds/warmpool_crd.yaml` | CRD definition |
| `pkg/crds/warmpod_crd.yaml` | CRD definition |
| `api/internal/services/warmpool/` | API service |
| `api/internal/mocks/warmpool.go` | Mock |
| `api/internal/validation/warmpool.go` | Validation |

**Edit these files to remove warm pool references:**

| File | What to remove |
|------|---------------|
| `controller/internal/controller/controller.go` | WarmPool + WarmPod reconciler setup |
| `controller/internal/resources/register.go` | WarmPool/WarmPod type registration (keep Sandbox, SandboxProfile, RuntimeEnvironment) |
| `api/internal/interfaces/interfaces.go` | WarmPoolHandler, WarmPoolService, WarmPoolInterface, WarmPodInterface |
| `api/internal/services/services.go` | warmpool service creation |
| `api/internal/server/router.go` | warmpool routes |
| `pkg/types/types.go` | WarmPool*, WarmPod*, WarmPodReference types |
| `pkg/interfaces/kubernetes.go` | WarmPoolInterface, WarmPodInterface |
| `pkg/kubernetes/client.go` | WarmPool/WarmPod client methods |
| `pkg/kubernetes/client_crds.go` | WarmPool/WarmPod CRD client methods |
| `controller/internal/common/constants.go` | WarmPool/WarmPod annotations/labels |
| `controller/internal/common/utils.go` | FindWarmPodForSandbox |
| `controller/internal/common/pod_manager.go` | RecyclePod, CreateWarmPodPod |

**Remove from Sandbox reconciler:**

| File | What to remove |
|------|---------------|
| `controller/internal/sandbox/controller.go` | `assignWarmPodToSandbox` method, warm pod lookup in `handleCreatingSandbox` |

**Remove dead code:**

| File | What to do |
|------|-----------|
| `controller/internal/controller/setup.go` | Delete entirely — duplicate of `controller.go` |

**Remove WarmPool metrics:**

| File | What to remove |
|------|---------------|
| `api/internal/services/metrics/metrics.go` | RecordWarmPoolMetrics, RecordWarmPoolScaling, UpdateWarmPoolHitRatio |
| `controller/internal/metrics/metrics.go` | WarmPool Prometheus metrics |

**Remove mocks:**

| File | What to do |
|------|-----------|
| `mocks/kubernetes/warmpool.go` | Delete |
| `mocks/kubernetes/warmpod.go` | Delete |
| `mocks/warmpool.go` | Delete |

## Design Reference

Section 8: Removing Warm Pools
Section P.2: CRD Type Ownership Model

## Effort

Medium (4-6 hours — lots of files to touch, but mechanical)
