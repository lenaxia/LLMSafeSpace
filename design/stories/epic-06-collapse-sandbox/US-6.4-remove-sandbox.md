# US-6.4: Remove Sandbox CRD, Controller, Service + DB Migration

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.3

## Objective

Delete everything sandbox-specific. Drop DB tables. Clean constants. No backwards compatibility.

## Files Deleted

### Controller
- `controller/internal/sandbox/` (entire directory — reconciler)
- `controller/internal/webhooks/sandbox_webhook.go`
- `controller/internal/webhooks/sandboxprofile_webhook.go`
- `controller/internal/common/condition_adapter.go` (sandbox-specific; workspace has its own condition helpers)
- `controller/internal/common/network_policy_manager.go` (takes `*v1.Sandbox`; workspace builds NetworkPolicy inline)
- `controller/internal/common/pod_manager.go` (takes `*v1.Sandbox`; workspace builds pod inline)
- `controller/internal/common/service_manager.go` (takes `*v1.Sandbox`; workspace uses PodIP directly, no Service)
- `controller/examples/sandbox.yaml`
- `controller/examples/sandboxprofile.yaml`
- `controller/examples/test-sandbox.yaml`

### API
- `api/internal/services/sandbox/` (entire directory — `sandbox_service.go`, `sandbox_service_test.go`, `validation/`)
- `api/internal/mocks/sandbox.go`
- `api/internal/server/router_sandbox_test.go`

### Types & CRDs
- `pkg/apis/llmsafespace/v1/sandbox_types.go`
- `pkg/apis/llmsafespace/v1/sandboxprofile_types.go`
- `pkg/crds/sandbox_crd.yaml`
- `pkg/crds/sandboxprofile_crd.yaml`
- `charts/llmsafespace/crds/sandbox.yaml`
- `charts/llmsafespace/crds/sandboxprofile.yaml`

## Files Modified (Cleanup)

| File | Change |
|------|--------|
| `controller/internal/controller/controller.go` | Remove `sandbox` import and `SandboxReconciler` registration |
| `controller/main.go` | Remove sandbox/sandboxprofile webhook registration |
| `controller/internal/common/constants.go` | Remove: `SandboxFinalizer`, `LabelSandboxID`, `ComponentSandbox`, `SandboxPhase*` constants, `ReasonPodTransientLoss`, `ReasonPodPersistentLoss`. Move to workspace package: `MaxTransientFailures`, `TransientFailureResetWindow`. |
| `controller/internal/common/common_test.go` | Remove `makeSandbox()` helper and all tests using sandbox finalizer. Replace with workspace-based equivalents. |
| `pkg/interfaces/kubernetes.go` | Remove `SandboxInterface`, `SandboxProfileInterface` from `LLMSafespaceV1Interface`; remove `Sandboxes()`, `SandboxProfiles()` methods |
| `mocks/kubernetes/mocks.go` | Remove `MockSandboxInterface`, `MockSandboxProfileInterface`, and `Sandboxes()`/`SandboxProfiles()` methods on `MockLLMSafespaceV1Interface` |
| `mocks/kubernetes/mocks_test.go` | Remove sandbox-related test cases |
| `pkg/kubernetes/client_crds.go` | Remove sandbox and sandboxprofile CRUD implementations |
| `api/internal/services/services.go` | Remove `SandboxService` from service registry |
| `api/internal/interfaces/interfaces.go` | Remove `SandboxService` interface, `GetSandbox()`, `CreateSandbox()`, `ListSandboxes()` |
| `api/internal/server/router.go` | Remove sandbox CRUD route group, `sandboxOwnershipMiddleware`, `registerSandboxCRUDRoutes` |
| `api/internal/services/database/database.go` | Remove `GetSandbox()`, `CreateSandbox()`, `ListSandboxes()`, `UpdateSandbox()`, `DeleteSandbox()` |
| `api/internal/mocks/database.go` | Remove sandbox DB mock methods |
| `pkg/types/types.go` | Remove `SandboxMetadata`, `SandboxUpdates`, `SandboxNotFoundError`, `CreateSandboxRequest`, `SandboxListItem` |
| `api/internal/handlers/proxy.go` | Remove `GetSandboxCRD()` method |

## Database Migration

New migration file `api/migrations/000004_drop_sandbox_tables.up.sql`:

```sql
-- Drop sandbox-related tables in FK dependency order
DROP TABLE IF EXISTS execution_history;
DROP TABLE IF EXISTS file_operations;
DROP TABLE IF EXISTS package_installations;
DROP TABLE IF EXISTS sandbox_labels;
DROP TABLE IF EXISTS sandboxes;
```

Down migration `api/migrations/000004_drop_sandbox_tables.down.sql`:
```sql
-- No restore — data loss accepted (project not live)
-- Tables would need to be recreated from 000001_initial_schema.up.sql if needed
```

**Evidence:** `migrations/000001_initial_schema.up.sql:22-96` creates `sandboxes`, `sandbox_labels`, `execution_history`, `file_operations`, `package_installations`. All have `sandbox_id` FK references and must be dropped in dependency order.

## Pre-deploy Migration

Before deploying, delete all in-cluster sandbox CRDs:
```bash
kubectl delete sandboxes --all -A
kubectl delete sandboxprofiles --all -A
kubectl delete crd sandboxes.llmsafespace.dev sandboxprofiles.llmsafespace.dev
```

## Acceptance Criteria

1. `make build` succeeds with zero sandbox references in Go code
2. `make test` passes
3. `grep -ri "sandbox" --include="*.go" controller/ api/ pkg/ mocks/ cmd/` returns zero matches (excluding comments in design docs)
4. No Sandbox or SandboxProfile CRD YAML files exist
5. `pkg/interfaces/kubernetes.go` has no `SandboxInterface` or `SandboxProfileInterface`
6. DB migration runs cleanly on existing database
7. Controller starts and reconciles workspaces without sandbox controller
8. No sandbox examples in `controller/examples/`
