# Open Work Report — 2026-06-15

Refresh of `0170_2026-06-05_open-work-report.md`. Items re-verified against current
codebase; resolved items removed, new Epic 42/43 work added.

Verification method: grep/glob for the specific symbols, files, and migrations each
item references. Every entry below was confirmed open on 2026-06-15 unless marked
otherwise.

---

## Resolved since the 2026-06-05 report (removed from open list)

| Item | Evidence |
|------|----------|
| Epic 10 US-10.13 — API auth tokens plaintext | `api/migrations/000017_api_key_hashing.up.sql` ships `key_hash` + `key_prefix`; raw key dropped |
| Epic 24 US-24.11 — recovery metrics absent | `controller/internal/metrics/metrics.go:29` `WorkspaceRecoveryAttemptsTotal` + success counter, wired in `metrics_wiring.go:81` |
| Epic 28 S28.8 — goroutine leak + write-deadline tests absent | `api/internal/handlers/stream_user_events_test.go:477` `TestStreamUserEvents_GoroutineExitsOnClientDisconnect`, `:520` `TestStreamUserEvents_WriteErrorCancelsStream` (real `httptest.Server`, broker subscriber-count polling) |

---

## 🔴 Launch Blocker — Multi-Tenant Product

These block a billable multi-tenant launch. Epic 43 Phase 4 is the critical path per
DECISIONS.md D12 (metered billing required at launch).

### Epic 43 Phase 4 — Billing Integration
**Directory:** `design/stories/epic-43-organization-management/README.md` (Phase 4)

| Story | Title | Effort | Status |
|-------|-------|--------|--------|
| US-43.14 | Stripe integration (Checkout, Customer Portal, webhook, 7-day grace — D14) | 8h | Not started |
| US-43.15 | Plan tiers (individual flat + usage; org per-seat + usage), feature gating, configurable trials (D13) | 8h | Not started |
| US-43.16 | Billing UI (plan status, usage, "Manage in Stripe" redirect) | 2h | Not started |
| US-43.17 | Usage-based pricing via Stripe Metered (`llm_tokens` + `compute_seconds` via Epic 12 `BillingExporter`) | 8h | **Critical path (D12)** |

Depends on US-43.1 (done). Webhook handler replaces the current `POST /api/v1/webhooks/billing` 20-line stub.

### Epic 43 Phase 5 — Platform Operations
| Story | Title | Effort | Status |
|-------|-------|--------|--------|
| US-43.18 | Platform admin dashboard (org list, user list, suspend/unsuspend) | 10h | Not started |
| US-43.19 | Org + user suspension (hard ops: kill pods, preserve PVCs, controller-query based, last-admin deadlock prevention — D19/D20) | 10h | Not started |
| US-43.20 | Cross-org audit view | 4h | Not started |

---

## 🟠 In Flight — Merge Pending

### Epic 43 US-43.10 — OIDC SSO
**Branch:** `feat/epic43-us43.10-oidc-sso` (4 commits ahead of main)
**Latest commit:** `c1900cc9 chore: trigger CI re-run for PKCE fix review`

Design per Phase 3 + DECISIONS.md D17 (auto-provision, domain mapping with DNS
verification, group claim → role mapping, server KEK for client secret storage).
Awaiting review approval + merge to main. Phase 3's other story, US-43.13 audit log,
is already merged (PR #174).

---

## 🟡 Important — Near-Term, Clear Value

### Epic 24 US-24.17 — DiskPressure condition not implemented
**Files:** `pkg/apis/llmsafespace/v1/workspace_types.go`, `controller/internal/workspace/health.go`

`DiskUsedBytes` / `DiskTotalBytes` are collected from the agent into `WorkspaceStatus`.
The `WorkspaceConditionDiskPressure` constant is still commented out
(`design/stories/epic-24-.../US-24.12-crd-schema-update.md:51`); the threshold check,
condition-set/remove logic, and frontend yellow banner are all absent. Design is
complete in `US-24.17-degraded-detection.md` — implementation is the remaining work.
Low effort, clear value (users currently get no signal before writes start failing).

### Epic 09 US-9.16 — `preferredModel` not wired into ModelSelector
**File:** `frontend/src/components/chat/ModelSelector.tsx`

The `preferredModel` Tier-3 user setting exists in `pkg/settings/schema.go`.
`ModelSelector` reads only the per-workspace current model — never seeds from user
preference when a workspace opens. Verified: no `preferredModel` reference in the chat
components. Thin frontend hook change.

### Epic 16 US-16.13 — No backend integration test for the question flow
**Directory:** `api/internal/tests/integration/` (does not exist)

Full question flow (proxy detects agent question in SSE → user replies → session
resumes) has handler-level tests but no end-to-end DB-backed test. Required by
README-LLM.md §0 (Definition of Done: passing e2e/integration tests).

### Epic 27a US-27a.9 — Credflow integration test missing
**File:** `api/internal/handlers/agent_reload_e2e_test.go` (partial)

Bind credential → `agentNeedsRefresh: true` → reload → `false` has handler tests but
no end-to-end DB-backed test. Epic 30 rewrote `PrepareSecretsForInjection` — primary
regression risk.

### Epic 27b US-27b.5 — Chat error enrichment body buffering deferred
**File:** `api/internal/handlers/proxy.go` (`SendMessage`)

`EnrichChatErrorBody` built and tested; `SetAgentStateChecker` wired; DB query runs on
4xx. The actual response-body rewrite is deferred — gin's streaming `ResponseWriter`
doesn't expose `Body() []byte`. Proper fix: buffer response in `doProxy` before
writing, then conditionally transform. Currently the hint is server-side only; clients
must poll `GET /workspaces/:id/status`.

---

## 🟢 Meaningful — Lower Urgency

### Epic 14 US-14.4 — Python SDK is sync-only
**File:** `sdks/python/llmsafespace/client.py`

No `AsyncLLMSafeSpace`. Python agent frameworks (FastAPI, LangChain async) are
async-native; sync client blocks their event loop.

### Epic 14 US-14.7 — Contract tests not executed in CI
**Directory:** `sdks/tests/contract/` (does not exist)

3 Hurl files referenced in the old report are no longer present. Missing:
sessions/pagination coverage, Prism mock server, Java step, CI wiring.

### Epic 14 US-14.9 — VS Code chat slash commands absent
**File:** `sdks/vscode-llmsafespace/src/providers/chat-participant.ts`

Tree-view commands exist (`workspace-commands.ts`: createWorkspace, suspend, resume,
activate, terminate). The chat **participant** slash commands (`/new-session`,
`/switch-workspace`, `/history`, `/status`) are still absent — no matches in
`chat-participant.ts`. Slash commands are the primary UX discoverability feature of a
VS Code chat participant.

### Epic 17 — Live re-pentest phases 2-7 not run
**Directory:** `design/stories/epic-17-security-review/`

~46 code fixes applied. Post-remediation live re-pentest (`phase-2-postfix` …
`phase-7-postfix`) not executed. Epic 30 threat-model addendum written; re-pentest
should cover the new Epic 30 attack surface (unified credential model) plus the
new Epic 43 surface (org crypto, invitations, OIDC).

### Epic 18 S18.11 — `WorkspaceConditionProviderReady` not added
**File:** `pkg/apis/llmsafespace/v1/workspace_types.go`

Readyz gate decoupled (primary goal done). Complementary CRD condition absent — only
referenced in a comment at `cmd/workspace-agentd/main.go:857`. Eliminates fragile
regex parsing of `AgentHealthy` message; gives operators
`kubectl wait --for=condition=ProviderReady`.

### Epic 23 Stories 2+3 — Status-update conflict metric absent
**File:** `controller/internal/metrics/metrics.go`

`LastActivityAt` has 3 writers. Deferral conditioned on observing >10 conflicts/day
from `WorkspaceStatusUpdateConflictsTotal` — metric never shipped. Deferral condition
permanently unverifiable.

### Epic 28 S28.5 — Session stream still uses legacy broker
**File:** `api/internal/handlers/proxy.go` (`StreamEvents`)

Uses `h.broker.Subscribe()` (old `WorkspaceEventBroker`) rather than
`h.userBroker.SubscribeWorkspace()`. Stream is stable; migration is architectural
cleanup. `SubscribeWorkspace()` is dead code in production.

### Epic 43 US-43.6b — Full PVC data export
**Directory:** `design/stories/epic-43-organization-management/README.md` (US-43.6b)

Streaming export endpoint, helper pod, signed download URL (D8 fast follow). No
dependencies — can be built independently. Needed for GDPR / org offboarding.

---

## 🔵 Planning — Not Started

### Epic 31 — Shared Workspace Per User (User Drive)
**Directory:** `design/stories/epic-31-shared-workspace-per-user/` (does not exist)

Per-user persistent shared workspace. All prerequisites (Epics 6, 9, 24) shipped.
Marked High priority in the prior report. Design directory not yet created — needs
story design before implementation.

### Epic 32 — VPN Sidecars, VPC Connectivity & AWS IAM
**Directory:** `design/stories/epic-32-vpn-network-iam/` (does not exist)

Workspace pods join private VPCs via VPN sidecar; AWS IAM role binding; access to
private services. All prerequisites shipped. Marked High priority. Design directory
not yet created.

> Note: Epic 42's relay-router WireGuard sidecar establishes the WG-sidecar pattern
> that Epic 32 would reuse. Epic 32 design should reference Epic 42's
> `cmd/relay-router` WG sidecar implementation once it merges.

### Epic 42 — Multi-Cloud Inference Relay
**Directory:** `design/stories/epic-42-multi-cloud-inference-relay/README.md`

Foundation progressing: relay binary, router (`cmd/relay-router`), CRD types, AWS
cascade logic merged. Remaining per story breakdown: reconciler (US-42.9), Helm
integration (US-42.10), fallback mode (US-42.11), observability (US-42.12), and the
**7-day OCI idle-reclamation validation gate** (US-42.5 hard gate — OQ5). Status
still marked "Planning" in the design doc. Relay admin UX (epic-43-relay-admin-ux) is
done and shipped separately.

---

## Summary

| Priority | Count | Items |
|----------|-------|-------|
| 🔴 Launch blocker | 7 | Epic 43 Phase 4 (43.14/15/16/**17**), Phase 5 (43.18/19/20) |
| 🟠 In flight | 1 | US-43.10 OIDC SSO (awaiting merge) |
| 🟡 Important | 5 | US-24.17 disk pressure, US-9.16 preferredModel, US-16.13 question E2E, US-27a.9 credflow E2E, US-27b.5 body buffering |
| 🟢 Lower urgency | 8 | Python async, contract tests, VS Code slash cmds, re-pentest, ProviderReady condition, conflict metric, session broker migration, US-43.6b PVC export |
| 🔵 Planning | 3 | Epic 31 (user drive), Epic 32 (VPN/IAM), Epic 42 (relay — ongoing) |
| **Total open** | **24** | (3 items closed since 0170; ~17 new items from Epic 42/43 planning) |

### Recommended order
1. Merge US-43.10 OIDC branch (unblocks Phase 3 completion).
2. Epic 43 Phase 4 billing — **the actual launch blocker** (US-43.14 → 43.17 metered critical path).
3. US-24.17 disk pressure (design done, low effort, clear UX value).
4. Test-gap items US-27a.9 / US-16.13 (regression protection per Definition of Done).
5. Everything else is deferrable past MVP launch.
