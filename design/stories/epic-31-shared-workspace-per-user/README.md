# Epic 31: Shared Workspace Per User (User Drive)

**Status:** Planning
**Created:** 2026-06-04
**Priority:** High
**Depends on:** Epic 6 (Collapse Sandbox into Workspace — shipped), Epic 9 (Configuration & Settings — shipped), Epic 24 (Self-Healing Lifecycle — shipped)

---

## Rationale

Every sandbox currently gets its own PVC-backed workspace mounted at `/workspace`. Files created in one sandbox are not visible from another — there is no way to share scripts, prompts, credentials, or files between workspaces owned by the same user.

Users need a **shared drive** that is mounted into every workspace they create. This enables:

1. **Reusable assets** — frequently used prompts, shell scripts, configuration files, and toolchains stored once and available everywhere
2. **Cross-workspace workflows** — output from one sandbox consumed by another (e.g., a build step in one workspace, a test step in another)
3. **Persistence across workspace lifecycles** — the shared drive outlives any individual sandbox, surviving suspend/resume and workspace recreation
4. **Monetization** — every user gets 5 GB free by default; additional capacity is a billable add-on

The shared drive capacity should be visible in the UI as a progress bar, similar to the existing context/disk/memory usage indicators, placed in the bottom-left status bar between the username and the settings gear icon.

---

## Design Principles

1. **Per-user, not per-workspace**: One shared drive per user account, mounted into every workspace that user creates. Not a per-workspace add-on.
2. **Durable and independent**: The shared drive PVC is separate from the workspace PVC. It outlives workspace suspend/resume and is garbage-collected only when the user account is deleted.
3. **Filesystem semantics**: Standard POSIX filesystem (read/write, directories, permissions). Not an object store. Users interact with it directly from the sandbox terminal and agent.
4. **Accessible from within the sandbox**: Mounted at a well-known path (e.g., `/shared` or `/home/user/shared`) inside every workspace pod.
5. **Capacity-constrained**: Enforced by PVC size and K8s resource quotas. The API tracks usage against the user's plan allocation.
6. **S3 as an alternative backend**: For multi-region or multi-cluster deployments, an S3 bucket (with a CSI driver mount like s3fs or Mountpoint for S3) is an optional backend. PVC is the default.
7. **Usage metering**: Shared drive usage (used bytes vs quota) is tracked, exposed via the API, and rendered in the frontend status bar.
8. **Admin-gated defaults**: The default per-user capacity is an admin setting. Individual user overrides are managed via the admin billing/settings API.

---

## Stated Assumptions

| # | Assumption | Verification |
|---|---|---|
| A1 | PVCs can be mounted as additional volumes in existing workspace pods without disrupting the primary workspace PVC | K8s pod spec supports `spec.volumes` and `spec.containers[].volumeMounts` — multiple PVCs per pod are supported natively |
| A2 | The controller can inject a shared-drive volume into every workspace pod without a CRD change if the user drive is a platform-level construct | The UserDrive is not per-workspace-configurable — it's always mounted. The controller can derive the PVC name from the user ID (`shared-<userID>`) without a new CRD field. If per-workspace opt-out is needed later, a `spec.disableSharedDrive` field can be added. |
| A3 | ReadWriteMany (RWX) PVC is available in the target cluster | Most CSI drivers support RWX (NFS, EFS, Longhorn, Portworx, etc.). For clusters without RWX, the default is ReadWriteOnce (RWO) — the drive is still shared across all the user's workspaces, but only one pod can mount it at a time. The epic defaults to RWO and documents RWX as a deployment option. |
| A4 | S3-backed volumes via CSI driver (e.g., s3fs-csi-driver, Mountpoint for Amazon S3 CSI) work with the same pod volume injection pattern | These drivers implement the standard CSI interface and are compatible with K8s PVC/PV provisioning. The controller code path is identical; only the StorageClass differs. |
| A5 | PVC size enforcement is sufficient for capacity limiting | K8s enforces PVC size at the storage layer. The API additionally meters usage via periodic `df`-style checks or CSI volume metrics. |
| A6 | The frontend already has a status bar area where capacity indicators (context, disk, memory) are rendered | Verified in frontend components: the status bar at the bottom of the layout has slots for resource usage indicators. The shared drive indicator is a new entry in that bar. |
| A7 | Usage data (used bytes, quota bytes) can be fetched from a single API endpoint | The API already has `GET /workspaces/:id/status`. A new endpoint or an extended response can expose `sharedDrive.usedBytes` and `sharedDrive.quotaBytes`. |

---

## Design Questions

| # | Question | Answer | Rationale |
|---|---|---|---|
| DQ1 | Should the shared drive be a new CRD (UserDrive) or a new field on Workspace? | **New CRD: `UserDrive`**. The drive is per-user, not per-workspace. A new CRD cleanly represents the per-user lifecycle (created with user, deleted with user). Workspace reconcilers reference it by user ID. | SRP separation. Workspace CRD is for workspace-level config; UserDrive is a platform-managed resource. |
| DQ2 | Should the shared drive mount path be configurable? | **No (fixed at `/shared`).** Consistency across all workspaces. Users can symlink if they need a different path. | Simplicity. A fixed path means all scripts, prompts, and agent instructions can rely on it. |
| DQ3 | Should users be able to opt out of the shared drive per workspace? | **Yes (future), No (V1).** In V1, the shared drive is always mounted. A per-workspace `spec.disableSharedDrive` may be added later for advanced use cases. | Keep V1 simple. Opt-out can be added without breaking changes. |
| DQ4 | How is capacity usage measured? | **CSI volume metrics + periodic agent-side `statfs` check.** The preferred path is CSI volume metrics (exposed via K8s CSI sidecar). The fallback is an agent-side check via `statfs` on the mount path, reported to the API. | CSI metrics are real-time and don't require agent cooperation. The fallback is needed for environments without CSI volume metrics. |
| DQ5 | S3 or PVC as the default backend? | **PVC (RWO) as default, S3/RWX as configurable alternative.** PVC is simpler, more portable, and doesn't require cloud-specific CSI drivers. S3 is documented as a deployment option for multi-cluster or geo-distributed setups. | PVC works everywhere. S3 requires additional infrastructure. |
| DQ6 | Should the UserDrive CRD support resizing? | **Yes.** When the user upgrades their plan, the admin updates the UserDrive CRD's `spec.size`, and the controller expands the PVC. | Resizing is a key monetization feature. K8s supports PVC expansion for most CSI drivers. |
| DQ7 | How does the frontend fetch usage data? | **Via a new `GET /api/v1/user/drive` endpoint** that returns `{userId, usedBytes, quotaBytes, mountPath}`. The status bar component polls this endpoint (or receives updates via the existing SSE stream). | Dedicated endpoint avoids bloating workspace status responses. SSE integration can come later. |

---

## Domains

### Domain 1: UserDrive CRD
- New CRD: `UserDrive` (namespaced, `llmsafespace.dev/v1`)
- Lifecycle: created on first workspace launch for a user, deleted on user account deletion
- Spec: `userID`, `size` (quantity), `storageClass`, `accessMode` (RWO/RWX)
- Status: `phase` (Pending/Creating/Active/Resizing/Failed), `usedBytes`, `quotaBytes`, `pvcName`

### Domain 2: Controller — UserDrive Reconciler
- Creates/manages PVC for the user's shared drive
- Watches `spec.size` for resize events
- Reports usage metrics to status
- Cleans up on UserDrive deletion
- Prevents deletion while any workspace referencing it exists

### Domain 3: Workspace Pod Injection
- Controller injects the UserDrive PVC as an additional volume in every workspace pod
- Mount path: `/shared`
- Added to the pod builder alongside the primary workspace PVC
- The shared drive is always mounted; no CRD toggle in V1

### Domain 4: Admin Settings
- `userDrive.enabled` (bool, default: `true`) — master toggle
- `userDrive.defaultSize` (string, default: `"5Gi"`) — default capacity per user
- `userDrive.storageClass` (string, default: `""` — cluster default)
- `userDrive.accessMode` (string, default: `"ReadWriteOnce"`)
- `userDrive.maxSizePerUser` (string, default: `"100Gi"`) — hard ceiling

### Domain 5: API Surface
- `GET /api/v1/user/drive` — current usage and quota
- `PUT /api/v1/admin/users/:id/drive` — admin override of shared drive size
- `GET /api/v1/admin/users/:id/drive` — admin view of shared drive status
- `DELETE /api/v1/admin/users/:id/drive` — admin purge of shared drive
- Extended workspace creation/status responses include shared drive info

### Domain 6: Frontend — Status Bar Indicator
- New shared drive capacity bar in the bottom-left status area
- Positioned between the username display and the settings gear icon
- Shows: icon (hard drive/folder) + used/quota (e.g., "1.2 GB / 5 GB") with a progress bar
- Polls `GET /api/v1/user/drive` on interval
- Same visual treatment as context/disk/memory usage indicators

### Domain 7: Usage Metering & Billing Integration
- UsedBytes tracked in the UserDrive status
- API endpoint feeds into the billing system (Epic 12)
- Quota enforcement: agent writes fail with `ENOSPC` when the PVC is full

### Domain 8: S3 Backend Support (Optional)
- Alternative StorageClass pointing to an S3 CSI driver
- Mountpoint for Amazon S3 CSI driver or s3fs-csi-driver
- Same UserDrive CRD, different `spec.storageClass`

---

## Scope

### In Scope

| # | What | Domain |
|---|---|---|
| 1 | UserDrive CRD definition (types, deepcopy, CRD YAML) | UserDrive CRD |
| 2 | UserDrive controller reconciler (create PVC, watch size, report status) | Controller — UserDrive Reconciler |
| 3 | PVC creation from UserDrive `spec.size` with configurable StorageClass | Controller — UserDrive Reconciler |
| 4 | PVC expansion (resize) when `spec.size` changes | Controller — UserDrive Reconciler |
| 5 | Workspace pod builder injection: mount UserDrive PVC at `/shared` | Workspace Pod Injection |
| 6 | Usage reporting: periodic `statfs` or CSI volume metrics → status.usedBytes | Controller — UserDrive Reconciler |
| 7 | Admin settings: enabled, defaultSize, storageClass, accessMode, maxSizePerUser | Admin Settings |
| 8 | API endpoint: `GET /api/v1/user/drive` for current user | API Surface |
| 9 | API endpoint: `PUT /api/v1/admin/users/:id/drive` for admin override | API Surface |
| 10 | API endpoint: `GET /api/v1/admin/users/:id/drive` for admin view | API Surface |
| 11 | API endpoint: `DELETE /api/v1/admin/users/:id/drive` for admin purge | API Surface |
| 12 | UserDrive auto-creation on first workspace launch for a user | Controller — UserDrive Reconciler |
| 13 | UserDrive cleanup on user account deletion (cascading delete) | Controller — UserDrive Reconciler |
| 14 | Prevention of UserDrive deletion while workspaces reference it | Controller — UserDrive Reconciler |
| 15 | Frontend status bar indicator: shared drive capacity bar | Frontend — Status Bar Indicator |
| 16 | DB migration: `user_drives` table mirroring UserDrive CRD for API query performance | API Surface |
| 17 | Authorization: only the owning user can view their own drive; admins can view/modify any | API Surface |

### Out of Scope

| # | What | Why |
|---|---|---|
| 1 | Per-workspace opt-out (`spec.disableSharedDrive`) | V2 concern. V1 always mounts the shared drive. |
| 2 | S3-native UI (file browser, upload/download) | The shared drive is a filesystem mount, not a separate file management UI. Users interact with it via the sandbox terminal/agent. |
| 3 | Cross-user file sharing (share a file from user A to user B) | Security boundary. Each user's shared drive is private to that user. Sharing is a future epic. |
| 4 | Snapshot/backup of shared drives | The PVC is backed by the cluster's storage provisioner. Backup is an infra-level concern. |
| 5 | Quota enforcement at the filesystem level (XFS project quotas, etc.) | PVC size is enforced by K8s at the CSI layer. `ENOSPC` at the filesystem level is sufficient. Advanced quota enforcement is V2. |
| 6 | Shared drive encryption at rest | Inherited from the StorageClass. If the cluster's default StorageClass encrypts volumes, the shared drive is encrypted. |
| 7 | Migration between StorageClasses | If a user's shared drive needs to move from one StorageClass to another, data must be copied. This is a manual admin operation in V1. |
| 8 | Shared drive versioning or trash | The shared drive is a plain filesystem. Versioning is not a platform concern. |

---

## CRD Changes

### New CRD: `UserDrive`

```go
// UserDriveSpec defines the desired state of a user's shared drive.
type UserDriveSpec struct {
    // UserID is the owning user's identifier.
    // +kubebuilder:validation:Required
    UserID string `json:"userId"`

    // Size is the capacity of the shared drive (e.g. "5Gi", "50Gi").
    // +kubebuilder:validation:Pattern=^(\d+(\.\d+)?)([KMGTPE]i?)$
    Size resource.Quantity `json:"size"`

    // StorageClass is the PVC StorageClass name. Empty = cluster default.
    // +kubebuilder:validation:Optional
    StorageClass string `json:"storageClass,omitempty"`

    // AccessMode is the PVC access mode (ReadWriteOnce or ReadWriteMany).
    // +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany
    // +kubebuilder:default=ReadWriteOnce
    AccessMode corev1.PersistentVolumeAccessMode `json:"accessMode,omitempty"`
}

// UserDriveStatus defines the observed state of a user's shared drive.
type UserDriveStatus struct {
    // Phase is the current lifecycle phase.
    // +kubebuilder:validation:Enum=Pending;Creating;Active;Resizing;Failed
    Phase UserDrivePhase `json:"phase,omitempty"`

    // PVCName is the name of the backing PVC.
    PVCName string `json:"pvcName,omitempty"`

    // UsedBytes is the current storage usage in bytes.
    UsedBytes int64 `json:"usedBytes,omitempty"`

    // QuotaBytes is the PVC capacity in bytes (derived from spec.size).
    QuotaBytes int64 `json:"quotaBytes,omitempty"`

    // Conditions represent the latest observations of the UserDrive state.
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// UserDrivePhase is a valid phase for a UserDrive.
// +kubebuilder:validation:Enum=Pending;Creating;Active;Resizing;Failed
type UserDrivePhase string

const (
    UserDrivePending  UserDrivePhase = "Pending"
    UserDriveCreating UserDrivePhase = "Creating"
    UserDriveActive   UserDrivePhase = "Active"
    UserDriveResizing UserDrivePhase = "Resizing"
    UserDriveFailed   UserDrivePhase = "Failed"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="User",type="string",JSONPath=".spec.userId"
// +kubebuilder:printcolumn:name="Size",type="string",JSONPath=".spec.size"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Used",type="integer",JSONPath=".status.usedBytes"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type UserDrive struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   UserDriveSpec   `json:"spec,omitempty"`
    Status UserDriveStatus `json:"status,omitempty"`
}
```

### New Conditions

```go
const (
    ConditionUserDriveReady    ConditionType = "Ready"
    ConditionUserDriveResizing ConditionType = "Resizing"
    ConditionUserDriveFull     ConditionType = "DiskFull"
)
```

### UserDrive lifecycle

```
Pending → Creating → Active → (PVC resize triggered) → Resizing → Active
                                       ↘
                         Failed (PVC creation error, resize error)
```

### Owner reference chain

```
User (external) → UserDrive (CRD) → PersistentVolumeClaim (PVC)
```

When user account is deleted, the UserDrive CRD is deleted (by the controller or admin API), and the PVC is garbage-collected via K8s owner references.

---

## Admin Settings

Add to `InstanceSettings()`:

| Key | Default | Type | Valid Values | Category | Description |
|---|---|---|---|---|---|
| `userDrive.enabled` | `true` | bool | | User Drive | Master toggle for shared drive auto-provisioning |
| `userDrive.defaultSize` | `5Gi` | string | quantity (e.g. `1Gi`, `10Gi`, `100Gi`) | User Drive | Default capacity per user |
| `userDrive.storageClass` | `""` | string | StorageClass name | User Drive | StorageClass for shared drive PVCs (empty = cluster default) |
| `userDrive.accessMode` | `ReadWriteOnce` | string | `ReadWriteOnce`, `ReadWriteMany` | User Drive | PVC access mode |
| `userDrive.maxSizePerUser` | `100Gi` | string | quantity | User Drive | Hard ceiling for per-user capacity |

---

## API Surface

### User endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/user/drive` | JWT/API Key | Get current user's shared drive info (usedBytes, quotaBytes, mountPath) |

Response body:
```json
{
  "userId": "usr_abc123",
  "usedBytes": 1234567890,
  "quotaBytes": 5368709120,
  "usedPercent": 23,
  "mountPath": "/shared"
}
```

### Admin endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `PUT` | `/api/v1/admin/users/:id/drive` | JWT (admin) | Admin override of shared drive size (body: `{size: "50Gi"}`) |
| `GET` | `/api/v1/admin/users/:id/drive` | JWT (admin) | Admin view of shared drive status |
| `DELETE` | `/api/v1/admin/users/:id/drive` | JWT (admin) | Purge shared drive (deletes PVC, requires force flag if workspaces exist) |

---

## Workspace Pod Injection

### Pod spec changes in `buildPod()`

The shared drive is injected as an additional volume. The volume name is `shared-drive`, mounted at `/shared`:

```go
func (r *WorkspaceReconciler) addSharedDriveVolume(
    pod *corev1.Pod,
    userDrive *v1.UserDrive,
) {
    pvcName := userDrive.Status.PVCName
    if pvcName == "" {
        return // drive not yet provisioned
    }

    vol := corev1.Volume{
        Name: "shared-drive",
        VolumeSource: corev1.VolumeSource{
            PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
                ClaimName: pvcName,
                ReadOnly:  false,
            },
        },
    }

    mount := corev1.VolumeMount{
        Name:      "shared-drive",
        MountPath: "/shared",
        ReadOnly:  false,
    }

    pod.Spec.Volumes = append(pod.Spec.Volumes, vol)
    // Mount in the main workspace container (index 0 after init containers).
    // In V2 pod spec, the main container is at containers[0].
    pod.Spec.Containers[0].VolumeMounts = append(
        pod.Spec.Containers[0].VolumeMounts, mount,
    )
}
```

### UserDrive auto-creation

When a user creates their first workspace and no UserDrive CRD exists for them yet, the controller (or the API service) creates a UserDrive with `spec.size` from the admin default setting. The UserDrive reconciler then provisions the PVC.

This can be triggered:
1. **In the worksapce API handler** — `POST /workspaces` checks for existing UserDrive; if absent, creates one before proceeding
2. **In the workspace reconciler** — the reconciler checks if a UserDrive exists for `workspace.Spec.UserID`; if not, creates it

Option 1 is preferred (API-level creation) to avoid delaying the workspace reconciler with PVC provisioning.

### Usage reporting

The controller periodically (every 5 minutes) checks usage:

```go
func (r *UserDriveReconciler) reportUsage(ctx context.Context, drive *v1.UserDrive) error {
    // Preferred: CSI volume metrics via VolumeAttachment status
    // Fallback: exec into a pod with the PVC mounted and run statfs

    // For the fallback path, find any workspace pod that belongs to this user
    // and has the shared drive mounted, then exec:
    // df --output=used,size /shared --block-size=1
    // Parse used and total bytes.

    return nil
}
```

The status is updated on the UserDrive CRD and mirrored to the `user_drives` DB table for API query performance.

---

## Frontend — Status Bar Indicator

### Location

Bottom-left status bar, between the username display and the settings gear icon:

```
┌──────────────────────────────────────────────────────────────┐
│ [Sandbox: running]  [opencode v0.42.0]                       │
│                                                               │
│                         (main content area)                    │
│                                                               │
│ username@example.com  [═══░░░░░ 1.2/5 GB]  [⚙️]  [📁 2.3GB] │
│                       ↑───────────↑                            │
│                      Shared Drive                            │
└──────────────────────────────────────────────────────────────┘
```

### Component behaviour

- Same visual pattern as existing context/disk/memory usage bars (see frontend components in `frontend/src/components/StatusBar/`)
- Shows: drive icon + `used / quota` with a slim horizontal progress bar
- Colour transitions: green (<70%) → yellow (70–90%) → red (>90%)
- Tooltip on hover: "Shared Drive — 1.2 GB of 5 GB used"
- Polls `GET /api/v1/user/drive` every 30 seconds
- Falls back to cached value if endpoint returns 5xx
- Hidden if `userDrive.enabled` is false (via existing settings SSE stream)

---

## User Stories

| Story | Title | Domain | Depends On | Key Acceptance Criteria |
|---|---|---|---|---|
| US-31.1 | UserDrive CRD definition and registration | UserDrive CRD | None | CRD types defined in `controller/internal/resources/`. Deepcopy generated. CRD YAML at `pkg/crds/`. Schema validated by webhook. |
| US-31.2 | UserDrive controller reconciler | Controller — UserDrive Reconciler | US-31.1 | Reconciler creates PVC from `spec.size`. Sets `status.phase` and `status.pvcName`. Watches `spec.size` changes → triggers PVC expansion. PVC owner-referenced to UserDrive. |
| US-31.3 | Admin settings for shared drive | Admin Settings | None | Settings in `InstanceSettings()`. Validated by schema. Accessible via `PUT /admin/settings/:key`. |
| US-31.4 | Shared drive auto-creation on first workspace | Controller — UserDrive Reconciler | US-31.1, US-31.2, US-31.3 | When user creates first workspace, UserDrive CRD auto-created with `spec.size` from admin default. PVC provisioned. |
| US-31.5 | Workspace pod injection: mount shared drive | Workspace Pod Injection | US-31.2 | Every workspace pod mounts the UserDrive PVC at `/shared`. Volume added in `buildPod()`. Workspace `restartGeneration` required to pick up new mounts. |
| US-31.6 | Shared drive usage reporting | Controller — UserDrive Reconciler | US-31.2 | Controller reports `status.usedBytes` periodically. Used bytes updated at least every 5 minutes. Quota bytes mirror PVC capacity. |
| US-31.7 | API: `GET /api/v1/user/drive` | API Surface | US-31.2, US-31.6 | Returns usedBytes, quotaBytes, usedPercent, mountPath for authenticated user. 404 if user has no shared drive. |
| US-31.8 | API: Admin drive management endpoints | API Surface | US-31.2, US-31.6 | `PUT /admin/users/:id/drive` updates UserDrive `spec.size`. `GET` returns status. `DELETE` purges drive (with force flag). Admin auth required. |
| US-31.9 | DB migration: `user_drives` table | API Surface | None | New table mirrors UserDrive CRD status for fast API queries. Migrated on startup. |
| US-31.10 | Frontend: shared drive status bar indicator | Frontend — Status Bar Indicator | US-31.7 | Capacity bar rendered in bottom-left status area. Polls `/user/drive`. Progress bar with colour transitions. Tooltip with details. |
| US-31.11 | UserDrive cleanup on user deletion | Controller — UserDrive Reconciler | US-31.2 | When user account deleted, UserDrive CRD deleted. PVC garbage-collected via owner reference. Workspaces without owner may need manual cleanup. |
| US-31.12 | UserDrive resize orchestration | Controller — UserDrive Reconciler | US-31.2 | Admin updates `spec.size`. Reconciler expands PVC. K8s PVC resize may require pod restart. Controller handles in-progress resize state. |
| US-31.13 | Shared drive capacity exceeded handling | Controller — UserDrive Reconciler | US-31.6 | When `usedBytes >= quotaBytes`, set `ConditionUserDriveFull`. Frontend shows "Shared Drive Full" warning. Agent operations writing to `/shared` receive `ENOSPC`. |
| US-31.14 | Shared drive S3 backend support (optional) | S3 Backend Support | US-31.2 | Alternative StorageClass configured via admin settings. CSI driver for S3 provisioned at cluster level. Platform code unchanged; only StorageClass differs. |

### Dependency Graph

```
US-31.3 (admin settings) ──┬── US-31.1 (CRD) ──┬── US-31.2 (reconciler)
                            │                    │
                            │                    ├── US-31.4 (auto-create) ── US-31.5 (pod injection)
                            │                    ├── US-31.6 (usage reporting)
                            │                    ├── US-31.11 (cleanup)
                            │                    ├── US-31.12 (resize)
                            │                    └── US-31.13 (capacity)
                            │
                            ├── US-31.7 (API: user drive) ── US-31.10 (frontend)
                            ├── US-31.8 (API: admin drive)
                            ├── US-31.9 (DB migration)
                            └── US-31.14 (S3 backend)
```

### Critical Path

```
Phase 1: US-31.3 → US-31.1 → US-31.2 (CRD + reconciler + admin settings)
Phase 2: US-31.4 + US-31.5 (auto-creation + pod injection)
Phase 3: US-31.6 + US-31.7 + US-31.8 + US-31.9 (usage reporting + API)
Phase 4: US-31.10 (frontend status bar)
Phase 5: US-31.11 + US-31.12 + US-31.13 (lifecycle management)
Phase 6: US-31.14 (S3 backend — optional, can be deferred)
```

---

## Test Plan

### Unit Tests

| Story | Test | What It Proves |
|---|---|---|
| US-31.1 | `TestUserDriveSpec_MarshalUnmarshal` | CRD types marshal/unmarshal correctly |
| US-31.1 | `TestUserDriveSpec_Validation` | Validation tags enforced (size pattern, accessMode enum) |
| US-31.2 | `TestUserDriveReconciler_CreatePVC` | Reconciler creates PVC with correct size, storageClass, accessMode |
| US-31.2 | `TestUserDriveReconciler_PVCExists_Skip` | Reconciler skips PVC creation if it already exists |
| US-31.2 | `TestUserDriveReconciler_ResizePVC` | Reconciler expands PVC when `spec.size` increases |
| US-31.2 | `TestUserDriveReconciler_ResizeDown_Error` | Shrinking PVC returns error (K8s limitation) |
| US-31.2 | `TestUserDriveReconciler_PVCFailure_StatusFailed` | PVC creation failure sets phase=Failed |
| US-31.3 | `TestUserDriveSettings_Defaults` | Default settings are sane (enabled=true, defaultSize=5Gi) |
| US-31.4 | `TestAutoCreate_FirstWorkspace` | UserDrive created when user creates first workspace |
| US-31.4 | `TestAutoCreate_ExistingDrive_Skip` | UserDrive not created if one already exists for user |
| US-31.5 | `TestBuildPod_SharedDriveMounted` | Pod spec includes shared-drive volume mounted at `/shared` |
| US-31.5 | `TestBuildPod_NoDrive_NoMount` | Pod built normally when UserDrive has no PVC yet |
| US-31.5 | `TestBuildPod_VolumeNotDuplicated` | Shared drive volume not added twice on re-reconciliation |
| US-31.6 | `TestUsageReport_CSI_Success` | Usage correctly parsed from CSI metrics |
| US-31.6 | `TestUsageReport_ExecFallback_Success` | Usage correctly parsed from `statfs` output |
| US-31.6 | `TestUsageReport_UpdateStatus` | `status.usedBytes` and `status.quotaBytes` updated after report |
| US-31.7 | `TestGetUserDrive_Success` | API returns correct usedBytes, quotaBytes, mountPath |
| US-31.7 | `TestGetUserDrive_NoDrive_404` | API returns 404 if user has no shared drive |
| US-31.7 | `TestGetUserDrive_WrongUser_404` | User cannot see another user's drive |
| US-31.8 | `TestAdminUpdateDriveSize_Success` | Admin can update shared drive size |
| US-31.8 | `TestAdminUpdateDriveSize_ExceedsMax` | Size exceeding `maxSizePerUser` rejected (400) |
| US-31.8 | `TestAdminDeleteDrive_NoForce_Blocked` | Delete without force fails if workspaces reference the drive |
| US-31.8 | `TestAdminDeleteDrive_Force_Success` | Delete with force succeeds and cleans up PVC |
| US-31.10 | `TestStatusBar_RendersCapacity` | Shared drive indicator renders with correct used/quota values |
| US-31.10 | `TestStatusBar_PollsEndpoint` | Component polls `GET /user/drive` on interval |
| US-31.10 | `TestStatusBar_ColorTransitions` | Bar colour changes at 70% and 90% thresholds |
| US-31.10 | `TestStatusBar_HiddenWhenDisabled` | Bar not rendered when `userDrive.enabled` is false |
| US-31.11 | `TestUserDrive_CleanupOnUserDelete` | UserDrive deleted when owning user is deleted |
| US-31.11 | `TestUserDrive_PVCGarbageCollected` | PVC deleted via owner reference when UserDrive deleted |
| US-31.12 | `TestResize_ExpandsPVC` | PVC expansion triggered on `spec.size` increase |
| US-31.12 | `TestResize_RequiresRestart` | Condition set indicating pod restart may be needed |
| US-31.13 | `TestCapacityExceeded_ConditionSet` | `ConditionUserDriveFull` set when `usedBytes >= quotaBytes` |

### Integration Tests (envtest)

| # | Test | What It Proves |
|---|---|---|
| I1 | Create UserDrive → PVC created with correct size and StorageClass | Full reconciler → PVC provisioning works |
| I2 | Create UserDrive → update `spec.size` → PVC expanded | Resize orchestration works |
| I3 | Create workspace for user with UserDrive → pod has `/shared` mount | Pod injection works end-to-end |
| I4 | Create workspace for user without UserDrive → drive auto-created + mounted | Auto-creation + injection pipeline works |
| I5 | Delete UserDrive → PVC garbage-collected | Cleanup works |
| I6 | Delete UserDrive with active workspaces (force=false) → blocked | Safety check works |
| I7 | UserDrive PVC creation fails → status phase=Failed | Error reporting works |
| I8 | Usage report updated → status.usedBytes reflects actual usage | Usage reporting works |

### E2E Tests (real cluster or kind)

| # | Test | What It Proves |
|---|---|---|
| E1 | User creates workspace → shared drive PVC created → `/shared` is writable and persistent | Shared drive works end-to-end |
| E2 | User writes file to `/shared` in workspace A → suspends A → resumes workspace B → file visible in B | Cross-workspace persistence works |
| E3 | Admin increases user's shared drive size → PVC resized → `df -h /shared` shows new size | Resize works end-to-end |
| E4 | User fills shared drive → agent receives `ENOSPC` writing to `/shared` | Capacity enforcement works |
| E5 | Delete user account → shared drive PVC cleaned up | Lifecycle cleanup works |

---

## Observability: New Metrics

```go
// Gauges
llmsafespace_userdrive_total{phase="active|pending|failed"}
llmsafespace_userdrive_size_bytes{userId}  // quota per user
llmsafespace_userdrive_used_bytes{userId}  // actual usage per user
llmsafespace_userdrive_used_percent{userId}

// Counters
llmsafespace_userdrive_create_total{status="success|failed"}
llmsafespace_userdrive_resize_total{status="success|failed"}
llmsafespace_userdrive_delete_total
```

---

## Rollout Plan

### Phase 1: Foundation (US-31.3, US-31.1, US-31.2)
- Admin settings for shared drive defaults
- UserDrive CRD definition
- UserDrive controller reconciler (PVC create/resize/delete)
- **Impact**: Platform can manage per-user drives, but they are not yet exposed or mounted

### Phase 2: Integration (US-31.4, US-31.5)
- Auto-creation on first workspace
- Pod injection (mount at `/shared`)
- **Impact**: Every workspace gets a shared drive. Users can start using `/shared` immediately.

### Phase 3: API + Frontend (US-31.6, US-31.7, US-31.8, US-31.9, US-31.10)
- Usage reporting
- User/admin API endpoints
- DB migration for query performance
- Frontend status bar indicator
- **Impact**: Users can see their shared drive capacity in the UI. Admins can manage drives.

### Phase 4: Lifecycle (US-31.11, US-31.12, US-31.13)
- Cleanup on user deletion
- Resize orchestration
- Capacity-exceeded handling
- **Impact**: Full lifecycle management.

### Phase 5: S3 Backend (US-31.14)
- Alternative StorageClass for S3-backed drives
- **Impact**: Multi-cluster and geo-distributed deployments supported.

---

## Risks

| Risk | Mitigation |
|---|---|
| PVC size expansion fails (StorageClass does not support `allowVolumeExpansion`) | Controller detects expansion support via StorageClass `allowVolumeExpansion` field. Falls back to setting `ConditionUserDriveResizing` and logging error. Admin must migrate PVC manually. |
| Shared drive mount disrupts workspace startup if PVC is not yet provisioned | Controller defers pod creation until UserDrive is in `Active` phase. Alternatively, pod starts without shared drive and a secondary reconciliation adds it (requires pod restart). |
| RWX access mode not available in target cluster | Default is RWO. RWX is explicitly configured via admin settings. If RWX is set but unavailable, the controller logs a warning and falls back to RWO. |
| User creates many workspaces → same PVC mounted in many pods → IO contention | With RWO, only one pod can mount at a time. RWX resolves this. If RWO is the only option, the shared drive is available only in actively running workspaces (one at a time per user). |
| `statfs`-based usage reporting requires a running workspace pod | If no workspace pod is running, usage reporting is skipped. The last known `usedBytes` is retained. |
| S3 CSI driver has different performance characteristics than PVC | Documented as an advanced deployment option. Not recommended for latency-sensitive workloads. |
| Deleting a user with an active workspace leaves the workspace with a missing shared drive | Workspace pod continues running — the shared drive volume mount will fail to attach, but the workspace container itself is unaffected. The workspace CRD remains; the user's Workspace CRDs must be cleaned up before or alongside the UserDrive. |
