# Worklog: Epic 46 — Codebase Debt Audit & Abstraction Foundation

**Date:** 2026-06-18
**Session:** Production code audit + Epic 46 design + 15 story breakdowns
**Status:** Complete

---

## Objective

User requested a focused review of the cloned `lenaxia/llmsafespace` repository for duplicative or over-complex code, then escalated to "are we really in that good of shape?" after an initial (too-rosy) answer. The follow-up session audited the actual production Go code against the project's own rules in `README-LLM.md`, identified systemic violations, and produced a fully-scoped epic with 15 ranked stories.

---

## Work Completed

### Audit pass (production code only)

Measured rule violations against `README-LLM.md` Rules 0–5:

| Rule | Finding | Count |
|------|---------|-------|
| Rule 0 (TDD) | `api/internal/middleware/MISSINGTESTS.md` lists 6 categories of missing tests including Auth Middleware RBAC | 1 file |
| Rule 1 (Type Safety) | `interface{}` / `any` usages; worst in `pkg/mcp/client.go`, `pkg/settings/instance_service.go`, `pkg/utilities/masking.go` | 458 |
| Rule 2 (Idiomatic Go) | Sentinel errors vs custom error types | 43 sentinel, 4 custom types |
| Rule 3 (Explicit) | Swallowed errors (`_ = .Close()`, `_ = .Error()`); `context.TODO()` / `context.Background()` in production | 140 swallowed; 187 context |
| Rule 4 (Code Quality) | God files: `cmd/workspace-agentd/main.go` (1451 lines, 43 functions, `main()` 367 lines); `pkg/types/types.go` (71 types, 905 lines) | 2 critical |
| Rule 5 (Zero Tech Debt) | `controller/internal/relay/gcp_driver.go` all-`ErrNotImplemented` stub; `annotateModels` dead branch (README:517 confirms); four-writer `agent-config.json` fragility (README:453 confirms) | 3 critical |

Additional findings:
- **Epic numbering broken**: epic-38 and epic-43 each used twice; epic-39 missing.
- **Duplicate design doc**: `design/0018_..._wARmpool.md` and `design/0018_..._warmpool.md` are byte-identical.
- **README-LLM.md link drift**: lines 57, 58, 185, 186 reference wrong design-doc numbers.
- **No shared Service interface** despite 30 `*Service` structs; two parallel mock trees.
- **Setter injection antipattern**: `SecretsHandler` has 9 setters, `auth.Service` has 4 (including security-critical `SetMasterKey`).

### Cross-epic dependency analysis

Verified scope overlap with existing epics before drafting to avoid duplication:
- **Epic 29** (Handler Decomposition): owns SecretsHandler split, AgentClient extraction, **constructor injection** (US-29.8). Active — referenced, not duplicated.
- **Epic 38** (Architectural Remediation): owns ProxyHandler decomposition (US-38.2), 9 dead-code locations (US-38.7), dual-pattern consolidation (US-38.8), services-out-of-handlers (US-38.9), credential triplication (US-38.13). Active — referenced, not duplicated.

### Deliverables created

1. **`design/0037_2026-06-18_epic-46-codebase-debt-audit.md`** — parent design doc with problem statement, scope boundaries, dependency graph, abstraction tiers (A/B/C), anti-goals, success criteria, verification protocol.
2. **`design/stories/epic-46-codebase-debt-audit/README.md`** — epic index with 15 stories ranked by effort vs ROI, phased execution plan.
3. **15 story files** (US-46.1 through US-46.15), each with: problem, scope in/out, acceptance criteria, verification commands.

---

## Key Decisions

1. **Position as complement, not competitor, to Epic 38 and 29.** The audit initially surfaced findings those epics already own. The final scope explicitly excludes those and cross-references them. Rationale: duplication of effort tracking is worse than the underlying code issues; one canonical owner per finding.

2. **Effort-vs-ROI ranking is the suggested execution order.** US-46.1 (trivial, very high ROI) first; US-46.10 (large, high ROI, highest risk) near the end after prerequisites land. Rationale: ship cheap wins immediately, defer the risky single-writer refactor until the code organisation story (US-46.6) lands.

3. **Abstraction opportunities tiered A/B/C with explicit non-abstractions.** Tier A = ≥2 consumers (extract now); Tier B = 1 consumer + imminent second (extract when extending); Tier C = speculative (forbidden by Rule 4, listed as anti-examples). Rationale: prevents the epic from becoming a SOLID crusade that adds indirection without value.

4. **No "big bang" — every story independently shippable.** Explicit anti-goal in the parent design doc. Rationale: reduces blast radius and lets the team ship value incrementally.

5. **US-46.13 (lint baseline) gated on US-46.6 (split main.go).** Without the split, the baseline would need to grandfather a 1451-line file, which defeats the point. Rationale: lint rules must protect the post-refactor state, not the pre-refactor mess.

6. **Used epic number 46, not 39.** Although 39 is missing, jumping to 46 (one past the highest existing) avoids confusion about whether epic 39 is "reserved" or "forgotten". Rationale: clarity over gap-filling.

7. **Did not implement any code in this session.** User asked for an epic detailing the issues; implementation is a separate ask. Rationale: respects the "ask first, then act" principle.

---

## Assumptions Stated and Validated

| Assumption | Validation |
|------------|------------|
| Epic 29 and Epic 38 are active (not abandoned) | Verified via `design/stories/README.md` status table — both listed as 🔶 Partial / Planning, not ⛔ Superseded or 🚫 Obsolete |
| Story numbering convention is `US-NN.M` | Verified by listing existing story files in `design/stories/epic-38-architectural-remediation/` |
| Design doc numbering is `NNNN_YYYY-MM-DD_description.md` | Verified by `ls design/` — pattern holds for all 36 docs |
| Worklog numbering is `NNNN_YYYY-MM-DD_description.md` | Verified by `ls worklogs/` — pattern holds; **initial assumption "next is 0330" was wrong** (origin/main had advanced to 0336 after clone); repolint caught the collision on PR CI; renumbered to 0337 |
| Epic numbering allows gaps (no epic 19, 39) | Verified by `ls design/stories/ | grep epic` — gaps exist |
| `golangci-lint` v2 baseline mechanism exists | Not validated — US-46.13 acceptance criteria includes "use v2 native baseline if available, otherwise line-number file" as a fallback |
| The `GCPDriver` is truly unwired (no registration) | Not validated in this session — US-46.2 acceptance criteria includes a grep step to confirm before deletion |

---

## Blockers

None. All deliverables created; no implementation attempted.

---

## Tests Run

None. This was a design + documentation session; no code changes to test. Per Rule 0, implementation stories (US-46.1 through US-46.15) will write tests first when executed.

Manual verification performed:
- `ls design/0018*` → confirmed two byte-identical files (15153 bytes each).
- `wc -l cmd/workspace-agentd/main.go` → confirmed 1451 lines.
- `grep -c '^type ' pkg/types/types.go` → confirmed 71 types (initially miscounted as 76 — corrected during PR review).
- `grep -rn "context.TODO()\|context.Background()" --include="*.go" | grep -v _test.go | wc -l` → confirmed 187.
- `grep -rn "GCPDriver" --include="*.go"` → confirmed 28-line stub exists.
- `ls design/stories/ | grep epic | sort -V` → confirmed epic-38 and epic-43 collisions; epic-39 missing.

---

## Next Steps

1. **User review of Epic 46 scope.** Confirm the scope boundaries (especially the "out of scope" list) match intent. Adjust tier-A/B/C abstraction candidates if priorities differ.
2. **If approved, execute Phase 1** (US-46.1, US-46.2, US-46.14, US-46.15) as a single trivial-cleanup PR. These four stories are <1 day combined and zero-risk.
3. **Sequence Phase 2** (US-46.3, US-46.4, US-46.5, US-46.9) as four independent PRs. These are mechanical and each independently shippable.
4. **Plan Phase 5 (US-46.10) carefully.** It is the highest-risk story and requires the manual 1-hour workspace soak per its acceptance criteria. Schedule it after US-46.6 lands and after the team has bandwidth for adversarial review (Rule 11).
5. **Re-evaluate Epic 38 and Epic 29 status** before starting Phase 3 of Epic 46. If either has been abandoned or completed, US-46.7 and US-46.11 may expand to absorb their scope.

---

## Files Modified

| File | Action |
|------|--------|
| `design/0037_2026-06-18_epic-46-codebase-debt-audit.md` | Created (parent design doc) |
| `design/stories/epic-46-codebase-debt-audit/README.md` | Created (epic index) |
| `design/stories/epic-46-codebase-debt-audit/US-46.1-fix-epic-numbering-and-dup-design-doc.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.2-remove-dead-code-stubs.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.3-split-pkg-types-types-go.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.4-introduce-domain-error-type.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.5-propagate-context-everywhere.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.6-split-workspace-agentd-main-go.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.7-define-service-shaped-interfaces.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.8-type-the-settings-subsystem.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.9-type-mcp-request-bodies.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.10-single-writer-agent-config-json.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.11-workspace-password-provider-interface.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.12-fill-missingtests-md-gaps.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.13-add-funlen-gocyclo-lint-baseline.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.14-archive-v1-design-docs.md` | Created |
| `design/stories/epic-46-codebase-debt-audit/US-46.15-fix-readme-llm-stale-references.md` | Created |
| `worklogs/0337_2026-06-18_epic-46-codebase-debt-audit.md` | Created (this file) |
