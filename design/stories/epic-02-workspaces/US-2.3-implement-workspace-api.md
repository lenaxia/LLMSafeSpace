# US-2.3: Implement Workspace API Service

**Epic:** 2 - Workspaces
**Priority:** Critical
**Depends on:** US-1.1

## User Story

As a user, I want REST API endpoints to create, manage, and delete workspaces, so that I can control my persistent environments programmatically.

## Acceptance Criteria

- [ ] CRUD endpoints: create, get, list, delete workspace
- [ ] Suspend/resume endpoints
- [ ] Credential management: PUT/DELETE credentials
- [ ] Auto-create workspace when sandbox created without workspaceRef
- [ ] Owner check on every operation

## Technical Details

**New file:** `api/internal/services/workspace/workspace_service.go`

**Methods:**
- `CreateWorkspace` — creates Workspace CRD + PostgreSQL record
- `GetWorkspace` — reads from PostgreSQL + CRD status
- `ListWorkspaces` — queries PostgreSQL
- `DeleteWorkspace` — updates Workspace CRD with deletionTimestamp
- `SuspendWorkspace` — sets CRD status.phase = Suspending
- `ResumeWorkspace` — sets CRD status.phase = Resuming
- `GetWorkspaceStatus` — reads from CRD status
- `SetCredentials` — creates K8s Secret owner-ref'd to Workspace
- `DeleteCredentials` — deletes the K8s Secret

**Endpoints (from design §11.1):**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/workspaces` | GET | List workspaces |
| `/api/v1/workspaces` | POST | Create workspace |
| `/api/v1/workspaces/{id}` | GET | Get workspace |
| `/api/v1/workspaces/{id}` | DELETE | Delete workspace |
| `/api/v1/workspaces/{id}/suspend` | POST | Suspend |
| `/api/v1/workspaces/{id}/resume` | POST | Resume |
| `/api/v1/workspaces/{id}/status` | GET | Get status |
| `/api/v1/workspaces/{id}/credentials` | PUT | Set credentials |
| `/api/v1/workspaces/{id}/credentials` | DELETE | Delete credentials |

**Auto-workspace creation:**
- In sandbox service, if `workspaceRef` is not specified in `CreateSandbox`, auto-create a workspace with defaults

## Design Reference

Section 11.1, 11.3

## Effort

Large (6-8 hours)
