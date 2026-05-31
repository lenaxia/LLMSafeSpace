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
| A1 | Workspace CRD supports `ReadWriteMany` access mode | ✅ `WorkspaceStorageConfig.AccessMode` enum includes `ReadWriteMany` (`pkg/apis/llmsafespace/v1/workspace_types.go:19`) |
| A2 | `buildPVC()` handles RWX | ✅ `controller.go:612`: `if workspace.Spec.Storage.AccessMode == "ReadWriteMany"` |
| A3 | Proxy resolves backend via `workspace.Status.PodIP` per-request | ✅ `proxy.go:293` fetches workspace CRD; line 361 reads `Status.PodIP`; retries with fresh IP on connection error (line 371) |
| A4 | Workspace reconciler sets PodIP during `handleCreating` only; `handleActive` does NOT re-set it | ✅ `controller.go:235` sets PodIP in handleCreating; handleActive only checks pod existence |
| A5 | Agentd tracks session state in memory via `sessionStatusTracker` | ✅ `cmd/workspace-agentd/main.go` — `statuses map[string]string` |
| A6 | Opencode stores conversation data at `$XDG_DATA_HOME/opencode/` on PVC | ✅ Entrypoint: `XDG_DATA_HOME=/workspace/.local`; opencode: `xdg-basedir` in `global.ts:10` |
| A7 | SSE reconnection is client-driven (standard protocol) | ✅ `proxy.go:233` sends `text/event-stream`; SSE spec requires client reconnect on close |
| A8 | Current storage is Longhorn (RWO, ext4) | ✅ Threat model G23: `/dev/longhorn/pvc-... /workspace ext4 rw` |
| A9 | Workspace reconciler finds pods by deterministic name `podName(workspace.Name, uid)` | ✅ `constants.go:45`; used in `handleCreating` (190), `handleActive` (255), `handleSuspending` (358), `handleTerminating` (406) |
| A10 | `handleActive` calls `recoverFromTransientPodLoss` when pod missing → clears PodIP, sets Creating | ✅ `controller.go:277` |
| A11 | `workspace.Status.PodName` is always set during `handleCreating` (line 216) | ✅ Set to `pod.Name` after pod creation |
| A12 | Password is per-workspace Secret, same for source and target pods | ✅ `ensurePasswordSecret` creates `{workspace}-password`; mounted via volume |
| A13 | Proxy uses single hardcoded namespace (`h.namespace`) | ✅ `proxy.go:293` — all `Workspaces(h.namespace)` calls |
| A14 | Workspace reconciler sets OwnerReference on pods (workspace owns pod) | ✅ `controller.go:207`: `controllerutil.SetControllerReference(workspace, pod, r.Scheme)` |
| A15 | `buildPod()` is unexported method on `WorkspaceReconciler` | ✅ `controller.go:640`: `func (r *WorkspaceReconciler) buildPod(...)` |
| A16 | Workspace admission webhook validates Workspace CRD only, not Pods | ✅ `workspace_webhook.go:17`: `resources=workspaces` — won't block migration controller pod creation |
| A17 | Opencode uses SQLite (WAL mode) for session/conversation storage | ✅ `opencode-upstream/packages/opencode/src/storage/db.ts:104`: `PRAGMA journal_mode = WAL`; DB at `$XDG_DATA_HOME/opencode/opencode.db` |
| A18 | SQLite + WAL over NFS = corruption if two processes open same DB | ✅ SQLite docs: WAL requires shared memory (`-shm` file via mmap); mmap not coherent across NFS clients. Two writers = guaranteed corruption |
| A19 | Agentd can run without opencode (`--supervise=false` mode) | ⚠️ NOT YET IMPLEMENTED — agentd currently always starts opencode. S18.3 must add this mode |

---

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Storage backend (prod) | EFS | RWX native, cross-AZ, AWS-managed, no share-manager pods |
| Storage backend (dev) | Any RWX CSI (Longhorn RWX, NFS) | StorageClass is the abstraction boundary |
| Default access mode | RWX for all new workspaces | Migration requires RWX; no reason to keep RWO as default |
| Sandbox runtime (prod) | gVisor | Kernel-level isolation for RWX attack surface |
| Sandbox runtime (dev) | runc | gVisor is security hardening, not functional requirement |
| Tenant isolation | Capsule + EFS access points (prod) | API-level + AWS-enforced storage isolation |
| Compute (prod) | Graviton Spot 80% + On-Demand 20% | 60-70% cost savings |
| Pod naming for migration | Target pod: `{workspace}-{uid[:8]}-mig`; after cutover, `Status.PodName` updated | Migration controller creates pod directly; workspace reconciler adopts via `Status.PodName` |
| Pod lookup in reconciler | Always use `workspace.Status.PodName` (not `podName()` derivation) | Simpler, no conditional; `Status.PodName` is always set (A11) |
| Concurrent write safety | Sequential phases, no Lease | No concurrent-write window exists by design |
| Opencode DB safety | Stop source opencode before starting target opencode | SQLite WAL over NFS corrupts if two processes open same DB (A17, A18). Sequential handoff is mandatory. |
| Target pod startup mode | Agentd-only (no opencode) until migration controller signals | Allows target pod to be "warm" (mounted, networked) without touching SQLite DB |
| Pod building for migration | Extract `buildPod` logic into shared `pkg/workspace/pod` package | Migration controller reuses same pod spec; avoids duplication |

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│              Migration Controller                          │
│  Triggers: node pressure, Spot warning, manual            │
└──┬────────────────────┬──────────────────────┬───────────┘
   │ 1. Create target   │ 4. Stop source OC    │ 7. Patch workspace.status
   │    (agentd only)   │ 5. Start target OC   │
   ▼                    ▼                      ▼
┌─────────────────────┐          ┌──────────────────────────┐
│  Source Pod (Node A) │          │  Target Pod (Node B)      │
│  agentd ✓            │──3.───── │  agentd ✓                 │
│  opencode ✓→✗(step4) │ snapshot │  opencode ✗→✓(step5)      │
└────────┬─────────────┘          └────────┬──────────────────┘
         │                                 │
         └────────────┬────────────────────┘
                      ▼
           ┌─────────────────────┐
           │   RWX PVC            │  ← both pods mount, but only ONE
           │   /workspace          │     opencode has DB open at a time
           └─────────────────────┘
```

**Migration sequence (8 steps):**
1. **CreatingTarget:** Create target pod with `--supervise=false` (agentd starts, opencode does NOT start). Same PVC, labels, password Secret, nodeAffinity, OwnerReference → workspace.
2. **WaitingAgentd:** Wait for target agentd healthy (`/v1/healthz` returns 200). Opencode is not running yet — no SQLite contention.
3. **Snapshotting:** `GET /v1/migrate/snapshot` on source agentd (captures session routing state).
4. **StoppingSource:** `POST /v1/migrate/stop-opencode` on source agentd → graceful SIGTERM to source opencode → wait for exit (WAL checkpoint, lock release). Source agentd remains running and returns `503 Retry-After: 5` to proxied requests.
5. **StartingTarget:** `POST /v1/migrate/start-opencode` on target agentd → starts opencode process → waits for healthy + providers connected. SQLite DB is now exclusively owned by target.
6. **Restoring:** `POST /v1/migrate/restore` on target agentd (restores session routing state).
7. **CuttingOver:** Patch `workspace.status.{podName, podIP, endpoint}` to target pod values. Proxy routes new requests to target.
8. **Cleanup:** Delete source pod.

**Why this sequence (SQLite safety):**
- Opencode uses SQLite with WAL mode for session/conversation storage (`/workspace/.local/opencode/opencode.db`)
- SQLite + WAL over NFS = corruption if two processes open the same DB simultaneously
- Steps 4-5 ensure **at most one opencode process has the DB open at any time**
- Source agentd stays alive during the gap (steps 4-6) to return `503 Retry-After` to any proxied requests

**User-visible disruption:**
- Steps 1-3: zero (source still serving normally)
- Steps 4-5: **5-10s of `503 Retry-After: 5`** (opencode handoff gap). SDK auto-retries. User sees "thinking..." slightly longer.
- Steps 6-8: zero (target serving, proxy cuts over)
- Total: **5-10s of retried requests** (vs 22s hard downtime with RWO volume migration)

**After step 7:** proxy routes new requests to target pod. Workspace reconciler finds target pod via `Status.PodName`.

**After step 8:** SSE connections to source pod break → clients reconnect → routed to target.

---

## Security Model

| Layer | Control | Environment |
|-------|---------|-------------|
| Container escape prevention | gVisor RuntimeClass | Production |
| Cross-tenant API isolation | Capsule namespaces | Production (multi-tenant) |
| Cross-tenant storage isolation | EFS access points (AWS-enforced root dir + UID/GID) | Production (multi-tenant) |
| Migration sequencing | Controller-driven phases; no concurrent-write window | All |
| Spot reclamation | Node termination handler → triggers migration | Production |

---

## Stories

### S18.1 — RWX Storage & Pod Lookup Refactor

**Goal:** Switch default storage to RWX and refactor workspace reconciler to use `Status.PodName` for pod lookup (prerequisite for migration).

**Acceptance Criteria:**
- [ ] Helm chart StorageClass template: configurable provisioner + parameters (EFS for prod, Longhorn RWX for dev)
- [ ] `WorkspaceStorageConfig.AccessMode` default changed from `ReadWriteOnce` to `ReadWriteMany`
- [ ] Mount options include `nosuid,nodev` (threat model G23)
- [ ] Two pods on different nodes can simultaneously mount the same PVC and read/write (integration test)
- [ ] Workspace reconciler refactored: `handleActive`, `handleSuspending`, `handleTerminating` use `workspace.Status.PodName` instead of `podName()` for pod lookup
- [ ] `handleCreating` continues to use `podName()` for NEW pod creation and sets `Status.PodName`
- [ ] `buildPod()` logic extracted to shared package `pkg/workspace/pod` (reusable by migration controller)
- [ ] Extracted `BuildPod()` accepts a pod name parameter (not hardcoded to `podName()` derivation)
- [ ] All existing unit tests pass with the refactored lookup
- [ ] Integration test: create workspace → verify pod found by `Status.PodName` → suspend → resume → verify still works

**Implementation Notes:**
- The refactor is safe because `Status.PodName` is always set during `handleCreating` (A11). Every workspace that reaches Active phase has this field populated.
- `buildPod` extraction: move to `pkg/workspace/pod/builder.go`. Accept `PodBuildParams` struct (workspace spec, name, namespace, runtime image, labels, annotations). Both workspace reconciler and migration controller call it.
- For local dev: Longhorn RWX uses share-manager pods internally. Alternatively, NFS-subdir-external-provisioner.

**Estimated Effort:** 3 points

---

### S18.2 — Migration CRD & Reconciler

**Goal:** Define `Migration` CRD and implement the reconciler that orchestrates the 8-step migration sequence.

**Acceptance Criteria:**
- [ ] `Migration` CRD in `pkg/apis/llmsafespace/v1/migration_types.go`
- [ ] Spec: `workspaceRef` (string), `targetNode` (string, optional), `reason` (enum: `SpotReclamation | NodePressure | Manual | Maintenance`), `priority` (int32)
- [ ] Status: `phase` (enum: `Pending | CreatingTarget | WaitingAgentd | Snapshotting | StoppingSource | StartingTarget | Restoring | CuttingOver | Cleanup | Completed | Failed`), `startTime`, `completionTime`, `sourceNode`, `targetNode`, `sourcePodName`, `targetPodName`, `cutoverDurationMs`, `handoffDurationMs`, `error`, `conditions`
- [ ] Reconciler implements 8-step sequence with idempotent phase transitions
- [ ] Write-ahead: phase persisted to status BEFORE executing next step
- [ ] One active Migration per workspace — reject if another in-progress
- [ ] Timeouts: CreatingTarget=60s, WaitingAgentd=30s, StoppingSource=15s, StartingTarget=120s, CuttingOver=5s
- [ ] Spot-triggered migrations use tighter timeouts (see S18.5): total budget 75s. Migration spec carries `timeoutBudgetSeconds` field; reconciler uses min(phase default, remaining budget).
- [ ] Abort (set Failed) if workspace phase is no longer `Active` at any step — prevents conflict with restart/suspend/terminate
- [ ] Failed → rollback: delete target pod, leave source running (if source opencode was stopped, restart it via `POST /v1/migrate/start-opencode`)
- [ ] Target pod created via shared `pkg/workspace/pod.BuildPod()` with name `{workspace}-{uid[:8]}-mig`, nodeAffinity, and `AGENTD_SUPERVISE=false` env var (agentd starts without launching opencode)
- [ ] Target pod has OwnerReference → workspace (for GC if workspace deleted during migration)
- [ ] CuttingOver phase patches `workspace.status.{podName, podIP, endpoint}` atomically
- [ ] Cleanup phase deletes source pod by `migration.status.sourcePodName`
- [ ] Metrics: `migration_total{reason,outcome}`, `migration_cutover_duration_seconds`, `migration_in_progress`
- [ ] RBAC: `get/list/watch/create/delete` Pods; `get/update/patch` Workspaces + status; `get/create/update/delete` Migrations
- [ ] Unit tests: happy path, timeout per phase, concurrent rejection, crash recovery per phase

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
  handoffDurationMs: 7200
```

**Why migration controller creates pods directly:**
- Workspace reconciler uses deterministic naming (A9) — cannot create a second pod with a different name
- Migration controller reuses the same `BuildPod()` function (extracted in S18.1) with a different name parameter
- OwnerReference ensures GC correctness (A14 pattern preserved)
- After cutover, workspace reconciler adopts target pod via `Status.PodName` — no special logic needed

**Estimated Effort:** 8 points

---

### S18.3 — Agentd Migration Endpoints

**Goal:** Add migration lifecycle endpoints to `workspace-agentd`: snapshot/restore session state, stop/start opencode process, and support `--supervise=false` startup mode.

**Acceptance Criteria:**
- [ ] `--supervise=false` flag (or `AGENTD_SUPERVISE=false` env): agentd starts without launching opencode. `/v1/healthz` returns healthy, `/v1/readyz` returns `ready: false` (no opencode).
- [ ] `POST /v1/migrate/stop-opencode`: sends SIGTERM to managed opencode process, waits for exit (max 10s, then SIGKILL). Returns 200 when opencode is fully stopped. After stop, agentd returns `503 Retry-After: 5` for all proxied requests.
- [ ] `POST /v1/migrate/start-opencode`: starts opencode process (same as `--supervise` mode). Returns 200 when opencode is healthy + providers connected. `/v1/readyz` becomes true.
- [ ] `GET /v1/migrate/snapshot`: returns JSON of agentd in-memory state (session statuses, provider cache)
- [ ] `POST /v1/migrate/restore`: accepts snapshot, reconstructs state
- [ ] Snapshot size < 50KB for 5 sessions (unit test)
- [ ] Restore is idempotent
- [ ] `409 Conflict` if snapshot already taken or restore already applied
- [ ] All endpoints cluster-internal only (port 4097, not exposed via Ingress)
- [ ] Unit tests: supervise=false mode, stop/start lifecycle, snapshot round-trip, conflict detection

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

**SQLite safety guarantee:**
- Source opencode stopped (step 4) → WAL checkpointed, DB locks released, `-shm`/`-wal` files flushed
- Target opencode started (step 5) → opens DB exclusively, no contention
- At no point do two opencode processes have the DB open simultaneously

**Implementation Notes:**
- `managedProcess` in `cmd/workspace-agentd/main.go` already has `start()` and `restart()` methods. Add `stop()` that sends SIGTERM + waits.
- `--supervise=false`: skip the `proc.start()` call in `main()`. Agentd serves health/status endpoints but opencode isn't running.
- After `stop-opencode`, set a flag so that `/v1/readyz` returns false and proxied requests get 503.

**Estimated Effort:** 5 points

---

### S18.4 — Proxy Handoff

**Goal:** Ensure proxy handles the migration gracefully: routes to source during normal operation, returns retryable errors during handoff gap, routes to target after cutover.

**Acceptance Criteria:**
- [ ] During steps 1-3 (source opencode running): proxy routes normally to source pod via `Status.PodIP`
- [ ] During steps 4-5 (handoff gap): source agentd returns `503 Retry-After: 5` for proxied requests. Proxy passes this through to client. SDK auto-retries.
- [ ] After step 7 (cutover): proxy reads updated `Status.PodIP`, routes to target pod
- [ ] After step 8 (source deleted): SSE connections break, clients reconnect, routed to target
- [ ] Proxy returns `Retry-After: 1` (not 10) when workspace has active Migration CR
- [ ] Integration test: send request during handoff gap → get 503 → retry after cutover → success

**Why this is simple:**
- During handoff gap, the SOURCE agentd handles the 503 (it knows opencode is stopped). Proxy doesn't need special logic.
- After cutover, proxy reads new `Status.PodIP` per-request (A3). No buffering needed.
- The only proxy change: reduce `Retry-After` from 10 to 1 when migration is active.

**Estimated Effort:** 1 point

---

### S18.5 — Spot Reclamation Handler

**Goal:** Migrate workspace pods off a node on Spot termination notice (2-min warning).

**Acceptance Criteria:**
- [ ] AWS Node Termination Handler (NTH) deployed as DaemonSet on Spot nodes
- [ ] NTH detects interruption via IMDS, cordons node, creates `Migration` CR per workspace pod
- [ ] Priority based on session activity (busy > idle)
- [ ] Spot migrations use tighter phase timeouts: CreatingTarget=20s, WaitingAgentd=10s, StoppingSource=10s, StartingTarget=30s, CuttingOver=5s (total budget: 75s, within 90s window)
- [ ] If any phase exceeds its Spot timeout → abort migration, set workspace to `Suspending` immediately (don't wait for full timeout)
- [ ] Timeout fallback: workspace enters `Suspending` (PVC retained, auto-resumes on next access)
- [ ] Metrics: `spot_reclamation_total`, `spot_reclamation_succeeded`, `spot_reclamation_suspended`
- [ ] Alert: suspension rate > 5% over 1 hour
- [ ] Workspace pods annotated `karpenter.sh/do-not-disrupt: "true"`
- [ ] Integration test: simulate interruption → verify migrate or suspend within 90s

**Implementation Notes:**
- NTH IMDS mode (no SQS). Cordons but does NOT delete pods.
- Up to 10 parallel migrations per node. Queue by priority beyond that.
- Dev/local: disabled by default (`spotHandler.enabled: false`).

**Estimated Effort:** 4 points

---

### S18.6 — Proactive Load Balancing

**Goal:** Migrate workspaces off nodes approaching resource exhaustion.

**Acceptance Criteria:**
- [ ] Background goroutine evaluates node pressure every 30s via metrics-server
- [ ] ConfigMap thresholds (hot-reloadable): high=CPU 80%/Mem 85%, low=CPU 60%/Mem 65%
- [ ] Node exceeds high for >60s → create Migration CRs for least-active pods
- [ ] "Least active" = longest since `workspace.status.lastActivityAt`
- [ ] Skip pods with `sessionsActive > 0` (from `/v1/statusz`)
- [ ] Cooldown: 1 migration/workspace/10min, 3 migrations/node/5min
- [ ] Target node: below low watermark, prefer same AZ
- [ ] Dry-run mode via ConfigMap
- [ ] Metrics: `proactive_migration_total{trigger}`, `node_pressure_seconds`

**Implementation Notes:**
- Background loop in migration controller binary.
- Dev/local: functional on multi-node clusters; no-op on single-node.

**Estimated Effort:** 4 points

---

### S18.7 — gVisor RuntimeClass (Production)

**Goal:** gVisor as container runtime for workspace pods in production.

**Acceptance Criteria:**
- [ ] gVisor installed on Graviton nodes via Karpenter userData
- [ ] `RuntimeClass` resource `gvisor` with `handler: runsc`
- [ ] New CRD field `workspace.spec.runtimeClass` (optional string)
- [ ] Workspace reconciler (via `BuildPod()`) sets `pod.spec.runtimeClassName`
- [ ] Helm default: `workspace.defaultRuntimeClass: gvisor` (prod), `""` (dev)
- [ ] All runtimes pass tests under gVisor (Python, Node, Go, Rust, Java)
- [ ] `mise install` works under gVisor
- [ ] I/O benchmark: <20% overhead vs runc
- [ ] Compatibility matrix in CI

**Risks:** Java JIT may be slower. Benchmark; document if >2x startup.

**Estimated Effort:** 5 points

---

### S18.8 — Tenant Namespace Isolation (Capsule)

**Goal:** Multi-tenant isolation via Capsule namespaces.

**Acceptance Criteria:**
- [ ] Capsule operator deployed via Helm
- [ ] `Tenant` CR per tenant: namespace quota, ResourceQuota, LimitRange, NetworkPolicy
- [ ] Workspaces + Migrations scoped to tenant namespace
- [ ] NetworkPolicy: deny cross-tenant ingress; allow from API namespace
- [ ] Proxy refactored: resolve namespace from JWT `tenant_id` claim (replaces hardcoded `h.namespace`)
- [ ] All `Workspaces(h.namespace)` calls accept namespace parameter
- [ ] Tenant deletion cascades (namespace → workspaces → PVCs → pods)
- [ ] EFS access points: one per workspace, root `/tenants/{tenant_id}/workspaces/{workspace_id}`
- [ ] Scale test: 100 tenants × 10 workspaces, reconcile <500ms p99

**Why Capsule:** vCluster = ~256MB/tenant = 256GB at 1000 tenants. Capsule = ~0 overhead.

**Implementation Notes:**
- Controller keeps cluster-wide RBAC + shared informer. Isolation enforced at API layer.
- Proxy refactor: `ProxyHandler` methods accept `namespace` from auth middleware context.

**Estimated Effort:** 8 points

---

### S18.9 — Karpenter NodePool (Production)

**Goal:** Cost-optimized Graviton Spot compute.

**Acceptance Criteria:**
- [ ] Karpenter as sole provisioner
- [ ] `baseline` pool: On-Demand Graviton, system workloads, taint, min 2 nodes
- [ ] `workspaces` pool: Spot Graviton (6+ instance types), workspace pods
- [ ] `karpenter.sh/do-not-disrupt: "true"` on workspace pods
- [ ] Consolidation: `WhenEmpty` (workspaces), `WhenUnderutilized` (baseline)
- [ ] Node expiry: 7 days
- [ ] Topology spread: AZ `maxSkew: 1`
- [ ] EC2NodeClass: AL2023, gVisor in userData

**Implementation Notes:**
- Dev/local: not used. Kind manages nodes.

**Estimated Effort:** 3 points

---

## Implementation Order

```
Phase A (Foundation — local testable):
  S18.1 (RWX + reconciler refactor + BuildPod extraction)
  S18.3 (Snapshot/Restore endpoints)
  S18.2 (Migration CRD + reconciler)
  S18.4 (Proxy Retry-After)

Phase B (Triggers — local multi-node):
  S18.6 (Load Balancing)

Phase C (Production hardening):
  S18.7 (gVisor)
  S18.9 (Karpenter)
  S18.5 (Spot Handler)

Phase D (Multi-tenancy):
  S18.8 (Capsule + proxy namespace refactor)
```

Phase A delivers a working migration system testable on any multi-node cluster with RWX storage.
Phase B adds automated triggers. Phase C/D are production-only.

**Total: 41 points (~3 sprints)**

---

## Cost Model (500 concurrent workspaces, production)

| Component | Monthly | Notes |
|-----------|---------|-------|
| Compute (Spot 80% + OD 20%) | ~$5,500 | c7g.xlarge Spot ~$0.04/hr |
| EFS (20TB, elastic throughput) | ~$6,000 | $0.30/GB-month |
| EKS control plane | $73 | |
| NAT Gateway | ~$500 | |
| **Total** | **~$12,000/mo** | **$24/workspace/month** |

vs current (Longhorn + On-Demand): ~$28,000/mo → **57% savings**

---

## Risks & Mitigations

| # | Risk | Likelihood | Mitigation |
|---|------|------------|------------|
| R1 | gVisor incompatible with runtime tooling | Medium | Per-workspace `runtimeClass` opt-out; compatibility matrix |
| R2 | EFS latency spikes | Low | Elastic throughput; CloudWatch; fallback to provisioned |
| R3 | Spot interruption exceeds migration capacity | Low | 6+ instance types; OD baseline; graceful suspension fallback |
| R4 | Session transfer fails mid-flight | Medium | Rollback to source; client retries; no corruption (shared PVC) |
| R5 | EFS access point limit (1000/fs) | Medium | Monitor; second filesystem at 800 |
| R6 | SQLite DB corruption from concurrent opencode processes | **Mitigated** | Sequential handoff: source opencode stopped + WAL checkpointed before target starts (A17, A18). Enforced by migration controller phase ordering. |
| R7 | Rollback after source opencode stopped leaves workspace with no opencode running | Medium | Rollback handler calls `POST /v1/migrate/start-opencode` on source agentd to restart it |

---

## Open Questions

| # | Question | Status | Resolution |
|---|----------|--------|------------|
| Q1 | vCluster vs Capsule? | ✅ | Capsule (vCluster overhead prohibitive) |
| Q2 | gVisor + Java JIT? | 🔶 | Benchmark in S18.7 |
| Q3 | EFS throughput mode? | 🔶 | Start elastic; switch if p99 > 10ms |
| Q4 | Session state size? | ✅ | <50KB (routing table only) |
| Q5 | Migration SLO? | ✅ | p99 handoff gap < 15s, p99 total < 30s |
| Q6 | EFS access point limit? | 🔶 | Second filesystem at 800 workspaces |
| Q7 | Requires EFS? | ✅ | No. Any RWX CSI works |
| Q8 | Pod conflict with workspace reconciler? | ✅ | Direct creation + `Status.PodName` adoption |
| Q9 | Concurrent PVC writes? | ✅ | SQLite WAL over NFS = corruption. Solved by sequential opencode handoff (stop source → start target). |
| Q10 | User-visible disruption during migration? | ✅ | 5-10s of `503 Retry-After` during opencode handoff. SDK retries transparently. vs 22s hard downtime with RWO. |

---

## Success Metrics

| Metric | Target |
|--------|--------|
| Opencode handoff gap (p99) | < 15s |
| Total migration (p99) | < 30s |
| Spot reclamation success | > 95% |
| Requests dropped (not retried) | 0 |
| Cost per workspace/month | < $25 |
| gVisor I/O overhead | < 20% |

---

## Design Assessment

| Dimension | Score | Justification |
|-----------|-------|---------------|
| **Robustness** | 5 | Write-ahead phases; rollback at every step; fallback to suspension; no data loss (shared PVC) |
| **Reliability** | 5 | Failure = suspend + auto-resume (proven path); client retries; no SPOF |
| **Maintainability** | 5 | `BuildPod()` extracted to shared package (DRY); migration controller is isolated new component; workspace reconciler change is replacing `podName()` calls with `Status.PodName` reads (simpler, not more complex) |
| **Scalability** | 5 | Stateless controller; bounded concurrency; Capsule + Karpenter to 1000+ tenants |
| **Security** | 5 | Defense-in-depth: gVisor + EFS access points + Capsule + NetworkPolicy; sequential phases prevent concurrent-write |
| **Performance** | 4 | 5-10s handoff gap (opencode restart) vs 22s RWO migration — 2-4x improvement. EFS +1-3ms vs EBS. gVisor ~5% CPU. -1: handoff gap is user-visible (503 retries) |
| **SOLID** | 5 | SRP: migration controller migrates, workspace reconciler manages pods. OCP: new triggers via CRD. DIP: migration controller uses `BuildPod()` interface, not reconciler internals |
| **Idiomatic** | 5 | CRD + reconciler, status subresource, write-ahead, OwnerReferences, leader election |
| **Complexity** | 5 | No lease fencing, no proxy buffering, no dual-pod mode. One refactor (pod lookup) + one new controller + two agentd endpoints. Minimal surface area |

**Overall: 4.8/5** (-1 for handoff gap latency; inherent to SQLite-on-NFS constraint)
