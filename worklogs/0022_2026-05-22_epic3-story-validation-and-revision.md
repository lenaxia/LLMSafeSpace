# Worklog 0022: Epic 3 Story Validation and Revision

**Date:** 2026-05-22
**Session:** Validate Epic 3 user stories against codebase, identify gaps, revise stories
**Status:** Complete

---

## Objective

Before starting Epic 3 implementation, perform a skeptical validation of all three user stories (US-3.1, US-3.2, US-3.3). Validate every assumption against the actual codebase, assess alignment with design doc, evaluate whether stories are over/under-engineered, and produce a test plan.

---

## Methodology

1. Listed 18 explicit assumptions the stories make
2. Validated each assumption by reading source code (types, interfaces, K8s client, controller, services)
3. Assessed correctness, alignment with EVOLUTION-V2.md, engineering quality
4. Identified required story changes
5. Revised all three story files

---

## Assumption Validation (18 assumptions)

| # | Assumption | Result | Evidence |
|---|-----------|--------|----------|
| A1 | `api/internal/handlers/` exists | **FAIL** | Directory does not exist |
| A2 | Sandbox CRD status has `podIP` | PASS | `v1.SandboxStatus.PodIP` confirmed |
| A3 | K8s client returns full CRD with status | PASS | `sandboxes.Get()` returns full `v1.Sandbox` |
| A4 | K8s client can read Secrets | PASS | `k8sClient.Clientset().CoreV1().Secrets()` used by workspace service |
| A5 | Informer-cached CRD reads | PARTIAL | CRD reads use REST calls, not informers |
| A6 | Workspace has `maxActiveSessions` | PASS | `v1.WorkspaceSpec.MaxActiveSessions` confirmed |
| A7 | Sandbox→workspace link via WorkspaceRef | PASS | `v1.SandboxSpec.WorkspaceRef` confirmed in both type systems |
| A8 | Router has no proxy routes | PASS | Verified in router.go |
| A9 | app.go can wire ProxyHandler | PASS | With caveats about router consolidation |
| A10 | Services interface has needed getters | PASS | All getters present |
| A11 | Workspace CRD status has `lastActivityAt` | PASS | `v1.WorkspaceStatus.LastActivityAt` confirmed |
| A12 | API can patch Workspace CRD status | PASS | `WorkspaceInterface.UpdateStatus()` exists |
| A13 | Proxy can resolve sandbox→workspace | PASS | Via `spec.workspaceRef` on Sandbox CRD |
| A14 | `httputil.ReverseProxy` available | PASS | Go stdlib |
| A15 | Gin route params work | PASS | Standard Gin |
| A16 | OpenCode serves on port 4096 | PASS | Design doc verified |
| A17 | Controller generates password Secret | PASS | `ensurePasswordSecret()` creates `sandbox-pw-{name}` |
| A18 | API SA can read password Secrets | UNVALIDATED | No RBAC manifests yet (Epic 5) |

---

## Key Findings

### Finding 1: SSE session tracking is correct (reversed initial recommendation)

Initially recommended simplifying to request-level tracking. On deeper analysis, `prompt_async` returns 204 immediately — the agent continues processing. Request-level tracking would make `maxActiveSessions` a no-op for the MCP/programmatic persona. SSE tracking is the only correct approach.

### Finding 2: CRD watcher is shared infrastructure, not just cache invalidation

The watcher serves three consumers: password cache invalidation, workspace config invalidation, and future MCP/notify needs (Epic 4). Building it once as shared infrastructure pays compound dividends.

### Finding 3: K8s client uses REST calls, not informer-cached reads

The `LLMSafespaceV1Client` uses a REST client for all CRD operations. Stories should not claim "informer-cached reads." For V1, REST calls + local caching is acceptable.

### Finding 4: `UpdateStatus()` is full PUT, not strategic merge patch

US-3.3 must use read-modify-write + `RetryOnConflict` pattern instead of assuming a patch operation exists.

---

## Story Changes Made

### US-3.1 (148→232 lines, +84 lines)

- Added Workspace→Sandbox→Session relationship diagram
- Added "Why SSE-based session tracking" section with `prompt_async` justification
- Added `workspaceConfig` cache struct with invalidation strategy
- Added `maxActiveSessions resolution` section (CRD chain lookup + caching)
- Added `CRD watcher` section as shared infrastructure
- Added `ProxyHandler lifecycle` with `Start()`/`Stop()`
- Documented REST calls (not informer-cached) with future upgrade path
- Updated effort: 8-10h → 10-12h

### US-3.2 (49→71 lines, +22 lines)

- Added ownership check section: uses CRD `labels["user-id"]` to avoid DB lookup
- Added app wiring section: consolidate `app.go` and `server/router.go` routing

### US-3.3 (52→124 lines, +72 lines)

- Added workspaceID resolution (reuses `wsConfig` from US-3.1)
- Added K8s API interaction section: read-modify-write + `RetryOnConflict`
- Added `Start()`/`Stop()` lifecycle with final flush on shutdown
- Added `ProxyHandler`↔`ActivityTracker` integration section

---

## Files Changed

| File | Change |
|------|--------|
| `design/stories/epic-3-proxy-sessions/US-3.1-implement-proxy-handler.md` | Revised with 6 new sections |
| `design/stories/epic-3-proxy-sessions/US-3.2-add-session-proxy-routes.md` | Added ownership check + app wiring |
| `design/stories/epic-3-proxy-sessions/US-3.3-implement-activity-tracking.md` | Added lifecycle, K8s API, integration |

Also includes all previously uncommitted Epic 2 validation fixes (9 gaps, 52 e2e tests).

---

## Test Status

- 463 tests, 0 failures (no new test changes this session — story revision only)
- Clean `go build ./...`

---

## Next Steps

- Begin US-3.1 implementation: create `api/internal/handlers/` directory, implement ProxyHandler with TDD
