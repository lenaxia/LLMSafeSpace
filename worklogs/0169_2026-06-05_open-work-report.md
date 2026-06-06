# Open Work Report — 2026-06-05 (Updated)

Stories that are still relevant but not yet implemented, grouped by urgency.
Last updated after fixing all 5 live bugs and 4 high-value items (commit 7325119).

---

## 🔴 Live Bugs — Fix Immediately

**All 5 live bugs from the original report have been fixed and pushed.**

| Bug | Fix | Commit |
|-----|-----|--------|
| Epic 25 B2 — Proxy streaming truncation | `doProxy` now treats `io.EOF` and `io.ErrUnexpectedEOF` as normal stream end; distinguishes true mid-stream failures; pod-IP retry and `c.JSON` error response both gated on `!c.Writer.Written()` to prevent double-writes on already-flushed streaming responses | 9a672cc + 7325119 |
| Epic 25 G1 — No body size limit | `http.MaxBytesReader(nil, body, 10MB)` wraps request body before `io.ReadAll`; nil first-arg prevents net/http from auto-writing 413; uses `errors.As(*http.MaxBytesError)` for detection | 9a672cc + 7325119 |
| Epic 27a/27b — Drain mode silent no-op | SSETracker wiring moved from `app.New()` to `app.Run()` after `proxyHandler.Start()`; nil guard added to metrics wiring block; App struct now holds reload handler references | 9a672cc + 7325119 |
| Epic 06 US-6.7 — `local/test.sh` hard-fails | `sandbox-pw-*` → `workspace-pw-*`; `kubectl exec -c sandbox` → `-c workspace` (lines 222, 227, 236) | 9a672cc |
| Epic 16 US-16.6 — MCP question tools missing | `session_question_reply`, `session_question_reject`, `session_permission_reply` added to `APIClient` interface and registered in `server.go`; server now has 14 tools | 9a672cc |

---

## 🟠 High Value — Do Next

**All 4 high-value items from the original report have been addressed.**

| Item | Status | Notes |
|------|--------|-------|
| Epic 13 US-13.3 — MaxActiveSessions silently ignored | ✅ Fixed | `applyWorkspaceDefaults` now reads `workspace.defaultMaxActiveSessions` from instance settings and sets `crd.Spec.MaxActiveSessions` |
| Epic 30 US-30.7/30.8 — Admin + User LLM Provider UI | ✅ Built | `AdminProviderCredentialsTab` + `UserProviderCredentialsTab` components; `providerCredentials.ts` API module; wired into SettingsPage as "Platform Credentials" (admin) + "Provider Keys" (all users) tabs |
| Epic 17 — Epic 30 threat model addendum | ✅ Written | `design/stories/epic-17-security-review/THREAT-MODEL-ADDENDUM-EPIC30.md` — 5 threat areas, 8 pentest cases |
| Epic 27b US-27b.4/27b.5 — Bulk reload + enrichment | ✅ Fixed | BulkReload: goroutine fan-out with semaphore (max 5 concurrent). Enrichment: `SetAgentStateChecker` wired; logs staged-credential hint on 4xx (full response-body rewrite deferred — requires buffering refactor tracked separately) |

---

## 🟡 Important — Near-Term, Clear Value

---

### Epic 09 US-9.16 — `preferredModel` not wired into ModelSelector
**File:** `frontend/src/components/chat/ModelSelector.tsx`

The `preferredModel` Tier-3 user setting exists in `pkg/settings/schema.go`. `ModelSelector` reads only the per-workspace current model from the API — never seeds from user preference when a workspace opens. A user who prefers a specific model must reselect it for every new workspace. Thin frontend hook change, independent of any backend work. Deferred until after Epic 30 US-30.9 (model listing rewrite) to avoid touching the same code twice.

---

### Epic 10 US-10.13 Part 1 — API auth tokens stored plaintext
**File:** `api/migrations/` (no migration exists), `api/internal/services/auth/auth.go`

All LLMSafeSpace API tokens (`api_keys.key` column) are stored as raw `VARCHAR(255)`. A database read-level breach exposes every user's token verbatim. Independent of Epic 30 — that covers LLM provider credentials; this covers the tokens users present to authenticate to LLMSafeSpace itself. Fix: migration adding `key_hash`/`key_prefix` columns, hash on write, compare against hash on authenticate.

---

### Epic 16 US-16.13 — No backend integration test for the question flow
**Directory:** `api/internal/tests/integration/` (does not exist)

The full question flow (proxy detects agent question in SSE stream → user replies via API → session resumes) has no backend integration test. Frontend Playwright tests exist but validate UI only. US-16.6 was just fixed — the missing tools now exist — but neither the tool dispatch nor the underlying proxy question-detection logic has automated regression protection.

---

### Epic 24 US-24.11 — No Prometheus metrics for the recovery system
**File:** `controller/internal/metrics/metrics.go`

The self-healing workspace recovery engine shipped in Epic 24 and is running in production. It emits zero metrics. No `workspace_recovery_attempts_total`, no `workspace_failures_by_class`, no backoff duration histogram. The recovery system is completely unobservable — operators cannot tell if workspaces are cycling through recovery loops.

---

### Epic 24 US-24.17 — Disk pressure detection not implemented
**Files:** `controller/internal/workspace/health.go`, `pkg/apis/llmsafespace/v1/workspace_types.go`

`DiskUsedBytes` and `DiskTotalBytes` are collected from the agent and stored in `WorkspaceStatus`. The threshold check, `WorkspaceConditionDiskPressure` constant, and health check logic are all absent. Workspaces silently fill their PVC with no signal to the user or operator until writes start failing.

---

### Epic 27a US-27a.9 — Credflow integration test missing
**File:** `api/internal/handlers/agent_reload_e2e_test.go` (partial)

The credential reload flow — bind credential → `agentNeedsRefresh: true` → call reload → `agentNeedsRefresh: false` — has handler-level tests but no end-to-end test against a real database. With Epic 30 having rewritten `PrepareSecretsForInjection`, this is the primary regression risk. Required by the project's Definition of Done (README-LLM.md §0: not done until passing e2e tests).

---

### Epic 28 S28.8 — Goroutine leak and write deadline tests absent
**File:** `api/internal/handlers/stream_user_events_test.go`

The zombie-connection fix (the core motivation for all of Epic 15/28 SSE work) has no automated regression protection. 10 tests cover happy paths; missing: goroutine-exits-on-disconnect test and write-deadline-evicts-slow-client test. Without these, a regression to zombie connections would not be caught by CI.

---

### Epic 27b US-27b.5 — Chat error enrichment (partial — response buffering deferred)
**File:** `api/internal/handlers/proxy.go` (`SendMessage`)

The `EnrichChatErrorBody` function is built and tested. `SetAgentStateChecker` is now wired and the DB query runs on 4xx errors. However, the actual body rewrite is deferred: gin's streaming `ResponseWriter` doesn't expose `Body() []byte`, so we cannot rewrite the response after it's been flushed. The proper fix requires buffering the response body in `doProxy` before writing it, then conditionally transforming it. Tracked as a follow-on to Epic 27b. Currently: the hint is logged server-side; clients still need to poll `GET /workspaces/:id/status` to see `agentNeedsRefresh: true`.

---

## 🟢 Meaningful — Lower Urgency

---

### Epic 14 US-14.4 — Python SDK is sync-only
**File:** `sdks/python/llmsafespace/client.py`

No `AsyncLLMSafeSpace` class. Python agent frameworks (FastAPI, LangChain async, asyncio pipelines) are async-native; the sync client blocks their event loop.

---

### Epic 14 US-14.7 — Contract tests not executed in CI
**Files:** `sdks/tests/contract/`, `.github/workflows/ci.yml`

3 Hurl files exist but are not run in CI. Missing: sessions/pagination coverage, Prism mock server, Java step.

---

### Epic 14 US-14.9 — VS Code chat slash commands absent
**File:** `sdks/vscode-llmsafespace/src/providers/chat-participant.ts`

57-line flat prompt forwarder. No `/new-session`, `/switch-workspace`, `/history`, `/status` commands. No `commands` array in `package.json`. Slash commands are the primary UX discoverability feature of a VS Code chat participant.

---

### Epic 17 — Live re-pentest phases 2-7 not run
**Directory:** `design/stories/epic-17-security-review/`

~46 code fixes applied. Post-remediation live re-pentest not executed — no `phase-2-postfix` through `phase-7-postfix` directories. `F1.7.2` (API keys plaintext — HIGH) and `G25` (secret in logging middleware — HIGH) remain unfixed (classified OTHER agent branch). Epic 30 threat model addendum now written; re-pentest should be scheduled to cover new Epic 30 attack surface.

---

### Epic 18 S18.11 — `WorkspaceConditionProviderReady` not added
**File:** `pkg/apis/llmsafespace/v1/workspace_types.go`

Readyz gate decoupled (primary S18.11 goal — done). Complementary CRD condition still worth adding post-Epic 30: eliminates fragile regex-parsing of `AgentHealthy` message string; gives operators a typed `kubectl wait --for=condition=ProviderReady` signal. Deferred until after Epic 30 stabilises provider connectivity semantics.

---

### Epic 23 Stories 2+3 — Status update conflict handling uninstrumented
**File:** `controller/internal/metrics/metrics.go`

`LastActivityAt` has 3 writers. Deferral conditioned on observing >10 conflicts/day from `WorkspaceStatusUpdateConflictsTotal` metric — but that metric was never shipped. The deferral condition is permanently unverifiable without it.

---

### Epic 28 S28.5 — Session stream still uses legacy broker
**File:** `api/internal/handlers/proxy.go` (`StreamEvents`)

`StreamEvents` uses `h.broker.Subscribe()` (old `WorkspaceEventBroker`) rather than `h.userBroker.SubscribeWorkspace()`. Stream is stable; migration is architectural cleanup only. `SubscribeWorkspace()` is currently dead code in production.

---

## 🔵 New Epics — Planning, Not Started

---

### Epic 31 — Shared Workspace Per User (User Drive)
**Directory:** `design/stories/epic-31-shared-workspace-per-user/`

Per-user persistent shared workspace. All prerequisites (Epics 6, 9, 24) shipped. Marked High priority.

---

### Epic 32 — VPN Sidecars, VPC Connectivity & AWS IAM
**Directory:** `design/stories/epic-32-vpn-network-iam/`

Workspace pods join private VPCs via VPN sidecar; AWS IAM role binding; access to private services. All prerequisites (Epics 6, 9, 24) shipped. Marked High priority.

---

## Summary

| Priority | Count | Items |
|----------|-------|-------|
| 🔴 Live bugs | 0 | **All fixed** ✅ |
| 🟠 High value | 0 | **All addressed** ✅ |
| 🟡 Important | 7 | preferredModel, API key hashing, question E2E test, recovery metrics, disk pressure, credflow test, goroutine leak tests; + 27b.5 response-body buffering |
| 🟢 Lower urgency | 7 | Python async, contract tests, VS Code slash cmds, re-pentest, ProviderReady condition, conflict metric, session stream migration |
| 🔵 New / planning | 2 | Epic 31 (user drive), Epic 32 (VPN/IAM) |
| **Total open** | **16** | Down from 25 — 9 items closed |
