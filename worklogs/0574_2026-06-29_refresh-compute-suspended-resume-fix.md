# Worklog: Fix — refresh compute on a suspended workspace was a no-op

**Date:** 2026-06-29
**Session:** Diagnosed and fixed a bug reported by the user: refreshing compute on a Suspended workspace did nothing.
**Status:** Complete

---

## Objective

The user reported that clicking "Refresh compute" on a **Suspended** workspace had no effect — it should "initialize it and refresh it." Diagnose the root cause and fix it.

---

## Bug

`RefreshWorkspaceCompute` (PR #452) allowed the Suspended phase and bumped `spec.restartGeneration`, and its test asserted the generation bump. But no pod was ever rebuilt, so the workspace stayed suspended.

## Root cause

An **unvalidated assumption** (Rule 7 violation) in PR #452: I assumed the controller rebuilds the pod on a `restartGeneration` bump regardless of phase. It does **not** for Suspended:

- `handleActive` (`controller/internal/workspace/phase_active.go:66`) and `handleCreating` (`phase_creating.go:35`) both observe `Spec.RestartGeneration > Status.ObservedRestartGeneration`.
- `handleSuspended` (`phase_suspend.go:74`) observes **only** `Spec.Suspend != nil && !*Spec.Suspend` — it never reads `restartGeneration`.

A suspended workspace has no pod, and its handler ignores the generation, so the bump updated the spec silently. The test `TestRefreshWorkspaceCompute_Suspended_…BumpsGeneration` asserted the bump but never validated the controller acts on it — a symptom-level assertion that passed while the feature was broken.

## Fix

When the workspace is Suspended, `RefreshWorkspaceCompute` now also requests a resume by delegating to `ActivateWorkspace` (after applying the defaults + generation bump). `ActivateWorkspace` writes `spec.suspend=false` (what `handleSuspended` watches), enforces the active-workspace cap, and runs the post-write persistence check (guards the worklog-0465 CRD-pruning incident). The controller then resumes → builds a fresh pod carrying the refreshed spec (latest image + resources).

Delegation reuses the existing, tested resume path rather than duplicating it (DRY). The spec refresh persists **before** the resume attempt; if the resume fails, the error surfaces but the config update remains for the next manual activate (correct partial state).

Non-suspended phases already work via the generation bump (their handlers observe it) — unchanged.

---

## Key Decisions

- **Delegate to `ActivateWorkspace`** rather than inlining `spec.suspend=false`. ActivateWorkspace encapsulates the full resume path (cap enforcement, RetryOnConflict, annotation, persistence check) — inlining would duplicate ~22 lines and risk drift.
- **Order: refresh-spec-then-resume.** Update the config first, then bring the workspace up so the fresh pod is built from the refreshed spec. ActivateWorkspace re-Gets, so it sees the bumped generation and reapplied defaults.
- **Cap enforcement is correct for refresh-resume.** Bringing a suspended workspace to active via any path must respect the active-workspace cap, consistent with a manual resume. If the user is at cap, refresh-resume makes room by suspending the stalest — the platform invariant holds.

---

## Tests

- `TestRefreshWorkspaceCompute_Suspended_AppliesDefaultsAndResumes` — reproduces the bug: asserts BOTH the refresh write (defaults + generation) AND the resume write (`spec.suspend=false`) happen. Without the fix, `sawResume` is false → test fails.
- `TestRefreshWorkspaceCompute_Suspended_ActivateFails_ReturnsError` — cap-store failure surfaces an error (spec refresh already persisted).
- All 12 prior refresh tests still pass; full workspace service + router suites green with `-race`.

---

## Blockers

None.

---

## Next Steps

- None for this fix.

---

## Files Modified

- `api/internal/services/workspace/workspace_service.go`
- `api/internal/services/workspace/workspace_refresh_test.go`
