# US-1.4: Remove Execution and File Services

**Epic:** 1 - Foundation
**Priority:** High
**Depends on:** US-1.1

## User Story

As a developer, I want exec-based execution and file services removed, so that the codebase reflects the V2 proxy model where opencode handles all tool execution internally.

## Acceptance Criteria

- [ ] Execution service and file service deleted
- [ ] K8s client exec/file methods removed from interfaces
- [ ] `go build ./...` succeeds after removal
- [ ] Sandbox service stubs (Execute, ListFiles, etc.) removed

## Technical Details

**Delete these files/directories:**

| Path | What |
|------|------|
| `api/internal/services/execution/` | Exec-based execution service |
| `api/internal/services/file/` | File operation service |
| `api/internal/mocks/execution.go` | Execution mock |
| `api/internal/mocks/file.go` | File mock |

**Edit these files:**

| File | What to remove |
|------|---------------|
| `api/internal/services/services.go` | execution + file service creation |
| `api/internal/services/sandbox/sandbox_service.go` | Stub methods: Execute, ListFiles, DownloadFile, UploadFile, DeleteFile, InstallPackages |
| `api/internal/interfaces/interfaces.go` | ExecutionService, FileService interfaces |
| `pkg/interfaces/kubernetes.go` | ExecuteInSandbox, ExecuteStreamInSandbox, ListFilesInSandbox, etc. |
| `pkg/kubernetes/client.go` | Method declarations for exec/file operations |
| `pkg/kubernetes/kubernetes_operations.go` | Exec/file implementations |
| `api/internal/mocks/mocks.go` | Mock references |
| `api/internal/services/metrics/metrics.go` | RecordExecution, RecordFileOperation, RecordPackageInstallation |

## Design Reference

Section 7: Agent Architecture — opencode handles all tool execution internally. LLMSafeSpace only proxies.

## Effort

Medium (3-4 hours)
