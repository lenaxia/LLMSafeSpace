# US-6.4: Remove Sandbox CRD, Controller, Service + DB Migration

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.3

## Objective

Delete everything sandbox-specific. Drop DB tables. Clean constants. No backwards compatibility.

## Files Deleted

### Controller
- `controller/internal/sandbox/` (entire directory)
- `controller/internal/webhooks/sandbox_webhook.go`
- `controller/internal/webhooks/sandboxprofile_webhook.go`

### API
- `api/internal/services/sandbox/` (entire directory, including `sandbox_service.go` and `sandbox_service_test.go`)

### Types
- `pkg/apis/llmsafespace/v1/sandbox_types.go`
- `pkg/apis/llmsafespace/v1/sandbox_deepcopy.go`

### CRDs
- `pkg/crds/sandbox_crd.yaml`
- `pkg/crds/sandboxprofile_crd.yaml`
- `charts/llmsafespace/crds/sandbox.yaml`
- `charts/llmsafespace/crds/sandboxprofile.yaml`

### Mocks
- `api/internal/mocks/sandbox.go`
- `mocks/kubernetes/sandbox.go`

## Files Modified (Cleanup)

| File | Change |
|------|--------|
| `controller/internal/common/constants.go` | Remove: `SandboxFinalizer`, `LabelSandboxID`, `ComponentSandbox`, `SandboxPhase*` constants, `ReasonPodTransientLoss`, `ReasonPodPersistentLoss`. Move to workspace package: `MaxTransientFailures`, `TransientFailureResetWindow` (these are used by the workspace reconciler's transient recovery logic). |
| `controller/main.go` | Remove sandbox controller setup, sandbox/sandboxprofile webhook registration |
| `api/internal/services/services.go` | Remove `SandboxService` from service registry |
| `api/internal/interfaces/interfaces.go` | Remove `SandboxService` interface, `GetSandbox()`, `CreateSandbox()`, `ListSandboxes()` |
| `api/internal/server/router.go` | Remove sandbox CRUD route group, sandbox ownership middleware |
| `api/internal/services/database/database.go` | Remove `GetSandbox()`, `CreateSandbox()`, `ListSandboxes()` (lines 271, 340, 502) |
| `api/internal/mocks/database.go` | Remove sandbox DB mock methods |
| `pkg/types/types.go` | Remove `SandboxMetadata`, `SandboxUpdates`, `SandboxNotFoundError` |
| `api/internal/handlers/proxy.go` | Remove `GetSandboxCRD()` method |

## Database Migration

New migration file to drop sandbox tables:

```sql
-- Drop sandbox-related tables and indexes
DROP TABLE IF EXISTS execution_history;
DROP TABLE IF EXISTS file_operations;
DROP TABLE IF EXISTS package_installations;
DROP TABLE IF EXISTS sandbox_labels;
DROP TABLE IF EXISTS sandboxes;
```

**Evidence:** `migrations/001_initial_schema.sql:22-96` creates `sandboxes`, `sandbox_labels`, `execution_history`, `file_operations`, `package_installations`. All have `sandbox_id` FK references and must be dropped in dependency order.

## Pre-deploy Migration

Before deploying, delete all in-cluster sandbox CRDs:
```bash
kubectl delete sandboxes --all -A
kubectl delete sandboxprofiles --all -A
kubectl delete crd sandboxes.llmsafespace.dev sandboxprofiles.llmsafespace.dev
```

## Acceptance Criteria

1. `make build` succeeds with zero sandbox references
2. `make test` passes
3. `grep -r "sandbox" --include="*.go" controller/ api/ pkg/` returns zero matches
4. No Sandbox or SandboxProfile CRD in charts
5. DB migration runs cleanly on existing database
6. Controller starts and reconciles workspaces without sandbox controller
