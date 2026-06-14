# Implementation Stories

Organized by epic, following the V2 design roadmap (design/EVOLUTION-V2.md v2.4).

**Last audited:** 2026-06-14 (code-verified; worklogs used as navigation hints, not source of truth)

---

## Status Legend

| Symbol | Meaning |
|--------|---------|
| ✅ Complete | All stories done, e2e tests pass, wired into live path |
| 🔶 Partial | Most stories done; specific verified gaps remain — see notes |
| ❌ Not Started | No implementation exists |
| ⛔ Superseded | Replaced by a later epic or architectural decision |
| 🚫 Obsolete | Original design conflicts with current architecture; close or redesign |
| 🔁 Deferred | Explicitly out of scope for current phase; tracked in issue #38 or V2.1 list |

---

## V1 Scope (Weeks 1-9)

| Epic | Goal | Status | Verified Gaps |
|------|------|--------|---------------|
| 00 | Unbreak: fix deepcopy generation, webhook decoders | ✅ Complete | None |
| 01 | Fix compile errors, remove warm pools, add security tools | ✅ Complete | `runtimes/tests/test_runtime.py` stale V1 Python test (non-Go, pre-existing debt); US-1.6 deferred by design |
| 02 | Workspace CRD, PVC persistence, suspend/resume | 🔶 Partial | Dead mock expectations in `router_workspace_test.go:110-113` for `SetCredentials`/`DeleteCredentials` — methods don't exist on the interface; `.Maybe()` prevents test failures but the mock is stale tech debt |
| 03 | Proxy to opencode, session endpoints | ✅ Complete | None |
| 04 | MCP server for external LLM tools | 🔶 Partial | US-4.1 story file describes old sandbox-centric architecture (never updated); `resources.go`/`prompts.go` not implemented (deferred V2.1, story spec is obsolete); actual 11-tool implementation is complete and tested |
| 05 | Helm chart | 🔶 Partial | US-5.1/5.2/5.3 deferred by design. US-5.4 gaps: `kyverno.enabled=true` is a silent no-op (no templates); README documents `rbac.scope` default as `"cluster"` but `values.yaml` defaults to `"namespace"`; NOTES.txt references deleted sandbox/sandboxprofile CRDs |

---

## V2 Scope (Post-Foundation)

| Epic | Goal | Status | Verified Gaps |
|------|------|--------|---------------|
| 06 | Collapse Sandbox into Workspace | 🔶 Partial | **US-6.5**: `workspaceConfig.workspaceID` field in proxy.go is never set in production code; `onSessionIdle` activity/sessionIndex recording branch is dead code. **US-6.7**: `local/test.sh` hard-fails: line 222 uses `sandbox-pw-*` secret name (should be `workspace-pw-*`), lines 227/236 use `kubectl exec -c sandbox` (container renamed to `workspace`). Minor: stale `// sandbox` comments in `proxy.go` and `workspace_service.go`. |
| 07 | Runtime Interception Layer — system daemon, PATH wrappers, RuntimePolicy CRD | 🚫 Partially Closed | US-7.1/7.2/7.4/7.7 **closed** (sidecar/wrapper approach incompatible with `ReadOnlyRootFilesystem: true` and mise shims — issue #40). US-7.3 **redesigned** as env-var injection (`PYTHONSTARTUP`, `NODE_OPTIONS`) — issue #41. US-7.5 partially deferred. US-7.6/7.8 open (actionable cleanup — issue #42). Future runtime enforcement will be via purpose-built Dockerfiles with baked-in wrapper scripts. |
| 08 | Credential Health & Agent Abstraction | 🔶 Partial | **US-8.5**: `WorkspaceConditionCredentialsAvailable` name is misleading — "no credentials" is valid (means free-tier opencode); the real signal is "providers connected". Defer to Epic 30 which will correctly set a provider-connectivity condition as part of the new injection pipeline. **US-8.9 superseded and generalized**: narrow `workspace.health` SSE story closed; replaced by broader push notification system (issue #43). US-8.0/8.4/8.10 superseded by Epic 10. |
| 09 | Configuration & Settings | 🔶 Partial | **US-9.16 partial**: `preferredModel` schema key exists in Tier-3 user settings; `ModelSelector` component reads only workspace's current model from API — user preference never wired as default selection. Deferred until after Epic 30 US-30.9. Verification correction: US-9.4 (`Seed()`) is **complete** — wired at `app.go:360`; US-9.7 (Tier-2 config fields) is **complete** — correct design (DB overrides config at runtime); US-9.10 (`WorkspaceSettingsDrawer`) is **complete** — mounted in `Sidebar.tsx:381`. US-9.13/14/15 (`credential_sets`) superseded by Epic 30. |
| 10 | Multi-Tenant Trust & Secret Management | 🔶 Partial | **US-10.10 Task 7**: MCP integration test for credential/model tools missing (basic lifecycle test exists, no credential/model coverage). **US-10.13 Part 1**: API keys stored plaintext in `api_keys.key` column; no `key_hash`/`key_ciphertext` migration; independent of Epic 30 (Epic 30 doesn't address `api_keys` table). US-10.6 (virtual namespaces) and US-10.7 (S3 shared folder) not started, no active roadmap entry. |
| 12 | Usage Metering & Billing | 🔶 Partial | Metering infrastructure built (`metering.Service` 939 lines, async batch writer, DLQ, quota enforcement). Tables: `usage_events`, `usage_limits`, `billing_accounts`, `billing_export_cursor`, `workspace_lifecycle_events` (migrations 024-028). Usage API endpoints registered (`/usage`, `/usage/quota`, `/admin/usage/:ownerId`). `BillingProvider` interface + `NoopBillingProvider` shipped. **Gaps**: no real billing provider (Stripe not implemented); no usage-based pricing calculation; `users.plan_id` column exists but no plan enforcement; webhook handler is a stub. US-12.13 (canary) and US-12.14 (logging) complete. |
| 13 | Settings Enforcement | 🔶 Partial | **US-13.3 gap**: `applyWorkspaceDefaults` never sets `crd.Spec.MaxActiveSessions`; proxy uses hardcoded fallback of 5 — admin-configured session cap is silently ignored. Two-line fix. **US-13.15 complete**: Epic 30 delivered `CredentialProvisioner` at `workspace_service.go:271-276` — the former comment stub is now live auto-provisioning via `SeedWorkspaceCredentials`. **US-13.10**: `ModelSelector` does not read `preferredModel` user setting. |
| 14 | Multi-Language SDKs & VS Code Extension | 🔶 Partial | **US-14.4**: No `AsyncLLMSafeSpace` class in Python SDK (sync-only). **US-14.6**: Java SDK is raw HTTP wrapper only — no typed facade, model classes, or tests. **US-14.7**: 3 Hurl files exist but are not executed in CI; no sessions/pagination coverage. **US-14.9**: VS Code chat participant has no slash commands (`/new-session`, `/switch-workspace`, `/history`, `/status`) — no `switch(request.command)` dispatch, no `commands` array in `package.json`. |
| 15 | Streaming State Resilience & Mid-Stream Reconnect | 🔶 Partial | Functionally complete (all 5 implementation stories done). **US-15.6**: 18/24 specified tests present; 6 missing are backend Go tests for SSE failure modes (goroutine leak, write deadline, k8s list failure, gap+replay+resync). Frontend reconnect tests are complete. |
| 16 | Agent Input Requests | 🔶 Partial | **US-16.6**: `session_question_reply`, `session_question_reject`, `session_permission_reply` tools NOT registered in `pkg/mcp/server.go` — worklog claim was false; `HTTPClient` methods exist but not in `APIClient` interface; MCP clients cannot respond to agent questions. **US-16.2b**: proxy.go still 1,405 lines; 5 hardcoded `"/session/"+sid` strings; pure refactor, low urgency. **US-16.13**: `api/internal/tests/integration/` does not exist; backend E2E absent. |
| 17 | Security Review & Penetration Testing | 🔶 Partial | Pentest + ~46 code fixes complete. Open: post-remediation live re-pentest not run (no `phase-{2..7}-postfix` dirs); **F1.7.2** (API keys plaintext) and **G25** (secret in logging middleware) are both HIGH severity and classified as OTHER agent's branch; **RT-7.9** (XSS corpus) — `rehype-sanitize` is present, test corpus unwritten. Epic 30 threat model addendum complete (`THREAT-MODEL-ADDENDUM-EPIC30.md`). |
| 18 | Hot Migration — zero-downtime pod replacement | 🔶 Partial | **S18.10** complete. **S18.11**: readyz gate decoupled ✅ (primary goal done); `WorkspaceConditionProviderReady` condition — Epic 30 is complete (credential injection pipeline stabilized); can now add condition + narrow the regex-parse in `agentHealthFromConditions`. Current `AgentHealthy` condition already surfaces provider issues via `HealthBanner`. S18.1–S18.9 not started — measured resume is ~17s p99, not 2min; low urgency. |
| 21 | ~~Workspace Recovery State Machine~~ | ⛔ Superseded | Fully superseded by Epic 24. One carryover gap: `WorkspaceStatusResult` never exposes `nextRetryAt`/`consecutiveFailures`/`safeMode` via API status endpoint. Filed in issue #38. Can be formally closed. |
| 22 | agentd Health-Endpoint Redesign | ✅ Complete | All 8 stories code-verified. |
| 23 | Controller Race Hardening | 🔶 Partial | Stories 1+4 complete. Stories 2+3 deferred — gating metric `WorkspaceStatusUpdateConflictsTotal` **does not exist** (was supposed to be delivered in Story 1); without it the deferral condition (>10 conflicts/day) is unverifiable. `LastActivityAt` still has 3 writers. |
| 24 | Self-Healing Workspace Lifecycle | 🔶 Partial | Core recovery engine complete; US-24.6 `handleFailed` is **complete**. Deferred to issue #38: **US-24.11** no recovery Prometheus metrics (recovery system unobservable); **US-24.13** `buildSafeModePod` doesn't exist (`SafeMode` flag set but normal pod still built); **US-24.17** `WorkspaceConditionDiskPressure` constant absent and no health check logic; **US-24.7** `ControllerRestartCount` field declared but never written. |
| 25 | API Server Robustness & Correctness | 🔶 Partial | G3 (SSE write deadline) fixed. Live bugs confirmed in code: **B2** streaming loop silently `break`s on read error, returns nil (HTTP 200 with corrupt JSON on pod restart); **G1** `io.ReadAll` at line 457 with no `LimitReader`; **B5** activity tracker map entries never deleted on NotFound (unbounded growth). proxy.go is 1,405 lines. 14 `context.TODO()` in `client_crds.go`. |
| 26 | Client-Proxied Inference | 🔶 Partial | CF Worker deployed (`relay.safespaces.dev`) and architecturally correct. **US-26.7 superseded**: all Tasks A-E described the deleted WebSocket relay — marked superseded in epic README. One remaining gap: confirm `helm upgrade` with `inferenceRelayURL` was applied to live cluster (no worklog confirms routing post-pivot). Minor: stale comment in `models.go:220`. |
| 27a | Credential Reload Foundation | 🔶 Partial | Core foundation complete. **US-27a.9**: full credflow e2e test missing — handler-level tests exist but the bind→`agentNeedsRefresh:true`→reload→`agentNeedsRefresh:false` path is untested. **Drain injection gap**: `proxyHandler.GetSSETracker()` is called in `app.New()` before `proxyHandler.Start()` — SSETracker is always nil at wiring time; drain mode is a silent no-op in production. |
| 27b | Credential Reload Polish | 🔶 Partial | **US-27b.3**: drain is silently skipped (same root cause as 27a gap). **US-27b.4**: BulkReload is serial for-loop (no parallelism). **US-27b.5**: `EnrichChatErrorBody` built and unit-tested but never wired into proxy routes. Epic 30 is complete — these items can proceed (credential model is now stable). |
| 28 | Unified Event Stream | 🔶 Partial | Backend complete and integrated. **S28.5**: `StreamEvents` still uses legacy `WorkspaceEventBroker.Subscribe()` — `SubscribeWorkspace()` never called; session stream is stable in practice but the migration was not completed. **S28.8**: 10 tests present; missing: goroutine leak test, write deadline expiry test, k8s list failure test. |
| 29 | Handler Decomposition & Agent Client Abstraction | ❌ Not Started | Epic 30 is complete (`secrets.go` and `models.go` credential targets are stable). Can proceed. US-29.4 (WorkspaceEnvHandler) and US-29.7 (Basic auth contract test) are safe to pull forward. |
| 30 | Unified Credential Model | ✅ Complete | All 14 stories (US-30.1–30.14) implemented, merged in PR #39, deployed as Helm rev 159, live-validated 2026-06-07 (worklog 0180). `provider_credentials` with `owner_type='user'\|'admin'\|'org'`; `CredentialProvisioner` wired at `workspace_service.go:271-276`; `decryptBinding` handles all three owner types (`injection.go:135-170`); `SeedWorkspaceCredentials` seeds in priority order (`pg_credential_store.go:81-138`). Epic 11 (Organizations) built on top of this in PR #137. Admin + User LLM Provider UIs built (`AdminProviderCredentialsTab.tsx` 631 lines). |
| 34 | Session Security — remember-me (30-day JWT + cookie), enforce `LLMSAFESPACE_MASTER_SECRET` at startup | ❌ Not Started | None |
| 35 | Secretless Credential Injection — eliminate `workspace-secrets-<id>` K8s Secret; init container self-fetches credentials from API server via projected SA token + TokenReview | ❌ Not Started | None |
| 37 | Session Activity & Unread State UX — activity spinners across workspaces, unread pulsation, "new messages" divider, persisted across refreshes | ❌ Not Started | Epics 15, 28 |
| 41 | Message Queue Reliability — fix streaming state clear timing, add 409 guard for in-flight sessions, restore dead `onSessionIdle` activity recording | ❌ Not Started | Epics 15, 28, 38 |
| 43 | Organization Management & Multi-Tenant Product — org admin portal, email invitations, SSO, policy engine, billing tiers | ❌ Not Started | Epic 11 (complete), Epic 12 (metering infra built), Epic 30 (complete) |

---

## V2.2 (In Planning)

| Epic | Goal | Depends On |
|------|------|------------|
| 32 | VPN Sidecars (WireGuard, Tailscale, ZeroTier), VPC Connectivity, & AWS IAM (IRSA + Pod Identity) — admin-gated per-workspace network attachment | Epics 6, 9, 24 |
| 31 | **Shared Workspace Per User (User Drive)** — per-user PVC/S3 drive mounted at `/shared` in every workspace, 5 GB default quota, resize for billing upgrades, frontend capacity bar in status area | Epics 6, 9, 24 |

## V2.1 (Deferred)

| Story | Reason |
|-------|--------|
| US-1.6: Injection detection | Not on critical path |
| US-5.1: PATH-shadowing wrappers | Superseded — mise handles runtime management; `ReadOnlyRootFilesystem: true` blocks binary relocation |
| US-5.2: Hardened Dockerfile | Only needed for high-security mode |
| US-5.3: Kyverno policies | Pod security contexts cover V1 |
| US-7.1/7.2: System daemon + package manager wrappers | **Closed** — architecture incompatible; future enforcement via Dockerfile baked-in scripts (issue #40) |
| US-7.3: Language runtime wrappers | **Redesigned** as env-var injection using existing V1 policy scripts; see issue #41 |
| US-7.4: RuntimePolicy CRD | **Closed** — no consuming implementation; revisit with US-7.3 redesign |
| US-10.6: Virtual namespace tenant isolation | Infrastructure complexity; single-tenant deployment currently |
| US-10.7: S3 shared folder | No active demand |
| Epic 12: Usage Metering & Billing (Stripe/provider integration) | Metering infrastructure built; real billing provider (Stripe) pending provider selection. US-12.12/13/14 are independent and can start sooner |
| Epic 18 S18.1–S18.9: Hot migration | Resume measured at ~17s p99 (not 2min); low urgency until production multi-tenant load |
| US-24.13: Safe mode fallback pod | Deferred issue #38 |
| US-24.14: Image pinning | Deferred issue #38 |
| US-24.16: File download endpoint | Deferred issue #38 (blocked on US-24.13) |
| WebSocket↔SSE bridge | SSE sufficient for browsers |
| MCP file upload/download tools | Agent can handle through its own tools |
| Session-level credential override | Workspace-level credentials sufficient |
| High-security mode | Standard security sufficient for V1 |

---

## Recommended Implementation Order

```
Epic 30 (Unified Credential Model)           ← ✅ COMPLETE (PR #39, deployed Helm rev 159)

Next priorities:
  ├─ Epic 43 (Organization Management)        ← design phase, all deps met (Epics 11, 12, 30 complete)
  ├─ Epic 12 completions (Stripe provider, plan gating, usage pricing)
  ├─ Epic 27b completions (bulk parallelism, enrichment wiring)  ← Epic 30 unblocked this
  ├─ Epic 08 US-8.5 (CredentialsAvailable condition)             ← Epic 30 unblocked this
  ├─ Epic 09 US-9.16 (preferredModel wiring)
  ├─ Epic 18 S18.11 (ProviderReady condition)                    ← Epic 30 unblocked this
  ├─ Epic 29 (Handler Decomposition)                             ← Epic 30 unblocked this
  ├─ Epic 24 US-24.11 (Prometheus metrics)    ← recovery system is unobservable
  ├─ Epic 24 US-24.17 (disk pressure)
  └─ Epic 28 S28.5 (session stream migration) + S28.8 tests

Fix now (small, independent, live bugs):
  ├─ Epic 25 B2 (proxy truncation)            ← HTTP 200 + corrupt JSON on pod restart
  ├─ Epic 25 G1 (body size limit)             ← io.ReadAll with no LimitReader
  ├─ Epic 27a drain injection gap             ← 3-line fix; drain is silent no-op
  ├─ Epic 06 US-6.7 (local/test.sh)          ← 2-line fix; e2e test hard-fails
  ├─ Epic 16 US-16.6 (MCP question tools)    ← small; MCP question flow is broken
  ├─ Epic 13 US-13.3 (MaxActiveSessions CRD) ← 2-line fix; proxy ignores admin setting
  ├─ Epic 34 (session security)              ← ~6h; independent; closes plaintext-DEK risk
  └─ Epic 35 (secretless credential injection) ← ~16h; independent; eliminates workspace-secrets-<id> from etcd

Lower priority / ongoing:
  ├─ Epic 07 US-7.6 + US-7.8 (cleanup only)
  ├─ Epic 17 phase-N-postfix re-run + issue #38 items
  ├─ Epic 14 US-14.4 (async Python), US-14.9 (slash cmds)
  ├─ Epic 23 Stories 2+3 (need WorkspaceStatusUpdateConflictsTotal metric first)
  └─ Epic 12 US-12.12/13/14 (independent metrics/canary stories)
```

---

## Story Dependency Graph

```
US-0.1 (deepcopy) ──┐
US-0.2 (webhooks) ──┼── US-1.1 (API) ──┐
                      │                   ├─ US-1.3 (remove warm pools) ──┐
                      └── US-1.2 (ctrl) ─┘                                │
                                                                        ▼
US-1.5 (redact) ────── US-1.7 (entrypoints) ── US-1.8 (Dockerfile)     │
                                                                         │
                                               US-2.1 (Workspace CRD) ──┤
                                               US-2.2 (Workspace rec.)  │
                                               US-2.3 (Workspace API) ──┤
                                               US-2.4 (Sandbox update) ──┤
                                               US-2.5 (DB migration) ────┤
                                                                         ▼
                                                     US-3.1 (proxy) ────┤
                                                     US-3.2 (routes)    │
                                                     US-3.3 (activity)  │
                                                                         ▼
                                                     US-4.1 (MCP) ──────┤
                                                                         ▼
                                                     US-5.4 (Helm) ──────┘
```
