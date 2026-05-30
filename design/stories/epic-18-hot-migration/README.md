# Epic 18: Hot Migration & RWX Storage

**Status:** Planning
**Author:** mikekao
**Depends On:** Epic 17 (security baseline), Epic 2 (workspaces)
**Target Environment:** EKS + Graviton Spot + gVisor + RWX storage (EFS in production, Longhorn/NFS in dev)

---

## Objective

Implement zero-downtime live migration of workspace pods across nodes, enabling:
- Proactive load balancing (move workspaces off hot nodes before users are impacted)
- Spot instance reclamation handling (2-min warning → graceful migration)
- Node maintenance/upgrades without workspace disruption

---

## Assumptions (Validated)

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | Workspace CRD already supports `ReadWriteMany` access mode | ✅ `WorkspaceStorageConfig.AccessMode` enum includes `ReadWriteMany` (`pkg/apis/llmsafespace/v1/workspace_types.go:19`) |
| A2 | `buildPVC()` already handles RWX | ✅ Line 552 of workspace controller: `if workspace.Spec.Storage.AccessMode == "ReadWriteMany"` |
| A3 | Proxy resolves backend via `workspace.Status.PodIP` per-request (no long-lived cache) | ✅ `proxy.go:293` fetches workspace CRD, line 361 reads `Status.PodIP`; retries with fresh IP on connection error (line 371) |
| A4 | Workspace reconciler sets PodIP from running pod during `handleCreating` only | ✅ `controller.go:206` sets PodIP; `handleActive` does NOT re-set PodIP on each reconcile |
| A5 | Agentd tracks session state (ID, status) in memory via `sessionStatusTracker` | ✅ `cmd/workspace-agentd/main.go` — `sessionStatusTracker` struct with `statuses map[string]string` |
| A6 | Opencode stores conversation/session data at `$XDG_DATA_HOME/opencode/` on the PVC | ✅ Entrypoint sets `XDG_DATA_HOME=/workspace/.local`; opencode uses `xdg-basedir` (`opencode-upstream/packages/core/src/global.ts:10`) |
| A7 | SSE reconnection is client-driven (standard protocol); proxy sends `text/event-stream` | ✅ `proxy.go:233` sets Content-Type; SSE spec requires client reconnect on connection close |
| A8 | Current production storage is Longhorn (RWO, ext4) | ✅ Threat model G23: `/dev/longhorn/pvc-... /workspace ext4 rw` |
| A9 | Single-node kind cluster used for local dev | ✅ `local/kind-cluster.yaml` — single control-plane node |
| A10 | Controller uses leader election via Lease | ✅ `controller/internal/common/leader_election.go` |
| A11 | Workspace reconciler finds pods by deterministic name `{workspace}-{uid[:8]}`, NOT by label selector | ✅ `constants.go:45` `podName()` function; `handleActive` does `r.Get()` by this name |
| A12 | `handleActive` calls `recoverFromTransientPodLoss` when pod is missing → sets phase=Creating, clears PodIP | ✅ `controller.go:248` — this would fight migration if source pod is deleted without updating the pod lookup |
| A13 | Password is per-workspace (K8s Secret), mounted into pod — same Secret for source and target pods | ✅ `ensurePasswordSecret` creates `{workspace}-password` Secret; both pods mount it |
| A14 | Proxy uses single namespace (`h.namespace`) for all workspace lookups | ✅ `proxy.go:293` — multi-tenant namespaces would require namespace resolution from request context |
| A15 | WorkspaceWatcher tracks phase changes only, not podIP changes | ✅ `crd_watcher.go:210-215` — only stores `knownPhases[name] = newPhase` |

---

## Key Decisions (from design session 2026-05-30)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Storage backend (prod) | EFS | RWX native, no share-manager pods, cross-AZ, AWS-managed |
| Storage backend (dev/local) | Any RWX-capable CSI (Longhorn RWX, NFS) | Epic must NOT be EFS-specific; StorageClass is the abstraction boundary |
| Volume access mode | RWX | Enables ~100ms cutover; both pods mount simultaneously |
| Sandbox runtime (prod) | gVisor | Eliminates container-escape risk that RWX would otherwise widen |
| Sandbox runtime (dev) | runc (gVisor optional) | gVisor not required for correctness; only for security hardening |
| Tenant isolation | Capsule + EFS access points (prod) / namespace-only (dev) | API-level isolation + AWS-enforced root directory per workspace |
| Compute (prod) | Graviton Spot (80%) + On-Demand baseline (20%) | 60-70% cost savings; Spot reclamation handled by migration system |
| Migration pod naming | Target pod uses `{workspace}-{uid[:8]}-mig` suffix; after cutover, workspace reconciler adopts it by updating `podName` derivation | Avoids conflict with deterministic pod naming (A11) |
| Concurrent write safety | Sequential controller-driven phases (no Lease needed) | Source is read-only during snapshot; target is write-only during restore; proxy doesn't route to target until podIP updated. No concurrent-write window exists |

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│              Migration Controller                          │
│  Triggers: node pressure, Spot warning, manual            │
└──────────┬───────────────────────────────────┬───────────┘
           │ 1. Create target pod               │ 4. Update workspace.status.podIP
           │    (name: ws-abc-1234-mig)         │    + workspace.status.podName
           ▼                                    ▼
┌─────────────────────┐          ┌──────────────────────────┐
│  Source Pod (Node A) │          │  Target Pod (Node B)      │
│  ws-abc-1234         │──3.───── │  ws-abc-1234-mig          │
│  workspace-agentd    │  session │  workspace-agentd         │
└────────┬─────────────┘  state  └────────┬──────────────────┘
         │                                 │
         └────────────┬────────────────────┘
                      ▼
           ┌─────────────────────┐
           │   RWX PVC            │  ← both pods mount simultaneously
           │   /workspace          │     zero data movement
           └─────────────────────┘
```

**Migration sequence (5 steps):**
1. Migration controller creates target pod directly (NOT via workspace reconciler annotation — see rationale below) with same PVC, same labels, nodeAffinity for target node
2. Wait for target pod Ready (opencode healthy, providers connected)
3. Transfer session state: `GET /v1/migrate/snapshot` on source → `POST /v1/migrate/restore` on target
4. Migration controller patches `workspace.status.podIP` and `workspace.status.podName` to target pod
5. Migration controller deletes source pod; workspace reconciler sees target pod by updated `podName` — no conflict

**Why migration controller creates the pod directly (not via annotation):**
- The workspace reconciler uses a deterministic pod name derived from workspace UID (A11). It cannot create a second pod with a different name.
- If we annotate the workspace, the reconciler would need significant refactoring to support dual-pod mode.
- Instead: migration controller creates the target pod directly (it has RBAC for pods). After cutover, it updates `workspace.status.podName` so the workspace reconciler adopts the new pod. The workspace reconciler's `handleActive` must be modified to use `workspace.status.podName` instead of `podName()` when the status field is set.
- This is a smaller, safer change than dual-pod mode: one conditional in `handleActive` (`if workspace.Status.PodName != "" { name = workspace.Status.PodName }`).

**Total user-visible disruption:** ~100ms (one HTTP retry or SSE reconnect)

---

## Security Model

| Layer | Control | Required For |
|-------|---------|--------------|
| Container escape prevention | gVisor RuntimeClass | Production only (defense-in-depth for RWX) |
| Cross-tenant API isolation | Capsule virtual namespaces | Multi-tenant production |
| Cross-tenant storage isolation | EFS access points (AWS-enforced root dir + UID/GID) | Multi-tenant production |
| Migration sequencing | Controller-driven phases; no concurrent-write window by design | All environments |
| Spot reclamation | Node termination handler → triggers migration | Production (Spot nodes only) |
| Single-tenant / dev | Namespace isolation + PodSecurityContext (existing) | Dev / single-tenant |

---

## Stories

### S18.1 — RWX Storage Class Configuration

**Goal:** Configure a StorageClass that provides RWX volumes. In production this is EFS; in dev/local this is Longhorn RWX or NFS.

**Acceptance Criteria:**
- [ ] Helm chart includes a configurable StorageClass template (name, provisioner, parameters all values-driven)
- [ ] Production values: `provisioner: efs.csi.aws.com`, parameters `provisioningMode: efs-ap`, `fileSystemId: ${EFS_FS_ID}`, `directoryPerms: "700"`, `basePath: /tenants`
- [ ] Dev/local values: `provisioner: driver.longhorn.io` with `numberOfReplicas: "1"`, `dataLocality: disabled` (enables RWX on Longhorn)
- [ ] PVCs created with `ReadWriteMany` when workspace `spec.storage.accessMode` is `ReadWriteMany`
- [ ] Two pods on different nodes can simultaneously mount the same PVC and read/write files (integration test)
- [ ] Mount options include `nosuid,nodev` (addresses threat model G23)
- [ ] Existing RWO workspaces continue to function unchanged (backward compatible — no migration of existing PVCs)
- [ ] Documentation: which RWX providers are tested (EFS, Longhorn RWX, NFS-subdir-external-provisioner)

**Implementation Notes:**
- No code changes to `buildPVC()` — it already handles `ReadWriteMany`. This story is purely infrastructure + Helm values + integration test.
- For local kind testing with multiple nodes: use [nfs-subdir-external-provisioner](https://github.com/kubernetes-sigs/nfs-subdir-external-provisioner) or Longhorn with `accessMode: rwx`.
- EFS mount options: `tls,iam` for encryption in transit + IAM-based mount authorization (prod only).
- The `storageClassName` field on WorkspaceStorageConfig is already optional — workspaces that don't specify it get the cluster default (RWO).

**Estimated Effort:** 2 points

---

### S18.2 — Migration CRD & Reconciler

**Goal:** Define a `Migration` CRD and implement the reconciler that orchestrates the 5-step migration sequence.

**Acceptance Criteria:**
- [ ] `Migration` CRD registered in `pkg/apis/llmsafespace/v1/migration_types.go`
- [ ] Spec: `workspaceRef` (string), `targetNode` (string, optional), `reason` (enum: `SpotReclamation | NodePressure | Manual | Maintenance`), `priority` (int32)
- [ ] Status: `phase` (enum: `Pending | CreatingTarget | WaitingReady | TransferringState | CuttingOver | Cleanup | Completed | Failed`), `startTime`, `completionTime`, `sourceNode`, `targetNode`, `sourcePodName`, `targetPodName`, `cutoverDurationMs`, `error`, `conditions`
- [ ] Reconciler implements the 5-step sequence with idempotent phase transitions
- [ ] Phase transition persisted to status BEFORE executing next step (write-ahead)
- [ ] Only one active Migration per workspace — reconciler rejects if another is in-progress
- [ ] Timeouts: CreatingTarget=60s, WaitingReady=120s, TransferringState=10s, CuttingOver=5s
- [ ] Failed migrations trigger rollback: delete target pod, leave source running, clear migration status fields on workspace
- [ ] Migration controller creates target pod DIRECTLY (not via workspace reconciler) with: same PVC, same labels, same password Secret mount, nodeAffinity for target node, name `{workspace}-{uid[:8]}-mig`
- [ ] After cutover: migration controller patches `workspace.status.podName` to target pod name
- [ ] Workspace reconciler change: `handleActive` uses `workspace.Status.PodName` for pod lookup when set (instead of `podName()` derivation). Falls back to `podName()` when `Status.PodName` is empty (backward compatible)
- [ ] After source pod deletion: workspace reconciler finds target pod by `Status.PodName` — no `recoverFromTransientPodLoss` triggered
- [ ] Metrics: `migration_total{reason,outcome}` (counter), `migration_cutover_duration_seconds` (histogram), `migration_in_progress` (gauge)
- [ ] RBAC: migration controller needs `get/list/watch/create/delete` on Pods, `get/update/patch` on Workspaces and Workspace/status, `get/create/update/delete` on Migrations
- [ ] Unit tests: happy path, timeout at each phase, concurrent migration rejection, crash recovery at each phase, workspace reconciler pod-lookup-by-status-name

**CRD Sketch:**
```yaml
apiVersion: llmsafespace.dev/v1
kind: Migration
metadata:
  name: migrate-ws-abc123-1717100000
spec:
  workspaceRef: ws-abc123
  targetNode: ""
  reason: SpotReclamation
  priority: 100
status:
  phase: Completed
  startTime: "2026-05-30T10:00:00Z"
  completionTime: "2026-05-30T10:00:03Z"
  sourceNode: ip-10-0-1-50.ec2.internal
  targetNode: ip-10-0-2-30.ec2.internal
  sourcePodName: ws-abc123-a1b2c3d4
  targetPodName: ws-abc123-a1b2c3d4-mig
  cutoverDurationMs: 95
```

**Workspace reconciler change (minimal):**
```go
// In handleActive, replace:
//   name := podName(workspace.Name, uid)
// With:
name := podName(workspace.Name, uid)
if workspace.Status.PodName != "" {
    name = workspace.Status.PodName
}
```

This single conditional is the only change to the workspace reconciler. It's backward compatible: `Status.PodName` is already set during `handleCreating` (line 187), so existing workspaces already have it populated with the deterministic name.

**Why migration controller creates pods directly (not via annotation on workspace):**
1. Workspace reconciler uses deterministic pod naming (A11) — cannot create a second pod
2. Adding dual-pod mode to workspace reconciler would be a large, risky refactor touching every phase handler
3. Direct pod creation by migration controller is simpler: one new controller, one conditional in workspace reconciler
4. Single-owner principle is preserved for steady-state (workspace reconciler owns the pod); migration controller only owns the pod during the transient migration window

**Estimated Effort:** 8 points

---

### S18.3 — Session State Snapshot/Restore

**Goal:** Add snapshot and restore endpoints to `workspace-agentd` so the migration controller can transfer in-memory session routing state between pods.

**Acceptance Criteria:**
- [ ] `GET /v1/migrate/snapshot` returns JSON blob of agentd's in-memory state
- [ ] `POST /v1/migrate/restore` accepts the snapshot and reconstructs internal state
- [ ] Snapshot includes: `sessionStatusTracker.statuses` (map[string]string), `providerCache` (connected list, configured count, sessions list)
- [ ] Snapshot does NOT include: opencode process state (reads from `$XDG_DATA_HOME/opencode/` on PVC), credentials (mounted from K8s Secret), file contents (on shared PVC)
- [ ] Snapshot size < 50KB for 5 active sessions (validated by unit test)
- [ ] Restore is idempotent — calling twice with same snapshot produces same state
- [ ] Snapshot returns `409 Conflict` if already taken and not consumed
- [ ] Restore returns `409 Conflict` if agentd already has sessions from a prior restore
- [ ] Target pod's `/v1/readyz` returns `true` only after opencode is healthy (existing behavior — no change needed; opencode reads state from PVC on startup)
- [ ] Auth: port 4097 is cluster-internal only (not exposed via Ingress). Migration controller calls via pod IP.
- [ ] Unit tests: round-trip, idempotency, conflict detection, size assertion

**Snapshot Schema:**
```json
{
  "version": 1,
  "timestamp": "2026-05-30T10:00:00Z",
  "workspaceID": "ws-abc123",
  "sessionStatuses": {"ses_001": "busy", "ses_002": "idle"},
  "providerCache": {
    "connected": ["anthropic"],
    "configured": 2,
    "sessions": [{"id": "ses_001", "title": "Debug auth", "status": "busy"}]
  }
}
```

**What happens to in-flight LLM requests during migration:**
- Source pod's opencode process is terminated when source pod is deleted (step 5).
- Target pod starts a fresh opencode process that reads conversation state from `/workspace/.local/opencode/` (shared PVC, per A6).
- Client sees SSE stream close → reconnects → resumes. Partial LLM response is lost; client re-sends last message.
- Acceptable: migrations are rare (~1/hour for spot); losing a partial response equals a network blip.

**Implementation Notes:**
- Add two handlers to the existing `mux` in `cmd/workspace-agentd/main.go`.
- `sessionStatusTracker` and `providerCache` are already struct types — export fields or add marshal methods.
- No new dependencies.
- Migration controller calls via `http://{pod.Status.PodIP}:4097/v1/migrate/snapshot`.

**Estimated Effort:** 3 points

---

### S18.4 — Proxy Connection Handoff

**Goal:** Ensure the API proxy seamlessly routes to the new pod after migration with minimal client disruption.

**Acceptance Criteria:**
- [ ] After migration controller updates `workspace.status.podIP`, new HTTP requests route to target pod (proxy reads podIP per-request — A3)
- [ ] Existing SSE connections to source pod continue until source pod is deleted (step 5), then client reconnects to target via new podIP
- [ ] Proxy returns `503` with `Retry-After: 1` (not 10) when workspace has an active Migration CR (detected by checking Migration CRD or annotation)
- [ ] Proxy invalidates `pwCache` entry when it detects pod IP changed between attempts (prevents stale cache)
- [ ] No buffering, no atomic swap — the existing per-request podIP lookup + retry-on-connection-error is sufficient
- [ ] Integration test: start SSE stream → trigger migration → verify stream resumes on new pod within 3 seconds

**Why no buffer or atomic swap is needed:**
1. HTTP requests: proxy reads `workspace.Status.PodIP` per-request (A3). After podIP update, new requests go to target. Old pod still running — no gap.
2. SSE streams: connected to old pod via TCP. Connection persists until old pod deleted. Client reconnects, gets new podIP. Standard SSE behavior.
3. In-flight requests on old pod: complete normally (old pod alive until Cleanup phase).
4. Race window (podIP updated but old pod still serves): harmless. Old pod returns valid responses. New requests go to new pod.

**Code changes required:**
1. `proxy.go`: when `proxyErr != nil && isConnectionError(proxyErr)` AND fresh workspace has a Migration CR in progress → use `Retry-After: 1` instead of `retryAfterSec` (10)
2. `proxy.go`: after successful retry with fresh pod IP, invalidate `pwCache` for that workspace (password is same, but cache hygiene)

**Estimated Effort:** 2 points

---

### S18.5 — Spot Reclamation Handler

**Goal:** Automatically migrate workspace pods off a node when AWS issues a Spot termination notice (2-minute warning).

**Acceptance Criteria:**
- [ ] AWS Node Termination Handler (NTH) deployed as DaemonSet on Spot nodes
- [ ] NTH detects Spot interruption via IMDS (`/latest/meta-data/spot/instance-action`)
- [ ] On detection: NTH cordons node, creates `Migration` CR for every workspace pod on condemned node
- [ ] Migrations prioritized: busy sessions (from `/v1/statusz`) get higher `priority` value
- [ ] All migrations must complete within 90 seconds (30s buffer before termination)
- [ ] If migration cannot complete: workspace enters `Suspending` phase (existing behavior — PVC retained, auto-resumes on next access)
- [ ] Metrics: `spot_reclamation_total`, `spot_reclamation_succeeded`, `spot_reclamation_suspended`
- [ ] Alert: `spot_reclamation_suspended / spot_reclamation_total > 0.05` over 1 hour
- [ ] Integration test: simulate spot interruption → verify workspaces migrate or gracefully suspend

**Implementation Notes:**
- NTH in IMDS mode (lowest latency, no SQS dependency). Cordons node but does NOT delete pods.
- Parallel migrations: up to 10 concurrent per node (configurable). Queue by priority beyond that.
- Fallback: migration timeout → set workspace phase to `Suspending`. Existing reconciler handles it.
- Workspace pods get `karpenter.sh/do-not-disrupt: "true"` annotation (Karpenter won't consolidate them independently).
- **Dev/local:** Production-only. Helm value `spotHandler.enabled: false` (default).

**Estimated Effort:** 4 points

---

### S18.6 — Proactive Load Balancing

**Goal:** Migrate workspaces off nodes approaching resource exhaustion before users experience degradation.

**Acceptance Criteria:**
- [ ] Background goroutine in migration controller evaluates node pressure every 30 seconds
- [ ] Reads node metrics from metrics-server (CPU/memory utilization per node)
- [ ] Configurable thresholds via ConfigMap (hot-reloadable): `highWaterMark` (trigger), `lowWaterMark` (target)
- [ ] Defaults: CPU 80%/60%, Memory 85%/65%
- [ ] When node exceeds high watermark for >60s: creates Migration CRs for least-active workspace pods
- [ ] "Least active" = longest since `workspace.status.lastActivityAt` (field exists — A4 area, confirmed in types)
- [ ] Does NOT migrate pods with `sessionsActive > 0` (checked via agentd `/v1/statusz`)
- [ ] Cooldown: max 1 migration per workspace per 10 min; max 3 per node per 5 min
- [ ] Target node: prefer below low watermark, same AZ
- [ ] Dry-run mode: `loadBalancer.dryRun: true` → logs only
- [ ] Metrics: `proactive_migration_total{trigger}`, `node_pressure_seconds`

**Implementation Notes:**
- Background loop inside migration controller binary. Creates Migration CRs when thresholds breached.
- Pod selection: list workspace pods on hot node → sort by `lastActivityAt` ascending → select until projected utilization < low watermark.
- **Dev/local:** Functional on multi-node clusters. No-op on single-node kind. Gated by `loadBalancer.enabled`.

**Estimated Effort:** 4 points

---

### S18.7 — gVisor RuntimeClass (Production Hardening)

**Goal:** Deploy gVisor as container runtime for workspace pods in production, providing kernel-level isolation for RWX mounts.

**Acceptance Criteria:**
- [ ] gVisor (`runsc`) installed on Graviton worker nodes via Karpenter `userData`
- [ ] `RuntimeClass` resource `gvisor` created with `handler: runsc`
- [ ] New field `workspace.spec.runtimeClass` (optional string) added to CRD
- [ ] Workspace reconciler sets `pod.spec.runtimeClassName` from `workspace.spec.runtimeClass`
- [ ] Helm value `workspace.defaultRuntimeClass: gvisor` sets default for production
- [ ] All runtime images pass test suites under gVisor (Python 3.11, Node 20, Go 1.22, Rust, Java 21)
- [ ] `mise install` works under gVisor
- [ ] File I/O benchmark: <20% overhead vs runc (fio 4K random r/w)
- [ ] opencode full agent session works end-to-end under gVisor
- [ ] Compatibility matrix automated in CI

**Compatibility Risks:**
- Java 21: JIT may be slower under gVisor. Benchmark; document if >2x startup penalty.
- gVisor on ARM64: supported since 2023. Use `containerd-shim-runsc-v1` for `linux/arm64`.
- Known non-issues: no FUSE (not used), limited `/proc` (not used), no `ptrace` (not used).

**Implementation Notes:**
- **Dev/local:** NOT required. Migration works identically with runc. Helm default: `workspace.defaultRuntimeClass: ""` (no RuntimeClass set → runc).
- CRD change: add `runtimeClass` to `WorkspaceSpec` (optional string, no enum).

**Estimated Effort:** 5 points

---

### S18.8 — Tenant Namespace Isolation (Capsule)

**Goal:** Provide API-level tenant isolation using Capsule so tenants cannot see or interact with each other's resources.

**Acceptance Criteria:**
- [ ] Capsule operator deployed via Helm (production only)
- [ ] `Tenant` CR per onboarded tenant with: namespace quota, ResourceQuota, LimitRange, NetworkPolicy
- [ ] Workspace CRDs created in tenant namespace — cross-tenant access denied
- [ ] Migration CRDs scoped to tenant namespace
- [ ] NetworkPolicy: deny ingress from other tenant namespaces; allow from API server namespace
- [ ] ResourceQuota per tenant: configurable max workspaces, max storage, max CPU/memory
- [ ] API server resolves tenant namespace from JWT `tenant_id` claim (requires proxy change — A14)
- [ ] Proxy change: resolve namespace from authenticated user's tenant context instead of hardcoded `h.namespace`
- [ ] Tenant deletion cascades: delete Capsule Tenant → all namespaces, workspaces, PVCs cleaned up
- [ ] Scale test: 100 tenants × 10 workspaces — controller reconcile latency <500ms p99
- [ ] EFS access points (prod): one per workspace, root dir `/tenants/{tenant_id}/workspaces/{workspace_id}`, UID/GID 1000:1000

**Why Capsule (not vCluster):**
- vCluster: ~256MB RAM per tenant → 256GB at 1000 tenants. Prohibitive.
- Capsule: ~0 overhead per tenant. Policy-enforced RBAC + NetworkPolicy. Sufficient when combined with EFS access points for storage isolation.
- Tenants don't get direct K8s API access — they interact through LLMSafeSpace API which enforces scoping.

**Implementation Notes:**
- Workspace controller uses shared informer cache (cluster-wide RBAC). Tenant isolation enforced at API layer.
- EFS access points: second layer of storage isolation (AWS-enforced, independent of K8s RBAC).
- **Dev/local:** Single-tenant. All workspaces in `llmsafespace` namespace. Capsule not deployed. Helm value `multiTenant.enabled: false` (default).
- **Breaking change for proxy:** `h.namespace` must become per-request. This is a significant refactor of `ProxyHandler` — every method that calls `h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace)` must accept namespace as parameter.

**Estimated Effort:** 8 points

---

### S18.9 — Karpenter NodePool Configuration (Production)

**Goal:** Configure Karpenter for cost-optimized Graviton Spot compute with proper disruption handling.

**Acceptance Criteria:**
- [ ] Karpenter deployed as sole node provisioner (cluster autoscaler removed)
- [ ] `baseline` NodePool: On-Demand Graviton (c7g, m7g), system workloads, taint `workload-type=system:NoSchedule`, min 2 nodes
- [ ] `workspaces` NodePool: Spot Graviton (c7g, m7g, r7g — 6+ types), workspace pods, toleration `workload-type=workspace`
- [ ] Workspace pods annotated `karpenter.sh/do-not-disrupt: "true"`
- [ ] Consolidation: `WhenEmpty` for workspace pool, `WhenUnderutilized` for baseline
- [ ] Node expiry: 7 days max lifetime (security patching rotation)
- [ ] Topology spread: workspace pods across AZs (`maxSkew: 1`)
- [ ] EC2NodeClass: AL2023 AMI, gVisor in userData, subnet/SG selectors
- [ ] Cost allocation tags: `team`, `environment`, `workload-type`

**NodePool Sketch:**
```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: workspaces
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["arm64"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot"]
        - key: node.kubernetes.io/instance-type
          operator: In
          values: ["c7g.xlarge", "c7g.2xlarge", "m7g.xlarge", "m7g.2xlarge", "r7g.xlarge", "r7g.2xlarge"]
      taints:
        - key: workload-type
          value: workspace
          effect: NoSchedule
      nodeClassRef:
        group: karpenter.k8s.aws
        kind: EC2NodeClass
        name: workspace-nodes
  disruption:
    consolidationPolicy: WhenEmpty
    consolidateAfter: 60s
  limits:
    cpu: "1000"
    memory: 4000Gi
```

**Implementation Notes:**
- 80/20 Spot/OD split via taints: system pods tolerate `baseline` taint only; workspace pods tolerate `workspaces` taint only.
- **Dev/local:** NOT used. Kind cluster manages its own nodes. Helm value `karpenter.enabled: false` (default).

**Estimated Effort:** 3 points

---

## Implementation Order

```
Phase A (Foundation — works on local + prod):
  S18.1 (RWX StorageClass) → S18.3 (Snapshot/Restore) → S18.2 (Migration CRD + reconciler change)

Phase B (Core migration — works on local multi-node):
  S18.4 (Proxy Handoff) → S18.6 (Load Balancing)

Phase C (Production hardening — prod only):
  S18.7 (gVisor) → S18.9 (Karpenter) → S18.5 (Spot Handler)

Phase D (Multi-tenancy — prod only):
  S18.8 (Capsule + namespace refactor)
```

**Rationale:**
- Phase A+B are storage-backend-agnostic and testable on a local multi-node kind cluster with Longhorn RWX or NFS.
- Phase C+D are production-specific. A developer validates the entire migration mechanism locally without EFS, gVisor, Karpenter, or Capsule.
- S18.8 is last because it requires a proxy namespace refactor (A14) that touches many code paths.

**Total estimated effort:** 39 points (~3 sprints at team capacity)

---

## Cost Model (500 concurrent workspaces, production)

| Component | Monthly | Notes |
|-----------|---------|-------|
| Compute (Spot 80% + OD 20%) | ~$5,500 | c7g.xlarge Spot ~$0.04/hr vs $0.17/hr OD |
| EFS (20TB, elastic throughput) | ~$6,000 | $0.30/GB-month + throughput |
| EKS control plane | $73 | Fixed |
| NAT Gateway | ~$500 | Data transfer dependent |
| **Total** | **~$12,000/mo** | **~$24/workspace/month** |

**vs current (Longhorn + On-Demand):** ~$28,000/mo → **57% savings**

---

## Risks & Mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|------------|------------|
| R1 | gVisor incompatibility with runtime tooling | Workspace broken for affected runtime | Medium | Compatibility matrix; per-workspace `runtimeClass` opt-out |
| R2 | EFS latency spikes | Degraded workspace I/O | Low | Elastic throughput; CloudWatch alerts; fallback to provisioned |
| R3 | Spot interruption exceeds migration capacity | Forced suspensions (not data loss) | Low | 6+ instance types; OD baseline absorbs overflow; suspension is graceful |
| R4 | Session state transfer fails | User sees SSE reconnect + loses partial response | Medium | Rollback to source pod; client retries; no data corruption (shared PVC) |
| R5 | EFS access point limit (1000/filesystem) | Cannot create new workspaces | Medium | Monitor; provision second filesystem at 800 |
| R6 | Workspace reconciler `handleActive` change introduces regression | Existing workspaces affected | Low | Change is one conditional; gated by `Status.PodName` being set (already set for all workspaces); extensive unit tests |
| R7 | Proxy namespace refactor (S18.8) breaks existing single-tenant deployments | API outage | Medium | Feature-gated behind `multiTenant.enabled`; single-tenant path unchanged |

---

## Open Questions

| # | Question | Status | Resolution |
|---|----------|--------|------------|
| Q1 | vCluster vs Capsule? | ✅ Resolved | Capsule (vCluster overhead prohibitive at scale) |
| Q2 | gVisor + Java JIT? | 🔶 Open | Benchmark in S18.7 |
| Q3 | EFS throughput mode? | 🔶 Open | Start elastic; switch to provisioned if p99 > 10ms |
| Q4 | Session state size? | ✅ Resolved | <50KB (routing table only, not conversation history) |
| Q5 | Migration SLO? | ✅ Resolved | p99 cutover < 500ms, p99 total < 10s |
| Q6 | EFS access point limit? | 🔶 Open | Monitor; plan second filesystem at 800 workspaces |
| Q7 | Does this require EFS? | ✅ Resolved | No. Any RWX CSI works. EFS is prod recommendation |
| Q8 | How does migration controller create target pod without conflicting with workspace reconciler? | ✅ Resolved | Direct pod creation + `Status.PodName` update (see S18.2) |
| Q9 | How does proxy handle multi-tenant namespaces? | ✅ Resolved | Namespace resolved from JWT tenant context (S18.8); breaking change for proxy |

---

## Success Metrics

| Metric | Target | Source |
|--------|--------|--------|
| Migration cutover duration (p99) | < 500ms | `migration_cutover_duration_seconds` |
| Total migration duration (p99) | < 10s | Migration CRD `completionTime - startTime` |
| Spot reclamation success rate | > 95% | `spot_reclamation_succeeded / total` |
| User-visible errors during migration | 0 dropped requests | Proxy 503 without client retry |
| Cost per workspace/month | < $25 | AWS Cost Explorer |
| gVisor I/O overhead | < 20% | fio benchmark |

---

## Design Assessment

| Dimension | Score (1-5) | Justification |
|-----------|-------------|---------------|
| **Robustness** | 5 | Write-ahead phase transitions; graceful fallback to suspension on failure; no data loss (shared PVC); rollback at every phase |
| **Reliability** | 5 | Migration failure = workspace suspends + auto-resumes (proven path); client auto-retries; no SPOF |
| **Maintainability** | 4.5 | Follows existing controller-runtime patterns; migration controller is a new, isolated component; workspace reconciler change is 3 lines. -0.5: proxy namespace refactor in S18.8 is invasive |
| **Scalability** | 5 | Stateless migration controller; bounded concurrency; Capsule + Karpenter to 1000+ tenants; no per-tenant infrastructure overhead |
| **Security** | 5 | Defense-in-depth: gVisor + EFS access points + Capsule + NetworkPolicy. Each layer independent. No new attack surface in dev (gVisor optional) |
| **Performance** | 4.5 | ~100ms cutover (podIP update); EFS adds ~1-3ms vs EBS. -0.5: gVisor ~5% CPU overhead (prod only) |
| **SOLID** | 5 | SRP: migration controller does migration, workspace reconciler does pods. OCP: new triggers via Migration CRD, not code changes. LSP: workspace reconciler behavior unchanged without migration. ISP: agentd snapshot endpoints are optional (no-op without migration). DIP: migration controller depends on workspace status interface, not reconciler internals |
| **Idiomatic** | 5 | Standard K8s patterns: CRD + reconciler, status subresource, conditions, write-ahead phases, leader election, label selectors |
| **Complexity** | 5 | Removed: lease fencing (unnecessary), proxy buffering (unnecessary), dual-pod workspace reconciler mode (too complex). Added: one conditional in handleActive. Right-sized for the problem |

**Overall: 4.9/5**

**Previous version issues fixed:**
1. ❌ Security model table said "Lease-based fencing" but S18.2 removed it → ✅ Fixed: table now says "Controller-driven phases"
2. ❌ S18.2 said "annotate workspace → workspace reconciler creates second pod" but reconciler uses deterministic pod naming and can't create a second pod → ✅ Fixed: migration controller creates pod directly + `Status.PodName` adoption
3. ❌ A6 said `/workspace/.opencode/` but actual path is `/workspace/.local/opencode/` → ✅ Fixed
4. ❌ No mention of `recoverFromTransientPodLoss` race after source pod deletion → ✅ Fixed: `Status.PodName` lookup prevents false recovery
5. ❌ S18.8 didn't acknowledge proxy namespace refactor needed (A14) → ✅ Fixed: called out as breaking change
6. ❌ WorkspaceWatcher only tracks phases, not podIP — SSE behavior during migration was ambiguous → ✅ Fixed: explicitly documented that SSE persists on old pod until deletion
