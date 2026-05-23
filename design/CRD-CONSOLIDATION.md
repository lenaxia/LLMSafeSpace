# CRD Type Consolidation — Field Inventory & Reconciliation

**Status:** Plan / pre-implementation field inventory
**Goal:** Eliminate the three parallel CRD-shaped Go type definitions and the `roundtrip_test.go` tripwire that exists only because the duplication exists.
**Backwards compatibility:** Not required. Project is not live yet.

---

## 1. Problem statement

The repository currently defines each CRD-shaped object in **three places**:

| Location | Purpose (in theory) | What's actually there |
|---|---|---|
| `pkg/apis/llmsafespace/v1/types.go` (272 lines) + `deepcopy.go` (399 lines, hand-written) | "API service's view of CRDs" | Hand-rolled types. Some align with deployed YAML, some don't. Has `SecurityCtx` field name, missing `CPUPinning`. RuntimeEnvironment uses `BaseImage` / `Packages` / `Ready`. SandboxProfile uses an entirely different schema. |
| `controller/internal/resources/*_types.go` (601 lines) + `*_deepcopy.go` (hand-written) | Controller-side authoritative types with kubebuilder annotations | Authoritative kubebuilder IDL. Drives the YAML in `pkg/crds/*.yaml` via `controller-gen crd`. Has `SecurityContext` field name, has `CPUPinning`. RuntimeEnvironment uses `Image` / `PreInstalledPackages` / `Available`. |
| `pkg/types/types.go` (753 lines) | API DTOs returned to clients | Mostly DTOs, but `Sandbox` (and several unused types) inappropriately embed `metav1.TypeMeta`/`ObjectMeta` and carry dead `+k8s:deepcopy-gen` directives. |

The deployed CRD YAMLs in `pkg/crds/*.yaml` are generated from the **controller-side** types via `controller/Makefile` line 21:

```
$(CONTROLLER_GEN) crd paths="./..." output:crd:artifacts:config=config/crd/bases
```

Therefore **the deployed schema = controller/internal/resources schema**. The `pkg/apis/llmsafespace/v1` schema is decorative; where it diverges from the deployed YAML, it would either be silently pruned by Kubernetes (CRD v1 default behavior) or rejected (when required fields are renamed).

`controller/internal/resources/roundtrip_test.go` exists explicitly to catch divergences between `pkg/apis/llmsafespace/v1` and `controller/internal/resources` because they must serialize byte-compatibly through etcd. The test (305 lines) covers Sandbox + Workspace and a subset of fields. It does not cover RuntimeEnvironment, SandboxProfile, `SecurityContext`, `CPUPinning`, or any of several other fields. Drift outside its asserted set ships silently.

This is the design tax. We're paying for parallel maintenance with a tripwire that has gaps.

---

## 2. Decision summary

| Decision | Choice | Rationale |
|---|---|---|
| Target package for the unified CRD types | `pkg/apis/llmsafespace/v1/` | Idiomatic K8s package layout. Importable from both `api/` and `controller/`. |
| Authoritative content source | `controller/internal/resources/*_types.go` | Bears kubebuilder annotations; matches deployed YAML; has webhooks. |
| Deepcopy generation | `controller-gen object` against the new location | Replaces hand-rolled `pkg/apis/llmsafespace/v1/deepcopy.go` and `controller/internal/resources/*_deepcopy.go`. Both currently lack `DO NOT EDIT` headers and are out of sync with the field sets. |
| YAML CRD generation | `controller-gen crd` against the new location | No content change to `pkg/crds/*.yaml` should result. Verify by diff. |
| Webhook handlers | Move into `controller/internal/webhooks/` (new package) | Cleaner separation than co-locating webhook business logic with type definitions. Webhooks can import `pkg/apis/llmsafespace/v1` like any other consumer. |
| API DTO layer (`pkg/types`) | Strip `metav1` embedding from `Sandbox`; delete dead types and dead deepcopy directives; replace `*metav1.Time` with `*time.Time` | Matches the worklog 0034 frontend's expectation of a clean Swagger/TS contract. |
| `roundtrip_test.go` | Delete | A round-trip test between two copies of the same type is meaningless when there is one type. |
| Controller-runtime `corev1.ConditionStatus` for `WorkspaceCondition.Status` | **Replace with `string`** in the unified type | The deployed YAML schema declares `enum: ["True", "False", "Unknown"]` of type `string`, not the Go type `corev1.ConditionStatus`. JSON serializes both as a string, but the unified type should not depend on `k8s.io/api/core/v1` for what is just an enum. Eliminates a heavy import. |

---

## 3. Field inventory matrices

For each CRD, every field is shown across all three sources. **`controller/internal/resources` ("ctrl") is treated as authoritative for content** because it produces the deployed YAML. Where `pkg/apis/llmsafespace/v1` ("apis") has a different name or shape, it is dropped. Where it has a field "ctrl" lacks, it's verified against the YAML and either added or dropped as appropriate.

Legend:

- **K** = kept in unified type (matches deployed YAML)
- **D** = dropped (not in YAML; dead field)
- **A** = added to unified type (in YAML and ctrl, missing from apis)
- **R** = renamed (YAML JSON tag is authoritative)

---

### 3.1 Sandbox

#### SandboxSpec

| JSON tag | YAML required? | apis Go field | ctrl Go field | Decision | Notes |
|---|---|---|---|---|---|
| `runtime` | yes | `Runtime` | `Runtime` | **K** | Identical. |
| `securityLevel` | no (default `standard`) | `SecurityLevel` | `SecurityLevel` | **K** | apis lacks kubebuilder enum/default; ctrl has it. Use ctrl. |
| `timeout` | no (default 300, 1-3600) | `Timeout` | `Timeout` | **K** | apis lacks min/max/default validation. Use ctrl. |
| `resources` | no | `*ResourceRequirements` | `*ResourceRequirements` | **K** | See ResourceRequirements below for inner divergence. |
| `networkAccess` | no | `*NetworkAccess` | `*NetworkAccess` | **K** | Same shape. |
| `filesystem` | no | `*FilesystemConfig` | `*FilesystemConfig` | **K** | Same shape. |
| `storage` | no | `*StorageConfig` | `*StorageConfig` | **K** | Same shape. |
| `securityContext` | no | `SecurityCtx *SecurityContext` (Go field renamed) | `SecurityContext *SecurityContext` | **R** | Go field name unifies as `SecurityContext`. JSON tag was always `securityContext`. apis's `SecurityCtx` Go name is dropped. |
| `profileRef` | no | `*ProfileReference` | `*ProfileReference` | **K** | Same shape. |
| `workspaceRef` | no | `WorkspaceRef string` | `WorkspaceRef string` | **K** | Same. |

#### ResourceRequirements (Sandbox-side)

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `cpu` | yes | `CPU` | `CPU` | **K** | |
| `memory` | yes | `Memory` | `Memory` | **K** | |
| `ephemeralStorage` | yes | `EphemeralStorage` | `EphemeralStorage` | **K** | |
| `cpuPinning` | yes (default false) | **MISSING** | `CPUPinning bool` | **A** | apis omits this entirely. The deployed CRD allows it. Adding it to the unified type. Currently no Go code reads/writes `CPUPinning`, so this is purely a plumbing fix. |

#### NetworkAccess

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `egress` | no | `[]EgressRule` | `[]EgressRule` | **K** | |
| `ingress` | no (default false) | `bool` | `bool` | **K** | |

#### EgressRule

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `domain` | yes | `string` | `string` | **K** |
| `ports` | no | `[]PortRule` | `[]PortRule` | **K** |

#### PortRule

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `port` | yes (1-65535) | `int` | `int` | **K** | |
| `protocol` | no (default TCP, enum TCP/UDP) | `string` | `string` | **K** | apis lacks validation tags. |

#### FilesystemConfig

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `readOnlyRoot` | no (default true) | `bool` | `bool` | **K** |
| `writablePaths` | no (default `["/tmp","/workspace"]`) | `[]string` | `[]string` | **K** |

#### StorageConfig

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `persistent` | no (default false) | `bool` | `bool` | **K** |
| `volumeSize` | no (default 5Gi) | `string` | `string` | **K** |

#### SecurityContext

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `runAsUser` | no (default 1000) | `int64` | `int64` | **K** | |
| `runAsGroup` | no (default 1000) | `int64` | `int64` | **K** | |
| `seccompProfile` | no | `*SeccompProfile` | `*SeccompProfile` | **K** | Same shape inside. |

#### SeccompProfile

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `type` | yes (enum RuntimeDefault/Localhost) | `string` | `string` | **K** |
| `localhostProfile` | no | `string` | `string` | **K** |

#### ProfileReference

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `name` | yes | `string` | `string` | **K** |
| `namespace` | no | `string` | `string` | **K** |

#### SandboxStatus

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `phase` | no (enum) | `string` | `string` | **K** | |
| `conditions` | no | `[]SandboxCondition` | `[]SandboxCondition` | **K** | |
| `podName` | no | `string` | `string` | **K** | |
| `podNamespace` | no | `string` | `string` | **K** | apis already has it. |
| `podIP` | no | `string` | `string` | **K** | |
| `startTime` | no | `*metav1.Time` | `*metav1.Time` | **K** | Same. |
| `endpoint` | no | `string` | `string` | **K** | |
| `resources` | no | `*ResourceStatus` | `*ResourceStatus` | **K** | |
| `lastActivityAt` | no | `*metav1.Time` | `*metav1.Time` | **K** | |

#### SandboxCondition

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `type` | yes | `string` | `string` | **K** | |
| `status` | yes (enum True/False/Unknown) | `string` | `string` | **K** | |
| `reason` | no | `string` | `string` | **K** | |
| `message` | no | `string` | `string` | **K** | |
| `lastTransitionTime` | no | `metav1.Time` | `metav1.Time` | **K** | Note: not a pointer. |

#### ResourceStatus

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `cpuUsage` | no | `string` | `string` | **K** |
| `memoryUsage` | no | `string` | `string` | **K** |

**Sandbox totals:** 1 rename (`SecurityCtx` → `SecurityContext`), 1 addition (`CPUPinning`), no drops, no semantic changes. All other fields are identical between apis and ctrl by JSON tag.

---

### 3.2 Workspace

#### WorkspaceSpec

| JSON tag | YAML required? | apis | ctrl | Decision |
|---|---|---|---|---|
| `owner` | yes | `WorkspaceOwner` | `WorkspaceOwner` | **K** |
| `defaultRuntime` | no | `string` | `string` | **K** |
| `securityLevel` | no (enum standard/high) | `string` | `string` | **K** |
| `storage` | yes | `WorkspaceStorageConfig` | `WorkspaceStorageConfig` | **K** |
| `networkAccess` | no | `*WorkspaceNetworkAccess` | `*WorkspaceNetworkAccess` | **K** |
| `autoSuspend` | no | `*WorkspaceAutoSuspend` | `*WorkspaceAutoSuspend` | **K** |
| `ttlSecondsAfterSuspended` | no (default 0) | `int64` | `int64` | **K** |
| `packages` | no | `[]WorkspacePackageSet` | `[]WorkspacePackageSet` | **K** |
| `initScript` | no | `string` | `string` | **K** |
| `maxActiveSessions` | no (default 5, 1-20) | `int32` | `int32` | **K** |
| `credentials` | no | `*WorkspaceCredentialRef` | `*WorkspaceCredentialRef` | **K** |

#### WorkspaceOwner

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `userID` | yes | `string` | `string` | **K** |

#### WorkspaceStorageConfig

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `size` | yes (pattern `^[0-9]+(Gi|Mi)$`) | `string` | `string` | **K** |
| `storageClassName` | no | `string` | `string` | **K** |
| `accessMode` | no (enum, default ReadWriteOnce) | `string` | `string` | **K** |

#### WorkspaceNetworkAccess

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `egress` | no | `[]WorkspaceEgressRule` | `[]WorkspaceEgressRule` | **K** |
| `ingress` | no (default false) | `bool` | `bool` | **K** |

#### WorkspaceEgressRule

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `domain` | yes | `string` | `string` | **K** |

#### WorkspaceAutoSuspend

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `enabled` | no (default false) | `bool` | `bool` | **K** |
| `idleTimeoutSeconds` | no (min 1, default 3600) | `int64` | `int64` | **K** |

#### WorkspacePackageSet

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `runtime` | yes | `string` | `string` | **K** |
| `requirements` | yes | `[]string` | `[]string` | **K** |

#### WorkspaceCredentialRef

| JSON tag | YAML | apis | ctrl | Decision |
|---|---|---|---|---|
| `secretName` | no (but this is the only field) | `string` | `string` | **K** |

#### WorkspaceStatus

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `phase` | no (enum) | `WorkspacePhase` | `WorkspacePhase` | **K** | Both have the same phase enum. |
| `pvcName` | no | `string` | `string` | **K** | |
| `activeSessions` | no | `int32` | `int32` | **K** | |
| `lastActivityAt` | no | `*metav1.Time` | `*metav1.Time` | **K** | |
| `suspendedAt` | no | `*metav1.Time` | `*metav1.Time` | **K** | |
| `conditions` | no | `[]WorkspaceCondition` | `[]WorkspaceCondition` | **K** | See below for inner divergence. |
| `message` | no | `string` | `string` | **K** | |
| `observedGeneration` | no | `int64` | `int64` | **K** | |

#### WorkspaceCondition

| JSON tag | YAML | apis | ctrl | Decision | Notes |
|---|---|---|---|---|---|
| `type` | yes | `string` | `WorkspaceConditionType` (string typedef) | **K** | Use `WorkspaceConditionType` typedef from ctrl for type safety on known condition types (Ready, PVCReady, Suspended). |
| `status` | yes (enum True/False/Unknown) | `string` | `corev1.ConditionStatus` | **R** | Replace `corev1.ConditionStatus` with plain `string`. Removes `k8s.io/api/core/v1` dependency from the type package. JSON shape unchanged. |
| `lastTransitionTime` | no | `metav1.Time` | `metav1.Time` | **K** | |
| `reason` | no | `string` | `string` | **K** | |
| `message` | no | `string` | `string` | **K** | |

**Workspace totals:** 1 rename (`corev1.ConditionStatus` → `string`), no field additions/drops. The roundtrip test passing for Workspace confirms the YAML and Go representations are aligned.

---

### 3.3 RuntimeEnvironment — completely incompatible schemas

This CRD has **divergent JSON tags** between the two Go sources. The YAML matches `controller/internal/resources` exactly. The `pkg/apis/llmsafespace/v1` version is dead code — it could not be persisted to the cluster because the required field `image` is missing (it has `baseImage` instead).

#### RuntimeEnvironmentSpec

| YAML field | YAML required? | apis Go (JSON tag) | ctrl Go (JSON tag) | Decision |
|---|---|---|---|---|
| `image` | **yes** | `BaseImage` (`baseImage`) — **wrong tag, would fail validation** | `Image` (`image`) | **K** ctrl. apis dropped. |
| `language` | yes | `Language` (`language`) | `Language` (`language`) | **K** |
| `version` | no | `Version` (`version`) | `Version` (`version`) | **K** |
| `tags` | no | **MISSING** | `Tags []string` (`tags`) | **A** |
| `preInstalledPackages` | no | `Packages` (`packages`) — **wrong tag** | `PreInstalledPackages` (`preInstalledPackages`) | **K** ctrl. apis dropped. |
| `packageManager` | no | **MISSING** | `PackageManager string` | **A** |
| `securityFeatures` | no | **MISSING** | `SecurityFeatures []string` | **A** |
| `resourceRequirements` | no | **MISSING** | `ResourceRequirements *RuntimeResourceRequirements` | **A** |

#### RuntimeResourceRequirements (new in unified type — adopted from ctrl)

| JSON tag | apis | ctrl | Decision |
|---|---|---|---|
| `minCpu` | n/a | `MinCPU` | **A** |
| `minMemory` | n/a | `MinMemory` | **A** |
| `recommendedCpu` | n/a | `RecommendedCPU` | **A** |
| `recommendedMemory` | n/a | `RecommendedMemory` | **A** |

#### RuntimeEnvironmentStatus

| YAML field | apis (JSON tag) | ctrl (JSON tag) | Decision |
|---|---|---|---|
| `available` | `Ready` (`ready`) — **wrong tag** | `Available` (`available`) | **K** ctrl. apis dropped. |
| `lastValidated` | `LastUpdateTime` (`lastUpdateTime`) — **wrong tag** | `LastValidated *metav1.Time` (`lastValidated`) | **K** ctrl. apis dropped. |

**RuntimeEnvironment totals:** Three renames (3 wrong JSON tags from apis), four additions from ctrl, zero kept fields from apis. The apis version is **completely replaced** by the ctrl version. No production code reads from the API-side type today; confirmed by grep of `BaseImage` and `RuntimeEnvironment` usage.

---

### 3.4 SandboxProfile — completely incompatible schemas

Like RuntimeEnvironment, SandboxProfile has parallel definitions with **zero shared schema**. The YAML matches ctrl exactly. The apis version is structured around generic resource/network/filesystem configs that don't appear in the YAML at all.

#### SandboxProfileSpec

| YAML field | YAML required? | apis Go field | ctrl Go field | Decision |
|---|---|---|---|---|
| `language` | yes | **MISSING** | `Language string` | **A** (from ctrl) |
| `securityLevel` | no (enum, default standard) | **MISSING** | `SecurityLevel string` | **A** (from ctrl) |
| `seccompProfile` | no | **MISSING** | `SeccompProfile string` | **A** (from ctrl) |
| `networkPolicies` | no | **MISSING** | `NetworkPolicies []NetworkPolicy` | **A** (from ctrl) |
| `preInstalledPackages` | no | **MISSING** | `PreInstalledPackages []string` | **A** (from ctrl) |
| `resourceDefaults` | no | **MISSING** | `ResourceDefaults *ResourceDefaults` | **A** (from ctrl) |
| `filesystemConfig` | no | **MISSING** | `FilesystemConfig *ProfileFilesystemConfig` | **A** (from ctrl) |
| (apis-only) | n/a | `Resources *ResourceRequirements` | n/a | **D** |
| (apis-only) | n/a | `NetworkAccess *NetworkAccess` | n/a | **D** |
| (apis-only) | n/a | `Filesystem *FilesystemConfig` | n/a | **D** |
| (apis-only) | n/a | `Storage *StorageConfig` | n/a | **D** |
| (apis-only) | n/a | `SecurityCtx *SecurityContext` | n/a | **D** |

#### NetworkPolicy (new — adopted from ctrl)

| JSON tag | ctrl Go field | Decision |
|---|---|---|
| `type` (enum egress/ingress) | `Type string` | **A** |
| `rules` | `[]NetworkRule` | **A** |

#### NetworkRule (new — adopted from ctrl)

| JSON tag | ctrl Go field | Decision |
|---|---|---|
| `domain` | `Domain string` | **A** |
| `cidr` | `CIDR string` | **A** |
| `ports` | `[]PortRule` | **A** (shared with Sandbox PortRule) |

#### ResourceDefaults (new — adopted from ctrl)

| JSON tag | ctrl Go field | Decision |
|---|---|---|
| `cpu` | `CPU` | **A** |
| `memory` | `Memory` | **A** |
| `ephemeralStorage` | `EphemeralStorage` | **A** |

#### ProfileFilesystemConfig (new — adopted from ctrl)

| JSON tag | ctrl Go field | Decision |
|---|---|---|
| `readOnlyPaths` | `[]string` | **A** |
| `writablePaths` | `[]string` | **A** |

**SandboxProfile totals:** Five drops of unused apis-side fields (`Resources`, `NetworkAccess`, `Filesystem`, `Storage`, `SecurityCtx`). Seven adoptions from ctrl + nested types. Zero shared fields between apis and ctrl. The apis version is entirely replaced.

---

## 4. `pkg/types` (DTO layer) cleanup

After consolidating the CRD types in `pkg/apis/llmsafespace/v1`, the API DTO layer is treated separately:

### 4.1 Deletions (dead code)

| Type | Currently in `pkg/types` | Action | Justification |
|---|---|---|---|
| `Sandbox` (with `metav1` embedding) | Yes | **Refactor** to remove embedding (see below) | Active DTO; only used as a return value from `convertCRDToAPI` |
| `SandboxList` | Yes (with `metav1` embedding) | **Delete** | Zero callers anywhere in the codebase |
| `RuntimeEnvironment`, `RuntimeEnvironmentSpec`, `RuntimeEnvironmentStatus`, `RuntimeEnvironmentList` | Yes (with `metav1` embedding) | **Delete** | Zero callers |
| `SandboxProfile`, `SandboxProfileSpec`, `SandboxProfileList` | Yes (with `metav1` embedding) | **Delete** | Zero callers |
| File-level `+k8s:deepcopy-gen=package` directive | Line 1 | **Delete** | No deepcopy file is generated for `pkg/types`; directive is dead. |
| `+k8s:deepcopy-gen:interfaces=...runtime.Object` directives | Lines 31, 304, 527, 562 | **Delete** | Same reason. |

### 4.2 `types.Sandbox` refactor

Before:
```go
type Sandbox struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   SandboxSpec   `json:"spec,omitempty"`
    Status SandboxStatus `json:"status,omitempty"`
}
```

After:
```go
type Sandbox struct {
    ID                string            `json:"id"`
    Namespace         string            `json:"namespace,omitempty"`
    Labels            map[string]string `json:"labels,omitempty"`
    Annotations       map[string]string `json:"annotations,omitempty"`
    CreationTimestamp time.Time         `json:"creationTimestamp,omitempty"`

    Spec   SandboxSpec   `json:"spec"`
    Status SandboxStatus `json:"status"`
}
```

`SandboxStatus` and nested types in `pkg/types` replace `*metav1.Time` with `*time.Time`. JSON serialization is unchanged (both produce RFC3339 strings).

### 4.3 Conversion function changes

`api/internal/services/sandbox/sandbox_service.go:391` (`convertCRDToAPI`) is rewritten to map the unified CRD type to the cleaned DTO:

```go
func convertCRDToAPI(crd *v1.Sandbox) *types.Sandbox {
    if crd == nil { return nil }
    return &types.Sandbox{
        ID:                crd.Name,
        Namespace:         crd.Namespace,
        Labels:            crd.Labels,
        Annotations:       crd.Annotations,
        CreationTimestamp: crd.CreationTimestamp.Time,
        Spec: types.SandboxSpec{ /* explicit field mapping, unchanged */ },
        Status: types.SandboxStatus{
            // existing fields...
            StartTime: timePtr(crd.Status.StartTime), // *metav1.Time → *time.Time
        },
    }
}
```

`buildCRDFromRequest` (line 362) is unchanged in signature but now constructs the unified `v1.Sandbox`.

### 4.4 Reader updates

| Caller | Current code | After refactor |
|---|---|---|
| `sandbox_service.go:307` | `s.k8sClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Delete(...)` | Identical — `sandbox.Namespace` is now an explicit field on the DTO. |
| `router.go:431, 530` | `sb.Labels["user-id"]` | Identical — `Labels` is on the DTO. |
| `proxy.go:631-644, 670-678` | reads `sandbox *v1.Sandbox` from CRD | No change — these consume `v1.Sandbox` directly, not `types.Sandbox`. |

---

## 5. Proposed package layout (post-refactor)

```
pkg/
  apis/
    llmsafespace/
      v1/
        sandbox_types.go              # Unified Sandbox + nested types (from ctrl content)
        workspace_types.go            # Unified Workspace + nested types
        runtimeenvironment_types.go   # Unified RuntimeEnvironment + nested types
        sandboxprofile_types.go       # Unified SandboxProfile + nested types
        register.go                   # AddToScheme + GroupVersion
        zz_generated_deepcopy.go      # controller-gen output (replaces hand-rolled deepcopy.go)
  crds/                               # Generated YAML (unchanged location)
    sandbox_crd.yaml
    workspace_crd.yaml
    runtimeenvironment_crd.yaml
    sandboxprofile_crd.yaml
  types/
    types.go                          # API DTOs only — no metav1 embedding, no deepcopy-gen directives
    doc.go
  kubernetes/
    client_crds.go                    # Imports pkg/apis/llmsafespace/v1 (unchanged)
    informers.go
  interfaces/
    kubernetes.go                     # Imports pkg/apis/llmsafespace/v1 (unchanged)

controller/
  internal/
    webhooks/                         # New — moved from internal/resources/, package name = webhooks
      sandbox_webhook.go
      workspace_webhook.go
      runtimeenvironment_webhook.go
      sandboxprofile_webhook.go
    sandbox/                          # Existing — imports pkg/apis/llmsafespace/v1
    workspace/
    common/
  main.go                             # imports pkg/apis/llmsafespace/v1 (replaces controller/internal/resources)
```

**Removed:**
- `controller/internal/resources/` (all four `*_types.go`, `*_deepcopy.go`, `register.go`, `roundtrip_test.go`, plus the existing four `*_webhook.go` files which move to `controller/internal/webhooks/`)
- `pkg/apis/llmsafespace/v1/types.go` (existing wrong-content file — replaced)
- `pkg/apis/llmsafespace/v1/deepcopy.go` (existing hand-rolled file — replaced by generated)

---

## 6. Implementation plan

Each phase is independently committable. TDD discipline applies: tests first where the intent is to lock in new behavior; otherwise, the existing test suite must continue to pass at every step.

### Phase 0: design review (this document)

Reviewer confirms:
- The reconciliation matrix correctly identifies the kept/dropped/added fields per CRD.
- The choice of `controller/internal/resources` content as authoritative is correct (matches deployed YAML).
- The DTO refactor in `pkg/types` is acceptable.

**Output:** approval, or revisions to this matrix.

### Phase 1: prepare unified CRD types

1. Create the unified CRD type files in `pkg/apis/llmsafespace/v1/`:
   - `sandbox_types.go` (Sandbox, SandboxList, SandboxSpec, SandboxStatus, ResourceRequirements, NetworkAccess, EgressRule, PortRule, FilesystemConfig, StorageConfig, SecurityContext, SeccompProfile, ProfileReference, SandboxCondition, ResourceStatus)
   - `workspace_types.go` (Workspace, WorkspaceList, WorkspaceSpec, WorkspaceStatus, WorkspaceOwner, WorkspaceStorageConfig, WorkspaceNetworkAccess, WorkspaceEgressRule, WorkspaceAutoSuspend, WorkspacePackageSet, WorkspaceCredentialRef, WorkspaceCondition, WorkspaceConditionType, WorkspacePhase + constants)
   - `runtimeenvironment_types.go` (RuntimeEnvironment, RuntimeEnvironmentList, RuntimeEnvironmentSpec, RuntimeEnvironmentStatus, RuntimeResourceRequirements)
   - `sandboxprofile_types.go` (SandboxProfile, SandboxProfileList, SandboxProfileSpec, NetworkPolicy, NetworkRule, ResourceDefaults, ProfileFilesystemConfig)

   Content is copied from `controller/internal/resources/{sandbox,workspace,runtimeenvironment,sandboxprofile}_types.go` with one substitution: `corev1.ConditionStatus` (in `WorkspaceCondition.Status`) becomes plain `string`. All `+kubebuilder:` annotations are preserved verbatim.
2. Create `pkg/apis/llmsafespace/v1/register.go` — `AddToScheme`, `GroupVersion`, `Resource`. Same shape as the existing `controller/internal/resources/register.go`, just in the new package.
3. Delete the existing `pkg/apis/llmsafespace/v1/types.go` (272 lines of wrong-content schema) and `pkg/apis/llmsafespace/v1/deepcopy.go` (399 lines of hand-rolled deepcopy).
4. Run `controller-gen object paths="./pkg/apis/llmsafespace/v1/..."` to generate `zz_generated_deepcopy.go`.
5. Run `controller-gen crd paths="./pkg/apis/llmsafespace/v1/..." output:crd:artifacts:config=pkg/crds` to regenerate the YAML.
6. **Diff `pkg/crds/*.yaml` against the prior version. Expected: no semantic changes.** If there are differences, investigate and reconcile before proceeding.

### Phase 2: move webhooks

1. Create `controller/internal/webhooks/` package.
2. Move `controller/internal/resources/*_webhook.go` (4 files) into the new package, updating imports to reference `pkg/apis/llmsafespace/v1` for the types.
3. Update `controller/main.go` line 96, 100, 104 to import the new webhook package.
4. Run `go build ./...` and `go test ./controller/...` — expect compile and webhook tests to pass.

### Phase 3: switch controller to unified types

1. Replace every `controller/internal/resources` import in:
   - `controller/main.go`
   - `controller/internal/sandbox/controller.go`, `runtime_resolver.go`, and tests
   - `controller/internal/workspace/controller.go`, `stale_pvc_test.go`, and tests
   - `controller/internal/common/{service_manager,pod_manager,network_policy_manager,condition_adapter,common_test}.go`
   - All test files in `controller/internal/sandbox/` and `controller/internal/workspace/`
2. Replace `resources.Sandbox` with `v1.Sandbox`, `resources.WorkspaceCondition` with `v1.WorkspaceCondition`, etc.
3. Adjust for the `Status` type rename: `WorkspaceCondition.Status` is now `string`, not `corev1.ConditionStatus`. Find every comparison or assignment and adjust (likely a small set; a `string(corev1.ConditionTrue)` → `"True"` constant lives somewhere).
4. Delete `controller/internal/resources/` entirely.
5. Run `go test ./controller/... -count=1` — expect all passing.

### Phase 4: roundtrip test deletion

1. Delete `controller/internal/resources/roundtrip_test.go` (the file is gone in Phase 3 step 4 if the directory is removed; included here for clarity that it's intentionally not preserved).

### Phase 5: clean up `pkg/types`

1. Write a new test in `pkg/types/types_test.go` that asserts:
   - `json.Marshal(types.Sandbox{ID: "sb-1", ...})` does **not** produce `kind`, `apiVersion`, or `metadata` keys.
   - `json.Marshal/Unmarshal` round-trips `ID`, `Namespace`, `Labels`, `Annotations`, `CreationTimestamp`, `Status.StartTime`.
   - The package does not import `metav1` (verified via `go list -deps`).
2. Strip `metav1.TypeMeta` and `metav1.ObjectMeta` embedding from `types.Sandbox`. Add explicit fields per §4.2.
3. Replace every `*metav1.Time` with `*time.Time` in `pkg/types`:
   - `SandboxStatus.StartTime`, `PodStartTime`
   - `SandboxCondition.LastTransitionTime`
   - `ContainerStatus.StartedAt`, `FinishedAt`
   - `Event.Time`
   - `SandboxListItem.StartTime`
4. Delete dead types: `SandboxList`, `RuntimeEnvironment`, `RuntimeEnvironmentSpec`, `RuntimeEnvironmentStatus`, `RuntimeEnvironmentList`, `SandboxProfile`, `SandboxProfileSpec`, `SandboxProfileList`.
5. Delete `+k8s:deepcopy-gen=` directives at lines 1, 31, 304, 527, 562.
6. Remove the `metav1` import from `pkg/types/types.go`.
7. Update `convertCRDToAPI` in `api/internal/services/sandbox/sandbox_service.go` per §4.3.
8. Add a `timePtr` helper that converts `*metav1.Time` → `*time.Time`.
9. Update tests in `api/internal/services/sandbox/sandbox_service_test.go` and `router_sandbox_test.go` that build `types.Sandbox{...}` with `ObjectMeta: metav1.ObjectMeta{...}` to use the new explicit fields.

### Phase 6: full validation

1. `go build ./...` — clean compile across all binaries.
2. `go vet ./...` — clean.
3. `go test -race -count=1 ./...` — all passing.
4. `make manifests` (controller) — confirm no diff in `pkg/crds/*.yaml`.
5. Local `kind` smoke test: deploy controller + API, register a user, create a workspace, create a sandbox, send a prompt, terminate. Same as worklog 0030 e2e flow.
6. `golangci-lint run` — clean.

### Phase 7: worklog

Worklog `0035_2026-MM-DD_crd-type-consolidation.md` documenting the change, the rationale, the field reconciliation, and the validation evidence.

---

## 7. Risks and mitigations

| Risk | Mitigation |
|---|---|
| `make manifests` produces YAML that differs from `pkg/crds/*.yaml` | Phase 1 step 6 explicitly diffs and gates on no-change. The matrix above predicts no semantic change because the source content is identical (we're moving ctrl's types). |
| A test fixture builds `resources.Sandbox{Spec: resources.SandboxSpec{SecurityContext: ...}}` and the field rename breaks compilation in unexpected files | The unified type uses `SecurityContext`, the same field name as `controller/internal/resources` already uses. Only `pkg/apis` callers (which use `SecurityCtx`) need rename — those are entirely in API service code, and `convertCRDToAPI` is the only reader. |
| `corev1.ConditionStatus` → `string` change breaks comparisons | Grep for `corev1.ConditionTrue`, `corev1.ConditionFalse`, `corev1.ConditionUnknown` and replace with `"True"`, `"False"`, `"Unknown"` literals. |
| `*metav1.Time` → `*time.Time` change in `pkg/types` breaks JSON deserialization for an existing client | Project is not live; no clients exist outside the test suite. Both serialize as RFC3339. |
| Hand-rolled deepcopy in `pkg/apis/llmsafespace/v1/deepcopy.go` had a behavior subtly different from `controller-gen` output | controller-gen output is the standard. Behavior diff is improbable (deepcopy is mechanical). Tests will catch any case. |
| Webhook package move loses an import that resources/* tests depended on | Webhook tests, if any, move with the webhooks. If a controller test referenced the webhook validators, update import. |
| `roundtrip_test.go` was secretly catching field divergences we don't yet realize | The matrix above is the manual replacement for that test. Field-by-field reconciliation is the structural guarantee. |

---

## 8. Resolved questions

1. **File layout:** Split per CRD. `pkg/apis/llmsafespace/v1/{sandbox,workspace,runtimeenvironment,sandboxprofile}_types.go`, plus `register.go` and `zz_generated_deepcopy.go`. Matches kubebuilder scaffolding convention and the existing layout being moved.
2. **Webhook package name:** `controller/internal/webhooks/`. The package contains only admission validators after the move; the name `resources` would be misleading.
3. **`WorkspaceConditionType` typedef:** Kept as `type WorkspaceConditionType string` with the existing constants (`WorkspaceConditionReady`, `WorkspaceConditionPVCReady`, `WorkspaceConditionSuspended`). Zero runtime cost; catches typos against the closed set of known condition types. Note the deliberate asymmetry: `SandboxCondition.Type` remains plain `string` because Sandbox conditions are open-ended in the YAML schema. `WorkspaceCondition.Status` collapses to `string` (was `corev1.ConditionStatus`) to drop the heavyweight `k8s.io/api/core/v1` import for what is just a True/False/Unknown enum.

---

## 9. Out of scope

- Docker deployment work (`design/DOCKER-DEPLOYMENT.md`) — explicitly deferred per user direction.
- Frontend Phase A (`worklogs/0034`) — gated on this consolidation landing first per user decision.
- Changes to `pkg/crds/*.yaml` content — verified to remain byte-identical (or semantically identical) after regeneration.
- Removal of the `permissions` table tech debt noted in worklog 0033 next steps — separate concern.
