# COORDINATE.md — Multi-Agent Work Coordination

This file is the source of truth for what work is in-flight across all agents.
**Before starting any work: read this file. After finishing any work: update this file and commit it.**

Rules:
- Claim a section before touching its files. If it's claimed by another agent, wait or pick different work.
- Keep claims specific (file paths, not vague areas).
- Mark work DONE immediately when finished — do not batch updates.
- If you abandon work, release the claim so another agent can pick it up.
- Always git pull before starting work. Always commit COORDINATE.md with your work commits.
- To queue behind a current claim, add a row to **Pending Claims**. When the blocking claim is released, move your row to Active Claims.

---

## Active Claims

| Agent | What | Files Claimed | Status | Started |
|-------|------|---------------|--------|---------|
| agent-relay-jun06 | Active workspace gauge + session duration histogram + auth failure counter | `controller/internal/workspace/reconciler.go`, `controller/internal/workspace/phase_active.go`, `api/internal/services/metrics/metrics.go`, `api/internal/middleware/auth.go`, `api/internal/handlers/session_tracker.go` | IN PROGRESS | 2026-06-06 07:30 |

---

## Pending Claims

Agents waiting to work on files currently held by an active claim. When the blocking claim is released, move your row to Active Claims.

| Agent | Waiting For | What They Plan To Do | Files Wanted |
|-------|-------------|----------------------|--------------|

---

## Recently Completed (last 10)

| Completed | Agent | What | Commit |
|-----------|-------|------|--------|
| 2026-06-06 | agent-oc-jun05-2330 | API key SHA-256 hashing (migration 000017) + CPU/disk/memory cgroup metering + controller metrics taxonomy (operational/recovery/metering/billing) + Pending Claims section in COORDINATE.md | this commit |
| 2026-06-06 | agent-audit-0606 | Epic 28 S28.8 — goroutine leak + write-deadline tests | f1af270 |
| 2026-06-06 | agent-audit-0606 | sseConnection cleanup + 2 new weak-point tests + worklog 0171 | 1baac7d |
| 2026-06-06 | agent-audit-0606 | Fix pre-existing flaky test: sseConnection.test.ts backoff-with-jitter pinned Math.random → deterministic | dc38ad8 |
| 2026-06-06 | agent-relay-jun06 | Fix #1: resolveModelIDFromCatalog relay providerID remap + billing/metering/ops metrics | b77b9c0 |
| 2026-06-06 | agent-relay-jun06 | CF Worker secret-path auth + phase-2 relay injector + opencode-relay model surfacing | d836c94 |
| 2026-06-05 | agent-oc-jun05-2330 | Epic 30 credential audit fixes | 0170cb4 |
| 2026-06-05 | agent-oc-jun05-2330 | CPU metering migration files only | 7b6e234 |
| 2026-06-05 | agent-relay-jun06 | API-side billing/metering metrics + relay model surfacing | b77b9c0 |
| 2026-06-05 | — | Live bugs + high-value items (proxy, drain, MCP, settings) | 9a672cc |

---

## Known Conflicts / Merge Notes

- `api/internal/services/metrics/metrics.go` — agent-relay-jun06 owns API-side billing metrics. **Do not overwrite.** Controller metrics live in `controller/internal/metrics/metrics.go` (separate file).
- `api/internal/app/app.go` — modified by b77b9c0 and 0170cb4. Pull before touching.
- `api/internal/handlers/session_tracker.go` — modified by b77b9c0. Pull before touching.
- `api/internal/handlers/models.go` — modified by b77b9c0. Pull before touching.
- `pkg/secrets/` — heavily modified by 0170cb4. Pull before touching.
- `frontend/src/components/settings/` — modified by 0170cb4. Pull before touching.

---

## Pending Work (unclaimed)

See `worklogs/0169_2026-06-05_open-work-report.md` for the full list.
High priority unclaimed items:

- Epic 09 US-9.16 — preferredModel wiring in ModelSelector (`frontend/src/components/chat/ModelSelector.tsx`)
- Epic 24 US-24.17 — Disk pressure detection (`controller/internal/workspace/health.go`)
- Epic 16 US-16.13 — Backend integration test for question flow (`api/internal/tests/integration/`)
- Epic 27a US-27a.9 — Credflow integration test (`api/internal/handlers/agent_reload_e2e_test.go`)
- Epic 27b US-27b.5 — Chat error enrichment body buffering (`api/internal/handlers/proxy.go`)
