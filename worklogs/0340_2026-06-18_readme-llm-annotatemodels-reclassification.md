# Worklog: README-LLM.md annotateModels Reclassification

**Date:** 2026-06-18
**Session:** Update README-LLM.md to resolve the contradiction between the doc and the code's own defense-in-depth rationale
**Status:** Complete

---

## Objective

PR #225 (Epic 46 US-46.2 amendment) retained the `annotateModels` remap guard as intentional defense-in-depth but explicitly deferred the `README-LLM.md:517` update to a follow-up PR. The AI reviewer on #225 flagged that this follow-up should not be deferred indefinitely: *"the README is the authoritative relay-config-subsystem reference and a future reader could be misled."*

This PR is that follow-up. It reclassifies the guard from "dead code (tech debt to remove)" to "intentional defense-in-depth" so the repo's two artifacts stop contradicting each other.

---

## Work Completed

### README-LLM.md edits

Three changes to `README-LLM.md`:

1. **Header version bump** (lines 5-6): `1.13` → `1.14`; `2026-06-12` → `2026-06-18`.
2. **annotateModels section rewrite** (lines 517-519): retitled "dead code (tech debt to remove)" → "intentional defense-in-depth". New body:
   - States the guard is **intentionally retained**, not removed
   - Explains the specific failure mode it guards against (opencode ignoring `disabled_providers`)
   - Notes the history (narrowed in 0178 to fix a real bug, re-reasoned in 0189)
   - Justifies the ~20 LoC cost vs the silent-failure mode it prevents
   - References worklog 0341 for the full rationale
3. **Version History entry** (line 1421): added 1.14 row summarising the reclassification.

The rewrite also corrects the guard's predicate paraphrase: the original text wrote `relayGloballyEnabled && relayInjected && p.ID=="opencode"`, omitting the `avail == ModelFreeTier` term. The new text includes the full predicate to match the actual code at `models.go:454`.

---

## Key Decisions

1. **Bumped the version to 1.14** rather than silently editing. The Version History table is the README's own audit trail; a substantive content change warrants an entry. 1.13's entry already mentioned "updated annotateModels remap note" — 1.14's entry records the reversal.

2. **Referenced worklog 0341 rather than re-deriving the argument.** The README is high-level; the detailed defense-in-depth reasoning lives in 0341 (and US-46.2's scope section). The README points there rather than duplicating ~100 lines of analysis.

3. **Corrected the predicate paraphrase.** The original text omitted `avail == ModelFreeTier`. A future reader comparing the README to the code would see a mismatch; the correction prevents that.

4. **Renumbered worklog 0338 → 0341 to fix a collision on main.** Origin/main had *two* worklogs numbered 0338 (a collision between PR #225 and a parallel PR — `0338_us-46.2-keep-annotatemodels-guard.md` and `0338_migration-safety-ci-parity.md`). repolint correctly caught this and blocked this PR. Fix: renamed the us-46.2 worklog to 0341 (repolint reported "next available: 0341") and updated all 6 references across `design/0037`, `US-46.2`, `README-LLM.md`, and this worklog. The collision root cause (repolint not re-running against updated main on non-ff merges) remains a separate concern.

---

## Assumptions Stated and Validated

| Assumption | Validation |
|------------|------------|
| The README Version History is append-only and bumps are expected on substantive edits | Verified by reading the table at lines 1419-1431 — every prior version is a recorded substantive change; pattern holds |
| Worklog 0340 is the next available number for THIS worklog | Verified via `git ls-tree origin/main worklogs/` — highest on main is 0339; the duplicate 0338 entries meant 0340 was safe for this file, and 0341 (repolint-reported next available) was used for the renumbered us-46.2 worklog |
| The guard's full predicate is `relayGloballyEnabled && relayInjected && avail == ModelFreeTier && p.ID=="opencode"` | Verified at `api/internal/handlers/models.go:454` |
| Epic 46's US-46.2 acceptance criteria flagged this README update as optional/separate | Verified in `design/stories/epic-46-codebase-debt-audit/US-46.2-remove-dead-code-stubs.md` acceptance criteria |

---

## Blockers

None.

---

## Out-of-band finding (not in scope)

**Worklog numbering collision on origin/main — FIXED in this PR.** Two distinct worklogs shared number 0338:
- `worklogs/0338_2026-06-18_migration-safety-ci-parity.md` (left as-is — not ours)
- `worklogs/0338_2026-06-18_us-46.2-keep-annotatemodels-guard.md` → renamed to `worklogs/0341_2026-06-18_us-46.2-keep-annotatemodels-guard.md` in this PR

repolint correctly caught the collision and blocked this PR until fixed. Root cause: two PRs (#225 and a parallel migration-safety PR) both claimed 0338 against different bases; repolint validated each PR against its own base but did not re-validate against the updated main at merge time. The merge-order gap remains a separate concern for the CI pipeline (repolint should re-run on non-ff merges), but the immediate collision is resolved.

---

## Tests Run

None — documentation-only change. The production code (`api/internal/handlers/models.go`, `models_test.go`) is unchanged.

Manual verification:
- `grep -n "annotateModels" README-LLM.md` — confirms the section heading changed.
- `grep -n "1.14" README-LLM.md` — confirms the version bump in two places (header + history table).

---

## Next Steps

1. User review of this PR. If approved, merge.
2. (Separate) The repolint merge-order gap that allowed the 0338 collision should be tracked — repolint needs to re-run against updated main on non-ff merges, not just against the PR's own base.
3. Proceed with executing the rest of Epic 46 per the phased plan.

---

## Files Modified

| File | Action |
|------|--------|
| `README-LLM.md` | Lines 5-6 (version/date), 517-523 (annotateModels section rewrite + predicate correction), 1425 (new Version History entry) |
| `design/0037_2026-06-18_epic-46-codebase-debt-audit.md` | 3 references to "worklog 0338" → "worklog 0341" |
| `design/stories/epic-46-codebase-debt-audit/US-46.2-remove-dead-code-stubs.md` | 1 reference "worklog 0338" → "worklog 0341" |
| `worklogs/0338_2026-06-18_us-46.2-keep-annotatemodels-guard.md` | Renamed to `worklogs/0341_2026-06-18_us-46.2-keep-annotatemodels-guard.md` (fixes main's 0338 collision) |
| `worklogs/0340_2026-06-18_readme-llm-annotatemodels-reclassification.md` | Created (this file); updated to reflect collision fix |
