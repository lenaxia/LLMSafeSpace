# Open Work Report — 2026-06-05

Stories that are still relevant but not yet implemented, grouped by urgency.
Generated after Epic 30 (Unified Credential Model) merged in PR #39.

---

## 🔴 Live Bugs — Fix Immediately

These are confirmed defects in production-serving code paths, verified by direct code inspection.

---

### Epic 25 B2 — Proxy streaming truncation
**File:** `api/internal/handlers/proxy.go` ~line 590

The `doProxy` streaming loop silently `break`s on any read error (pod restart, network blip) and returns `nil`. The HTTP response headers were already flushed with status 200, so the client receives truncated JSON with no error signal and no way to distinguish success from failure. Every workspace pod restart during an active streaming session hits this path. Fix: distinguish `io.EOF` from real network errors; return a meaningful error for the latter.

---

### Epic 25 G1 — No request body size limit on proxy path
**File:** `api/internal/handlers/proxy.go` line 457

`io.ReadAll(c.Request.Body)` is called with no `http.MaxBytesReader` or `io.LimitReader` wrapper on the proxy route. A single oversized request body can exhaust API server memory. The auth routes in the same file correctly use `MaxBytesReader`; the proxy route was missed.

---

### Epic 27a / 27b — Drain mode is a silent no-op in production
**File:** `api/internal/app/app.go` ~line 281

`proxyHandler.GetSSETracker()` is called during `app.New()` (construction time), before `proxyHandler.Start()` runs. `Start()` is what allocates the SSETracker. At wiring time the tracker is always nil; the `if tracker != nil` guard means `SetSSETracker` is never called on the reload handler. Any call to `POST /workspaces/:id/agent/reload?drain=true` silently behaves identically to a non-drain reload — active sessions are never drained first. Fix: move the drain wiring into `Run()` after `proxyHandler.Start()` completes.

---

### Epic 06 US-6.7 — `local/test.sh` hard-fails
**File:** `local/test.sh` lines 222, 227, 236

The primary e2e validation script references two names that were renamed during the sandbox→workspace collapse (Epic 06):
- Line 222: `sandbox-pw-${WORKSPACE_NAME}` → should be `workspace-pw-${WORKSPACE_NAME}`
- Lines 227, 236: `kubectl exec -c sandbox` → container is now named `workspace`

The script aborts with a `die` at the password fetch step before any workspace tests run. Two-line fix.

---

### Epic 16 US-16.6 — MCP question/permission reply tools not registered
**File:** `pkg/mcp/server.go`

Worklog 0076 claimed `session_question_reply`, `session_question_reject`, and `session_permission_reply` were added as MCP tools. They were not. `pkg/mcp/server.go` registers exactly 11 tools; none of the three reply tools are among them. The `HTTPClient` methods for these operations exist on the concrete type but are not part of the `APIClient` interface and cannot be called from the server handlers. Any MCP client whose agent asks a question receives a stalled `session_message` call with no mechanism to reply, causing the agent to hang.

---

## 🟠 High Value — Do Next

---

### Epic 13 US-13.3 — Admin `MaxActiveSessions` setting silently ignored
**File:** `api/internal/services/workspace/workspace_service.go` (`applyWorkspaceDefaults`)

`applyWorkspaceDefaults` sets SecurityLevel, StorageClassName, Resources, AutoSuspend, and NetworkAccess from instance settings — but never `MaxActiveSessions`. The proxy reads `workspace.Spec.MaxActiveSessions` and falls back to a hardcoded constant of 5 when the field is zero. An admin who configures `workspace.defaultMaxActiveSessions = 10` sees no effect; every workspace is capped at 5 regardless. Two-line fix + two tests.

---

### Epic 30 US-30.7 / US-30.8 — Admin and User LLM Provider UI not built
**Files:** `frontend/src/` (AdminCredentialsTab, user credential management)

The Epic 30 backend is fully shipped: CRUD APIs for both admin and user provider credentials, server-KEK and user-DEK encryption, auto-apply seeding, and the full injection pipeline. The frontend is not built. Without it, admins must use raw API calls (`POST /api/v1/admin/provider-credentials`) to configure provider credentials, and users have no product UI to add their own API keys. This is the most visible missing piece of Epic 30 from a user perspective.

---

### Epic 17 — Threat model addendum required before next security review
**File:** `design/stories/epic-17-security-review/THREAT-MODEL.md`

Epic 30 introduced significant new attack surface that was not in scope when the threat model was written:
- Server-KEK encryption of admin credentials (KEK blast radius covers all admin credentials)
- `credential_auto_apply` auto-injects credentials into workspaces without explicit user action
- `EnsureFreeTierCredential` and `BackfillFreeTierBindings` write to the DB at API startup
- `PrepareSecretsForInjection` now merges three credential sources with priority semantics

A threat model addendum covering these should be written before the next pentest cycle.

---

### Epic 27b US-27b.4 / US-27b.5 — Bulk reload serial + enrichment not wired
**Files:** `api/internal/handlers/agent_reload.go`, `api/internal/handlers/proxy.go`

**US-27b.4 (serial bulk reload):** `BulkReload` iterates pending workspaces in a serial for-loop with no concurrency. At 15s per workspace timeout, 10 workspaces = up to 150s total. With Epic 30 shipping `credential_auto_apply`, a single admin credential change can now trigger pending-refresh state on every workspace simultaneously. Parallelism is operationally necessary at any meaningful scale.

**US-27b.5 (enrichment not wired):** `EnrichChatErrorBody` is implemented and unit-tested. It adds a `agentNeedsRefresh: true` hint to JSON error responses from the proxy so the frontend can prompt users to reload credentials. The function is never called — the proxy route never wraps its response writer with the enrichment. Chat errors silently omit the hint.

---

## 🟡 Important — Near-Term, Clear Value

---

### Epic 09 US-9.16 — `preferredModel` not wired into ModelSelector
**File:** `frontend/src/components/chat/ModelSelector.tsx`

The `preferredModel` Tier-3 user setting exists in `pkg/settings/schema.go`. `ModelSelector` reads only `data?.currentModel` from the per-workspace API — it never checks user preference when a workspace opens. A user who prefers a specific model must reselect it for every new workspace. The fix is a thin frontend hook reading the setting as the initial selection default, independent of any backend work.

---

### Epic 10 US-10.13 Part 1 — API auth tokens stored plaintext
**File:** `api/migrations/` (no migration exists for this), `api/internal/services/auth/auth.go`

All LLMSafeSpace API tokens (`api_keys.key` column) are stored as raw `VARCHAR(255)`. A database read-level breach exposes every user's token verbatim. This is independent of Epic 30 — that epic covers LLM provider credentials; the `api_keys` table stores the tokens users present to authenticate to LLMSafeSpace itself. Fix: migration to add `key_hash`/`key_prefix` columns, update `CreateAPIKey` to hash on write and `AuthenticateAPIKey` to compare against hash.

---

### Epic 16 US-16.13 — No backend integration test for the question flow
**Directory:** `api/internal/tests/integration/` (does not exist)

The full question flow (proxy detects agent question in SSE stream → frontend shows prompt → user replies via API → session resumes) has no backend integration test. Frontend Playwright tests exist but validate UI only, not the backend routing. Without this, US-16.6's fix (the missing MCP tools) and the underlying proxy question-detection logic have no automated regression protection.

---

### Epic 24 US-24.11 — No Prometheus metrics for the recovery system
**File:** `controller/internal/metrics/metrics.go`

The self-healing workspace recovery engine (failure classification, per-class backoff, consecutive failure counting) shipped in Epic 24 and is running in production. It emits zero metrics. There is no `workspace_recovery_attempts_total`, no `workspace_failures_by_class` breakdown, no backoff duration histogram, no safe-mode entry counter. Operators have no visibility into whether workspaces are cycling through recovery, how often failures occur by class, or whether the backoff is working. The recovery system is unobservable.

---

### Epic 24 US-24.17 — Disk pressure detection not implemented
**Files:** `controller/internal/workspace/health.go`, `pkg/apis/llmsafespace/v1/workspace_types.go`

The agent already reports `DiskUsedBytes` and `DiskTotalBytes` in the statusz response, and these fields are stored in `WorkspaceStatus`. The threshold check, the `WorkspaceConditionDiskPressure` constant, and the health check logic that would set the condition are all absent. Workspaces silently fill their PVC with no signal to the user or operator until writes start failing.

---

### Epic 27a US-27a.9 — Credflow integration test missing
**File:** `api/internal/handlers/agent_reload_e2e_test.go` (partial; full scenario absent)

The credential reload flow — bind credential → workspace shows `agentNeedsRefresh: true` → call `POST /workspaces/:id/agent/reload` → `agentNeedsRefresh: false` — has handler-level tests but no end-to-end test against a real database. With Epic 30 having rewritten `PrepareSecretsForInjection`, this integration path is the primary regression risk. A full credflow test is required by the project's Definition of Done (README-LLM.md §0: not done until passing e2e tests).

---

### Epic 28 S28.8 — Goroutine leak and write deadline tests absent
**File:** `api/internal/handlers/stream_user_events_test.go`

The zombie-connection fix — the core motivation for all of Epic 15 and Epic 28's SSE work — has no automated proof it holds. The `stream_user_events_test.go` file has 10 tests covering happy paths, replay, and snapshot behavior. Missing: a test that verifies all goroutines (heartbeat, main loop) exit when a client disconnects, and a test that verifies a slow or dead client is evicted when the 30-second write deadline fires. Without these, a regression to the zombie-connection behavior would not be caught by CI.

---

## 🟢 Meaningful — Lower Urgency

---

### Epic 14 US-14.4 — Python SDK is sync-only
**File:** `sdks/python/llmsafespace/client.py`

`LLMSafeSpace` and all sub-API classes use `httpx.Client` (synchronous). No `AsyncLLMSafeSpace` class exists. Python agent frameworks (FastAPI, LangChain async, asyncio event loops) are async-native; the sync client blocks their event loop and requires users to wrap calls in `asyncio.run_in_executor`. The V1 policy scripts and SDK structure are already in place — adding async variants is primarily duplication of existing methods onto `httpx.AsyncClient`.

---

### Epic 14 US-14.7 — Contract tests not executed in CI
**Files:** `sdks/tests/contract/` (3 Hurl files exist), `.github/workflows/ci.yml`

Three Hurl files (`auth.hurl`, `workspaces.hurl`, `errors.hurl`) exist and cover auth and workspace lifecycle. The CI `sdk-contract` job runs unit tests for Go, TypeScript, and Python — it has no `hurl` step. The contract tests provide drift detection between the OpenAPI spec and live server behavior. Missing: sessions/pagination Hurl files, Prism mock server setup, Java test step.

---

### Epic 14 US-14.9 — VS Code chat slash commands absent
**File:** `sdks/vscode-llmsafespace/src/providers/chat-participant.ts`

The VS Code chat participant is 57 lines of flat prompt forwarding to the active workspace. There is no `switch(request.command)` dispatch, no `/new-session`, `/switch-workspace`, `/history`, or `/status` commands, and no `commands` array in `package.json`. Slash commands are how users discover and invoke specific capabilities in a VS Code chat participant. Without them the extension offers no UX discoverability beyond raw freeform prompting.

---

### Epic 17 — Live re-pentest phases 2-7 not run
**Directory:** `design/stories/epic-17-security-review/` (no `phase-2-postfix` through `phase-7-postfix` dirs)

Approximately 46 code fixes were applied across pentest phases 2-7. The MASTER-TRACKER requires a post-fix live re-pentest verification pass per phase (`phase-N-postfix/` subdirectory). Only phase 1 has been re-run. Additionally, `F1.7.2` (API keys plaintext — HIGH) and `G25` (secret value in logging middleware — HIGH) remain unfixed and classified under a separate agent's branch.

---

### Epic 18 S18.11 — `WorkspaceConditionProviderReady` not added
**File:** `pkg/apis/llmsafespace/v1/workspace_types.go`

The readyz gate was already decoupled from provider connectivity (primary S18.11 goal — done). The complementary CRD condition is still worth adding now that Epic 30 has shipped: provider connectivity is now more deterministic (the free-tier credential seeds every workspace), so the condition will flip much less frequently — making it a clean, stable signal. It also eliminates the current approach of regex-parsing the freeform `AgentHealthy` condition message to extract connected provider names, which is fragile.

---

### Epic 23 Stories 2+3 — Status update conflict handling uninstrumented
**File:** `controller/internal/metrics/metrics.go`

`LastActivityAt` is written by three separate code paths (`activity.go:123`, `phase_suspend.go:61`, `workspace_service.go:458`). The deferral of single-writer migration was conditioned on observing >10 conflicts/day from a `WorkspaceStatusUpdateConflictsTotal` Prometheus counter. That counter was never shipped as part of Story 1. The deferral condition is permanently unverifiable without it, and the decision of whether to implement single-writer coordination remains indefinitely unmade.

---

### Epic 28 S28.5 — Session stream still uses legacy broker
**File:** `api/internal/handlers/proxy.go` (`StreamEvents` function)

`StreamEvents` calls `h.broker.Subscribe(workspaceID)` on the old `WorkspaceEventBroker` rather than `h.userBroker.SubscribeWorkspace()`. The session stream is stable and there is no active bug. Migrating completes the Epic 28 unified event stream goal, makes `SubscribeWorkspace()` a live code path (it is currently dead), and makes the two SSE streams architecturally consistent — simplifying future changes to either.

---

## 🔵 New Epics — Planning, Not Started

---

### Epic 31 — Shared Workspace Per User (User Drive)
**Directory:** `design/stories/epic-31-shared-workspace-per-user/`

A per-user persistent shared workspace ("user drive") — a long-lived workspace that persists across sessions, accumulates installed tools, retains file history, and acts as a personal environment for the user. All three prerequisites (Epics 6, 9, 24) have shipped. Marked High priority.

---

### Epic 32 — VPN Sidecars, VPC Connectivity & AWS IAM
**Directory:** `design/stories/epic-32-vpn-network-iam/`

Allow workspace pods to join private VPCs via VPN sidecar injection, access AWS resources via IAM role binding, and connect to private services (RDS, Elasticache, internal APIs) without exposing credentials to the agent. All three prerequisites (Epics 6, 9, 24) have shipped. Marked High priority.

---

## Summary

| Priority | Count | Items |
|----------|-------|-------|
| 🔴 Live bugs | 5 | Proxy truncation (B2), body size limit (G1), drain no-op, test.sh, MCP question tools |
| 🟠 High value | 4 | MaxActiveSessions, provider UI (30.7/30.8), threat model, bulk reload + enrichment |
| 🟡 Important | 7 | preferredModel, API key hashing, question E2E test, recovery metrics, disk pressure, credflow test, goroutine leak tests |
| 🟢 Lower urgency | 7 | Python async, contract tests, VS Code slash cmds, re-pentest, ProviderReady condition, conflict metric, session stream migration |
| 🔵 New / planning | 2 | Epic 31 (user drive), Epic 32 (VPN/IAM) |
| **Total** | **25** | |
