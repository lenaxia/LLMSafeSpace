# Worklog 0172 — API Key Hashing + Cgroup CPU/Disk/Memory Metering + Controller Metrics Taxonomy

**Date:** 2026-06-06
**Agent:** agent-oc-jun05-2330
**Commit:** 04f17ab

---

## Summary

Three independent improvements shipped in one commit:

1. **API key at-rest encryption** (Epic 10 US-10.13) — API auth tokens are now stored as SHA-256 hashes instead of plaintext.
2. **Per-workspace resource metering** (Epic 24 US-24.11 partial) — CPU milliseconds, disk GB-seconds, and memory GB-seconds are now emitted from the controller on each statusz poll cycle.
3. **Controller Prometheus metric taxonomy** — Operational, metering, and billing metric families defined and registered.

---

## 1. API Key Hashing (Epic 10 US-10.13)

### Problem

`api_keys.key` stored raw `VARCHAR(255)` plaintext. A database read-level breach exposed every user's API token verbatim.

### Design

- **SHA-256** rather than bcrypt/argon2: API tokens are already 256-bit random (`hex.EncodeToString(32 random bytes)`), so bcrypt's brute-force cost is irrelevant. SHA-256 is fast, constant-time (via Go's stdlib), and produces a stable 64-char hex string suitable as a DB unique key.
- **One-way**: plaintext returned to the caller exactly once at creation time. The DB stores only the hash.
- **Legacy fallback**: pre-migration keys are marked `key_legacy=true`. Authentication tries SHA-256 first; if that misses and the presented key is shorter than 64 chars (i.e. not a hash itself), it falls back to a plaintext comparison with a deprecation warning log. This lets existing sessions keep working until users rotate.
- **`APIKeyLegacyTotal` Prometheus gauge** tracks rotation progress toward 0.

### Migration 000017

```sql
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS key_prefix  VARCHAR(12) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS key_legacy  BOOLEAN     NOT NULL DEFAULT FALSE;

UPDATE api_keys
    SET key_prefix = LEFT(key, 8),
        key_legacy = TRUE
    WHERE key_prefix = '';
```

Existing rows: `key_legacy=true`, `key_prefix` backfilled from first 8 chars of plaintext. New rows: `key_legacy=false`, `key_prefix` from first 8 chars of the returned plaintext (stored before hashing).

### Files changed

- `api/migrations/000017_api_key_hashing.{up,down}.sql`
- `charts/llmsafespace/migrations/000017_*` (synced)
- `pkg/types/types.go` — `APIKey.Legacy bool` field
- `api/internal/services/auth/auth.go` — `CreateAPIKey` (hash on write, return plaintext once), `AuthenticateAPIKey` (hash-first + legacy fallback)
- `api/internal/services/database/database.go` — `CreateAPIKey` INSERT includes `key_prefix`, `key_legacy` ($8, $9)
- `api/internal/services/auth/auth_test.go` — updated mocks to expect hash for happy path; legacy plaintext mock for invalid-key fallback path

---

## 2. Cgroup CPU/Disk/Memory Metering

### Design

The workspace pod's resource consumption is measured from **inside the pod** via cgroup v2 interfaces read by `workspace-agentd` on each `/v1/statusz` poll (~60s cadence). The controller computes rate/integral metrics from the values in the statusz response.

**Scope: entire pod cgroup** — all processes (opencode + agentd + any user-spawned shells, compilers, scripts). This is intentional: billing should cover all compute the user's workspace consumed, not just the opencode process.

#### CPU

- `getCPUUsage()` in agentd reads `/sys/fs/cgroup/cpu.stat` → `usage_usec` (monotonically increasing cumulative µs).
- Also reads `/sys/fs/cgroup/cpu.max` to derive the quota limit (`quota/period × 1e6 µs/s`).
- Controller stores `CpuUsageMicros` on `WorkspaceStatus` between polls. On each poll: `delta_ms = (current - prev) / 1000`. This gives milliseconds of CPU consumed in the ~60s interval — second-level precision without sub-second scraping.

#### Disk and Memory

Already collected by agentd (`getDiskUsage()`, `getMemoryUsage()`). Extended to emit time-integral counters:
- `(bytes × elapsed_seconds)` added to workspace-level and user-level counters on each enrichAgentStatus cycle.
- Divide by `1e9 × 3600` for GB-hours.

#### enrichAgentStatus signature change

`enrichAgentStatus` now accepts `elapsed time.Duration` propagated from `maybeEnrichAgentStatus`'s poll tracker. This gives accurate elapsed time rather than assuming the nominal `deepStatusInterval` (60s).

### New fields on WorkspaceStatus

```go
CpuUsageMicros       int64  // cumulative µs, stored for delta computation
CpuLimitMicrosPerSec int64  // CPU quota (0 = unlimited)
```

CRD YAML (`charts/llmsafespace/crds/workspace.yaml`) updated with both fields.

### Files changed

- `pkg/agentd/types.go` — `CPUUsage` struct; `CPU *CPUUsage` on `StatuszResponse`
- `cmd/workspace-agentd/main.go` — `getCPUUsage()` function; `CPU: getCPUUsage()` in statusz handler
- `pkg/apis/llmsafespace/v1/workspace_types.go` — `CpuUsageMicros`, `CpuLimitMicrosPerSec` on `WorkspaceStatus`
- `charts/llmsafespace/crds/workspace.yaml` — CRD fields added
- `controller/internal/workspace/health.go` — `enrichAgentStatus` signature + metering emission
- `controller/internal/workspace/health_enrichment_test.go` — updated calls with `elapsed` arg
- `controller/internal/workspace/health_test.go` — updated calls with `elapsed` arg

---

## 3. Controller Prometheus Metric Taxonomy

`controller/internal/metrics/metrics.go` rewritten with three clearly separated families:

### OPERATIONAL — is the system healthy right now?

| Metric | Type | Purpose |
|--------|------|---------|
| `llmsafespace_workspaces_created/deleted_total` | Counter | Capacity planning, growth trends |
| `llmsafespace_workspaces_running` | Gauge | Live active workspace count |
| `llmsafespace_workspaces_failed_total` | Counter | Terminal failures |
| `llmsafespace_workspace_recovery_attempts_total{failure_class}` | Counter | Recovery system activity |
| `llmsafespace_workspace_recovery_success_total{failure_class}` | Counter | Recovery effectiveness |
| `llmsafespace_workspace_recovery_backoff_duration_seconds` | Histogram | Outage window length during recovery |
| `llmsafespace_workspace_consecutive_failures_max` | Gauge | Crash-loop detection |
| `llmsafespace_workspace_safe_mode_active` | Gauge | File-recovery-mode workspaces |
| `llmsafespace_workspace_status_update_conflicts_total` | Counter | Gates Epic 23 Stories 2+3 |
| `llmsafespace_workspace_create/resume_duration_seconds` | Histogram | SLO tracking |
| `llmsafespace_workspace_init_container_duration_seconds` | Histogram | Package install latency |
| `llmsafespace_reconciliation_duration_seconds` | Histogram | Controller health |
| `llmsafespace_reconciliation_errors_total` | Counter | Controller health |

### METERING — per workspace, second-level precision

| Metric | Unit | Billing derivation |
|--------|------|-------------------|
| `llmsafespace_workspace_active_seconds_total` | seconds | ÷ 3600 = compute-hours |
| `llmsafespace_workspace_cpu_milliseconds_total` | ms | ÷ 60000 = CPU-minutes |
| `llmsafespace_workspace_disk_used_bytes_seconds_total` | byte-seconds | ÷ 1e9 ÷ 3600 = disk GB-hours |
| `llmsafespace_workspace_memory_used_bytes_seconds_total` | byte-seconds | ÷ 1e9 ÷ 3600 = memory GB-hours |
| `llmsafespace_workspace_disk/memory_used_bytes` | bytes | Point-in-time gauge for alerting |
| `llmsafespace_workspace_storage_bytes` | bytes | Provisioned storage baseline |
| `llmsafespace_workspace_llm_requests_total` | requests | LLM cost basis |
| `llmsafespace_workspace_llm_request_duration_seconds` | seconds | LLM p99 SLO |
| `llmsafespace_workspace_proxy_bytes_total{direction}` | bytes | Network egress attribution |

### BILLING — per user aggregates

| Metric | Unit | Invoice line |
|--------|------|-------------|
| `llmsafespace_user_active_seconds_total` | seconds | Compute-hours billed |
| `llmsafespace_user_cpu_milliseconds_total` | ms | CPU-minutes billed |
| `llmsafespace_user_disk_bytes_seconds_total` | byte-seconds | Disk GB-hours billed |
| `llmsafespace_user_memory_bytes_seconds_total` | byte-seconds | Memory GB-hours billed |
| `llmsafespace_user_llm_calls_total{provider,credential_source}` | calls | LLM usage charges |
| `llmsafespace_api_key_legacy_total` | count | Rotation progress (target: 0) |

All time-integral billing counters use seconds as the base unit, matching the workspace-level metering precision. The billing engine converts to the appropriate invoice interval at billing time.

---

## Multi-Agent Coordination Note

This worklog covers work that required 6+ attempts to land due to concurrent pushes from `agent-relay-jun06` and `agent-audit-0606` overwriting working-tree changes between edit and commit. Root cause: Python-based file edits were applied to the in-memory file state but lost during `git pull --rebase` operations. Fixed by delegating the final file-application pass to a sub-agent that wrote files and staged them atomically before returning, then committing immediately with `make fmt && git add -A && git commit`.

COORDINATE.md was also updated to add a **Pending Claims** section allowing agents to queue for files that are currently claimed.
