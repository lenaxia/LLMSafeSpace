# Worklog: Session-wide adversarial validation — findings register

**Date:** 2026-06-19
**Session:** Post-session audit of all work shipped in PRs #231, #239, #247, #251. Four parallel validators independently traced every claim against merged code. This worklog is the canonical register of verified findings.
**Status:** Complete (audit); fixes tracked as follow-ups below

---

## Objective

Answer: "Did we solve the right problem at the right level of abstraction with the right solution in the right way at the right level of complexity? Is everything robust, reliable, maintainable, secure, scalable, performant, SOLID, and idiomatic? What assumptions did we make — did we validate all of them? Which ones aren't valid?"

Method: dispatched 4 independent validation agents (Epic 29, Epic 23, Epic 24+44, assumptions/SOLID) + 1 deep-revalidation agent that independently verified each finding against the code.

---

## Verified findings: 22 REAL, 2 PARTIALLY-REAL, 1 FALSE ALARM

### CRITICAL (real bugs / violations)

| ID | Finding | File:Line | Status | Fix needed |
|----|---------|-----------|--------|------------|
| C1 | ControllerRestartCount never resets in the common case — health-check restarts don't bump ConsecutiveFailures, so maybeResetConsecutiveFailures early-returns before the reset | `recovery_policy.go:64-79`, `health.go:122-123,153-154` | **REAL** | Move ControllerRestartCount reset outside the ConsecutiveFailures==0 guard, OR add a separate reset path for health-check-only restarts |
| C2 | Client.ListModels uses 1 MiB body limit (was 32 MiB before extraction) — large /provider catalogs silently truncate | `pkg/agent/opencode/client.go:274` | **REAL** | Restore 32 MiB limit; add test with >1 MiB response |
| C3 | Contract test aggregate assertion only proves ≥1 route sent auth; routes that 400/409 before backend are uncovered | `contract_auth_test.go:167-207` | **PARTIALLY-REAL** (structural gap is real; "5 of 14" count is inaccurate — actual: 13 routes registered, coverage varies) | Restructure to per-route assertions; add SetModel coverage |
| C4 | Zero tests for ControllerRestartCount anywhere | (absent) | **REAL** | Add the 5 tests from US-24.7 design spec |
| C5 | Zero tests for WorkspaceControllerRestartsTotal, WorkspaceSafeModeEntriesTotal, WorkspaceSafeModeExitsTotal | (absent) | **REAL** | Add emission tests |

### HIGH (design flaws)

| ID | Finding | File:Line | Status | Fix needed |
|----|---------|-----------|--------|------------|
| H1 | AgentClient.ListModels returns raw []byte — caller must know /provider schema; parsing triplicated | `agent_client.go:35`, `models_handler.go:275,310`, `models.go:137` | **REAL** | Either return typed `[]Model` from AgentClient, or document the raw-bytes contract explicitly and consolidate parsing |
| H2 | ISP violation: AgentClient has 5 methods; ModelsHandler uses only 2 (ListModels, PatchConfig) | `agent_client.go:34-40` vs `models_handler.go:90,169,204` | **REAL** | Split into caller-shaped interfaces (e.g. ModelClient{ListModels,PatchConfig}) or accept the fat interface with documentation |
| H3 | ModelsHandler has 8 setters; constructed with nil agentClient then wired later — violates US-29.8 constructor-injection principle | `models_handler.go:48-55`, `app.go:234` | **REAL** | Either make agentClient a required constructor param (requires reordering app.go init), or accept the two-phase init with documentation |
| H4 | buildRelayChecker hardcodes "4098" instead of agentd.AgentdAdminPort; drops the 16 KiB body limit that fetchRelayInjected had | `app.go:917,934` | **REAL** | Use constant; restore LimitReader |
| H5 | WorkspacesInRecovery gauge drifts on suspend (not just terminal-Failed) — handleSuspending clears ConsecutiveFailures without Dec'ing the gauge | `phase_suspend.go:32-36` | **REAL** | Dec the gauge when clearing recovery state in handleSuspending |
| H6 | EstimatedMemoryMB formula (2 bytes/token) is off by 5-6 orders of magnitude vs actual KV cache cost | `memory_pressure.go:147-149` | **REAL** | Either recalibrate the constant (KV cache is ~0.5-2 MB per 1K tokens) or rename to ContextWeightRelativeMB to avoid implying absolute memory |
| H7 | WorkspacesFailedTotal help text says "terminal Failed phase" but it actually increments on SafeMode entry | `metrics.go:26`, `metrics_wiring.go:84` | **REAL** | Fix help text to "workspaces entering SafeMode by failure class" |
| H8 | stability_reset exit label documented in worklog but never emitted in code | (absent) | **REAL** | Either emit it in maybeResetConsecutiveFailures (if SafeMode is cleared there) or remove from documentation |

### INVALID ASSUMPTIONS (Rule 7 violations)

| ID | Assumption | Claimed in | Status | Evidence |
|----|-----------|------------|--------|----------|
| A1 | "AgentClient.ListModels returns the same []byte as the old inline HTTP call" | Worklog 0369 A3 | **INVALID** — 32 MiB → 1 MiB (C2 above) | `client.go:274` vs original `32<<20` |
| A2 | "doReload uses passwordGetter" (justifying why passwordGetter stays on SecretsHandler) | Worklog 0369 A2 | **FALSE ALARM** — the validation agent was working from a stale mental model. passwordGetter was correctly removed from SecretsHandler in PR #251. doReload uses only podIPResolver. The conclusion (remove passwordGetter) was correct; the stated justification was wrong. | `secrets.go:22-29` (no passwordGetter field), `secrets.go:500` (doReload uses podIPResolver only) |
| A3 | GetSecretByName returns (nil, nil) for not-found — implicit, undocumented contract | workspace_env.go:103,187 | **REAL (observation)** — the handler checks `existing != nil` rather than `err != nil`. The real implementation (`pg_secret_store.go:74-76`) returns `(nil, nil)` for ErrNoRows. Any alternative implementation returning `(nil, ErrNotFound)` would silently break SetWorkspaceEnv. | Document the contract on the WorkspaceEnvService interface |
| A4 | "fake-client faithfully models CRD defaults" (implicit, never stated) | All controller tests | **INVALID** — proven false by the `default:false` incident in PR #231 round 1. No envtest was added to remediate. | `grep -rn envtest` = 0 hits across the entire codebase |
| A5 | "Stability reset clears ControllerRestartCount" | Worklog 0356 A1 | **INVALID** — disproven by C1 above. The reset is unreachable in the common case. | `recovery_policy.go:64` early-returns before line 79 |

### MEDIUM / LOW (tracked but non-blocking)

| ID | Finding | File:Line | Status | Notes |
|----|---------|-----------|--------|-------|
| M1 | agentPort package-level var mutated in tests (not parallel-safe) | `agent_client.go:68` | **REAL** | Inject via constructor or field |
| M2 | defaultModelCache is a package-level global; ModelsHandler has no cache field | `models.go:78` | **REAL** | Inject ModelCache into handler |
| M3 | resolveModelFromCatalog has 7 params; calls relayChecker (not pure) | `models_handler.go:301` | **REAL** | Make it a method or accept pre-resolved relayInjected bool |
| M4 | NewWorkspaceEnvHandler doc claims nil-check but doesn't implement it | `workspace_env.go:39-44` | **PARTIALLY-REAL** | Doc is wrong for env handler; ModelsHandler makes no such claim |
| M5 | No envtest anywhere in the codebase | (absent) | **REAL** | All CRD-default-sensitive code tested only with fake-client |
| M6 | ControllerRestartCount > 5 safe-mode trigger (US-24.7 AC 3) not implemented | (absent) | **REAL** | shouldEnterSafeMode only checks ConsecutiveFailures |
| M7 | restartGeneration bump doesn't clear ControllerRestartCount | `phase_creating.go:35-50` | **REAL** | Add `ControllerRestartCount = 0` to the restartGeneration block |
| M8 | SetModel makes redundant AgentClient calls when pod is down | `models_handler.go:169,204` | **REAL** | Skip PatchConfig when catalog fetch failed |
| M9 | Password-secret-missing restart doesn't increment ControllerRestartCount | `phase_active.go:87-106` | **REAL** | Add increment or clarify scope |
| M10 | WorkspaceConsecutiveFailuresMax dead metric (registered, never written) | `metrics.go:45-47` | **REAL** (pre-existing) | Wire it or remove it |
| M11 | http.Client allocated per WorkspaceClient.resolve() call (no connection reuse) | `agent_client.go:71-85`, `client.go:65` | **REAL** | Share a single http.Client across resolves |
| M12 | New HTTP Client per WorkspaceClient call defeats transport reuse | `client.go:65` | **REAL** (same as M11) | — |
| M13 | ActivateWorkspace annotation write is a full Update (not Patch) — no retry on conflict | `workspace_service.go:1170` | **REAL** (pre-existing) | Switch to Patch or wrap in RetryOnConflict |

---

## Summary by severity

| Severity | Count | Blocking? |
|----------|-------|-----------|
| CRITICAL | 5 (C1-C5) | C1, C2 should be fixed before next deploy; C3-C5 are test gaps |
| HIGH | 8 (H1-H8) | H4, H5, H7 are quick fixes; H1-H3, H6 are design decisions |
| INVALID ASSUMPTIONS | 5 (A1-A5) | A1, A4, A5 are disproven; A2 is a false alarm; A3 is an undocumented contract |
| MEDIUM/LOW | 13 (M1-M13) | Non-blocking; tech debt register |

**Overall assessment:** The work shipped is functionally correct for the happy path (verified by passing tests + reviewer approval). The problems are in: (1) untested edge cases (C1, C4, C5), (2) a regression introduced during extraction (C2), (3) over-claimed scope (H1, H2, C3), and (4) unvalidated assumptions that were either circular (A1) or hand-waved (A4, A5). None of the CRITICALs are data-loss or security bugs — they are correctness gaps in observability and model-listing functionality.

---

## Recommended fix priority

1. **C2** (body limit) — one-line fix, immediate user impact
2. **C1 + M7 + O6** (ControllerRestartCount reset + restartGeneration clear + safe-mode trigger) — coherent unit
3. **H4** (buildRelayChecker port constant + body limit) — quick fix
4. **H5** (WorkspacesInRecovery Dec on suspend) — quick fix
5. **H7** (WorkspacesFailedTotal help text) — one-line fix
6. **C4 + C5** (test gaps) — add the missing tests
7. **H6** (EstimatedMemoryMB formula/name) — design decision
8. **H1 + H2 + M3** (AgentClient interface shape) — design decision for US-29.2/29.3 follow-on
9. **M5** (envtest) — infrastructure investment

---

## Files Modified

This is a documentation-only worklog. No code changes.
