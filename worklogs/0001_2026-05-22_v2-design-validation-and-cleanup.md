# Worklog: V2 Design Validation and Story Cleanup

**Date:** 2026-05-22
**Session:** Two-pass design validation of EVOLUTION-V2.md and all implementation stories, then fix all identified issues
**Status:** Complete

---

## Objective

Validate the V2 design document (EVOLUTION-V2.md v2.2) and 22 implementation stories for sustainability, robustness, reliability, maintainability, and unnecessary complexity. State and validate all assumptions. Fix all issues found.

---

## Work Completed

### 1. First Validation Pass (v2.2 → v2.3)

Read full EVOLUTION-V2.md (~2100 lines) and all 22 stories across 5 epics.

**Found 5 critical issues:**
- C1: Stories reference `cmd/` dir that doesn't exist; US-3.1/3.2 use Chi but codebase uses Gin
- C2: US-1.1/US-1.2 miss broken `zz_generated.deepcopy.go` (6 errors) and all 5 webhook decoder bugs
- C3: Duplicate CRD types (`pkg/types/` vs `controller/internal/resources/`) unaddressed
- C4: SandboxProfile and RuntimeEnvironment CRDs ignored by design
- C5: MCP file upload/download has no implementation path (opencode has no upload endpoint)

**Found 7 design concerns:**
- D1: Dual CRD status writers with no conflict resolution
- D2: Over-complexity for V1 (two security levels, WebSocket bridge, injection detection, PATH wrappers)
- D3: No graceful opencode shutdown timeout specified
- D4: In-memory activity tracker loses data on API restart
- D5: No testing strategy in any story
- D6: Existing runtime security wrappers not addressed
- D7: 10-week timeline assumes ideal conditions

**Validated 7 assumptions** (A1-A7) against live opencode tests and source code.

**Applied fixes to EVOLUTION-V2.md (v2.2 → v2.3):**
- Added §P.1-P.3: CRD disposition, CRD type ownership model, Gin web framework note
- Fixed proxy handler from Chi to Gin
- Added RetryOnConflict for dual status writers
- Added graceful shutdown spec (30s SIGTERM → SIGKILL)
- Added §7.5a: MCP file transfer implementation path
- Removed WebSocket↔SSE bridge from V1
- Updated §14 roadmap to Phase 0-5 + deferred items
- Updated Appendix A, risk assessment

**Created new stories:**
- `epic-0-unbreak/US-0.1-fix-deepcopy-generation.md`
- `epic-0-unbreak/US-0.2-fix-webhook-decoders.md`

**Fixed 8 stories:** US-1.1, US-1.2, US-1.3, US-1.5, US-3.1, US-3.2, US-4.1
**Marked 4 stories deferred:** US-1.6, US-5.1, US-5.2, US-5.3
**Rewrote:** `design/stories/README.md`

### 2. Second Validation Pass (v2.3 → v2.4)

Re-read full updated design and all 24 stories.

**Found 1 critical inconsistency:**
- Workspace CRD phase enum missing `Suspending` and `Resuming`

**Found 18 stale references** to deferred features in the design doc.

**Identified 10 removal candidates**, validated each:
- Deferred sections: mark, don't delete
- `securityLevel`, `networkAccess`: keep in CRD, controller ignores in V1
- `initScript`: keep — needed beyond simple packages
- Auto-suspend: keep — critical cost-saving feature
- `ttlSecondsAfterSuspended`: keep — trivial to implement
- `mode-gate`: remove from V1
- MCP file tools: mark deferred in code block

**Validated 3 additional assumptions:**
- A7: Session-level credentials needed → QUESTIONABLE → deferred to V2.1
- A8: Proxy reads password every request → add caching
- A10: Existing runtime wrappers should be removed → add to US-1.8

**Applied 28 fixes to EVOLUTION-V2.md (v2.3 → v2.4):**
- Added Suspending/Resuming to Workspace CRD phase enum and lifecycle diagram
- Fixed `api/cmd/` → `cmd/` paths
- Moved MCP file tools to V2.1 in diagram
- Split security overview into V1/V2.1 rows
- Updated R4, R7 to note deferred parts
- Removed mode-gate from V1 pod diagram and init containers
- Marked §9.2, §9.4, §9.5, §9.6, §9.7, §13.4 as DEFERRED
- Deferred session-level credentials (removed 4 API endpoints, simplified controller)
- Added password caching to proxy handler
- Cleaned up §13.1 base image structure
- Fixed 4 stale references

**Fixed 4 stories:** US-2.4, US-3.1, US-1.8, README.md

### 3. README-LLM.md Update

Updated `README-LLM.md` from v1.0 to v1.1:
- Replaced V1 project overview (warm pools, exec services) with V2 (agent proxy, workspaces, MCP)
- Replaced V1 architecture diagram with V2 (4 CRDs, proxy handler, MCP server, no warm pools)
- Updated CRD table: 5 → 4 CRDs (added Workspace, removed WarmPool/WarmPod)
- Updated sandbox lifecycle: added suspend/resume phases
- Added workspace lifecycle diagram
- Updated service initialization order
- Updated key documents table to highlight EVOLUTION-V2.md as authoritative

---

## Key Decisions

1. **Deferred 8 features to V2.1** — injection detection, PATH wrappers, hardened Dockerfile, Kyverno policies, WebSocket bridge, MCP file tools, session-level credentials, high-security mode. Each has clear rationale documented in the deferred table.

2. **Session-level credentials deferred** — workspace-only credentials simplify the controller (one secret resolution path) and API (4 fewer endpoints). Users can set credentials at the workspace level. Add session-level override in V2.1 if users ask.

3. **Password caching in proxy** — passwords are generated at sandbox creation and never change. Cache by sandbox ID, invalidate on phase change to Suspending/Suspended/Terminated. Avoids K8s Secret read on every proxy request.

4. **Keep CRD fields for deferred features** — `securityLevel` and `networkAccess` stay in the Workspace CRD schema. Adding them now means no schema migration later. Controller ignores `high` in V1.

5. **Remove mode-gate init container from V1** — only needed for high-security mode. The deferred section retains the design for V2.1.

6. **Created Epic 0 (Unbreak)** — the broken deepcopy generation and webhook decoders block the entire monorepo build. These must be fixed before any other work begins. Two new stories: US-0.1 and US-0.2.

---

## Blockers

None. All identified issues have been resolved.

---

## Tests Run

No code tests were run — this was a design and documentation session. The validation was performed by:
- Re-reading all design docs and stories for internal consistency
- Cross-referencing file paths, function signatures, and router patterns against the actual codebase
- Checking that deferred features are not referenced in V1-scope sections
- Verifying phase enums match lifecycle diagrams

---

## Next Steps

1. **Start Epic 0:** US-0.1 (fix deepcopy) and US-0.2 (fix webhook decoders) — these unblock everything
2. **Then Epic 1:** US-1.1 (fix API compile) and US-1.2 (fix controller compile)
3. US-1.3 (remove warm pools) and US-1.4 (remove exec/file services) can proceed
4. US-1.5 (redact binary), US-1.7 (entrypoints), US-1.8 (Dockerfile) can be done in parallel with the compile fixes since they're new standalone files

---

## Files Modified

### Design document
- `design/EVOLUTION-V2.md` — v2.2 → v2.4 (~30 edits across 2 sessions)

### New stories
- `design/stories/epic-0-unbreak/US-0.1-fix-deepcopy-generation.md` (new)
- `design/stories/epic-0-unbreak/US-0.2-fix-webhook-decoders.md` (new)

### Updated stories
- `design/stories/README.md` (rewritten)
- `design/stories/epic-1-foundation/US-1.1-fix-api-compile-errors.md` (rewritten)
- `design/stories/epic-1-foundation/US-1.2-fix-controller-compile-errors.md` (rewritten)
- `design/stories/epic-1-foundation/US-1.3-remove-warm-pools.md` (rewritten)
- `design/stories/epic-1-foundation/US-1.5-build-redact-binary.md` (edited)
- `design/stories/epic-1-foundation/US-1.6-port-injection-detection.md` (marked deferred)
- `design/stories/epic-1-foundation/US-1.8-rewrite-base-dockerfile.md` (edited)
- `design/stories/epic-2-workspaces/US-2.4-update-sandbox-reconciler.md` (rewritten)
- `design/stories/epic-3-proxy-sessions/US-3.1-implement-proxy-handler.md` (rewritten)
- `design/stories/epic-3-proxy-sessions/US-3.2-add-session-proxy-routes.md` (rewritten)
- `design/stories/epic-4-mcp-server/US-4.1-implement-mcp-server.md` (rewritten)
- `design/stories/epic-5-security-hardening/US-5.1-build-wrappers.md` (marked deferred)
- `design/stories/epic-5-security-hardening/US-5.2-create-hardened-dockerfile.md` (marked deferred)
- `design/stories/epic-5-security-hardening/US-5.3-write-kyverno-policies.md` (marked deferred)

### Documentation
- `README-LLM.md` — v1.0 → v1.1 (architecture, CRDs, lifecycle, docs table updated)

### Unchanged stories (verified correct)
- US-1.4, US-1.7, US-2.1, US-2.2, US-2.3, US-2.5, US-3.3, US-5.4

### New directory
- `worklogs/` (created)
- `design/stories/epic-0-unbreak/` (created)
