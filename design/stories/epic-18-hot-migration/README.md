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
| A20 | Proxy currently routes directly to opencode (port 4096), bypassing agentd | ✅ `proxy.go:405`: `targetURL = http://{podIP}:4096/{path}`. **S18.3 changes this: route through agentd:4097 instead, enabling drain during migration.** |
| A21 | SIGKILL on NFS: kernel closes fd → NFS COMMIT flushes data to server (node still alive) | ✅ NFS protocol: close() triggers COMMIT. Only unsafe if node itself dies (kernel panic). Our migration keeps source node alive. |

---

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Storage backend (prod) | EFS | RWX native, cross-AZ, AWS-managed, no share-manager pods |
| Storage backend (dev) | Any RWX CSI (Longhorn RWX, NFS) | StorageClass is the abstraction boundary |
| Default access mode | RWX for all new workspaces | Migration requires RWX; no reason to keep RWO as default |
| Sandbox runtime (prod) | gVisor | Kernel-level isolation for RWX attack surface |
| Sandbox runtime (dev) | runc | gVisor is security hardening, not functional requirement |
| Tenant isolation | Capsule + EFS access points (prod) | API-level + AWS-enforced storage isolation — **⛔ Capsule superseded by Epic 51; EFS access points remain in S18.8 (reduced)** |
| Compute (prod) | Graviton Spot 80% + On-Demand 20% | 60-70% cost savings |
| Pod naming for migration | Target pod: `{workspace}-{uid[:8]}-mig`; after cutover, `Status.PodName` updated | Migration controller creates pod directly; workspace reconciler adopts via `Status.PodName` |
| Pod lookup in reconciler | Always use `workspace.Status.PodName` (not `podName()` derivation) | Simpler, no conditional; `Status.PodName` is always set (A11) |
| Concurrent write safety | Sequential phases, no Lease | No concurrent-write window exists by design |
| Opencode DB safety | Stop source opencode before starting target opencode | SQLite WAL over NFS corrupts if two processes open same DB (A17, A18). Sequential handoff is mandatory. |
| Target pod startup mode | Agentd-only (no opencode) until migration controller signals | Allows target pod to be "warm" (mounted, networked) without touching SQLite DB |
| Pod building for migration | Extract `buildPod` logic into shared `pkg/workspace/pod` package | Migration controller reuses same pod spec; avoids duplication |
| Request routing | Route all proxy traffic through agentd:4097 (not directly to opencode:4096) | Agentd becomes the control plane for traffic + lifecycle. Enables drain during migration, future rate limiting, circuit breaking. Cost: <1ms loopback hop. |

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
4. **StoppingSource:** `POST /v1/migrate/stop-opencode` on source agentd → agentd stops forwarding new requests (returns 503 Retry-After: 5) → waits for in-flight requests to complete (max 30s drain) → closes SSE connections (sends `retry: 1000`) → SIGTERM opencode → wait for exit (WAL flushed via kernel fd close).
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
- Steps 1-3: zero (source still serving normally via agentd)
- Step 4 drain: in-flight LLM responses complete normally (up to 30s). New requests get `503 Retry-After: 5`. SSE connections closed after drain (clients reconnect within 1s).
- Steps 4-5: **5-15s of `503 Retry-After`** from source agentd (opencode handoff gap). SDK auto-retries.
- Steps 6-8: zero (target serving, proxy cuts over instantly)
- Total: **5-15s of retried requests, zero interrupted responses** (vs 22s hard downtime with RWO)
- Worst case (cold provider + long drain): up to 45s total migration time (30s drain + 15s startup).

**After step 7:** proxy routes new requests to target pod. Workspace reconciler finds target pod via `Status.PodName`.

**After step 8:** SSE connections to source pod break → clients reconnect → routed to target.

---

## Security Model

| Layer | Control | Environment |
|-------|---------|-------------|
| Container escape prevention | gVisor RuntimeClass | Production — **➡️ Moved to Epic 51 (S51.1)** |
| Cross-tenant API isolation | ~~Capsule namespaces~~ Shared namespace + gVisor + network policy | Production (multi-tenant) — **⛔ Capsule superseded by Epic 51** |
| Cross-tenant storage isolation | EFS access points (AWS-enforced root dir + UID/GID) | Production (multi-tenant) — **remains in S18.8 (reduced)** |
| Migration sequencing | Controller-driven phases; no concurrent-write window | All |
| Spot reclamation | Node termination handler → triggers migration | Production |

---

## Stories

### S18.10 — Workspace Startup Latency Benchmarking

**Goal:** Instrument and measure both the first-create and resume startup paths end-to-end. Produces a per-gate baseline before any optimisation work begins.

See full spec: [`S18.10-resume-latency-benchmark.md`](S18.10-resume-latency-benchmark.md)

**Summary of scope:**
- `llmsafespace_workspace_create_duration_seconds` histogram (controller) — Pending→Active with `has_packages`, `has_init_script` labels
- `llmsafespace_workspace_resume_duration_seconds` histogram (controller) — Resuming→Active with `resume_type` label
- `llmsafespace_agentd_gate_duration_seconds` histogram (agentd) — per-gate: `opencode_up`, `providers_connected`, `readyz_first_200`
- `status.pendingAt` and `status.resumedAt` timestamp anchors on `WorkspaceStatus`
- agentd `/metrics` endpoint on admin port
- Per-gate structured log lines from agentd on every boot
- `hack/benchmark-resume.sh` and `hack/benchmark-create.sh` — run against live cluster; p50/p90/p99 per gate
- Worklog entry with baseline measurements for both paths

**Acceptance Criteria:** See story file.

**Estimated Effort:** 4 points

---

### S18.11 — Decouple readyz from Provider Connectivity

**Goal:** Remove provider connectivity from the pod readiness gate. Expected to cut p99 resume latency from 90–140s to 15–25s.

See full spec: [`S18.11-decouple-readyz-from-provider.md`](S18.11-decouple-readyz-from-provider.md)

**Summary of scope:**
- `readyz` returns 200 as soon as `snap.Initialized && snap.Healthy` (opencode process up) — provider connectivity no longer required
- Add `WorkspaceConditionProviderReady` condition to workspace CRD
- `checkAgentHealth` sets `ProviderReady` condition from statusz `connected` field
- Frontend: amber banner when `phase=Active && ProviderReady=False`
- Benchmark re-run (S18.10 script) to validate improvement

**Acceptance Criteria:** See story file.

**Estimated Effort:** 3 points

---

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
- [ ] Timeouts: CreatingTarget=60s, WaitingAgentd=30s, StoppingSource=45s, StartingTarget=120s, CuttingOver=5s
- [ ] Spot-triggered migrations use tighter timeouts (see S18.5): total budget 75s. Migration spec carries `timeoutBudgetSeconds` field; reconciler uses min(phase default, remaining budget).
- [ ] Abort (set Failed) if workspace phase is no longer `Active` at any step — prevents conflict with restart/suspend/terminate
- [ ] Failed → rollback: (1) stop target opencode via `POST /v1/migrate/stop-opencode` on target (best-effort), (2) if target unreachable, delete target pod (SIGKILL), (3) only after target confirmed dead, restart source opencode via `POST /v1/migrate/start-opencode` on source. This ordering prevents two opencode processes running simultaneously.
- [ ] Migration CR has finalizer `llmsafespace.dev/migration-cleanup` — deletion triggers rollback sequence above before CR is removed
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

### S18.3 — Agentd Migration Endpoints & Request Proxy

**Goal:** Make agentd the single control plane for workspace traffic and lifecycle. Add reverse proxy (agentd:4097 → opencode:4096), migration lifecycle endpoints, and `--supervise=false` mode.

**Acceptance Criteria:**
- [ ] Agentd reverse-proxies all HTTP requests from port 4097 to opencode on localhost:4096 (transparent pass-through, including SSE streaming)
- [ ] API proxy changed: `opencodePort` constant from 4096 → 4097 (route through agentd)
- [ ] Agentd tracks in-flight request count (increment on forward, decrement on response complete)
- [ ] `--supervise=false` flag (or `AGENTD_SUPERVISE=false` env): agentd starts without opencode. `/v1/healthz` returns 200, `/v1/readyz` returns `ready: false`. Proxied requests get `503 Retry-After: 5`.
- [ ] `POST /v1/migrate/stop-opencode`: (1) stop forwarding new requests (503), (2) wait for in-flight count to reach 0 (max 30s), (3) close SSE connections (send `retry: 1000`), (4) SIGTERM opencode, (5) wait for exit (max 10s, then SIGKILL). Returns 200 when complete.
- [ ] `POST /v1/migrate/start-opencode`: starts opencode, waits for healthy + providers connected. Returns 200. Proxied requests resume forwarding.
- [ ] `GET /v1/migrate/snapshot`: returns session statuses + provider cache JSON
- [ ] `POST /v1/migrate/restore`: accepts snapshot, reconstructs state
- [ ] Snapshot size < 50KB for 5 sessions (unit test)
- [ ] Restore is idempotent
- [ ] `409 Conflict` if snapshot already taken or restore already applied
- [ ] All migration endpoints cluster-internal only (port 4097, not in Ingress — but request proxy IS exposed via Ingress)
- [ ] Unit tests: reverse proxy pass-through, in-flight tracking, drain to zero, stop/start lifecycle, snapshot round-trip

**Implementation Notes:**
- Reverse proxy: use `net/http/httputil.ReverseProxy` targeting `localhost:4096`. Handles streaming (SSE) natively.
- In-flight tracking: atomic counter. Increment before forwarding, decrement in response handler (deferred).
- Drain: `stop-opencode` spins on in-flight counter with 100ms poll interval, max 30s. After drain, SIGTERM.
- The `/v1/migrate/*` and `/v1/healthz` and `/v1/readyz` and `/v1/statusz` endpoints are handled by agentd directly (not proxied to opencode).
- All other paths (`/session/*`, `/event`, `/config/*`, etc.) are reverse-proxied to opencode:4096.

**Estimated Effort:** 8 points

---

### S18.4 — Proxy Handoff

**Goal:** Ensure proxy routes to target pod after migration cutover.

**Acceptance Criteria:**
- [ ] API proxy `opencodePort` constant changed from 4096 → 4097 (talks to agentd, not opencode directly)
- [ ] During steps 1-3: proxy routes to source agentd:4097 → agentd forwards to opencode:4096. Normal operation.
- [ ] During step 4 (drain + stop): source agentd returns `503 Retry-After: 5` for new requests. In-flight requests complete normally (drain). SDK retries new requests.
- [ ] After step 7 (cutover): proxy reads updated `Status.PodIP`, routes to target agentd:4097 → target forwards to target opencode:4096.
- [ ] Integration test: send request during drain → completes normally. Send new request during stop → gets 503 → retry after cutover → success.

**Implementation Notes:**
- This is a one-line change: `opencodePort = agentd.AgentdPort` (4097 instead of 4096).
- All migration-aware behavior (503, drain) is handled by agentd, not the proxy.
- The proxy's existing retry logic (connection error → fresh podIP → retry) still works as fallback.

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

### S18.7 — gVisor RuntimeClass — ➡️ MOVED TO EPIC 51

**Status:** Moved to [Epic 51: Tenant Isolation — gVisor + Resource Quotas](../epic-51-tenant-isolation/README.md) (story S51.1).

gVisor is the primary container-escape control for multi-tenancy and is not dependent on hot migration, RWX, or Karpenter. It was promoted out of Epic 18 Phase C to a first-class multi-tenancy prerequisite. The original acceptance criteria are preserved below for reference; implementation now lives in Epic 51.

<details>
<summary>Original acceptance criteria (now in Epic 51 S51.1)</summary>

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

</details>

---

### S18.8 — Tenant Storage Isolation (EFS Access Points) — REDUCED

**Status:** Reduced from "Tenant Namespace Isolation (Capsule)". The namespace, proxy refactor, NetworkPolicy, and cascade-deletion items moved to or are satisfied by [Epic 51](../epic-51-tenant-isolation/README.md). What remains here is **EFS storage isolation only**, which genuinely depends on the RWX storage migration (Epic 18 Phase A / S18.1) and therefore stays in this epic.

**Why the reduction:** Per-tenant namespaces don't solve the primary threat (container escape — that's gVisor, now in Epic 51) and don't scale to 1,000+ tenants (namespace-count limits). The proxy namespace refactor (`h.namespace` → JWT `tenant_id`) is no longer needed since Epic 51 keeps a shared namespace. NetworkPolicy cross-tenant deny is already shipped via chart-level default-deny ingress + RFC1918/CGNAT-filtered egress. Tenant deletion cascade is handled by existing user/org deletion flows. See Epic 51 "Why not namespaces" for the full rationale.

**Goal:** Per-workspace EFS access points enforcing storage root directory and UID/GID isolation.

**Acceptance Criteria:**
- [ ] EFS access points: one per workspace, root `/tenants/{tenant_id}/workspaces/{workspace_id}`
- [ ] Tenant context flows to EFS CSI via PVC annotations: workspace reconciler sets `efs.csi.aws.com/rootDirectory` and `efs.csi.aws.com/uid`/`gid` annotations on PVC based on workspace owner's tenant_id. CSI driver reads these during dynamic provisioning.
- [ ] Integration test: workspace A cannot read workspace B's EFS root via access point boundary

**Implementation Notes:**
- Depends on S18.1 (RWX storage migration) — access points are an EFS feature.
- `tenant_id` resolution matches Epic 51 S51.3: `WorkspaceOwner.OrgID` if set, else `WorkspaceOwner.UserID`.

**Estimated Effort:** 3 points (down from 8)

<details>
<summary>Original S18.8 scope (superseded — do not implement)</summary>

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
- [ ] Tenant context flows to EFS CSI via PVC annotations: workspace reconciler sets `efs.csi.aws.com/rootDirectory` and `efs.csi.aws.com/uid`/`gid` annotations on PVC based on workspace owner's tenant_id. CSI driver reads these during dynamic provisioning.
- [ ] Scale test: 100 tenants × 10 workspaces, reconcile <500ms p99

**Why Capsule:** vCluster = ~256MB/tenant = 256GB at 1000 tenants. Capsule = ~0 overhead.

**Implementation Notes:**
- Controller keeps cluster-wide RBAC + shared informer. Isolation enforced at API layer.
- Proxy refactor: `ProxyHandler` methods accept `namespace` from auth middleware context.

**Estimated Effort:** 8 points

</details>

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
Phase 0 (Observability — runs against current cluster, no prerequisites):
  S18.10 (Resume latency benchmarking + instrumentation)
  S18.11 (Decouple readyz from provider — immediate latency win)

Phase A (Foundation — local testable):
  S18.1 (RWX + reconciler refactor + BuildPod extraction)
  S18.3 (Snapshot/Restore endpoints)
  S18.2 (Migration CRD + reconciler)
  S18.4 (Proxy Retry-After)

Phase B (Triggers — local multi-node):
  S18.6 (Load Balancing)

Phase C (Production hardening):
  S18.9 (Karpenter)
  S18.5 (Spot Handler)
  (S18.7 gVisor — ➡️ moved to Epic 51)

Phase D (Multi-tenancy):
  (S18.8 Capsule — ⛔ superseded by Epic 51; S18.8 reduced to EFS storage isolation, depends on S18.1)
```

Phase 0 requires no infrastructure changes — S18.10 runs against the live cluster
today and S18.11 is a 3-point logic change. Both unblock Phase A by establishing
a latency baseline and removing the worst resume bottleneck before migration work begins.

Phase A delivers a working migration system testable on any multi-node cluster with
RWX storage. Phase B adds automated triggers. Phase C/D are production-only.

**Total: 40 points (~3 sprints)** — reduced from 50: S18.7 (gVisor, 5pts) moved to Epic 51; S18.8 (Capsule, 8pts → EFS storage only, 3pts).

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
| Q1 | vCluster vs Capsule? | ⛔ | **Moot** — both superseded by Epic 51 (shared namespace + gVisor; per-tenant namespaces don't scale to 1000+ tenants and don't solve container escape) |
| Q2 | gVisor + Java JIT? | 🔶 | Benchmark in Epic 51 S51.1 (moved from S18.7) |
| Q3 | EFS throughput mode? | 🔶 | Start elastic; switch if p99 > 10ms |
| Q4 | Session state size? | ✅ | <50KB (routing table only) |
| Q5 | Migration SLO? | ✅ | p99 handoff gap < 15s, p99 total < 30s |
| Q6 | EFS access point limit? | 🔶 | Second filesystem at 800 workspaces |
| Q7 | Requires EFS? | ✅ | No. Any RWX CSI works |
| Q8 | Pod conflict with workspace reconciler? | ✅ | Direct creation + `Status.PodName` adoption |
| Q9 | Concurrent PVC writes? | ✅ | SQLite WAL over NFS = corruption. Solved by sequential opencode handoff (stop source → start target). |
| Q10 | User-visible disruption during migration? | ✅ | 5-10s of `503 Retry-After` during opencode handoff. SDK retries transparently. vs 22s hard downtime with RWO. |
| Q11 | What is the dominant gate in 2-min resume? | 🔶 | Hypothesis: Gate 5 (provider connectivity in readyz). S18.10 benchmark will confirm. S18.11 addresses it. |
| Q12 | What is the remaining latency floor after S18.11? | 🔶 | Expected ~15–25s: 10s InitialDelaySeconds + 5–10s opencode startup + 5s requeueCreating. Addressable in future story (startup probe tuning, probe interval reduction). |
| Q13 | gVisor checkpoint/restore for sub-10s resume? | 🔶 | Requires Epic 51 S51.1 (gVisor) complete first. Checkpoint taken post-startup, stored on PVC. SQLite WAL safety: SIGTERM opencode before checkpoint. Design in S18.12 (future story). |

---

## Success Metrics

| Metric | Baseline (observed) | Target | Story |
|--------|--------------------|---------|----|
| Resume latency p99 (Resuming → proxy ok) | ~120s | < 25s | S18.10 measures, S18.11 fixes dominant gate |
| First-create latency p99, no packages (Pending → proxy ok) | unknown | < 30s | S18.10 measures |
| First-create latency p99, with packages | unknown | baseline + package install time only | S18.10 measures |
| Opencode handoff gap (p99) | — | < 15s | S18.2/S18.3 |
| Total migration time (p99) | — | < 30s | S18.2/S18.3 |
| Spot reclamation success | — | > 95% | S18.5 |
| Requests dropped (not retried) | — | 0 | S18.4 |
| Cost per workspace/month | ~$28 | < $25 | S18.9 |
| gVisor I/O overhead | — | < 20% | Epic 51 S51.1 (moved from S18.7) |

---

## Design Assessment

| Dimension | Score | Justification |
|-----------|-------|---------------|
| **Robustness** | 5 | Write-ahead phases; rollback at every step; fallback to suspension; no data loss (shared PVC) |
| **Reliability** | 5 | Failure = suspend + auto-resume (proven path); client retries; no SPOF |
| **Maintainability** | 5 | `BuildPod()` extracted to shared package (DRY); migration controller is isolated new component; workspace reconciler change is replacing `podName()` calls with `Status.PodName` reads (simpler, not more complex) |
| **Scalability** | 5 | Stateless controller; bounded concurrency; shared namespace + gVisor + admission webhook quotas scale to 1000+ tenants (per Epic 51; Capsule dropped) |
| **Security** | 5 | Defense-in-depth: gVisor (Epic 51) + EFS access points (S18.8 reduced) + NetworkPolicy; sequential phases prevent concurrent-write |
| **Performance** | 4 | 5-10s handoff gap (opencode restart) vs 22s RWO migration — 2-4x improvement. EFS +1-3ms vs EBS. gVisor ~5% CPU. -1: handoff gap is user-visible (503 retries) |
| **SOLID** | 5 | SRP: migration controller migrates, workspace reconciler manages pods. OCP: new triggers via CRD. DIP: migration controller uses `BuildPod()` interface, not reconciler internals |
| **Idiomatic** | 5 | CRD + reconciler, status subresource, write-ahead, OwnerReferences, leader election |
| **Complexity** | 5 | No lease fencing, no proxy buffering, no dual-pod mode. One refactor (pod lookup) + one new controller + two agentd endpoints. Minimal surface area |

**Overall: 4.8/5** (-1 for handoff gap latency; inherent to SQLite-on-NFS constraint)
