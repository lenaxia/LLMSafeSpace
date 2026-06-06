# Epic Status Report — 2026-06-05 (Verified Revision)

**Scope:** All epics assessed for completion and relevance.
**Method:** One sub-agent per epic; code evidence required for all findings; worklogs used as navigation hints only. Three false-positive gaps from the initial report have been corrected after direct code verification.

---

## Corrections from Initial Report

The following gaps reported initially are **actually complete** — verified by reading the source:

| Epic | Gap Reported | Actual State |
|------|-------------|--------------|
| 09 | US-9.4 `Seed()` not wired | `app.go:360` calls `settings.Seed()` at startup — **complete** |
| 09 | US-9.7 Tier-2 fields in Config | Two-layer design is correct; DB overrides config — **complete by design** |
| 09 | US-9.10 `WorkspaceSettingsDrawer` not mounted | Mounted in `Sidebar.tsx:381` — **complete** |
| 26 | US-26.7 Tasks A-E open | Tasks describe deleted WebSocket relay code; pivot to CF Worker made them obsolete — **N/A** |
| 24 | US-24.6 partial | `handleFailed` is fully implemented and dispatched from reconciler switch — **complete** |

---

## Executive Summary

| Category | Count | Epics |
|----------|-------|-------|
| ✅ Complete | 4 | 00, 01, 03, 22 |
| 🔶 Partial | 18 | 02, 04, 05, 06, 08, 09, 10, 13, 14, 15, 16, 17, 18, 23, 24, 25, 26, 27a, 27b, 28 |
| ❌ Not Started | 3 | 12, 29, 30 |
| ⛔ Superseded | 1 | 21 |
| 🚫 Obsolete/Redesign | 1 | 07 |

**Confirmed live bugs (highest severity):**
1. `proxy.go` B2 — streaming loop returns nil on mid-stream pod restart → HTTP 200 + corrupt JSON (confirmed: `doProxy` line ~590)
2. `local/test.sh` hard-fails — `sandbox-pw-*` secret name and `kubectl exec -c sandbox` both wrong (confirmed: lines 222, 227, 236)
3. Epic 16 US-16.6 — `session_question_reply/reject`, `session_permission_reply` NOT in `pkg/mcp/server.go` (worklog claim was false)
4. Drain mode silent no-op — `GetSSETracker()` called before `Start()` in `app.New()` (confirmed: drain always skipped in production)

**Next priority: Epic 30** (Unified Credential Model — design complete, implementation not started)

---

## Verified Findings by Epic

---

### Epic 00 ✅ Complete
No gaps. Webhooks use correct `admission.Decoder` interface. Deepcopy deleted. Build clean.

---

### Epic 01 ✅ Complete
US-1.6 deferred by design. One pre-existing debt: `runtimes/tests/test_runtime.py` asserts V1 tooling that was deleted — this Python docker test fails but is not a Go test and predates V2. All Go stories complete.

---

### Epic 02 🔶 Partial

**US-2.3 — Dead mock expectations**
- **Gap confirmed**: `router_workspace_test.go:110-113` has `.On("SetCredentials",...)` and `.On("DeleteCredentials",...)` mock registrations
- Neither method exists on the `WorkspaceService` interface in `interfaces.go`
- `.Maybe()` prevents test failures — silently ignored phantom expectations
- **Relevance: RELEVANT** — violates Rule 5 (zero technical debt); mock will drift further as interface evolves
- **Fix**: Remove the two dead mock registrations

---

### Epic 03 ✅ Complete
All three stories done with e2e tests. Minor dead code: `CreateSession`/`ListSessions` methods on `ProxyHandler` registered in tests but not in live router (session management moved to workspace service). Non-blocking but should be cleaned up.

---

### Epic 04 🔶 Partial

**US-4.1 story file — obsolete, not a development blocker**
- Story file describes `sandbox_create`/`sandbox_terminate` tools and `api/internal/mcp/` path — both wrong
- All 11 actual tools (`workspace_create` through `model_set`) are registered and tested in `pkg/mcp/`
- Story file should be updated to reflect reality, but this is documentation debt, not functional debt
- **Relevance: OBSOLETE as written** — update or archive the story file; the implementation is correct

**`resources.go` / `prompts.go` — deferred V2.1**
- Neither file exists in `pkg/mcp/`
- These were part of original MCP RFC scope, never implemented, no active story requires them
- **Relevance: OBSOLETE** — close these acceptance criteria items

---

### Epic 05 🔶 Partial

**US-5.4 Helm chart gaps (all confirmed in code):**

1. `kyverno.enabled=true` is a silent no-op — no kyverno templates exist, no warning printed
   - **Relevance: RELEVANT but deferred** — V2.1 known deferral; document this explicitly in chart README
2. README `rbac.scope` default documented as `"cluster"` but `values.yaml` defaults to `"namespace"`
   - **Relevance: RELEVANT** — will mislead operators; one-line README fix
3. NOTES.txt line 34 references `sandboxes.llmsafespace.dev` and `sandboxprofiles.llmsafespace.dev` CRDs (deleted)
   - **Relevance: RELEVANT** — post-install verification command will error; small fix
4. `chart_test.go` skip when `helm` absent — **actually correct behavior, no action needed**

---

### Epic 06 🔶 Partial

**US-6.5 — `onSessionIdle` activity tracking dead code**
- **Confirmed**: `workspaceConfig.workspaceID` field in `proxy.go:44-48` is never assigned in production code
- `onSessionIdle` guard `if ok && cfg.workspaceID != ""` is always false
- Activity recording and session index persistence via SSE idle events never fire
- Activity IS recorded via the `proxyToWorkspace` hot path (`proxy.go:496-500`) — so normal proxy calls do trigger activity
- **Relevance: RELEVANT** — SSE-triggered idle activity recording is broken; title persistence on idle also never fires (`fetchAndPersistTitle` call at line 504 is also dead)
- **Fix**: Set `workspaceID` in `wsConfig` when populating it, or simplify by calling `activityTracker.Record(workspaceID)` directly

**US-6.7 — `local/test.sh` hard-fails**
- **Confirmed**: line 222 uses `sandbox-pw-*`, lines 227/236 use `-c sandbox`
- Script aborts at password fetch with `die` before any workspace tests run
- `local/test.sh` is the primary e2e validation script
- **Relevance: RELEVANT, HIGH** — e2e testing is broken; two-line fix

**Stale comments**
- `proxy.go:504`: "sends the request to the sandbox"
- `proxy.go:1345`: "verify sandbox ownership"
- `workspace_service.go:750`: "has a Running sandbox"
- **Relevance: RELEVANT (low)** — Rule 5 violation; cosmetic but creates cognitive dissonance

---

### Epic 07 🚫 Obsolete (Redesign Needed)

**Core design is architecturally blocked:**
- `ReadOnlyRootFilesystem: true` locked in by Epic 17 regression test in `security_test.go:79-81`
- mise shim layer at `/usr/local/share/mise/shims` conflicts with PATH-wrapper approach
- Sidecar daemon cannot write to main container's read-only rootfs

**Per-story actions:**
- **US-7.1, 7.2, 7.7**: Close — architecturally incompatible, will never work as designed
- **US-7.3**: Redesign as env-var injection (`PYTHONSTARTUP`/`NODE_OPTIONS`/`PYTHONPATH`) — the V1 policy scripts (`sitecustomize.py`, `nodejs-security-wrapper.js`) are reusable; the mechanism changes
- **US-7.4, 7.5**: Scope to redesigned US-7.3 only; remove security context change from US-7.5
- **US-7.6**: Still relevant — dead fields still in `runtimeenvironment_types.go`; zero-conflict cleanup
- **US-7.8**: Still relevant — `runtimes/python/`, `nodejs/`, `go/`, `tests/` dirs still exist; zero-conflict cleanup

---

### Epic 08 🔶 Partial

**US-8.5 — `WorkspaceConditionCredentialsAvailable` never set**
- **Confirmed**: no production controller code ever calls `setCondition` with this condition type
- Only referenced in tests and type definitions
- API always returns `credentialState.reason = "NotChecked"` for every workspace
- **Relevance: DEFERRED to Epic 30** — correct implementation requires checking whether credentials exist in the new `provider_credentials` model; implementing against the old model is wrong

**US-8.9 — SSE `workspace.health` events**
- **Confirmed**: zero matches for `workspace.health` event type anywhere in Go or TypeScript
- Health state is now exposed via pull-based `GET /workspaces/:id/status` (enrichAgentStatus → statusz polling)
- Epic 28 unified event stream did not add this event type
- **Relevance: SUPERSEDED** — pull-based health via Epic 22 statusz polling adequately serves current needs; no active story requires push-based health events; close this story

---

### Epic 09 🔶 Partial

**Three previously-reported gaps are actually complete:**
- US-9.4 `Seed()` — wired at `app.go:360`; complete
- US-9.7 Tier-2 config fallbacks — correct two-layer design (config provides cold-start fallback, DB overrides at runtime); complete
- US-9.10 `WorkspaceSettingsDrawer` — mounted in `Sidebar.tsx:381`; complete

**US-9.16 — `preferredModel` not wired**
- **Confirmed**: `ModelSelector.tsx` reads only `data?.currentModel` from workspace API; no `useUserSetting("preferredModel")` call
- Schema key exists in `pkg/settings/schema.go:99` (Tier-3 user setting)
- **Relevance: RELEVANT but low priority** — small frontend hook; deferred until after Epic 30 US-30.9 (model listing rewrite), to avoid touching the same code twice

**US-9.13/14/15 — `credential_sets` system**
- Being deleted by Epic 30 US-30.1
- **Relevance: SUPERSEDED** — do not extend this code

**US-13.15 comment stub** (same comment applies to Epic 13):
- `workspace_service.go:214` comment stub is intentionally correct — Epic 30 US-30.3 fills it with `CredentialProvisioner`

---

### Epic 10 🔶 Partial

**US-10.10 Task 7 — MCP integration test for credential/model tools**
- **Confirmed**: `pkg/mcp/integration_test.go` covers only workspace/session lifecycle (6 tools); no credential or model tool test
- **Relevance: RELEVANT** — ~45-minute gap; blocks claiming US-10.10 is done per Definition of Done

**US-10.13 Part 1 — API key at-rest encryption**
- **Confirmed**: `api_keys.key` column is VARCHAR(255) plaintext; no hash migration exists
- Epic 30 README does NOT address the `api_keys` table — this is independent
- **Relevance: RELEVANT** — active security gap; DB breach exposes all API keys verbatim; independent of Epic 30

**US-10.6, US-10.7** — Virtual namespaces, S3 shared folder
- **Confirmed**: zero code exists, no active worklog activity
- **Relevance: DEFERRED** — no active demand; single-tenant deployment currently

---

### Epic 12 ❌ Not Started

No implementation. Correct to defer — needs stable LLM provider routing from Epic 30.
**US-12.12/13/14 are independent** (dependency health metrics, synthetic canary, structured request logging) and can start anytime.

---

### Epic 13 🔶 Partial

**US-13.3 — `MaxActiveSessions` CRD gap**
- **Confirmed**: `applyWorkspaceDefaults` sets SecurityLevel, StorageClassName, Resources, AutoSuspend, NetworkAccess — never `MaxActiveSessions`
- Proxy reads `workspace.Spec.MaxActiveSessions` with fallback constant of 5 (`proxy.go:423-425`)
- Admin-configured `workspace.defaultMaxActiveSessions` affects the `/sessions/active` endpoint response but never the actual enforcement
- **Relevance: RELEVANT** — a user can see "max 10 sessions" in the UI but the proxy blocks them at 5; two-line fix

**US-13.15 — autoProvision stub**
- Comment-only stub; correctly waiting for Epic 30 US-30.3
- **Relevance: CORRECTLY DEFERRED** — do not implement

**US-13.10 — `preferredModel` not wired**
- Same as Epic 09 — deferred until Epic 30

---

### Epic 14 🔶 Partial

**US-14.4 — Python async client missing**
- **Confirmed**: `client.py` is sync-only; no `AsyncLLMSafeSpace` class; no `async def` anywhere
- **Relevance: RELEVANT** — Python agent frameworks (FastAPI, asyncio pipelines, LangChain async) are async-native; sync-only blocks these use cases

**US-14.6 — Java SDK incomplete**
- **Confirmed**: 2 files only (`LLMSafeSpaceClient.java`, `LLMSafeSpaceException.java`); raw HTTP wrapper with no typed facade, no model classes, no tests
- **Relevance: LOWER PRIORITY** — no identified Java consumer in worklogs; mark stub clearly in README

**US-14.7 — Contract tests not in CI**
- **Confirmed**: 3 Hurl files exist (`auth.hurl`, `workspaces.hurl`, `errors.hurl`) but CI `sdk-contract` job runs unit tests only — no `hurl` step
- Missing: `sessions.hurl`, Prism mock server, per-SDK harnesses
- **Relevance: RELEVANT** — contract tests provide drift detection; existing Hurl files are a solid foundation

**US-14.9 — VS Code chat slash commands missing**
- **Confirmed**: `chat-participant.ts` is 57 lines of flat prompt forwarding; no `switch(request.command)`, no `commands` array in `package.json`
- **Relevance: RELEVANT** — slash commands are the primary UX differentiator for VS Code chat participants

---

### Epic 15 🔶 Partial

**US-15.6 — 6 missing backend test cases**
- **Confirmed**: 18 tests in `ChatPage.reconnect.test.tsx` (frontend); missing 6 are backend Go tests for SSE failure modes
- Frontend reconnect coverage is complete; the 6 missing tests are in `stream_user_events_test.go` and `stream_events_test.go`
- **Relevance: RELEVANT** — goroutine leak and write deadline tests protect against regressions to the zombie-connection problem this epic was written to fix

---

### Epic 16 🔶 Partial

**US-16.6 — MCP question tools not registered (worklog claim was false)**
- **Confirmed**: `pkg/mcp/server.go` has exactly 11 tools; none are `session_question_reply`, `session_question_reject`, `session_permission_reply`
- `HTTPClient` methods for these exist (`client.go:349-380`) but NOT in `APIClient` interface
- Worklog 0076 claimed these were added — **this is incorrect**
- **Relevance: HIGH** — MCP users who trigger an agent question via `session_message` receive a stalled call with no way to respond; the feature is half-functional

**US-16.2b — proxy.go not split**
- **Confirmed**: 1,405 lines; 5 hardcoded `"/session/"+sid` strings; `dialect.SessionMessagePath()` never called
- **Relevance: LOW** — pure refactor; no behavioral difference; defer until second agent runtime begins

**US-16.13 — backend E2E test absent**
- **Confirmed**: `api/internal/tests/integration/` directory does not exist
- **Relevance: RELEVANT** — only path to validate proxy↔MCP question flow end-to-end; add alongside US-16.6 fix

---

### Epic 17 🔶 Partial

**Post-remediation live re-pentest not run**
- **Confirmed**: only `phase-1-postfix/` exists; phases 2-7 have no postfix dirs
- Many fixes are code-complete but unverified against live cluster
- **Relevance: RELEVANT** — required by epic's own success criteria

**F1.7.2 — API keys plaintext (classified: OTHER agent)**
- **Confirmed**: `api_keys.key` column is VARCHAR(255) plaintext; `GetUserByAPIKey` does direct plaintext comparison
- **Relevance: HIGH** — but classified as OTHER agent's branch per MASTER-TRACKER.md

**G25 — Secret value in logging middleware (classified: OTHER agent)**
- Service/handler code does NOT log secret values (verified)
- Risk is in `api/internal/middleware/logging.go:54` (request body logging intercepts POST /secrets body)
- **Relevance: HIGH** — but classified as OTHER agent's branch

**RT-7.9 — XSS corpus**
- `rehype-sanitize` is present on both markdown render sites in `MessagePart.tsx`
- Missing: fuzz/corpus test to validate edge cases
- **Relevance: MEDIUM** — sanitization is in place; test coverage gap only

**Epic 30 threat model update needed**
- New attack surface: admin credential API, server KEK, `credential_auto_apply`, `EnsureFreeTierCredential`
- Should be written before Epic 30 US-30.1 implementation begins

---

### Epic 18 🔶 Partial

**S18.11 partial — readyz gate is done; CRD condition is not**
- **Confirmed**: `main.go:576` has `ready = snap.Initialized && snap.Healthy` (provider connectivity excluded) — primary goal complete
- `WorkspaceConditionProviderReady` constant does NOT exist anywhere in workspace types
- No controller code sets a ProviderReady condition
- **Relevance: DEFERRED** — primary S18.11 goal (reduce resume latency) is achieved; the CRD condition sub-goal may be superseded by Epic 30's credential health improvements; do not implement now

**S18.1–S18.9 (hot migration) — not started**
- Measured resume is ~17s p99, not 2 minutes
- **Relevance: LOW** — 17s→10s gain doesn't justify 40+ story points of infrastructure work until production multi-tenant load exists

**README-LLM.md factual error**
- States "resuming creates a new pod (~3s)" — actual measured p99 is 17.2s (worklog 0132)
- Should be corrected

---

### Epic 21 ⛔ Superseded

Fully superseded by Epic 24. One carryover: `WorkspaceStatusResult` never exposes recovery fields (`nextRetryAt`, `consecutiveFailures`, `safeMode`) via `GET /workspaces/:id/status`. Filed in issue #38. Can be formally closed.

---

### Epic 22 ✅ Complete

All 8 stories code-verified. No gaps.

---

### Epic 23 🔶 Partial

**Stories 1+4 complete.** Stories 2+3 deferred pending conflict-rate data.

**Key finding: deferral gate metric does not exist**
- `WorkspaceStatusUpdateConflictsTotal` is NOT in `controller/internal/metrics/metrics.go`
- This metric was supposed to be delivered as part of Story 1's "heavy instrumentation"
- Without it, the deferral condition (>10 conflicts/day) can never be evaluated
- **Relevance: RELEVANT** — shipping the metric is prerequisite for making a data-driven decision on Stories 2+3

**`LastActivityAt` multi-writer — all 3 writers confirmed**
- `activity.go:123`, `phase_suspend.go:61`, `workspace_service.go:458` all write `LastActivityAt`
- Epic 24 deployment did not address this
- **Relevance: RELEVANT** — Stories 2+3 remain valid; gated on metric

---

### Epic 24 🔶 Partial

**US-24.6 `handleFailed` — actually complete**
- Reconciler switch dispatches `WorkspacePhaseFailed` to `handleFailed` which self-heals legacy Failed workspaces
- **Complete — previously misreported as partial**

**Deferred to issue #38 (confirmed open):**

| Story | Gap | Code Confirmation |
|-------|-----|-------------------|
| US-24.11 | No recovery Prometheus metrics | `WorkspacesFailedTotal` exists but recovery-attempt/class/duration metrics absent from `metrics.go` |
| US-24.13 | Safe mode pod | `SafeMode` field + policy logic present; `buildSafeModePod` function does not exist anywhere |
| US-24.17 | Disk pressure | `WorkspaceConditionDiskPressure` constant absent; no health check logic for disk threshold |
| US-24.7 | ControllerRestartCount | Field declared in types; zero writers anywhere in controller code |

All four are confirmed open. US-24.11 (metrics) is the most impactful — the recovery system is production-deployed but unobservable.

---

### Epic 25 🔶 Partial

**All bugs confirmed in code:**

**B2 — Silent truncation (HIGH)**
- `doProxy` streaming loop (lines ~583-596): `if readErr != nil { break }; return nil`
- Any mid-stream read error (pod restart, network partition) → HTTP 200 + truncated JSON
- No error log, no retry signal to client
- Confirmed present

**G1 — No body size limit (MEDIUM-HIGH)**
- `proxy.go:457`: `io.ReadAll(c.Request.Body)` with no `LimitReader`
- Auth routes correctly use `MaxBytesReader`; proxy routes do not
- Confirmed present

**B5 — Activity tracker growth (MEDIUM)**
- `activity.go` `flushOne`: on K8s NotFound error, map entries never deleted
- Deleted workspace IDs accumulate forever; one failed K8s API call per 60s per dead workspace
- Confirmed present

**proxy.go 1,405 lines, 14 `context.TODO()` in `client_crds.go`** — both confirmed

---

### Epic 26 🔶 Partial

**Architecture pivot is complete; US-26.7 is obsolete as written**

The CF Worker architecture is deployed correctly:
- `workers/inference-relay/src/index.ts` — 37-line transparent proxy to `opencode.ai/zen/v1`
- Controller injects `OPENCODE_AUTH_CONTENT` with `metadata.baseURL` at pod creation via `buildOpenCodeAuthContent()`
- `charts/llmsafespace/values.yaml:612`: `inferenceRelayURL: "https://relay.safespaces.dev"`

**US-26.7 Tasks A-E describe the deleted WebSocket relay** — no relay_proxy.go, relay_handler.go, or useRelayClient.ts exists. The README was not updated after the pivot.

**Real remaining gap:**
- The CF Worker is deployed to Cloudflare; no worklog confirms a cluster `helm upgrade` that activated `inferenceRelayURL` for new pods
- Existing pods created before the pivot have the old `OPENCODE_AUTH_CONTENT` without `metadata.baseURL`
- **Relevance: MEDIUM** — needs deployment confirmation, not code changes

**Minor:** stale comment in `models.go:220` ("client-side relay") — one-line fix

---

### Epic 27a 🔶 Partial

**Drain injection gap (HIGH) — confirmed still broken**
- `app.go` drain wiring block runs in `New()` (construction)
- `proxyHandler.GetSSETracker()` returns `h.sseTracker`, which is set inside `Start()` (called later in `Run()`)
- At wiring time, `GetSSETracker()` always returns nil; `SetSSETracker` is never called
- `agent_reload.go:139`: `drain && h.sseTracker != nil` is always false
- `?drain=true` is silently treated as a non-drain reload
- **Fix**: Wire drain dependencies inside `Run()` after `proxyHandler.Start()`, or restructure `GetSSETracker()` to use lazy initialization

**US-27a.9 — credflow integration test missing**
- `agent_reload_e2e_test.go` covers handler isolation only (mocked DB, mocked agentd)
- Full bind→`agentNeedsRefresh:true`→reload→`agentNeedsRefresh:false` scenario not tested
- `tests/integration/` directory does not exist
- **Relevance: RELEVANT** — required by Definition of Done; regression risk as Epic 30 changes credential model

**US-27a.8 list-view banner**
- `AgentReloadBanner` used only in `ChatPage.tsx` (detail view only)
- No consolidated list-level banner showing all workspaces with pending reload
- **Relevance: LOW** — detail view satisfies the primary use case

---

### Epic 27b 🔶 Partial

**US-27b.3 drain — same root cause as 27a**
- `agent_reload.go:139` guard always false because SSETracker is nil
- **Fix is shared with 27a**: wire SSETracker after `Start()`

**US-27b.4 — serial bulk reload**
- **Confirmed**: `BulkReload` iterates `pending` workspaces in a serial for-loop; no goroutine, no semaphore
- 10 workspaces × 15s timeout = potentially 150s total
- **Relevance: MEDIUM, but defer until after Epic 30** — Epic 30's credential model change may require rethinking what "reload" means and which workspaces need it; implementing parallelism now against a model that's about to change is low-value

**US-27b.5 — enrichment not wired**
- **Confirmed**: `EnrichChatErrorBody` pure function exists and is tested; `chatErrorEnrichmentWriter` struct does not exist; `proxy.go` `SendMessage` has no enrichment wrapper
- **Relevance: LOW, defer until after Epic 30** — enrichment is tied to `agentNeedsRefresh` state; Epic 30 may change how this state is tracked

**US-27b.8 — pending-workspaces gauge missing**
- 4 reload metrics exist (counters + histogram); no gauge for pending count
- **Relevance: LOW, defer until after Epic 30**

---

### Epic 28 🔶 Partial

**S28.5 — `StreamEvents` still uses legacy broker**
- **Confirmed**: `StreamEvents` calls `h.broker.Subscribe(workspaceID)` (legacy `WorkspaceEventBroker`)
- `SubscribeWorkspace()` on the new `UserEventBroker` is never called in production
- Session stream is stable in practice — no crashes reported
- The FP1 panic risk (send to closed channel) is theoretical; the legacy broker uses capacity-checked select, not raw send
- **Relevance: LOW** — architectural cleanup, not an active bug; migrate when the session stream needs to be extended for another reason

**S28.8 — 3 specific missing tests**
- **Confirmed**: 10 tests present; missing: goroutine leak, write deadline expiry, k8s list failure
- **Relevance: MEDIUM** — goroutine leak and write deadline tests are the only automated proof that the zombie-connection fix works

---

### Epics 29, 30 — Not Started

**Epic 29**: Defer until after Epic 30 (direct code overlap). US-29.4 (WorkspaceEnvHandler) and US-29.7 (Basic auth contract test) can be pulled forward independently.

**Epic 30**: Next priority. Design complete. Unblocks: Epic 27b utility, US-13.15 auto-provision, user LLM provider UI, free-tier key management. Gate before US-30.4: verify `ConfigProviderPlugin` `apiKey:"public"` behavior matches `AccountPlugin`.

---

## Priority Action List

### Do now (small, independent, confirmed live issues)

| # | Item | Epic | Effort | Severity |
|---|------|-------|--------|----------|
| 1 | Fix `local/test.sh` lines 222/227/236 (`sandbox-pw-*` → `workspace-pw-*`, `-c sandbox` → `-c workspace`) | 06 | 5 min | High — e2e testing broken |
| 2 | Fix drain injection: wire SSETracker/passwordGetter inside `Run()` after `proxyHandler.Start()` | 27a/27b | 30 min | High — drain is silent no-op |
| 3 | Fix `proxy.go` B2: distinguish `io.EOF` from network errors in streaming loop | 25 | 2h | High — corrupt JSON on pod restart |
| 4 | Fix US-16.6: add `session_question_reply/reject`, `session_permission_reply` to `APIClient` interface + register in `server.go` | 16 | 3h | High — MCP question flow broken |
| 5 | Fix US-13.3: two lines in `applyWorkspaceDefaults` to set `crd.Spec.MaxActiveSessions` from settings | 13 | 30 min | Medium — admin setting silently ignored |
| 6 | Fix `proxy.go` G1: wrap `c.Request.Body` with `io.LimitReader` before `io.ReadAll` | 25 | 30 min | Medium-High — OOM risk |
| 7 | Fix US-6.5: set `workspaceConfig.workspaceID` when populating wsConfig, or call `activityTracker.Record(workspaceID)` directly in `onSessionIdle` | 06 | 1h | Medium — idle activity tracking dead |
| 8 | Ship `WorkspaceStatusUpdateConflictsTotal` metric for Epic 23 Story 1 | 23 | 1h | Medium — deferral gate unverifiable |

### Do before or with Epic 30

| # | Item | Epic | Note |
|---|------|-------|------|
| 9 | Write Epic 17 threat model addendum for Epic 30 attack surface | 17 | Gate for US-30.1 |
| 10 | Verify `ConfigProviderPlugin` free-tier catalog behavior | 30 | Gate for US-30.4 |
| 11 | Fix US-6.7 stale comments (`// sandbox` in proxy.go, workspace_service.go) | 06 | Low, Rule 5 |
| 12 | Fix Epic 05 NOTES.txt stale sandbox CRD references + README rbac.scope contradiction | 05 | Low, misleads operators |

### Do after Epic 30

| # | Item | Epic | Note |
|---|------|-------|------|
| 13 | Fix US-8.5 `CredentialsAvailable` condition — set from new credential pipeline | 08 | Requires Epic 30 injection result |
| 14 | Wire US-9.16 `preferredModel` into ModelSelector | 09 | After Epic 30 US-30.9 model rewrite |
| 15 | Wire US-27b.5 chat-proxy error enrichment; add bulk reload parallelism | 27b | Model change affects reload semantics |
| 16 | Add Epic 24 US-24.11 Prometheus metrics (recovery_attempts, failures_by_class) | 24 | High value, unobservable recovery system |
| 17 | Implement Epic 24 US-24.17 disk pressure condition | 24 | Data already collected, easy |
| 18 | Migrate S28.5 `StreamEvents` to `SubscribeWorkspace` | 28 | Architectural cleanup |
| 19 | Add S28.8 goroutine leak + write deadline tests | 28 | Regression protection |
| 20 | Write US-27a.9 full credflow integration test | 27a | Required by Definition of Done |

### Lower priority / deferred

| Item | Epic | Note |
|------|-------|------|
| US-7.6 + US-7.8 (RuntimeEnvironment cleanup + delete legacy runtimes dirs) | 07 | Zero conflict, zero risk |
| US-14.4 (Python async client) | 14 | Relevant for async frameworks |
| US-14.9 (VS Code slash commands) | 14 | VS Code UX gap |
| US-10.13 Part 1 (API key encryption) | 10 | Security gap, independent of Epic 30 |
| US-10.10 Task 7 (MCP credential/model integration test) | 10 | ~45-min gap |
| Epic 17 phase-2..7 postfix re-run | 17 | Operationally required |
| US-23.3 single-writer migration (after metric data) | 23 | Data-driven decision |
| US-24.13 safe mode pod (after issue #38 prioritization) | 24 | Behavioral inconsistency |
| Epic 12 US-12.12/13/14 (independent infra stories) | 12 | No prerequisites |
| B5 activity tracker growth fix | 25 | Medium severity leak |
| US-25.13 context.TODO() cleanup | 25 | 14 occurrences in client_crds.go |

---

## Design Questions for Owner

The following items require a design decision before work can proceed:

1. **Epic 07**: Should the language runtime wrapper (US-7.3) be redesigned as env-var injection (`PYTHONSTARTUP`, `NODE_OPTIONS`) using the existing V1 policy scripts, or is runtime policy enforcement no longer a priority at all? The current architecture (`ReadOnlyRootFilesystem: true`, mise shims) makes the original PATH-wrapper approach impossible without significant reversals.

2. **Epic 08 US-8.9**: Should `workspace.health` SSE push events be built, or is the current pull-based model (poll `GET /workspaces/:id/status` → `agentHealth` field) sufficient? The infrastructure for push (Epic 28 UserEventBroker) is ready, but no frontend currently consumes health events.

3. **Epic 18 S18.11**: After Epic 30 ships (which fixes credential injection reliability), will the `WorkspaceConditionProviderReady` CRD condition still be needed? Or will Epic 30's credential model make provider connectivity state deterministic (eliminating the need to surface it separately)?

4. **Epic 26 US-26.7**: US-26.7 in the README still describes Tasks A-E targeting the deleted WebSocket relay code. These are all obsolete. Should the story be updated to reflect post-pivot status, or does the pivot introduce new gaps that need their own story (e.g., validating that `relay.safespaces.dev` is actually being used by pods in production)?

5. **Epic 28 S28.5**: Should `StreamEvents` (session stream) be migrated from the legacy `WorkspaceEventBroker` to `UserEventBroker.SubscribeWorkspace()`? The session stream is stable today; the migration is architectural cleanup. Is it worth doing before other higher-priority work?
