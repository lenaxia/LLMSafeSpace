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
| Rule 5 | Zero technical debt; remove legacy code | `controller/internal/relay/gcp_driver.go` is a 28-line all-`ErrNotImplemented` stub; documented "fragility" in the 4-writer `agent-config.json` design is unfixed. (The `annotateModels` remap guard at `models.go:454` was originally flagged via README-LLM.md:517 but is retained as intentional defense-in-depth — see US-46.2 and worklog 0341.) |

A second audit (2026-06-21, triggered by Epic 53 design work) found that **feature flagging and billing are conflated** throughout the codebase despite the backend having clean internal separation. The feature-flag layer (`PlanFeatures` + `IsFeatureAllowed`) lives in `pkg/billing` alongside the Stripe integration, feature names are untyped string literals with a fail-open default (a typo silently disables enforcement), two feature flags are dead (`SSOEnabled` is never checked by any route; `CustomCredentials` is always-true), plan-tier quotas (`MaxWorkspaces`, `MaxMembers`) are defined but never enforced, Stripe webhooks never actually write `plan_id` (the documented integration point is broken), the frontend has no feature-flag awareness at all (no features endpoint, no 402 handler, tabs render then fail with generic errors), and `README-LLM.md` documents neither layer. See the "Entitlements / Feature-Flag Separation" section for the full audit and stories US-46.16–46.22.

Additionally, the codebase is missing **abstractions that would make testing cheaper and extension safer**, while not currently demanding them. These are listed in the Abstraction Opportunities section.

This epic is **scoped to findings not already covered** by Epic 38 (architectural remediation) or Epic 29 (handler decomposition). Where overlap exists, this epic **depends on** or **extends** those epics rather than duplicating them.

---

## Scope Boundaries (what this epic does NOT do)

To prevent scope creep, the following are **explicitly out of scope**:

- **ProxyHandler decomposition** — covered by US-38.2. This epic references it as a dependency for US-46.6.
- **Removing the 9 confirmed dead code locations** — covered by US-38.7. This epic only adds the `GCPDriver` stub. (The `annotateModels` remap guard was originally included but is retained as defense-in-depth — see US-46.2.)
- **Dual-pattern consolidation** — covered by US-38.8.
- **Moving business logic out of handlers** — covered by US-38.9.
- **Credential type triplication** — covered by US-38.13.
- **SecretsHandler setter removal** — covered by US-29.8. This epic references it.
- **Frontend changes** — none, **except** US-46.21 (feature-flag awareness: features client, tab gating, 402 handler). That story is included because the feature-flag system is broken without frontend cooperation — tabs currently render then fail with generic errors when the backend returns `402`.
- **New features** — zero, **except** US-46.20 (`GET /orgs/:id/features` read-only endpoint). That endpoint is the minimal server contract the frontend needs to determine feature availability without attempting operations and catching `402`s. It exposes existing flag state; it does not create new capability.
- **Stripe billing integration** — out of scope. The finding that Stripe webhooks never write `plan_id` (the documented integration point is broken) is listed in Out-of-Band Findings and belongs to Epic 12.

---

## Stories (ranked by effort vs ROI)

ROI = (impact on correctness, testability, maintainability) ÷ (engineering days).
Ranking is *suggested execution order* — top stories first because they are cheap and unlock later work.

| # | Story | Effort | ROI | Why this rank |
|---|-------|--------|-----|---------------|
| US-46.1 | Remove duplicate design doc + fix epic numbering collisions | Trivial (0.25d) | Very High | Zero risk; clears navigation debt that hides real issues; prerequisite for clean epic tracking |
| US-46.2 | Delete `GCPDriver` stub (annotateModels guard retained) | Small (0.25d) | Very High | README-LLM.md:517 originally classified the guard as debt; adversarial review found the code author's defense-in-depth argument stronger (see US-46.2 and worklog 0341) |
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
| US-46.16 | Extract feature-flag layer from `pkg/billing` to `pkg/entitlements` | Small (0.5d) | Very High | Decouples feature checks from the Stripe SDK transitively; `plan_tiers.go` is pure flag logic with zero payment code; only 1 production import site (`feature_guard.go`) |
| US-46.17 | Typed `Feature` enum; remove fail-open `default: return true` | Small (0.25d) | Very High | A typo in a feature-name string silently disables enforcement (security hole, not a crash); fail-closed is the only safe default |
| US-46.18 | Enforce or delete dead feature flags (`SSOEnabled`, `CustomCredentials`) | Small (0.5d) | High | `SSOEnabled` is Enterprise-only in `PlanTiers` but no SSO route has `FeatureGuard`; `CustomCredentials` is `true` on every plan and never checked — both are misleading dead config |
| US-46.19 | Split `PlanFeatures` into `FeatureFlags` (booleans) + `PlanQuotas` (numerics); delete unenforced quotas | Small (0.5d) | High | `MaxWorkspaces`/`MaxMembers` are defined per tier but read by zero code paths — dead fields that mislead readers into thinking quotas are enforced |
| US-46.20 | Add `GET /api/v1/orgs/:id/features` endpoint (resolved flag state) | Small (0.5d) | High | No way for a client to learn "what can my plan do?" without attempting operations and catching `402`s; unblocks US-46.21 |
| US-46.21 | Frontend: features client + `featureRequired` tab gating + `402` handler | Medium (1.5d) | High | `OrgAdminLayout` filters nav by `adminOnly` only; `api/client.ts` handles `401` only; the rich `{feature, planId, hint}` body from `FeatureGuard` is silently discarded; tabs render then fail with generic errors |
| US-46.22 | Validate `config.Billing.PlanPrices` keys against `PlanTiers` at startup | Trivial (0.25d) | Medium | Two independent plan-tier maps (`PlanTiers` static + `config.Billing.PlanPrices` from Helm); nothing validates their keys match; a plan can exist in one but not the other |

**Total estimated effort:** ~25 engineering days (~5 working weeks for one engineer; parallelisable across 2 engineers in ~3 weeks given dependency order).

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
US-46.15 (README refs)   ──┤
US-46.16 (extract entitlements) ──┤
US-46.22 (validate plan keys)  ──┘

US-46.6 (split main.go) ────── no deps; unblocks US-46.10
US-46.7 (Service interfaces) ── no deps; benefits from US-46.4 done first
US-46.12 (missing tests) ──── benefits from US-46.7 (fakes vs hand mocks)

US-46.10 (single writer agent-config.json) ── depends on US-46.6 (split main.go)

US-46.17 (typed Feature enum) ── depends on US-46.16 (enum lives in the new package)
US-46.18 (dead flags)         ── depends on US-46.17 (uses typed Feature constants)
US-46.19 (split flags/quotas) ── depends on US-46.16; blocked on quota decision (Out-of-Band)
US-46.20 (features endpoint)  ── depends on US-46.16 (reads from entitlements package)
US-46.21 (frontend gating)    ── depends on US-46.20 (calls the endpoint)

Cross-epic dependencies:
- US-46.7 extends US-29.8 (constructor injection from Epic 29)
- US-46.6 complements US-38.2 (proxy decomposition from Epic 38)
- US-46.11 complements US-29.1 (AgentClient from Epic 29)
- US-46.16–46.22 extend Epic 53 D12 (the separation principle Epic 53 established)
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

**Phase 7 — Entitlements / feature-flag separation (days 22–25):** US-46.16, US-46.17, US-46.22
US-46.18, US-46.19, US-46.20, US-46.21
Decouples the feature-flag layer from billing, types the feature names, wires the frontend. See the "Entitlements / Feature-Flag Separation" section for the audit context. US-46.16 → US-46.17 are the critical path (extract package, then add typed enum); US-46.20 → US-46.21 are the frontend chain. US-46.18 and US-46.19 are gated on product decisions (see Out-of-Band Findings) but can proceed once those are answered.

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

## Entitlements / Feature-Flag Separation

### The principle (established by Epic 53 D12)

Feature flagging and billing are separate concerns:

| Concern | Question it answers | Code location |
|---|---|---|
| **Feature flagging** | "Is this capability enabled for this principal?" | `pkg/billing/plan_tiers.go` (the flag layer — `PlanFeatures` + `IsFeatureAllowed`; no payment code) |
| **Billing** | "How did the principal obtain/keep the capability?" | `pkg/billing/stripe_provider.go`, `api/internal/handlers/org_billing.go`, `webhook.go` |

The flag layer is the source of truth for "enabled?"; billing is one writer of the state the flag reads (`plan_id`). They connect at exactly one seam: the `plan_id` column.

### Audit findings (2026-06-21)

| ID | Severity | Finding | Story |
|---|---|---|---|
| E1 | HIGH | Flag layer lives in `pkg/billing` — importing `IsFeatureAllowed` transitively pulls in the Stripe SDK | US-46.16 |
| E2 | HIGH | Feature names are untyped strings; `IsFeatureAllowed` has `default: return true` (fail-open) — a typo silently disables enforcement | US-46.17 |
| E3 | HIGH | `SSOEnabled` is Enterprise-only in `PlanTiers` but no SSO route mounts `FeatureGuard` — a Free/Team org can configure full OIDC SSO. **Product decision (2026-06-21):** SSO is not Enterprise-only; the flag is wrong and the guard is missing | US-46.18 |
| E4 | HIGH | `CustomCredentials` is `true` on every plan and never read by any handler — dead configuration | US-46.18 |
| E5 | HIGH | `PlanFeatures` mixes boolean flags with numeric quotas (`MaxWorkspaces`, `MaxMembers`); the quotas are defined per-tier but read by zero code paths | US-46.19 |
| E6 | HIGH | No `GET /features` or `/entitlements` endpoint exists — the only way for a client to learn feature availability is to attempt operations and catch `402`s | US-46.20 |
| E7 | HIGH | Frontend has no feature-flag awareness: `OrgAdminLayout` filters nav by `adminOnly` only; `api/client.ts` handles `401` only; the `{feature, planId, hint}` body from `FeatureGuard` is silently discarded; tabs render then fail with generic errors | US-46.21 |
| E8 | MEDIUM | Two independent plan-tier maps (`PlanTiers` static + `config.Billing.PlanPrices` from Helm); nothing validates their keys match | US-46.22 |
| E9 | HIGH | Stripe webhooks never write `plan_id` — every `UpdateOrgStatus` call in `webhook.go` passes `nil` for `planID`; the documented integration point is broken | Out-of-Band (Epic 12) |
| E10 | MEDIUM | `402 Payment Required` is used for entitlement denial, but no `402` is ever emitted for an actual billing condition (unpaid invoices surface as `status='suspended'` → `403`); semantically off | Documented; no story (changing the status code is a breaking API contract change — deferred) |

### Story detail

**US-46.16 — Extract feature-flag layer to `pkg/entitlements`.** Move `plan_tiers.go` (`PlanFeatures`, `PlanTiers`, `GetPlanFeatures`, `IsFeatureAllowed`, `TrialConfig`) to `pkg/entitlements/`. Update all imports — only one production caller (`feature_guard.go:54`) and the test file. No Stripe code moves (stays in `pkg/billing`). Result: `pkg/entitlements` has zero Stripe dependency.

**US-46.17 — Typed `Feature` enum; fail-closed.** Replace the `feature string` parameter on `FeatureGuard` with a `Feature` type. Add typed constants (`FeatureSSO`, `FeaturePolicies`, `FeatureAudit`, etc.). Remove `default: return true` in `IsFeatureAllowed` — unknown features return `false` (fail-closed). Update `router.go` call sites to use the typed constants. Depends on US-46.16 (the enum lives in the new package).

**US-46.18 — Wire or delete dead feature flags (`SSOEnabled`, `CustomCredentials`).**

**Product decision (recorded 2026-06-21):** SSO is **not** Enterprise-only. The current `SSOEnabled = true only for Enterprise` (`plan_tiers.go:42`) is wrong. The SSO gating model has three dimensions:

| Dimension | Concern | Layer | Owner |
|---|---|---|---|
| Is SSO available at all for this tier? | Boolean capability flag | Feature-flag layer (`pkg/entitlements`) | **US-46.18** (this story) |
| Up to N SSO users free, then subscription (self-hosted: 5 free) | Metered count | Quota/metering system (`usage_limits`, `metering.Service`) | **Epic 12** (metering) — NOT a boolean flag |
| Self-hosted vs hosted apply different gating | Deployment mode | New config signal | **Prerequisite** (see Out-of-Band) |

**What US-46.18 does:**
- Update `SSOEnabled` to `true` on the tiers where SSO is available (at minimum: all paid tiers; exact tier threshold confirmable in the `PlanTiers` map).
- Mount `FeatureGuard(reader, FeatureSSO)` on the SSO routes (`router.go:1191-1197`). This enforces the boolean "is SSO available for this plan?" gate that is currently missing.
- Delete `CustomCredentials` — it is `true` on every tier and checked by zero handlers. Dead code per Rule 5.

**What US-46.18 explicitly does NOT do:**
- The "5 free SSO users then subscription" count is metering, not a boolean flag. A flag cannot express "enabled, up to 5, then pay" — that is a count against a counter. This belongs in the quota/metering system (`PlanQuotas` post-US-46.19 + `usage_limits` seeding, which is Epic 12's open work). Routing it here would re-conflate flags with quotas — the exact anti-pattern US-46.19 exists to prevent.
- The deployment-mode signal (self-hosted vs hosted) does not exist in the codebase yet. It is a prerequisite for differentiating the self-hosted "5 free" model from the hosted model. See Out-of-Band Findings.
- Individual/personal SSO (an individual not in an org configuring their own IdP) is **undecided** — the existing SSO system is org-scoped (`/orgs/:id/sso`). Whether "SSO for individuals on hosted" means a new `/me/sso` surface or simply solo users creating a single-member org is a product question to resolve during implementation. See Out-of-Band Findings.

**This story is a clean illustration of why the flag/quota separation (US-46.19) matters:** the boolean flag answers "is SSO on?"; the metering system answers "how many SSO users have been used?". Mixing them in one field (`SSOEnabled`) would make neither question answerable correctly.

**US-46.19 — Split flags from quotas; delete unenforced quotas.** `PlanFeatures` currently mixes booleans (capability flags) with integers (quotas). These have different lifecycles and enforcement paths. Split into `FeatureFlags` (booleans) and `PlanQuotas` (numerics). The quota fields (`MaxWorkspaces`, `MaxMembers`) are never read by any code — `AddMember` doesn't check `MaxMembers`, workspace creation doesn't check `MaxWorkspaces`. **Product decision required:** delete the dead quotas, or wire them into enforcement? Note: three independent "max workspaces" concepts already exist (plan-tier quota unenforced, K8s admission webhook enforced, org admin policy enforced) — only the plan-tier one has no enforcement. Depends on US-46.16.

**US-46.20 — `GET /api/v1/orgs/:id/features` endpoint.** Returns the resolved `FeatureFlags` for the org's plan. Route behind `OrgMemberGuard` (members can see what features exist; no secret material). Response: `{sso: bool, policies: bool, audit: bool, ...}`. Minimal read-only surface — exposes existing flag state, creates no new capability. Depends on US-46.16 (reads from the entitlements package).

**US-46.21 — Frontend feature-flag awareness.** Three deliverables:
1. `frontend/src/api/features.ts` — calls `GET /orgs/:id/features`; cached in `OrgAdminLayout` context.
2. `OrgAdminLayout.tsx` / `SettingsPage.tsx` — add `featureRequired?: Feature` to `navItems` / `allTabs`; filter on it. Tabs whose feature is disabled are hidden (not rendered-then-failed).
3. `api/client.ts` — add a `402` handler that surfaces `{feature, planId, hint}` to the caller (toast, banner, or upgrade CTA) instead of discarding it. Currently `client.ts:15-19` handles only `401`.
Depends on US-46.20 (calls the endpoint).

**US-46.22 — Validate plan-key consistency at startup.** At boot, check that every key in `config.Billing.PlanPrices` exists in `PlanTiers` and vice versa. Log a fatal or warning on mismatch. Prevents the silent disagreement where a plan exists in one map but not the other.

---

## Success Criteria

1. `controller/internal/relay/gcp_driver.go` is deleted or fully implemented (no `ErrNotImplemented` stub). The `annotateModels` remap guard at `models.go:454` is retained as intentional defense-in-depth (see US-46.2 rationale and worklog 0341).
2. `pkg/types/` contains ≥6 files, each ≤250 lines, organised by domain (auth, workspace, session, network, secrets, settings).
3. `cmd/workspace-agentd/` `main.go` is ≤300 lines; supporting logic lives in `agentdhttp/`, `sessiontracker/`, `processsupervisor/`, `sysmetrics/` subpackages.
4. A `DomainError` type exists in `api/internal/errors/`; ≥10 of the 43 sentinel errors are migrated to wrap it; `errors.As(err, &DomainError{})` works in at least 5 call sites.
5. Zero `context.TODO()` calls remain in production code (tests still allowed).
6. `pkg/settings/` exposes typed getters (`DefaultStorageSize() resource.Quantity`, not `GetString("workspace.defaultStorageSize")`).
7. `pkg/mcp/client.go` builds request bodies from structs, not `map[string]any`.
8. `agent-config.json` has exactly one writer; `reloadMu` coordination removed or reduced to a single mutex around the writer.
9. `MISSINGTESTS.md` is deleted because every listed category is implemented.
10. `golangci-lint` runs `funlen`/`gocyclo` with a baseline file; new violations are blocked.
11. Epic numbering has no collisions: epic-38, epic-43 each consolidated; `epic-NN` folders are unique.
12. `design/0001`–`design/0020` (V1, superseded) live under `design/archive/v1/`.
13. All existing tests pass unchanged (every story is behaviour-preserving).
14. Every fix or abstraction in this epic has regression tests.
15. `pkg/entitlements/` exists and contains the flag layer (`PlanFeatures`, `IsFeatureAllowed`); it has zero imports from `pkg/billing` or the Stripe SDK.
16. Feature names are typed constants, not string literals; `IsFeatureAllowed` for an unknown feature returns `false`, not `true`.
17. Every feature flag in `PlanFeatures` is either enforced by a `FeatureGuard` on its route or deleted — no dead flags remain.
18. `PlanFeatures` contains only boolean capability flags; numeric quotas are in a separate type or deleted.
19. `GET /api/v1/orgs/:id/features` returns the resolved flag state; the frontend uses it to gate tab visibility.
20. `api/client.ts` handles `402` responses by surfacing `{feature, planId, hint}` to the caller.
21. `config.Billing.PlanPrices` keys are validated against `PlanTiers` keys at startup.

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
| Stripe webhooks never write `plan_id` — every `UpdateOrgStatus` in `webhook.go:151,179,197,223,229,234,238,258` passes `nil`; the doc comment at `webhook.go:127` claims plan syncing but the code doesn't do it; `types/orgs.go:28-32` defers to "US-43.15" | **Epic 12** (billing integration) |
| `PlanFeatures.MaxWorkspaces` / `MaxMembers` — delete the dead quota fields or wire them into enforcement? Three independent "max workspaces" concepts exist; only the plan-tier one is unenforced | **Product decision** (then US-46.19 executes the answer) |
| Per-user metering vs per-org plans — usage is metered per-user (`OwnerTypeUser`), but `plan_id` is per-org; a Free org with 25 members gets 25× the per-user Free quota | **Epic 12** (usage metering design) |
| `usage_limits` table is never seeded — `CheckQuota` returns `(true, 0, nil)` on the empty table; quota enforcement is a no-op out of the box | **Epic 12** (usage metering design) |
| `org_billing.go:20-40` defines a duplicate `OrgBilling` interface + adapter that re-exports `billing.CheckoutProvider`'s two methods verbatim — byte-for-byte identical signatures | Future cleanup (LOW; works correctly, just unnecessary indirection) |
| **Deployment-mode signal** (self-hosted vs hosted) — does not exist; needed to differentiate self-hosted "5 free SSO users" from hosted model where SSO is included in org/enterprise plans and value-added for individuals. Likely an instance setting or Helm value (`deployment.mode: "self-hosted"\|"hosted"`) | **Prerequisite** for full SSO gating; blocks the self-hosted metering model in Epic 12 |
| **SSO user-count metering** (self-hosted: 5 free SSO users, then subscription) — this is a count against a counter, not a boolean flag; belongs in the quota/metering system (`PlanQuotas` + `usage_limits`), gated on the deployment-mode signal above | **Epic 12** (metering); depends on deployment-mode prerequisite |
| **Individual/personal SSO scope** — "SSO as value-added for individuals on hosted" is undecided; existing SSO is org-scoped (`/orgs/:id/sso`); could mean a new `/me/sso` surface or solo-users-as-single-member-org; needs product resolution | **Product decision** before SSO-for-individuals implementation |
