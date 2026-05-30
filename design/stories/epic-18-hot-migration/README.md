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
| A3 | Proxy resolves backend via `workspace.Status.PodIP` | ✅ `proxy.go:361` reads `workspace.Status.PodIP`; retries with fresh IP on failure (line 371) |
| A4 | Workspace reconciler sets PodIP from running pod | ✅ `controller.go:206`: `workspace.Status.PodIP = existingPod.Status.PodIP` |
| A5 | Agentd tracks session state (ID, status, title) in memory | ✅ `sessionStatusTracker` + `providerCache` in `cmd/workspace-agentd/main.go` |
| A6 | Opencode stores conversation history on filesystem (`/workspace/.opencode/`) | ✅ Implied by PVC-backed design; opencode reads state from disk on startup |
| A7 | SSE reconnection with `Last-Event-ID` is supported by the client SDK | ✅ SSE protocol standard; proxy already sends `text/event-stream` (proxy.go:233) |
| A8 | Current storage is Longhorn (RWO, ext4) | ✅ Threat model G23: `/dev/longhorn/pvc-... /workspace ext4 rw` |
| A9 | Single-node kind cluster used for local dev | ✅ `local/kind-cluster.yaml` — single control-plane node |
| A10 | Controller uses leader election via Lease | ✅ `controller/internal/common/leader_election.go` |

---

## Key Decisions (from design session 2026-05-30)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Storage backend (prod) | EFS | RWX native, no share-manager pods, cross-AZ, AWS-managed |
| Storage backend (dev/local) | Any RWX-capable CSI (Longhorn RWX, NFS, local-path-provisioner with NFS) | Epic must NOT be EFS-specific; StorageClass is the abstraction boundary |
| Volume access mode | RWX | Enables ~100ms cutover; both pods mount simultaneously |
| Sandbox runtime (prod) | gVisor | Eliminates container-escape risk that RWX would otherwise widen |
| Sandbox runtime (dev) | runc (gVisor optional) | gVisor not required for correctness; only for security hardening |
| Tenant isolation | Capsule + EFS access points (prod) / namespace-only (dev) | API-level isolation + AWS-enforced root directory per workspace |
| Compute (prod) | Graviton Spot (80%) + On-Demand baseline (20%) | 60-70% cost savings; Spot reclamation handled by migration system |
| IAM model | One IRSA role per component | Scales to 1000+ tenants without per-tenant IAM roles |

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│              Migration Controller                          │
│  Triggers: node pressure, Spot warning, manual            │
└──────────┬───────────────────────────────────┬───────────┘
           │ 1. Start target pod                │ 3. Update workspace.status.podIP
           ▼                                    ▼
┌─────────────────────┐          ┌──────────────────────────┐
│  Source Pod (Node A) │          │  Target Pod (Node B)      │
│  workspace-agentd    │──2.───── │  workspace-agentd         │
│                      │  session │                           │
└────────┬─────────────┘  state  └────────┬──────────────────┘
         │                                 │
         └────────────┬────────────────────┘
                      ▼
           ┌─────────────────────┐
           │   RWX PVC            │  ← both pods mount simultaneously
           │   /workspace          │     zero data movement
           └─────────────────────┘
```

**Migration sequence:**
1. Migration controller creates target pod on destination node (mounts same RWX PVC)
2. Transfer session state: `GET /v1/migrate/snapshot` on source agentd → `POST /v1/migrate/restore` on target agentd (<500ms)
3. Migration controller updates `workspace.status.podIP` to target pod IP → proxy naturally routes to new pod on next request
4. Terminate source pod (background, after drain period)

**Total user-visible disruption:** ~100ms (one HTTP retry or SSE reconnect)

**Why this works on any RWX storage (not just EFS):**
- The migration sequence depends only on: (a) two pods mounting the same PVC simultaneously, (b) agentd snapshot/restore endpoints, (c) proxy reading `workspace.status.podIP`. None of these require EFS specifically.
- EFS access points provide *additional* tenant isolation in production but are not required for the migration mechanism itself.

---

## Security Model

| Layer | Control | Required For |
|-------|---------|--------------|
| Container escape prevention | gVisor RuntimeClass | Production only (defense-in-depth for RWX) |
| Cross-tenant API isolation | Capsule virtual namespaces | Multi-tenant production |
| Cross-tenant storage isolation | EFS access points (AWS-enforced root dir + UID/GID) | Multi-tenant production |
| Migration safety | Lease-based fencing — only one agentd writes session state at a time | All environments |
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
- For local kind testing: use [nfs-subdir-external-provisioner](https://github.com/kubernetes-sigs/nfs-subdir-external-provisioner) or Longhorn with `accessMode: rwx` (Longhorn uses share-manager pods for RWX — acceptable for dev, not for prod).
- EFS mount options: `tls,iam` for encryption in transit + IAM-based mount authorization (prod only).
- The `storageClassName` field on WorkspaceStorageConfig is already optional — workspaces that don't specify it get the cluster default (RWO).

**Estimated Effort:** 2 points

---


### S18.2 — Migration CRD & Reconciler

**Goal:** Define a `Migration` CRD that declaratively represents a workspace migration, and implement the reconciler that orchestrates the 4-step sequence.

**Acceptance Criteria:**
- [ ] `Migration` CRD registered in `pkg/apis/llmsafespace/v1/migration_types.go`
- [ ] Spec fields: `workspaceRef` (string, workspace name), `targetNode` (string, optional — empty = scheduler picks), `reason` (enum: `SpotReclamation | NodePressure | Manual | Maintenance`), `priority` (int32, higher = sooner)
- [ ] Status fields: `phase` (enum: `Pending | CreatingTarget | TransferringState | CuttingOver | Cleanup | Completed | Failed`), `startTime` (metav1.Time), `completionTime` (metav1.Time), `sourceNode` (string), `targetNode` (string), `cutoverDurationMs` (int64), `error` (string), `conditions` ([]metav1.Condition)
- [ ] Reconciler implements the 4-step sequence with idempotent phase transitions (crash at any point → re-reconcile resumes from current phase)
- [ ] Phase transition is persisted to status BEFORE executing the next step (write-ahead pattern)
- [ ] Only one active Migration per workspace — reconciler rejects (sets Failed) if another Migration for the same workspace is in-progress
- [ ] Timeout per phase: CreatingTarget=60s, TransferringState=10s, CuttingOver=5s — exceeded → phase=Failed with descriptive error
- [ ] Failed migrations trigger rollback: delete target pod, leave source pod running, clear any migration annotations on workspace
- [ ] Workspace reconciler integration: when migration controller sets annotation `llmsafespace.dev/migration-target-node` on workspace, workspace reconciler creates a second pod with nodeAffinity for that node (new behavior — currently it only allows one pod)
- [ ] After target pod is Ready, migration controller proceeds to TransferringState
- [ ] After state transfer, migration controller patches `workspace.status.podIP` to target pod IP (CuttingOver phase)
- [ ] After cutover, migration controller deletes source pod and removes migration annotation (Cleanup phase)
- [ ] Metrics: `migration_total{reason,outcome}` (counter), `migration_cutover_duration_seconds` (histogram), `migration_in_progress` (gauge)
- [ ] RBAC: migration controller needs `get/list/watch/create/delete` on Pods, `get/update/patch` on Workspaces, `get/create/update/delete` on Migrations
- [ ] Unit tests: happy path, timeout at each phase, concurrent migration rejection, crash recovery at each phase

**CRD Sketch:**
```yaml
apiVersion: llmsafespace.dev/v1
kind: Migration
metadata:
  name: migrate-ws-abc123-1717100000
  namespace: tenant-acme
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
  cutoverDurationMs: 95
```

**Implementation Notes:**
- Follow the same controller-runtime pattern as `WorkspaceReconciler`: single reconciler, status subresource, conditions.
- The workspace reconciler needs a small change: if annotation `llmsafespace.dev/migration-target-node` is present AND workspace is Active, allow a second pod to exist (currently `handleActive` assumes exactly one pod). The second pod gets `nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution` for the target node.
- The migration controller does NOT directly create pods — it annotates the workspace and lets the workspace reconciler handle pod creation. This preserves the single-owner principle (workspace reconciler owns pods).
- Lease-based fencing (from original design) is REMOVED. Rationale: the migration sequence is controller-driven and sequential. The source agentd is read-only during snapshot (GET), and the target agentd is write-only during restore (POST). There is no concurrent-write window because the proxy doesn't route to the target until `podIP` is updated. A Lease adds complexity without preventing a real failure mode.

**Estimated Effort:** 8 points

---


### S18.3 — Session State Snapshot/Restore

**Goal:** Add snapshot and restore endpoints to `workspace-agentd` so the migration controller can transfer in-memory session routing state between pods.

**Acceptance Criteria:**
- [ ] `GET /v1/migrate/snapshot` returns JSON blob of agentd's in-memory state
- [ ] `POST /v1/migrate/restore` accepts the snapshot and reconstructs internal state
- [ ] Snapshot includes: session status tracker state (session ID → busy/idle), provider cache (connected providers, configured count), agentd uptime reset
- [ ] Snapshot does NOT include: opencode process state (it reads from `/workspace/.opencode/` on disk), credentials (injected from K8s Secrets on pod start), file contents (on shared PVC)
- [ ] Snapshot size < 50KB for a workspace with 5 active sessions (it's just a routing table — validated by unit test)
- [ ] Restore is idempotent — calling restore twice with same snapshot produces same state
- [ ] Snapshot endpoint returns `409 Conflict` if a snapshot was already taken and not yet consumed (prevents double-snapshot)
- [ ] Restore endpoint returns `409 Conflict` if agentd already has active sessions from a prior restore
- [ ] Target pod's `/v1/readyz` returns `true` only after: (a) opencode process is healthy, AND (b) restore has been called (or no migration is in progress). This prevents the proxy from routing to a target pod that hasn't received state yet.
- [ ] Auth: endpoints are only accessible from within the cluster (bound to `0.0.0.0:4097` which is not exposed via Ingress). Migration controller calls them via pod IP + cluster DNS.
- [ ] Unit tests: snapshot/restore round-trip, idempotency, conflict detection, snapshot size assertion

**Snapshot Schema:**
```json
{
  "version": 1,
  "timestamp": "2026-05-30T10:00:00Z",
  "workspaceID": "ws-abc123",
  "sessionStatuses": {
    "ses_001": "busy",
    "ses_002": "idle"
  },
  "providerCache": {
    "connected": ["anthropic"],
    "configured": 2
  }
}
```

**What about opencode's in-flight LLM requests?**
- If opencode has an in-flight LLM request during migration, the source pod's opencode process will be terminated mid-request when the source pod is deleted.
- The target pod starts a fresh opencode process that reads conversation state from `/workspace/.opencode/` (shared PVC).
- The client will see the SSE stream close → reconnect → resume. The partial LLM response is lost; the client can re-send the last message.
- This is acceptable: migrations are rare events (~1/hour for spot, less for load balancing), and losing a partial response is equivalent to a network blip.

**Implementation Notes:**
- The snapshot/restore endpoints are added to the existing `mux` in `cmd/workspace-agentd/main.go`.
- The `sessionStatusTracker` and `providerCache` are already struct types — add `MarshalJSON`/`UnmarshalJSON` or export their fields for serialization.
- No new dependencies required.
- The migration controller calls these endpoints using the pod's cluster IP (from `pod.Status.PodIP`) on port 4097.

**Estimated Effort:** 3 points

---


### S18.4 — Proxy Connection Handoff

**Goal:** Ensure the API proxy seamlessly routes to the new pod after migration with minimal client disruption.

**Acceptance Criteria:**
- [ ] Proxy already reads `workspace.Status.PodIP` on every request (validated: proxy.go:361) — no buffering needed for HTTP requests
- [ ] Proxy already retries with fresh pod IP on failure (validated: proxy.go:371) — migration cutover naturally handled
- [ ] SSE streams: when source pod dies, SSE connection closes → client reconnects → proxy reads new `podIP` → stream resumes on target pod
- [ ] SDK `Retry-After` header: proxy returns `503` with `Retry-After: 1` (not 10) when workspace is in migration (detected via annotation `llmsafespace.dev/migration-target-node` present on workspace)
- [ ] Proxy invalidates `pwCache` entry for workspace when it detects pod IP changed (prevents stale password cache pointing to dead pod)
- [ ] Integration test: start SSE stream → trigger migration → verify stream resumes on new pod within 2 seconds

**Implementation Notes:**
- The proxy does NOT need a buffer or atomic swap mechanism. Here's why:
  - HTTP requests: proxy reads `workspace.Status.PodIP` per-request. During the brief window where podIP is being updated, the old pod is still running. After update, new requests go to new pod. Requests in-flight on old pod complete normally (pod isn't deleted until Cleanup phase).
  - SSE streams: the SSE connection is to the old pod. When the old pod is eventually deleted (Cleanup phase, after cutover), the TCP connection breaks. The client reconnects, and the proxy routes to the new podIP.
  - This means there is NO request loss and NO buffering needed. The "100ms disruption" is actually just the SSE reconnect time.
- The only code change needed: (a) reduce `retryAfterSec` from 10 to 1 when migration annotation is present, (b) invalidate `pwCache` on pod IP change.
- The existing retry logic (proxy.go:371) already handles the race where podIP changes between read and proxy attempt.

**Why the original "buffer + atomic swap" design was over-engineered:**
- The proxy is stateless per-request (reads podIP from workspace status each time).
- The old pod remains running until Cleanup phase — there's no window where neither pod is available.
- SSE reconnection is already a client responsibility (standard SSE protocol).
- Adding a buffer introduces complexity (memory management, overflow handling, ordering) for zero benefit.

**Estimated Effort:** 2 points

---


### S18.5 — Spot Reclamation Handler

**Goal:** Automatically migrate all workspace pods off a node when AWS issues a Spot termination notice (2-minute warning).

**Acceptance Criteria:**
- [ ] AWS Node Termination Handler (NTH) deployed as DaemonSet on Spot nodes
- [ ] NTH detects Spot interruption via IMDS (`/latest/meta-data/spot/instance-action`)
- [ ] On detection: NTH cordons the node and creates a `Migration` CR for every workspace pod on the condemned node
- [ ] Migrations are prioritized: `priority` field set based on session activity (busy sessions get higher priority)
- [ ] All migrations must complete within 90 seconds (leaving 30s buffer before termination)
- [ ] If migration cannot complete in time: workspace enters `Suspending` phase (existing behavior — PVC retained, pod lost, auto-resumes on next access)
- [ ] Metric: `spot_reclamation_total` (counter), `spot_reclamation_succeeded` / `spot_reclamation_suspended` (counters)
- [ ] Alert rule: if `spot_reclamation_suspended / spot_reclamation_total > 0.05` over 1 hour
- [ ] Integration test: simulate spot interruption (delete node with 2-min grace) → verify workspaces migrate or gracefully suspend

**Implementation Notes:**
- Use [AWS Node Termination Handler](https://github.com/aws/aws-node-termination-handler) in IMDS mode (lowest latency, no SQS dependency).
- NTH cordons the node but does NOT delete pods. The migration controller handles pod lifecycle.
- Parallel migrations: migration controller processes up to 10 concurrent Migrations per node (configurable via ConfigMap). Beyond that, queue by priority.
- Fallback path (migration fails or times out): migration controller sets workspace phase to `Suspending`. The existing workspace reconciler handles suspension (deletes pod, retains PVC). On next user request, workspace auto-resumes on a healthy node.
- Karpenter integration: add `karpenter.sh/do-not-disrupt: "true"` annotation to workspace pods so Karpenter doesn't independently consolidate them. Karpenter still provisions replacement nodes when the condemned node is cordoned.
- **Dev/local:** This story is production-only. On local kind clusters (single node, no Spot), this controller is a no-op. Deploy is gated by Helm value `spotHandler.enabled: false` (default).

**Estimated Effort:** 4 points

---


### S18.6 — Proactive Load Balancing

**Goal:** Automatically migrate workspaces off nodes approaching resource exhaustion before users experience degradation.

**Acceptance Criteria:**
- [ ] Separate goroutine within migration controller evaluates node pressure every 30 seconds
- [ ] Reads node metrics from metrics-server (CPU/memory utilization per node)
- [ ] Configurable thresholds via ConfigMap (hot-reloadable): `highWaterMark` (trigger), `lowWaterMark` (target after rebalance)
- [ ] Defaults: CPU 80%/60%, Memory 85%/65%
- [ ] When node exceeds high watermark for >60 seconds: creates Migration CRs for least-active workspace pods on that node
- [ ] "Least active" = longest time since last proxy request (uses existing `workspace.status.lastActivityAt` or annotation)
- [ ] Does NOT migrate pods with `sessionsActive > 0` (checked via agentd `/v1/statusz` endpoint — already exists)
- [ ] Cooldown: max 1 migration per workspace per 10 minutes; max 3 migrations per node per 5 minutes
- [ ] Target node selection: prefer nodes below low watermark, same AZ as source (minimize cross-AZ latency)
- [ ] Dry-run mode: `loadBalancer.dryRun: true` in ConfigMap → logs decisions without creating Migration CRs
- [ ] Metric: `proactive_migration_total{trigger}` (counter), `node_pressure_seconds` (histogram)

**Implementation Notes:**
- This is a background loop, not a separate controller. It runs inside the migration controller binary and creates Migration CRs when thresholds are breached.
- Pod selection: list workspace pods on hot node → sort by `lastActivityAt` ascending → select pods until projected node utilization drops below low watermark.
- The `statusz` endpoint on agentd (port 4097) already returns `sessions_active`. The load balancer calls this before migrating to avoid disrupting active conversations.
- Cross-AZ: EFS is cross-AZ transparent. Longhorn RWX is also cross-AZ (data locality disabled). No special handling needed.
- **Dev/local:** Functional on multi-node clusters (e.g., kind with 3 workers). On single-node kind, it's a no-op (no target node available). Gated by `loadBalancer.enabled: true`.

**Estimated Effort:** 4 points

---


### S18.7 — gVisor RuntimeClass (Production Hardening)

**Goal:** Deploy gVisor as the container runtime for workspace pods in production, providing kernel-level isolation that compensates for the expanded attack surface of RWX mounts.

**Acceptance Criteria:**
- [ ] gVisor (`runsc`) installed on Graviton worker nodes via Karpenter `userData` bootstrap script
- [ ] `RuntimeClass` resource `gvisor` created with `handler: runsc`
- [ ] Workspace reconciler conditionally sets `spec.runtimeClassName` based on new field `workspace.spec.runtimeClass` (default: empty = use node default; `gvisor` = use gVisor)
- [ ] Helm value `workspace.defaultRuntimeClass: gvisor` sets the default for all new workspaces in production
- [ ] All runtime images (Python 3.11, Node 20, Go 1.22, Rust, Java 21) pass their test suites under gVisor
- [ ] `mise install` works under gVisor (test: `mise install python@3.12 node@20`)
- [ ] File I/O benchmark: <20% overhead vs runc on EFS (measured with `fio` random read/write 4K blocks)
- [ ] opencode agent session (prompt → response → tool use → file write) works end-to-end under gVisor
- [ ] Compatibility test matrix automated in CI (run on gVisor-enabled node pool)

**Compatibility Test Matrix:**

| Runtime | Test | Risk |
|---------|------|------|
| Python 3.11 | `pip install numpy pandas && python -c "import numpy"` | Low (numpy uses mmap — supported) |
| Node 20 | `npm install && node -e "require('express')"` | Low |
| Go 1.22 | `go build ./...` | Low |
| Rust | `cargo build` | Low |
| Java 21 | `javac Hello.java && java Hello` | Medium (JIT perf) |
| mise | `mise install python@3.12 node@20` | Medium (downloads + extracts) |
| opencode | Full agent session | Low |

**Implementation Notes:**
- gVisor on ARM64 (Graviton): supported since 2023. Use `containerd-shim-runsc-v1` for `linux/arm64`.
- Known gVisor limitations that DON'T affect us: no FUSE, limited `/proc` (opencode doesn't need it), no `ptrace`.
- Java concern: gVisor supports JVM but JIT compilation may be slower. Benchmark startup time. If >2x slower, document as known limitation (not a blocker — Java workspaces are rare).
- **Dev/local:** gVisor is NOT required for local development. The migration mechanism works identically with runc. gVisor is a security hardening layer, not a functional requirement. Helm value `workspace.defaultRuntimeClass: ""` (default) means no RuntimeClass is set → uses node default (runc).
- CRD change: add `runtimeClass` field to `WorkspaceSpec` (optional string, no enum — allows future runtime classes).

**Estimated Effort:** 5 points

---


### S18.8 — Tenant Namespace Isolation (Capsule)

**Goal:** Provide API-level tenant isolation using Capsule so tenants cannot see or interact with each other's resources.

**Acceptance Criteria:**
- [ ] Capsule operator deployed via Helm (production only)
- [ ] `Tenant` CR created per onboarded tenant with: namespace quota, ResourceQuota, LimitRange, NetworkPolicy
- [ ] Workspace CRDs are created in tenant namespace — tenant A cannot list/get tenant B's workspaces via K8s API
- [ ] Migration CRDs are scoped to tenant namespace
- [ ] NetworkPolicy per tenant namespace: deny all ingress from other tenant namespaces; allow from API server namespace
- [ ] ResourceQuota per tenant: configurable max workspaces, max total storage, max CPU/memory
- [ ] API server maps JWT `tenant_id` claim → tenant namespace for all workspace operations
- [ ] Tenant deletion cascades: delete Capsule Tenant → all namespaces, workspaces, PVCs cleaned up
- [ ] Scale test: 100 tenants × 10 workspaces — controller reconcile latency <500ms p99
- [ ] EFS access points (production): one access point per workspace, root directory `/tenants/{tenant_id}/workspaces/{workspace_id}`, UID/GID 1000:1000

**Why Capsule (not vCluster or HNC):**

| Criterion | vCluster | Capsule | Decision |
|-----------|----------|---------|----------|
| Per-tenant overhead | ~256MB RAM | ~0 | Capsule wins at 1000+ tenants |
| Isolation strength | Full API server | Policy-enforced RBAC + NetworkPolicy | Sufficient (defense-in-depth with EFS access points) |
| CRD visibility | Fully isolated | Requires RBAC rules | Acceptable — tenants don't interact with CRDs directly |
| Maturity | Production-ready | Production-ready | Tie |

**Implementation Notes:**
- Capsule provides namespace-level isolation via policies. Tenants don't get direct K8s API access — they interact through the LLMSafeSpace API, which enforces tenant scoping.
- The workspace controller uses a shared informer cache (not per-tenant). Tenant isolation is enforced at the API layer, not the controller layer. The controller has cluster-wide RBAC.
- EFS access points provide a second layer of storage isolation (AWS-enforced, independent of K8s RBAC). Even if a pod escapes its namespace, it can only access its own access point's root directory.
- **Dev/local:** Single-tenant mode. All workspaces in a single namespace (`llmsafespace`). Capsule not deployed. Helm value `multiTenant.enabled: false` (default).

**Estimated Effort:** 8 points

---


### S18.9 — Karpenter NodePool Configuration (Production)

**Goal:** Configure Karpenter for cost-optimized Graviton Spot compute with proper disruption handling.

**Acceptance Criteria:**
- [ ] Karpenter deployed and configured as sole node provisioner (cluster autoscaler removed)
- [ ] `baseline` NodePool: On-Demand Graviton (c7g, m7g), system workloads only (taint `workload-type=system:NoSchedule`), min 2 nodes
- [ ] `workspaces` NodePool: Spot Graviton (c7g, m7g, r7g — 6+ instance types), workspace pods only (toleration `workload-type=workspace`)
- [ ] Workspace pods annotated `karpenter.sh/do-not-disrupt: "true"` (migration controller handles moves)
- [ ] Consolidation: `WhenEmpty` for workspace pool, `WhenUnderutilized` for baseline
- [ ] Node expiry: 7 days max lifetime (forces rotation for security patching + gVisor updates)
- [ ] Topology spread: workspace pods spread across AZs (`maxSkew: 1`)
- [ ] EC2NodeClass: AL2023 AMI, gVisor install in userData, proper subnet/SG selectors
- [ ] Cost allocation tags: `team`, `environment`, `workload-type` on all nodes
- [ ] Metric: `karpenter_nodes_total{pool,instance_type}`, `karpenter_spot_interruption_total`

**NodePool Sketch:**
```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: workspaces
spec:
  template:
    metadata:
      labels:
        workload-type: workspace
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
- The 80/20 Spot/OD split is achieved by taints: system pods tolerate only `baseline` pool taint; workspace pods tolerate only `workspaces` pool taint.
- Spot best practices: Karpenter uses `price-capacity-optimized` allocation strategy by default.
- **Dev/local:** Karpenter is NOT used locally. Kind cluster uses its own node provisioning. This story is production-only. Helm value `karpenter.enabled: false` (default).

**Estimated Effort:** 3 points

---


## Implementation Order

```
Phase A (Foundation — works on local + prod):
  S18.1 (RWX StorageClass) → S18.3 (Snapshot/Restore) → S18.2 (Migration CRD)

Phase B (Core migration — works on local multi-node):
  S18.4 (Proxy Handoff) → S18.6 (Load Balancing)

Phase C (Production hardening — prod only):
  S18.7 (gVisor) → S18.9 (Karpenter) → S18.5 (Spot Handler)

Phase D (Multi-tenancy — prod only):
  S18.8 (Capsule + EFS Access Points)
```

**Rationale:**
- Phase A+B are storage-backend-agnostic and testable on a local multi-node kind cluster with Longhorn RWX or NFS.
- Phase C+D are production-specific hardening and scaling concerns.
- A developer can validate the entire migration mechanism locally without EFS, gVisor, Karpenter, or Capsule.

**Total estimated effort:** 39 points (~3 sprints at team capacity)

---

## Cost Model (500 concurrent workspaces, production)

| Component | Monthly | Notes |
|-----------|---------|-------|
| Compute (Spot 80% + OD 20%) | ~$5,500 | c7g.xlarge Spot ~$0.04/hr vs $0.17/hr OD |
| EFS (20TB, elastic throughput) | ~$6,000 | $0.30/GB-month + throughput charges |
| EKS control plane | $73 | Fixed |
| NAT Gateway | ~$500 | Data transfer dependent |
| Karpenter | $0 | Open source |
| gVisor | $0 | Open source, ~5% CPU overhead |
| **Total** | **~$12,000/mo** | **~$24/workspace/month** |

**vs current (EBS + On-Demand):** ~$28,000/mo → **57% savings**

---

## Risks & Mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|------------|------------|
| R1 | gVisor incompatibility with specific runtime tooling | Workspace broken for affected runtime | Medium | Compatibility matrix in S18.7; `runtimeClass` field allows per-workspace opt-out |
| R2 | EFS latency spikes under load | Degraded workspace I/O | Low | Elastic throughput; CloudWatch monitoring; fallback to provisioned throughput |
| R3 | Spot interruption rate exceeds migration capacity | Forced suspensions (not data loss) | Low | 6+ instance type diversification; OD baseline absorbs overflow; suspension is graceful |
| R4 | Session state transfer fails mid-flight | User sees SSE reconnect + loses partial response | Medium | Rollback to source pod; client auto-retries; no data corruption possible (PVC is shared) |
| R5 | EFS access point limit (1000/filesystem) | Cannot create new workspaces beyond limit | Medium | Monitor count; provision second EFS filesystem before hitting limit |
| R6 | Workspace reconciler dual-pod logic introduces bugs | Existing workspaces affected | Medium | Feature-gated behind migration annotation; extensive unit tests; no behavior change without annotation |

---

## Open Questions

| # | Question | Status | Resolution |
|---|----------|--------|------------|
| Q1 | vCluster vs Capsule for 1000+ tenants? | ✅ Resolved | Capsule — vCluster overhead prohibitive (see S18.8) |
| Q2 | gVisor + Java JIT performance? | 🔶 Open | Benchmark in S18.7; document if >2x slower |
| Q3 | EFS throughput mode? | 🔶 Open | Start with elastic; switch to provisioned if p99 > 10ms |
| Q4 | Session state size? | ✅ Resolved | <50KB (just routing table, not conversation history) |
| Q5 | Migration SLO? | ✅ Resolved | p99 cutover < 500ms, p99 total < 10s |
| Q6 | EFS access point limit? | 🔶 Open | Monitor; plan second filesystem at 800 workspaces |
| Q7 | Does this require EFS? | ✅ Resolved | No. Any RWX-capable CSI works. EFS is prod recommendation; Longhorn RWX / NFS works for dev/local |

---

## Success Metrics

| Metric | Target | Source |
|--------|--------|--------|
| Migration cutover duration (p99) | < 500ms | `migration_cutover_duration_seconds` |
| Total migration duration (p99) | < 10s | Migration CRD `completionTime - startTime` |
| Spot reclamation success rate | > 95% | `spot_reclamation_succeeded / spot_reclamation_total` |
| User-visible errors during migration | 0 dropped requests | Proxy logs (503 without subsequent retry = dropped) |
| Cost per workspace per month | < $25 | AWS Cost Explorer |
| gVisor I/O overhead | < 20% | `fio` benchmark suite |

---

## Design Assessment

| Dimension | Score (1-5) | Notes |
|-----------|-------------|-------|
| **Robustness** | 5 | Crash recovery at every phase (write-ahead status); graceful fallback to suspension if migration fails; no data loss possible (shared PVC) |
| **Reliability** | 5 | No single point of failure; migration failure = workspace suspends + auto-resumes (existing proven path); client auto-retries |
| **Maintainability** | 4 | Follows existing controller-runtime patterns; single-owner principle (workspace reconciler owns pods); migration controller only annotates. -1: dual-pod logic in workspace reconciler adds conditional complexity |
| **Scalability** | 5 | Stateless migration controller; parallel migrations bounded by configurable concurrency; Capsule + Karpenter scale to 1000+ tenants |
| **Security** | 5 | Defense-in-depth: gVisor (kernel isolation) + EFS access points (storage isolation) + Capsule (API isolation) + NetworkPolicy (network isolation). Each layer independent |
| **Performance** | 4 | ~100ms cutover (just a podIP update); EFS adds ~1-3ms latency vs EBS. -1: gVisor adds ~5% CPU overhead; EFS sequential I/O slower than EBS |
| **SOLID** | 4 | Single Responsibility (migration controller does migration, workspace reconciler does pods); Open/Closed (new triggers via Migration CRD, not code changes); -1: workspace reconciler gains migration-aware conditional |
| **Idiomatic** | 5 | Standard K8s patterns: CRD + reconciler, status subresource, conditions, annotations for cross-controller communication, leader election |
| **Complexity** | 4 | Right-sized: removed lease-based fencing (unnecessary), removed proxy buffering (unnecessary), kept CRD-driven orchestration (necessary). -1: 9 stories is a large epic; could split Phase C/D into separate epic |

**Overall: 4.6/5** — Well-architected for the problem space. Main complexity risk is the workspace reconciler dual-pod logic (S18.2), which should be thoroughly tested.
