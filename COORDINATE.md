# COORDINATE.md — Multi-Agent Work Coordination

This file is the source of truth for what work is in-flight across all agents.
**Before starting any work: read this file. After finishing any work: update this file and commit it.**

Rules:
- Claim a section before touching its files. If it's claimed by another agent, wait or pick different work.
- Keep claims specific (file paths, not vague areas).
- Mark work DONE immediately when finished — do not batch updates.
- If you abandon work, release the claim so another agent can pick it up.
- Always git pull before starting work. Always commit COORDINATE.md with your work commits.

---

## Active Claims

| Agent | What | Files Claimed | Status | Started |
|-------|------|---------------|--------|---------|
| agent-relay-jun06 | Relay+metrics work complete. Deployed ts-1780727304 (rev 152). | — | DONE | 2026-06-06 00:00 |
| agent-oc-jun05-2330 | API key hashing + CPU/disk/memory metering + controller metrics taxonomy — fixing test failures | `api/internal/services/auth/auth.go`, `api/internal/services/auth/auth_test.go`, `pkg/types/types.go`, `api/internal/services/database/database.go`, `pkg/agentd/types.go`, `cmd/workspace-agentd/main.go`, `pkg/apis/llmsafespace/v1/workspace_types.go`, `controller/internal/workspace/health.go`, `controller/internal/workspace/health_enrichment_test.go`, `controller/internal/metrics/metrics.go`, `COORDINATE.md` | IN PROGRESS | 2026-06-06 06:17 |

---

## Recently Completed (last 10)

| Completed | Agent | What | Commit |
|-----------|-------|------|--------|
| 2026-06-06 | agent-audit-0606 | Epic 28 S28.8 — goroutine leak + write-deadline tests (brokerSubCount helper, 12 total tests) | f1af270 |
| 2026-06-06 | agent-audit-0606 | sseConnection cleanup + 2 new weak-point tests + worklog 0171 | 1baac7d |
| 2026-06-06 | agent-audit-0606 | Fix pre-existing flaky test: sseConnection.test.ts backoff-with-jitter pinned Math.random → deterministic | dc38ad8 |
| 2026-06-06 | agent-oc-jun05-2330 | API key SHA-256 hashing (migration 000017, auth service, DB service, types) + CPU/disk/memory cgroup metering (agentd getCPUUsage, StatuszResponse, WorkspaceStatus delta tracking) + full controller metrics taxonomy (operational/recovery/metering/billing) | pending |
| 2026-06-06 | agent-relay-jun06 | Fix #1: resolveModelIDFromCatalog relay providerID remap + billing/metering/ops metrics (inference tokens, model selections, relay injector outcomes, workspace phase transitions) | b77b9c0 |
| 2026-06-06 | agent-relay-jun06 | CF Worker secret-path auth + phase-2 relay injector + opencode-relay model surfacing in ListModels | d836c94 |
| 2026-06-05 | agent-oc-jun05-2330 | Epic 30 credential audit fixes | 0170cb4 |
| 2026-06-05 | agent-oc-jun05-2330 | CPU metering migration files only | 7b6e234 |
| 2026-06-05 | agent-relay-jun06 | API-side billing/metering metrics + relay model surfacing | b77b9c0 |
| 2026-06-05 | — | Validation fixes for live bug fixes | 7325119 |
| 2026-06-05 | — | Live bugs + high-value items (proxy, drain, MCP, settings) | 9a672cc |

---

## Known Conflicts / Merge Notes

- `api/internal/services/metrics/metrics.go` — agent-relay-jun06 added API-side billing metrics in b77b9c0. **Do not overwrite.** Controller metrics live in `controller/internal/metrics/metrics.go` (separate file, claimed by agent-oc-jun05-2330).
- `api/internal/app/app.go` — modified by b77b9c0 and 0170cb4. Pull before touching.
- `api/internal/handlers/session_tracker.go` — modified by b77b9c0 (added InferenceCallback + handleSessionUpdated). Pull before touching.
- `api/internal/handlers/models.go` — modified by b77b9c0 (relay providerID remap). Pull before touching.
- `pkg/secrets/` — heavily modified by 0170cb4. Do not touch without pulling.
- `frontend/src/components/settings/` — modified by 0170cb4. Do not touch without pulling.
- `cmd/workspace-agentd/main.go` — claimed by agent-oc-jun05-2330. Do not touch.
- `pkg/agentd/types.go` — claimed by agent-oc-jun05-2330. Do not touch.

---

## Pending Work (unclaimed)

See `worklogs/0169_2026-06-05_open-work-report.md` for the full list.
High priority unclaimed items:

- Epic 09 US-9.16 — preferredModel wiring in ModelSelector (`frontend/src/components/chat/ModelSelector.tsx`)
- Epic 24 US-24.17 — Disk pressure detection (`controller/internal/workspace/health.go`) — **blocked by agent-oc-jun05-2330 claim above**
- Epic 28 S28.8 — Goroutine leak + write deadline tests (`api/internal/handlers/stream_user_events_test.go`)
- Epic 16 US-16.13 — Backend integration test for question flow (`api/internal/tests/integration/`)
- Epic 27a US-27a.9 — Credflow integration test (`api/internal/handlers/agent_reload_e2e_test.go`)
- Epic 27b US-27b.5 — Chat error enrichment body buffering (`api/internal/handlers/proxy.go`)
