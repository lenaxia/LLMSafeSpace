# Epic 46: Codebase Debt Audit & Abstraction Foundation

**Status:** Proposed
**Created:** 2026-06-18
**Priority:** High (foundational — unblocks safe iteration on every other epic)
**Depends on:** Epic 38 (Architectural Remediation) — partial overlap; this epic avoids duplicating US-38.2, US-38.7, US-38.8, US-38.9, US-38.13 and explicitly cross-references them
**Related:** Epic 29 (Handler Decomposition) — US-29.8 (constructor injection) is the foundation several US-46 stories build on

---

## Problem Statement

A focused audit of the production Go code (excluding tests, generated code, and SDKs) found that **`README-LLM.md`'s quality rules are enforced at PR review time but not at codebase level**. The codebase ships systemic violations of its own Rules 0–5:

| Rule | Rule text (paraphrased) | Violation count found |
|------|-------------------------|-----------------------|
| Rule 0 | TDD mandatory, never ship missing tests | `api/internal/middleware/MISSINGTESTS.md` lists 6 missing categories including Auth Middleware RBAC |
| Rule 1 | Type safety first; no `map[string]interface{}` for structured data | **458** `interface{}`/`any` usages; worst in `pkg/mcp/client.go`, `pkg/settings/instance_service.go`, `pkg/utilities/masking.go` |
| Rule 2 | Idiomatic Go; custom error types for domain errors | **43 sentinel errors, only 4 custom error types**; callers cannot `errors.As` on domain conditions |
| Rule 3 | Explicit over implicit; no swallowed errors | **140** swallowed `Close()`/`Error()` calls; **187** `context.TODO()`/`context.Background()` in production code |
| Rule 4 | Functions ≤50 lines; not over-engineered | `cmd/workspace-agentd/main.go` is **1451 lines, 43 functions, `main()` spans 367 lines**; `pkg/types/types.go` has **71 types in one 905-line file** |
| Rule 5 | Zero technical debt; remove legacy code | `controller/internal/relay/gcp_driver.go` is a 28-line all-`ErrNotImplemented` stub; documented "fragility" in the 4-writer `agent-config.json` design is unfixed; dead `annotateModels` branch confirmed by README-LLM.md:517 remains in `api/internal/handlers/models.go:454` |

Additionally, the codebase is missing **abstractions that would make testing cheaper and extension safer**, while not currently demanding them. These are listed in the Abstraction Opportunities section.

This epic is **scoped to findings not already covered** by Epic 38 (architectural remediation) or Epic 29 (handler decomposition). Where overlap exists, this epic **depends on** or **extends** those epics rather than duplicating them.

---

## Scope Boundaries (what this epic does NOT do)

To prevent scope creep, the following are **explicitly out of scope**:

- **ProxyHandler decomposition** — covered by US-38.2. This epic references it as a dependency for US-46.6.
- **Removing the 9 confirmed dead code locations** — covered by US-38.7. This epic only adds the `GCPDriver` stub and the `annotateModels` remap guard.
- **Dual-pattern consolidation** — covered by US-38.8.
- **Moving business logic out of handlers** — covered by US-38.9.
- **Credential type triplication** — covered by US-38.13.
- **SecretsHandler setter removal** — covered by US-29.8. This epic references it.
- **Frontend changes** — none.
- **New features** — zero; every story is a fix, split, or abstraction extraction with no behaviour change.

---

## Stories (ranked by effort vs ROI)

ROI = (impact on correctness, testability, maintainability) ÷ (engineering days).
Ranking is *suggested execution order* — top stories first because they are cheap and unlock later work.

| # | Story | Effort | ROI | Why this rank |
|---|-------|--------|-----|---------------|
| US-46.1 | Remove duplicate design doc + fix epic numbering collisions | Trivial (0.25d) | Very High | Zero risk; clears navigation debt that hides real issues; prerequisite for clean epic tracking |
| US-46.2 | Delete `GCPDriver` stub and `annotateModels` dead branch | Small (0.5d) | Very High | README-LLM.md already justifies both; removes interface-satisfying-for-no-purpose; mechanical |
| US-46.3 | Split `pkg/types/types.go` (71 types → per-domain files) | Small (1d) | High | Pure mechanical move; unblocks every future type addition; reduces merge conflicts |
| US-46.4 | Introduce `DomainError` type + error mapping convention | Small (1d) | High | Unblocks typed `errors.As` across all callers; 4 existing types prove the pattern; small surface |
| US-46.5 | Replace `context.TODO()` / `context.Background()` with propagated context | Medium (2d) | High | 187 sites; mechanical with grep; restores deadline propagation (Rule 3); enables timeout tests |
| US-46.6 | Split `cmd/workspace-agentd/main.go` (1451 lines → ≤300 lines/file) | Medium (3d) | High | Unblocks US-46.10; necessary for test isolation; pure file move + package extraction |
| US-46.7 | Define `Service`-shaped interfaces for cross-cutting services (Settings, Metering, Secrets) | Medium (2d) | High | Enables fake injection without hand-written mocks; 30 concrete services currently lack contracts; small interfaces only (3–5 methods) |
| US-46.8 | Type the Settings subsystem (`map[string]any` → typed registry) | Medium (2d) | Medium-High | Eliminates 6+ untyped usages; admin UX gets compile-time safety; one bounded subsystem |
| US-46.9 | Type the MCP request bodies (`map[string]any` → request structs) | Small (1d) | Medium | 5 untyped body builders in `pkg/mcp/client.go`; MCP is externally visible; small surface |
| US-46.10 | Consolidate the 4-writer `agent-config.json` design into a single writer | Large (4d) | High | Removes README-documented "fragility"; eliminates the `reloadMu` + `atomic.Pointer` coordination dance; depends on US-46.6 |
| US-46.11 | Define `WorkspacePasswordProvider` interface + delete inline `func` injection | Small (0.5d) | Medium | Replaces `passwordGetter func(ctx, ...) (string, error)` field on `SecretsHandler` with a named, mockable contract; complements US-29.1 AgentClient |
| US-46.12 | Add missing tests documented in `MISSINGTESTS.md` | Medium (2d) | Medium | Rule 0 compliance; the file is itself a Rule 0 violation; focuses on Auth Middleware RBAC + Rate Limiting bursting (highest-signal gaps) |
| US-46.13 | Add `funlen` / `gocyclo` to golangci-lint with current-state baseline | Small (0.5d) | Medium | Locks in the splits from US-46.6; prevents regressions; baseline file excludes existing offenders so the rule is opt-in progressive |
| US-46.14 | Archive V1 design docs (`design/0001`–`design/0020`) to `design/archive/v1/` | Trivial (0.25d) | Medium | README-LLM.md:52 marks them "reference only — superseded"; they currently pollute `design/` navigation |
| US-46.15 | Fix README-LLM.md stale design-doc references | Trivial (0.25d) | Low-Medium | README-LLM.md:57 cites `0007_network.md` and :58 cites `0006_runtimeenv.md` but actual files are `0020_network.md` and `0007_runtimeenv.md` |

**Total estimated effort:** ~21 engineering days (4 working weeks for one engineer; parallelisable across 2 engineers in ~2.5 weeks given dependency order).

---

## Dependency Graph

```
US-46.1 (numbering)      ──┐
US-46.2 (dead code)      ──┤
US-46.3 (types split)    ──┤
US-46.4 (DomainError)    ──┤── can start immediately (no deps)
US-46.5 (context)        ──┤
US-46.8 (settings types) ──┤
US-46.9 (MCP types)      ──┤
US-46.11 (PasswordProvider)──┤
US-46.13 (lint baseline) ──┤
US-46.14 (archive V1)    ──┤
US-46.15 (README refs)   ──┘

US-46.6 (split main.go) ────── no deps; unblocks US-46.10
US-46.7 (Service interfaces) ── no deps; benefits from US-46.4 done first
US-46.12 (missing tests) ──── benefits from US-46.7 (fakes vs hand mocks)

US-46.10 (single writer agent-config.json) ── depends on US-46.6 (split main.go)

Cross-epic dependencies:
- US-46.7 extends US-29.8 (constructor injection from Epic 29)
- US-46.6 complements US-38.2 (proxy decomposition from Epic 38)
- US-46.11 complements US-29.1 (AgentClient from Epic 29)
```

---

## Execution Strategy (phased)

**Phase 1 — Debt clearance (days 1–2):** US-46.1, US-46.2, US-46.14, US-46.15
Trivial, zero-risk, immediate readability wins. Ship in one PR.

**Phase 2 — Type-safety foundations (days 3–6):** US-46.3, US-46.4, US-46.5, US-46.9
Mechanical refactors; each is independently shippable. Establishes the typed-error and context conventions later stories rely on.

**Phase 3 — Interface extraction (days 7–10):** US-46.7, US-46.8, US-46.11
Abstractions that enable better testing without changing behaviour. Settings and MCP subsystems typed; `Service`-shaped interfaces defined where ≥2 consumers exist.

**Phase 4 — File decomposition (days 11–14):** US-46.6
Workspace-agentd `main.go` split. Prerequisite for US-46.10.

**Phase 5 — The single-writer refactor (days 15–18):** US-46.10
The highest-risk story in this epic. Eliminates the documented four-writer fragility in the relay config subsystem. Must land with regression tests covering all four current write paths.

**Phase 6 — Hygiene and lock-in (days 19–21):** US-46.12, US-46.13
Add the tests `MISSINGTESTS.md` admits are missing; add lint rules to prevent regression.

---

## Abstraction Opportunities (where interfaces earn their keep)

This section lists interfaces that **do not currently exist** but would materially improve testability or extensibility, without crossing the "speculative generality" line forbidden by Rule 4. Each interface is justified by ≥2 existing consumers or a clear imminent need (Epic 30/42/45).

### Tier A — define now (clear ≥2 consumers)

| Proposed interface | Replaces | Consumers today | Testability win |
|--------------------|----------|-----------------|-----------------|
| `agentd.AgentClient` | per-handler `passwordGetter func(...)` fields + ad-hoc `http.Client` calls to port 4096 | `SecretsHandler`, `ModelsHandler`, `ProxyHandler`, `AgentReloadHandler` (4 sites) | Single fake replaces 4 hand-written `httptest.Server` setups; enables auth-enforcing mock (Epic 29 US-29.6) |
| `settings.Repository` (typed) | `InstanceService.data map[string]any` | Admin UX, workspace creation, metering, rate-limit middleware (4 sites) | Compile-time safety on key names; eliminates stringly-typed `GetString`/`GetInt` calls |
| `secrets.WorkspacePasswordProvider` | `passwordGetter func(ctx, wsID) (string, error)` field on `SecretsHandler` | SecretsHandler, ModelsHandler (2 sites, growing) | Named contract; mockable per-test; replaces function-typed injection |

### Tier B — define when extending (1 consumer today, second imminent)

| Proposed interface | Replaces | Current consumer | Imminent second consumer |
|--------------------|----------|------------------|--------------------------|
| `metering.UsageExporter` | direct Stripe import in `metering.Service.exportToStripe` | `metering.Service` | Epic 42 (multi-cloud relay) will need usage export to non-Stripe |
| `relay.VMProvisioner` (already exists as `ProviderDriver` — keep) | n/a | `relay.Reconciler` | Document as the canonical pattern; mirror for other infra interfaces |
| `database.WorkspaceMetadataStore` (extract from 1162-line `database.Service`) | concrete `*database.Service` passed to 8+ handlers | 8 handlers | Allows swapping storage layer (e.g. for tests, or for a multi-tenant sharded future) |

### Tier C — explicitly DO NOT abstract (would be speculative generality)

| Candidate | Why not |
|-----------|---------|
| `Handler` base interface for all `*Handler` structs | No polymorphic caller; would be empty marker interface; Rule 4 forbids |
| `Repository<T>` generic interface | Go generics would add complexity; concrete stores per aggregate are clearer |
| `EventBus` interface over `eventbroker.Broker` | Only one implementation and one consumer; abstracting now is YAGNI |
| `Logger` interface | Already exists as `pkginterfaces.LoggerInterface`; do not duplicate |
| `Authenticator` interface over `auth.Service` | `auth.Service` is already an interface-shaped concrete; extracting further adds indirection without benefit |

---

## Anti-Goals (what this epic will not become)

1. **A "big bang" refactor.** Every story is independently shippable. No story depends on another except where the dependency graph explicitly says so.
2. **A SOLID correctness crusade.** Interface extraction is gated on ≥2 consumers (Tier A) or imminent need (Tier B). Single-consumer speculative interfaces are forbidden.
3. **A documentation rewrite.** README-LLM.md fixes (US-46.15) are limited to factual errors in design-doc references; the doc's structure is unchanged.
4. **A test-coverage quota.** US-46.12 adds the specific tests `MISSINGTESTS.md` admits are missing, not a coverage percentage target.

---

## Success Criteria

1. `controller/internal/relay/gcp_driver.go` is deleted or fully implemented (no `ErrNotImplemented` stub).
2. The `annotateModels` dead branch (README-LLM.md:517) and its tests are removed.
3. `pkg/types/` contains ≥6 files, each ≤250 lines, organised by domain (auth, workspace, session, network, secrets, settings).
4. `cmd/workspace-agentd/` `main.go` is ≤300 lines; supporting logic lives in `agentdhttp/`, `sessiontracker/`, `processsupervisor/`, `sysmetrics/` subpackages.
5. A `DomainError` type exists in `api/internal/errors/`; ≥10 of the 43 sentinel errors are migrated to wrap it; `errors.As(err, &DomainError{})` works in at least 5 call sites.
6. Zero `context.TODO()` calls remain in production code (tests still allowed).
7. `pkg/settings/` exposes typed getters (`DefaultStorageSize() resource.Quantity`, not `GetString("workspace.defaultStorageSize")`).
8. `pkg/mcp/client.go` builds request bodies from structs, not `map[string]any`.
9. `agent-config.json` has exactly one writer; `reloadMu` coordination removed or reduced to a single mutex around the writer.
10. `MISSINGTESTS.md` is deleted because every listed category is implemented.
11. `golangci-lint` runs `funlen`/`gocyclo` with a baseline file; new violations are blocked.
12. Epic numbering has no collisions: epic-38, epic-43 each consolidated; `epic-NN` folders are unique.
13. `design/0001`–`design/0020` (V1, superseded) live under `design/archive/v1/`.
14. All existing tests pass unchanged (every story is behaviour-preserving).
15. Every fix or abstraction in this epic has regression tests.

---

## Verification

Per Rule 11 (Adversarial Self-Review), each story's PR must include:

- The grep counts proving the rule violation is gone (e.g. `grep -rn "context.TODO" --include="*.go" | grep -v _test.go | wc -l` returns 0 for US-46.5).
- `make build && make test && make lint` passing.
- A worklog entry (per Worklog Requirements).
- A diff-size guard: any story exceeding its estimated effort by >50% must be split.

---

## Out-of-Band Findings (referenced but not in scope)

These were discovered during the audit but belong to other epics or are user decisions:

| Finding | Owner |
|---------|-------|
| Epic numbering has collisions (epic-38, epic-43 used twice; epic-39 missing) | Resolved by US-46.1 |
| README-LLM.md vs README.md content overlap (Auth, Config, API sections duplicated) | Future doc epic |
| 328 worklogs need periodic archival | Future tooling task (`repolint` already sequences them) |
| `cmd/repolint/main.go` uses `fmt.Println` for CLI output (legitimate, not a violation) | No action |
| 94 `panic()` / `os.Exit()` calls — most are in `cmd/` (legitimate); library panics need case-by-case review | Separate hardening epic if warranted |
