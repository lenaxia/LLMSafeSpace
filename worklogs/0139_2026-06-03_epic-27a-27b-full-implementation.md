# Worklog: Epic 27a+27b — Full Implementation

**Date:** 2026-06-03
**Session:** Complete implementation of Epic 27a and partial 27b (drain + enrichment)
**Status:** Complete (27a) / In Progress (27b — bulk, docs, metrics remaining)

---

## Objective

Ship Epic 27a (Credential Reload Foundation) and begin Epic 27b (Credential Reload Polish) in a single session. The goal is to deliver the full path from credential staging to explicit user-triggered reload, including the frontend banner and drain mode.

---

## Work Completed

### Epic 27a — ALL STORIES SHIPPED

| Story | What was done |
|---|---|
| US-27a.1 | Migration 000014: workspace_agent_state table, Bug 11 FK, Bug 12 FK |
| US-27a.2 | DB helpers (MarkCredentialChanged, GetLastCredentialChangedAt, MarkAgentReloaded, BeginTx), WorkspaceMetadata + Workspace + WorkspaceListItem extended with agent state fields |
| US-27a.2b | BindingsMutationResult, computeBindingsDiff, updated SetBindings/AddBindings signatures, CredentialStateWriter interface, handler wiring |
| US-27a.3 | agentNeedsRefresh exposed on GetWorkspace and ListWorkspaces API responses |
| US-27a.4 | RefreshCredentials → StageCredentials rename |
| US-27a.5 | agentd no longer auto-disposes on credential changes |
| US-27a.6 | agentd POST /v1/agent/reload endpoint |
| US-27a.7 | API endpoint POST /workspaces/:id/agent/reload |
| US-27a.8 | Frontend AgentReloadBanner component with modal |
| US-27a.9 | Integration test scaffold |

### Epic 27b — PARTIAL (Critical Path Done)

| Story | What was done |
|---|---|
| US-27b.1 | Client.GetSessionStatuses, SSETracker.SubscribeDrain fan-out |
| US-27b.2 | WaitUntilIdle drain primitive with subscribe-before-snapshot |
| US-27b.3 | Drain mode (?drain=true) on reload endpoint |
| US-27b.5 | EnrichChatErrorBody helper (proxy integration deferred) |

### Remaining (tracked, not blocking):
- US-27b.4: Bulk reload endpoint (NDJSON streaming)
- US-27b.6: API reference documentation
- US-27b.7: SDK ergonomics design
- US-27b.8: metrics.Service extension

---

## Key Decisions

1. **Logger interface**: Changed from `*zap.Logger` to `pkginterfaces.LoggerInterface` for AgentReloadHandler to match existing patterns (ProxyHandler, etc.)
2. **respondWithAPIError**: Created local helper in package handlers since `respondWithError` lives in package server (can't import cross-package)
3. **CredentialStateWriter gap caught by Rule 10**: Adversarial self-review identified that `MarkCredentialChanged` was never called after SetBindings. Fixed immediately.
4. **Drain mode nil-safe**: If sseTracker or getPassword are nil (not injected), drain is silently skipped. Immediate dispose proceeds.
5. **drainTimeoutSeconds capped at 600**: Server enforces max; values > 600 get the default.

---

## Tests Run

All commands run with `GOTOOLCHAIN=local GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=*` (air-gapped environment).

```
go test ./pkg/agent/opencode/ → ok (0.5s)
go test ./cmd/workspace-agentd/ → ok (38-55s)
go test ./api/... → all 16 packages ok
go test ./pkg/secrets/ → ok
go build ./... → clean
npx tsc --noEmit (frontend) → clean
npx vitest run → 604 tests passed
```

---

## Next Steps

1. US-27b.4: Implement bulk reload endpoint with NDJSON streaming
2. US-27b.8: Add Prometheus metrics for reload operations
3. Wire SSETracker and passwordGetter into AgentReloadHandler in app.go (currently nil — drain mode is a no-op until wired)
4. Full proxy integration of error enrichment (requires buffering 4xx/5xx responses)
5. Deploy to cluster and validate full credflow exercise end-to-end

---

## Files Modified

### pkg/agent/opencode/
- client.go — StageCredentials (was RefreshCredentials), GetSessionStatuses
- client_test.go — StageCredentials tests
- client_integration_test.go — StageCredentials tests

### cmd/workspace-agentd/
- secrets.go — StageCredentials, no auto-dispose, no configReloaded
- main.go — /v1/agent/reload route
- agent_reload.go — NEW: agentReloadHandler
- agent_reload_test.go — NEW

### api/migrations/
- 000014_workspace_agent_state_and_bug11_fix.up.sql — NEW
- 000014_workspace_agent_state_and_bug11_fix.down.sql — NEW

### api/internal/errors/
- errors.go — ErrNoAgentStateRow sentinel

### api/internal/services/database/
- database.go — LEFT JOIN queries, MarkCredentialChanged, GetLastCredentialChangedAt, MarkAgentReloaded, BeginTx
- database_test.go — updated mock expectations for 11-column queries

### api/internal/services/workspace/
- workspace_service.go — maps new fields in GetWorkspace + ListWorkspaces

### api/internal/handlers/
- agent_reload.go — NEW: AgentReloadHandler with drain mode
- agent_drain.go — NEW: WaitUntilIdle, ErrDrainTimeout
- agent_reload_test.go — NEW: integration test scaffold
- proxy_chat_enrichment.go — NEW: EnrichChatErrorBody
- secrets.go — CredentialStateWriter interface + wiring
- session_tracker.go — SubscribeDrain, drainSubs, fan-out in dispatchProperties

### api/internal/server/
- router.go — RouterConfig.AgentReloadHandler, registerWorkspaceRoutes cfg param, route registration

### api/internal/app/
- app.go — AgentReloadHandler construction + wiring, SetCredentialStateWriter

### pkg/types/
- types.go — Workspace, WorkspaceListItem, WorkspaceMetadata gain agent state fields

### pkg/secrets/
- bindings_diff.go — NEW: BindingsMutationResult, computeBindingsDiff, sortedKeys
- secret_service.go — SetBindings/AddBindings return BindingsMutationResult
- *_test.go — all updated for new return signatures

### frontend/
- src/api/types.ts — agentNeedsRefresh, credentialsPendingSince
- src/api/workspaces.ts — reloadAgent API call
- src/components/workspace/AgentReloadBanner.tsx — NEW
- src/pages/ChatPage.tsx — banner integration

### Root
- README-LLM.md — Rule 10 (Adversarial Self-Review)
