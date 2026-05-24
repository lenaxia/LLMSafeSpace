# US-6.1: Rewrite Workspace CRD Types

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** None

## Objective

Rewrite Workspace CRD types so workspace owns pod lifecycle directly. Remove `DefaultRuntime` (replaced by `Runtime`). Add `Creating` phase. Add `SecurityContext`. No backwards compatibility.

## Changes

### WorkspaceSpec

```go
type WorkspaceSpec struct {
    Owner                WorkspaceOwner         `json:"owner"`
    Runtime              string                 `json:"runtime"`              // WAS DefaultRuntime
    SecurityLevel        string                 `json:"securityLevel,omitempty"`
    Storage              WorkspaceStorageConfig  `json:"storage"`
    NetworkAccess        *WorkspaceNetworkAccess `json:"networkAccess,omitempty"`
    AutoSuspend          *WorkspaceAutoSuspend   `json:"autoSuspend,omitempty"`
    TTLSecondsAfterSuspended int64              `json:"ttlSecondsAfterSuspended,omitempty"`
    Packages             []WorkspacePackageSet   `json:"packages,omitempty"`
    InitScript           string                 `json:"initScript,omitempty"`
    MaxActiveSessions    int32                  `json:"maxActiveSessions,omitempty"`
    Credentials          *WorkspaceCredentialRef `json:"credentials,omitempty"`

    // NEW — pod lifecycle:
    Timeout              int                    `json:"timeout,omitempty"`     // Max pod lifetime seconds. 0 = no limit. See D3.
    Resources            *ResourceRequirements  `json:"resources,omitempty"`
    RestartGeneration    int64                  `json:"restartGeneration,omitempty"`
    MaxRetries           int32                  `json:"maxRetries,omitempty"`  // 0-10, default 3
    PodSecurityContext   *PodSecurityContext    `json:"podSecurityContext,omitempty"`
}
```

**Removed:** `DefaultRuntime` (replaced by `Runtime`).

**Not added:** `spec.Suspend` (see D2 — controllers must not write to spec; phase transitions only).

### PodSecurityContext (new type, replaces sandbox's SecurityContext)

```go
type PodSecurityContext struct {
    RunAsUser       int64  `json:"runAsUser,omitempty"`       // Default 1000
    RunAsGroup      int64  `json:"runAsGroup,omitempty"`      // Default 1000
    SeccompProfile  string `json:"seccompProfile,omitempty"` // "RuntimeDefault" or "Localhost"
}
```

**Why this type exists:** `buildPodSecurityContext` (`sandbox/controller.go:900-916`) reads RunAsUser/RunAsGroup from sandbox spec. Without it, the pod defaults to UID/GID 1000 always — no way to customize.

### ResourceRequirements (moved from sandbox_types.go:61-79)

Same type, different file. No changes to fields.

### WorkspacePhase — add Creating

```go
const (
    WorkspacePhasePending     WorkspacePhase = "Pending"
    WorkspacePhaseCreating    WorkspacePhase = "Creating"    // NEW — pod exists but not running
    WorkspacePhaseActive      WorkspacePhase = "Active"      // Pod running, PodIP set
    WorkspacePhaseSuspending  WorkspacePhase = "Suspending"
    WorkspacePhaseSuspended   WorkspacePhase = "Suspended"
    WorkspacePhaseResuming    WorkspacePhase = "Resuming"
    WorkspacePhaseTerminating WorkspacePhase = "Terminating"
    WorkspacePhaseTerminated  WorkspacePhase = "Terminated"
    WorkspacePhaseFailed      WorkspacePhase = "Failed"
)
```

**Why:** Without Creating, the proxy cannot distinguish "PVC bound, pod starting" from "pod running, ready for traffic". Active now means "pod running with PodIP". Creating means "pod exists, waiting for Running". This matches the current sandbox lifecycle exactly (see D1).

### WorkspaceStatus — add pod fields

```go
type WorkspaceStatus struct {
    // Existing:
    Phase                WorkspacePhase `json:"phase,omitempty"`
    PVCName              string         `json:"pvcName,omitempty"`
    ActiveSessions       int32          `json:"activeSessions,omitempty"`
    LastActivityAt       *metav1.Time   `json:"lastActivityAt,omitempty"`
    SuspendedAt          *metav1.Time   `json:"suspendedAt,omitempty"`
    Conditions           []WorkspaceCondition `json:"conditions,omitempty"`
    Message              string         `json:"message,omitempty"`
    ObservedGeneration   int64          `json:"observedGeneration,omitempty"`

    // NEW — pod status (from SandboxStatus):
    PodName              string         `json:"podName,omitempty"`
    PodNamespace         string         `json:"podNamespace,omitempty"`
    PodIP                string         `json:"podIP,omitempty"`
    Endpoint             string         `json:"endpoint,omitempty"`
    StartTime            *metav1.Time   `json:"startTime,omitempty"`
    RestartCount         int32          `json:"restartCount,omitempty"`
    TransientFailureCount int32         `json:"transientFailureCount,omitempty"`
    LastTransientFailureAt *metav1.Time `json:"lastTransientFailureAt,omitempty"`
    ObservedRestartGeneration int64     `json:"observedRestartGeneration,omitempty"`
    CredentialSecretHash string        `json:"credentialSecretHash,omitempty"`
}
```

### Types NOT moved (see D6)

- `FilesystemConfig` — hardcoded in pod spec. No CRD-level config needed.
- `StorageConfig` — replaced by `WorkspaceStorageConfig` (already exists).
- `NetworkAccess` / `EgressRule` / `PortRule` — workspace already has `WorkspaceNetworkAccess`.
- `ProfileReference` — SandboxProfile is dead code (A4), removed entirely.

## Files Modified

| File | Change |
|------|--------|
| `pkg/apis/llmsafespace/v1/workspace_types.go` | Add Creating phase; new spec/status fields; PodSecurityContext type |
| `pkg/apis/llmsafespace/v1/workspace_deepcopy.go` | Regenerate |
| `pkg/apis/llmsafespace/v1/workspace_webhook.go` | Validate Runtime (required), Timeout (0-3600), MaxRetries (0-10) |
| `pkg/crds/workspace_crd.yaml` | Add new spec/status fields |
| `charts/llmsafespace/crds/workspace_crd.yaml` | Sync |
| `api/internal/services/workspace/workspace_service.go` | Use `Runtime` instead of `DefaultRuntime` (line 502) |

## Acceptance Criteria

1. `make deepcopy` succeeds
2. `make test` passes
3. Webhook validates Runtime (required), Timeout, MaxRetries
4. `WorkspacePhaseCreating` constant exists
5. No reference to `DefaultRuntime` remains
