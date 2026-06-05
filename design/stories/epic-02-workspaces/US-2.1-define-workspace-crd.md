# US-2.1: Define Workspace CRD Types

**Epic:** 2 - Workspaces
**Priority:** Critical

## User Story

As a controller developer, I want Workspace CRD types defined in Go, so that the controller can reconcile workspace resources.

## Acceptance Criteria

- [ ] `controller/internal/resources/workspace_types.go` defines Workspace, WorkspaceList, WorkspaceSpec, WorkspaceStatus
- [ ] DeepCopy methods generated
- [ ] CRD registered in `register.go`
- [ ] YAML CRD definition in `pkg/crds/workspace_crd.yaml`
- [ ] `go build ./controller/...` succeeds

## Technical Details

**Spec fields (from design §5.2):**

| Field | Type | Description |
|-------|------|-------------|
| owner | object `{userID: string}` | User who owns this workspace |
| runtime | string | Runtime (python, nodejs, go) |
| securityLevel | string | standard or high (default: standard) |
| packages | string array | Packages to install on every pod start |
| initScript | string | Script to run on every pod start |
| storage | object `{size, storageClassName, accessMode}` | PVC configuration (RWO default) |
| networkAccess | object `{egress: [{domain, ports}]}` | Egress rules |
| autoSuspend | object `{enabled, idleTimeoutSeconds}` | Auto-suspend after idle (default: disabled, 3600s) |
| ttlSecondsAfterSuspended | integer | Auto-delete after suspended (default: 0 = off) |
| maxActiveSessions | integer | Max concurrent active sessions (default: 5, min: 1, max: 20). Inactive sessions (read-only history) are unlimited. |

**Status fields (from design §5.2):**

| Field | Type | Description |
|-------|------|-------------|
| phase | string | Pending, Active, Suspending, Suspended, Resuming, Terminating, Terminated, Failed |
| conditions | array | Standard K8s conditions |
| pvcName | string | Name of the bound PVC |
| activeSessions | integer | Count of sandboxes referencing this workspace |
| lastActivityAt | string (date-time) | Last API activity timestamp |

## Design Reference

Section 5.2: Workspace CRD, Section 10.1

## Effort

Medium (3-4 hours)
