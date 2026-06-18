# Worklog: Epic 47 — Frontend Architecture Consolidation design

**Date:** 2026-06-18
**Session:** Adversarial re-review of findings 0037–0040, product decision on autoSuspend, full epic design with 12 stories
**Status:** Complete

---

## Objective

The user asked to validate whether each finding from the frontend analysis (0037–0040) is a false positive, whether it identifies the right problem at the right level of abstraction, and to flesh out the actionable findings as a new epic or extension to an existing one. The user also confirmed the autoSuspend product decision: no per-workspace, yes account-level.

---

## Work Completed

### Adversarial re-review of all findings

Re-validated every finding against current main (`1eba839e`). **Zero false positives.** Three findings reclassified as symptoms of deeper issues (Q1 autoSuspend = speculative UI for unbuilt feature; Q3 two fetch paradigms = no enforced data-access convention; F1 dual busy = ChatPage god-component). Two findings correctly dismissed as false alarms (cache writers race, dual useQuery workspaces).

### Stash assessment

Examined the pre-existing stashed migration-safety work. Validated it fills a real gap: `make migration-safety` runs 4 checks locally but CI only ran 3. Opened PR #226.

### Epic 47 design

Created `design/stories/epic-47-frontend-architecture-consolidation/` with:
- README (problem statement, scope, story index, execution plan)
- 12 story files (US-47.1 through US-47.12)

### Epic registry update

Added Epic 46 (codebase-debt-audit) and Epic 47 (frontend-architecture-consolidation) to `design/stories/README.md`.

---

## Key Decisions

1. **Epic 47, not 46.** Epic 46 (codebase-debt-audit) was already taken by another agent's backend Go audit. Frontend consolidation is a distinct scope — separate epic avoids mixing frontend and backend concerns.

2. **Account-level autoSuspend is Tier 3 (per-user), not per-workspace.** The user confirmed: no per-workspace autoSuspend; account-level (Tier 3 user setting that overrides the global instance default) is wanted. US-47.4 designs this as a new backend feature (inject `UserService` into `WorkspaceService`, resolve user → instance → default at creation). US-47.2 removes the misleading per-workspace UI first.

3. **12 stories across 5 phases.** Phase 1 (dead code + silent failures) is zero-risk and prerequisite for later phases. Each story is independently shippable.

4. **Two findings deferred.** ChatPage god-component decomposition and cross-tab SSE multiplexing are out of scope for this epic — they are larger architectural changes tracked separately.

---

## Blockers

None. All stories are scoped with acceptance criteria and validation gates.

---

## Tests Run

None — design + documentation session.

---

## Next Steps

1. Merge this PR (Epic 47 design docs).
2. Merge PR #226 (migration-safety CI parity).
3. Execute Phase 1 (US-47.1, US-47.2, US-47.3) as a single trivial-cleanup PR.
4. US-47.4 (account-level autoSuspend) can proceed in parallel with Phase 3/4.

---

## Files Modified

- `design/stories/epic-47-frontend-architecture-consolidation/README.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.1-dead-code-sweep.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.2-remove-per-workspace-autosuspend-ui.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.3-remove-dead-abort-controller.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.4-account-level-autosuspend.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.5-unify-busy-state-source.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.6-consolidate-loading-busy-primitives.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.7-normalise-active-busy-vocabulary.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.8-gate-wslog-remove-stray-log.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.9-performance-hot-path-fixes.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.10-migrate-to-tanstack-query.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.11-route-code-splitting.md` (created)
- `design/stories/epic-47-frontend-architecture-consolidation/US-47.12-fold-secrets-llm-provider-into-provider-keys.md` (created)
- `design/stories/README.md` (updated — added Epic 46 + 47 to registry)
- `worklogs/0339_2026-06-18_epic-47-frontend-consolidation.md` (this worklog)
