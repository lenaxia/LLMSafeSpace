# Epic 18: Test Plan

**Covers:** S18.1–S18.9
**Test Pyramid:** Unit → Integration → E2E
**Environments:** Local (kind + Longhorn RWX), Staging (EKS + EFS + gVisor)

---

## S18.1 — RWX Storage & Pod Lookup Refactor

### Unit Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| U1.1 | `buildPVC()` with `AccessMode=ReadWriteMany` produces RWX PVC | Happy | PVC spec has correct access mode |
| U1.2 | `BuildPod()` extracted function produces identical pod spec to old `buildPod()` | Happy | Refactor correctness — no behavioral change |
| U1.3 | `BuildPod()` accepts custom pod name parameter | Happy | Migration controller can use different name |
| U1.4 | `handleActive` finds pod by `Status.PodName` | Happy | Refactored lookup works |
| U1.5 | `handleActive` calls `recoverFromTransientPodLoss` when pod named in `Status.PodName` is missing | Unhappy | Transient recovery still works after refactor |
| U1.6 | `handleSuspending` deletes pod by `Status.PodName` | Happy | Correct pod deleted after migration |
| U1.7 | `handleTerminating` deletes pod by `Status.PodName` | Happy | Correct pod deleted after migration |
| U1.8 | `handleCreating` still uses `podName()` for new pod creation | Happy | New pods get deterministic names |
| U1.9 | `handleCreating` sets `Status.PodName` after pod creation | Happy | Adoption field always populated |
| U1.10 | `BuildPod()` with empty name returns error | Edge | Validates input |
| U1.11 | `BuildPod()` with name exceeding 63 chars (K8s limit) returns error | Edge | DNS label validation |

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I1.1 | Create workspace with RWX StorageClass → PVC bound → pod running → write file → read file | Happy | Full RWX lifecycle |
| I1.2 | Two pods mount same RWX PVC simultaneously, both read/write different files | Happy | RWX concurrent mount works |
| I1.3 | Two pods mount same RWX PVC, both write to same file → last writer wins (no corruption) | Edge | NFS write semantics |
| I1.4 | Workspace suspend → resume cycle with RWX PVC | Happy | Suspend/resume unaffected by RWX |
| I1.5 | Workspace with `nosuid,nodev` mount options → verify via `mount` inside pod | Security | G23 mitigation confirmed |

---

## S18.2 — Migration CRD & Reconciler

### Unit Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| U2.1 | Create Migration CR → reconciler moves to `CreatingTarget` phase | Happy | Initial phase transition |
| U2.2 | Each phase transition persists status BEFORE executing next step | Happy | Write-ahead correctness |
| U2.3 | Target pod created with correct name (`{ws}-{uid[:8]}-mig`), labels, PVC, nodeAffinity | Happy | Pod spec correctness |
| U2.4 | Target pod has OwnerReference → workspace | Happy | GC correctness |
| U2.5 | Migration completes → `workspace.status.{podName, podIP, endpoint}` updated to target | Happy | Cutover correctness |
| U2.6 | Migration completes → source pod deleted | Happy | Cleanup |
| U2.7 | Concurrent Migration for same workspace → second rejected (Failed) | Unhappy | Single-migration-per-workspace invariant |
| U2.8 | Workspace phase changes to `Suspending` mid-migration → migration aborted (Failed) | Unhappy | Phase conflict detection |
| U2.9 | Workspace deleted during migration → target pod GC'd via OwnerReference | Edge | No orphaned pods |
| U2.10 | `CreatingTarget` timeout exceeded → Failed + target pod deleted | Unhappy | Timeout at phase 1 |
| U2.11 | `WaitingAgentd` timeout exceeded → Failed + target pod deleted | Unhappy | Timeout at phase 2 |
| U2.12 | `StoppingSource` timeout exceeded → Failed + source opencode restarted | Unhappy | Timeout at phase 4 with rollback |
| U2.13 | `StartingTarget` timeout exceeded → Failed + source opencode restarted + target deleted | Unhappy | Timeout at phase 5 with rollback |
| U2.14 | `CuttingOver` timeout exceeded → Failed + rollback | Unhappy | Timeout at phase 7 |
| U2.15 | Crash recovery: controller restarts at each phase → resumes from persisted phase | Robustness | Idempotent recovery |
| U2.16 | Migration with `targetNode=""` → target pod scheduled by K8s scheduler (no nodeAffinity) | Happy | Optional target node |
| U2.17 | Migration with `targetNode` set → target pod has nodeAffinity for that node | Happy | Explicit target |
| U2.18 | Migration with `timeoutBudgetSeconds=75` → phases use tighter timeouts | Happy | Spot budget enforcement |
| U2.19 | Rollback after StoppingSource: source agentd `start-opencode` called → opencode running again | Unhappy | Rollback restores service |
| U2.20 | `handoffDurationMs` recorded = time from StoppingSource start to StartingTarget complete | Happy | Metric correctness |
| U2.21 | Metrics emitted: `migration_total`, `migration_in_progress`, `migration_cutover_duration_seconds` | Happy | Observability |

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I2.1 | Full migration happy path: create workspace → create Migration CR → wait Completed → verify target pod serving | Happy | End-to-end migration |
| I2.2 | Migration + workspace reconciler coexistence: reconciler doesn't interfere during migration | Robustness | No race between controllers |
| I2.3 | Migration rollback: inject failure at StartingTarget → verify source pod still serving | Unhappy | Rollback works end-to-end |
| I2.4 | Migration with Spot budget (75s): verify completes within budget on healthy cluster | Performance | Spot timing feasible |

---

## S18.3 — Agentd Migration Endpoints

### Unit Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| U3.1 | `AGENTD_SUPERVISE=false` → agentd starts, `/v1/healthz` returns 200, `/v1/readyz` returns `ready: false` | Happy | Supervise-false mode |
| U3.2 | `POST /v1/migrate/start-opencode` → opencode starts → `/v1/readyz` becomes true | Happy | Start lifecycle |
| U3.3 | `POST /v1/migrate/stop-opencode` → opencode stops → `/v1/readyz` becomes false | Happy | Stop lifecycle |
| U3.4 | `stop-opencode` drains in-flight requests (waits up to 10s) before SIGTERM | Happy | Drain period |
| U3.5 | `stop-opencode` with no in-flight requests → immediate SIGTERM (no 10s wait) | Edge | Drain skipped when empty |
| U3.6 | `stop-opencode` with stuck request (>10s) → SIGKILL after timeout | Unhappy | Force kill on drain timeout |
| U3.7 | After `stop-opencode`, all active SSE connections closed with `retry: 1000` | Happy | SSE cleanup |
| U3.8 | After `stop-opencode`, new proxied requests get `503 Retry-After: 5` | Happy | Request rejection |
| U3.9 | `GET /v1/migrate/snapshot` returns session statuses + provider cache | Happy | Snapshot content |
| U3.10 | Snapshot size < 50KB with 5 sessions | Performance | Size constraint |
| U3.11 | `POST /v1/migrate/restore` with valid snapshot → state reconstructed | Happy | Restore works |
| U3.12 | Restore is idempotent (call twice → same state) | Edge | Idempotency |
| U3.13 | Snapshot when already taken → `409 Conflict` | Unhappy | Double-snapshot prevention |
| U3.14 | Restore when sessions already exist → `409 Conflict` | Unhappy | Double-restore prevention |
| U3.15 | `stop-opencode` when opencode not running → 200 (no-op) | Edge | Idempotent stop |
| U3.16 | `start-opencode` when opencode already running → 409 | Unhappy | Prevent double-start |
| U3.17 | `start-opencode` after `stop-opencode` → opencode restarts successfully | Happy | Stop/start cycle |

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I3.1 | Full stop/start cycle: send request → stop-opencode → verify 503 → start-opencode → send request → success | Happy | Lifecycle end-to-end |
| I3.2 | Snapshot on pod A → restore on pod B → verify session statuses match | Happy | Cross-pod state transfer |
| I3.3 | SSE stream active → stop-opencode → verify client receives close → reconnect succeeds after start | Happy | SSE handoff |

---

## S18.4 — Proxy Handoff

### Unit Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| U4.1 | Proxy reads `workspace.Status.PodIP` per request (not cached) | Happy | Per-request resolution |
| U4.2 | Connection error + active Migration CR → `Retry-After: 1` (not 10) | Happy | Fast retry during migration |
| U4.3 | Connection error + no Migration CR → `Retry-After: 10` (default) | Happy | Normal behavior preserved |
| U4.4 | Proxy retries with fresh podIP on connection error → routes to new pod | Happy | Existing retry handles cutover |

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I4.1 | During migration handoff gap: client sends request → gets 503 → retries → succeeds after cutover | Happy | Full handoff flow |
| I4.2 | SSE stream during migration: stream closes → client reconnects → new stream from target pod | Happy | SSE continuity |
| I4.3 | 10 concurrent HTTP requests during cutover → all eventually succeed (none dropped) | Robustness | No request loss |

---

## S18.5 — Spot Reclamation Handler

### Unit Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| U5.1 | Node termination event → Migration CR created for each workspace pod on node | Happy | Trigger works |
| U5.2 | Busy sessions get higher priority than idle | Happy | Priority ordering |
| U5.3 | Migration exceeds Spot budget → workspace set to Suspending | Unhappy | Graceful fallback |
| U5.4 | >10 workspace pods on node → first 10 migrate in parallel, rest queued | Edge | Concurrency limit |

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I5.1 | Simulate node cordon + termination event → workspaces migrate to other nodes | Happy | Full Spot flow |
| I5.2 | Spot with tight budget (75s) + slow provider → workspace suspends gracefully | Unhappy | Fallback works |

---

## S18.6 — Proactive Load Balancing

### Unit Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| U6.1 | Node above high watermark for >60s → Migration CRs created | Happy | Trigger fires |
| U6.2 | Node above high watermark for <60s → no migration | Edge | Debounce works |
| U6.3 | Least-active workspace selected first | Happy | Selection algorithm |
| U6.4 | Pod with `sessionsActive > 0` skipped | Happy | Active session protection |
| U6.5 | Cooldown: second migration for same workspace within 10min → skipped | Edge | Cooldown enforced |
| U6.6 | Cooldown: 4th migration on same node within 5min → skipped | Edge | Node cooldown |
| U6.7 | Dry-run mode → logs but no Migration CRs created | Happy | Dry-run works |
| U6.8 | Target node selection prefers same AZ, below low watermark | Happy | Placement logic |

---

## S18.7 — gVisor RuntimeClass

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I7.1 | Workspace with `runtimeClass: gvisor` → pod runs under gVisor (`dmesg` shows gVisor) | Happy | RuntimeClass applied |
| I7.2 | Python runtime: `pip install numpy && python -c "import numpy"` under gVisor | Compatibility | Python works |
| I7.3 | Node runtime: `npm install express && node -e "require('express')"` under gVisor | Compatibility | Node works |
| I7.4 | Go runtime: `go build ./...` under gVisor | Compatibility | Go works |
| I7.5 | Java runtime: `javac Hello.java && java Hello` under gVisor | Compatibility | Java works |
| I7.6 | `mise install python@3.12 node@20` under gVisor | Compatibility | mise works |
| I7.7 | Full opencode agent session under gVisor (prompt → response → tool use → file write) | Compatibility | Agent works |
| I7.8 | fio benchmark: 4K random r/w under gVisor vs runc → <20% overhead | Performance | Acceptable overhead |
| I7.9 | Migration works identically under gVisor (full 8-step sequence) | Robustness | Migration + gVisor compatible |

---

## S18.8 — Tenant Namespace Isolation

### Unit Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| U8.1 | Proxy resolves namespace from JWT `tenant_id` claim | Happy | Namespace routing |
| U8.2 | Request with missing `tenant_id` → 401 | Security | Auth enforcement |
| U8.3 | Request with `tenant_id` for non-existent namespace → 404 | Unhappy | Graceful error |
| U8.4 | Tenant A cannot access Tenant B's workspace via API | Security | Cross-tenant isolation |

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I8.1 | Create tenant → namespace created with ResourceQuota + LimitRange + NetworkPolicy | Happy | Onboarding |
| I8.2 | Workspace created in tenant namespace → PVC has EFS access point annotation with correct tenant_id | Happy | EFS isolation |
| I8.3 | Pod in tenant-A namespace cannot reach pod in tenant-B namespace (NetworkPolicy) | Security | Network isolation |
| I8.4 | Delete tenant → all workspaces, PVCs, pods cascade-deleted | Happy | Cleanup |
| I8.5 | 100 tenants × 10 workspaces → controller reconcile p99 < 500ms | Performance | Scale |
| I8.6 | Migration within tenant namespace works (Migration CR scoped to tenant ns) | Happy | Migration + tenancy |

---

## S18.9 — Karpenter NodePool

### Integration Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| I9.1 | Workspace pod scheduled on Spot Graviton node (workspaces pool) | Happy | Pool selection |
| I9.2 | System pod (API, controller) scheduled on On-Demand node (baseline pool) | Happy | Taint/toleration |
| I9.3 | Workspace pod has `karpenter.sh/do-not-disrupt: "true"` annotation | Happy | Disruption protection |
| I9.4 | Empty workspace node consolidated after 60s | Happy | Cost optimization |
| I9.5 | Node expires after 7 days → replacement provisioned → workspaces migrated | Robustness | Rotation works |

---

## E2E Tests (Cross-Story)

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| E2E.1 | **Full migration lifecycle:** Create workspace → user sends prompt → response streaming → trigger migration → handoff gap (503 retries) → stream resumes on target → conversation intact | Happy | Complete user journey |
| E2E.2 | **Spot reclamation under load:** 5 workspaces on Spot node, 3 with active sessions → Spot interrupt → all migrate within 75s or suspend gracefully | Robustness | Spot at scale |
| E2E.3 | **Migration + suspend + resume:** Migrate workspace → suspend → resume → verify pod name reverts to deterministic, conversation history intact | Edge | Lifecycle interaction |
| E2E.4 | **Migration rollback:** Trigger migration → inject failure at StartingTarget (target opencode crashes) → verify source pod resumes serving within 30s | Unhappy | Rollback UX |
| E2E.5 | **Concurrent migrations on same node:** 5 workspaces migrate simultaneously from hot node → all complete, no interference | Robustness | Parallel migration |
| E2E.6 | **Migration during active tool use:** Agent is executing a file write tool → migration triggers → tool completes on source → next request served by target | Edge | Mid-operation migration |
| E2E.7 | **Multi-tenant isolation during migration:** Tenant A migrates → Tenant B's workspace unaffected, cannot observe migration | Security | Tenant boundary |
| E2E.8 | **SQLite integrity after migration:** Migrate workspace → verify `PRAGMA integrity_check` passes on target → all sessions/messages present | Robustness | No data corruption |
| E2E.9 | **Repeated migrations:** Migrate workspace 5 times in sequence (A→B→A→B→A) → workspace functional after each, no state accumulation or leak | Robustness | Repeated migration stability |
| E2E.10 | **Migration under memory pressure:** Node at 85% memory → load balancer triggers migration → workspace moves to healthy node → node pressure drops below 65% | Happy | Load balancing effectiveness |
| E2E.11 | **Provider reconnection timing:** Migrate workspace → measure time from stop-opencode to target readyz → assert < 15s p99 | Performance | Handoff SLO |
| E2E.12 | **Zero dropped requests:** Send 100 requests/s during migration → verify all get 200 or retryable 503 → none get 500 or timeout | Performance | Request safety |

---

## Security Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| SEC.1 | Migration endpoints (`/v1/migrate/*`) not accessible from outside cluster (port 4097 not in Ingress) | Security | Endpoint isolation |
| SEC.2 | Migration controller cannot access workspaces in other tenant namespaces (RBAC) | Security | Tenant boundary |
| SEC.3 | Target pod cannot access source pod's credentials (each mounts own Secret) | Security | Credential isolation |
| SEC.4 | EFS access point enforces root directory — pod cannot traverse to parent tenant's files | Security | Storage isolation |
| SEC.5 | gVisor prevents container escape even with RWX mount (attempt `nsenter`, `mount` syscalls) | Security | Runtime isolation |
| SEC.6 | Migration CR cannot be created by non-admin user (RBAC) | Security | Privilege escalation prevention |
| SEC.7 | Snapshot endpoint doesn't leak credentials or secrets in response | Security | Data exposure |

---

## Performance Tests

| ID | Test | Type | What It Validates |
|----|------|------|-------------------|
| PERF.1 | Handoff gap (stop-source to target-ready) p50/p95/p99 over 20 migrations | Performance | SLO validation |
| PERF.2 | Total migration time p50/p95/p99 over 20 migrations | Performance | SLO validation |
| PERF.3 | EFS I/O latency: fio 4K random read/write p99 under workspace load | Performance | Storage baseline |
| PERF.4 | gVisor overhead: same fio test with/without gVisor → delta < 20% | Performance | Runtime overhead |
| PERF.5 | 10 concurrent migrations on same cluster → no migration starves | Performance | Concurrency |
| PERF.6 | Controller reconcile latency with 100 Migration CRs in various phases | Performance | Controller scalability |
| PERF.7 | Proxy latency during migration (p99 for non-migrating workspaces) → no degradation | Performance | Blast radius |
