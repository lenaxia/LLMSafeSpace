# Worklog: Fix worklog-0372 adversarial validation findings

**Date:** 2026-06-19
**Session:** Triage and fix of the verified findings in worklog 0372 (the
session-wide adversarial validation register). All REAL findings that were
clear-cut or part of the recovery state-machine coherent unit were fixed
under TDD; design-decision items were surfaced to the user rather than
guessed (Rule 6).
**Status:** Complete (code fixes); design-decision items deferred to user

---

## Objective

Take the canonical findings register (worklog 0372: 22 REAL, 2 PARTIALLY-REAL,
1 FALSE ALARM) and remediate every finding that is a clear bug or test gap,
validating each against the live code AND the authoritative design specs
(US-24.7 / US-24.8 / US-24.13) before touching code (Rule 7 + Rule 11 Phase 2).
Surface genuine design decisions to the user instead of guessing.

---

## Work Completed

### Assumptions stated and validated (Rule 7)

1. `ControllerRestartCount` is incremented ONLY in `checkAgentHealth`
   (health.go:123,154). **Validated** via repo-wide grep — true. Therefore the
   persistent-unreachability SafeMode trigger belongs in the health-restart
   path, not in `enterRecovery`.
2. The `restartGeneration` bump is the sole spec-mandated place that should
   clear `ControllerRestartCount` (US-24.7 AC 5). **Validated** against
   `design/stories/epic-24-.../US-24.7-controller-restart-not-failure.md`.
3. SafeMode exits ONLY via restartGeneration bump or resume — NOT via stability
   reset. **Validated** against `US-24.13-safe-mode.md` (Critical-priority,
   authoritative). This **supersedes** US-24.8 AC 3 ("SafeMode condition
   removed on stability reset"). Resolves H8.
4. `WorkspacesInRecovery` is Inc'd exactly once per recovery episode (on the
   not-in-recovery→in-recovery transition in `recordRecoveryMetrics`), so a
   single Dec on suspend/recovery-success balances it. **Validated** in
   `metrics_wiring.go:68-70`.

### C2 — ListModels body limit regressed 32 MiB → 1 MiB (CRITICAL)

`pkg/agent/opencode/client.go`: the US-29.5 extraction (PR #251) reverted the
`/provider` read limit from 32 MiB to 1 MiB. The catalog is ~5 MB (139+
providers from models.dev), so it silently truncated. Restored to 32 MiB via a
named constant `providerCatalogReadLimit` and corrected the doc comment.
Regression test `TestListModels_LargeProviderCatalog_NotTruncated` (4 MiB
body) fails at 1 MiB, passes at 32 MiB. Verified the regression origin:
commit `7213e32a` established 32 MiB; `cbc8e534` (PR #251) regressed it.

### H4 — buildRelayChecker hardcoded port + dropped read limit (HIGH)

`api/internal/app/app.go`: `buildRelayChecker` hardcoded `"4098"` and decoded
the readyz body without `LimitReader`; the original `fetchRelayInjected` (at
`cbc8e534^`) used `agentd.AgentdAdminPort` and `io.LimitReader(resp.Body,
16*1024)`. Extracted a testable `newRelayChecker(client, port, ...)` seam;
`buildRelayChecker` now wires `agentd.AgentdAdminPort` + `readyzReadLimit`
(16 KiB, matching the statusz precedent in `proxy_events.go:547`). Added
`api/internal/app/relay_checker_test.go` (6 tests: relay true/false, oversized
body decode-fails, non-OK, resolve/password short-circuits, wiring).

### C1 + M7 + M6 — recovery state-machine coherent unit (CRITICAL/HIGH)

- **C1** `recovery_policy.go`: `maybeResetConsecutiveFailures` guard changed
  from `ConsecutiveFailures == 0` to `ConsecutiveFailures == 0 &&
  ControllerRestartCount == 0` (matches US-24.8 spec lines 13-17). A
  health-check restart bumps `ControllerRestartCount` without touching
  `ConsecutiveFailures`, so the reset was unreachable for it.
- **M7** `phase_creating.go`: restartGeneration bump now clears
  `ControllerRestartCount` (US-24.7 AC 5).
- **M6** `health.go`: added the persistent-unreachability SafeMode trigger
  (US-24.7 AC 3 / US-24.13 entry trigger 2): `ControllerRestartCount > 5`
  trips SafeMode. Factored the two near-identical restart sites into a single
  `restartAgentPod` helper (removes duplication) plus an idempotent
  `maybeEnterSafeModeFromRestarts`. Emits `WorkspaceSafeModeActive.Inc` +
  `WorkspaceSafeModeEntriesTotal{trigger="controller_restart"}`.
- Added `controller_restart_test.go` (US-24.7's 5 specified tests + metric
  emission + boundary cases).

### H5 — WorkspacesInRecovery gauge drift on suspend + F22 (HIGH)

`phase_suspend.go`: `handleSuspending` cleared `ConsecutiveFailures` without
`WorkspacesInRecovery.Dec()`, drifting the gauge on every in-recovery suspend.
Now Dec's the gauge when `wasInRecovery` (with Inc rollback on Status().Update
conflict, matching the `WorkspacesRunning` idiom) and clears
`ControllerRestartCount` per US-24.8 F22. SafeMode is intentionally preserved
(US-24.13 AC 9). Updated the existing `TestSuspend_ClearsRecoveryState` to
seed the gauge (mirror the production invariant) and assert CRC clearance.

### H7 — WorkspacesFailedTotal misleading help text (HIGH)

`controller/internal/metrics/metrics.go`: help text said "entered terminal
Failed phase" but the counter is incremented only on SafeMode entry
(`metrics_wiring.go:84`, labeled by failure class). Fixed the help text to
describe SafeMode entry. (Metric name intentionally retained — renaming would
break dashboards; the worklog scoped this to help-text only.)

### C4 + C5 — missing test coverage (CRITICAL)

The US-24.7 spec mandated 5 tests for `ControllerRestartCount` and 3 metric
emission tests; none existed. Added all of them in `controller_restart_test.go`
(increment RestartCount, increment ControllerRestartCount, ConsecutiveFailures
unchanged, 6-consecutive-triggers-SafeMode, stability-resets-count) plus
emission tests for `WorkspaceControllerRestartsTotal` and the SafeMode
entry/active metrics.

### M10 — dead metric removed (MEDIUM)

`controller/internal/metrics/metrics.go`: `WorkspaceConsecutiveFailuresMax`
was registered but never written (verified zero writers repo-wide). Proper
wiring would require a global rescan to track a true max — over-engineering
for an unused metric. Removed the declaration + AllCollectors registration;
updated `TestAllCollectorsGatherableAfterRegisterWith`.

### A3 + M9 — documentation (LOW)

- **A3** `api/internal/handlers/workspace_env.go`: documented the implicit
  `(nil, nil)` not-found contract of `GetSecretByName` on the
  `WorkspaceEnvService` interface (the handler branches on `existing != nil`,
  so a `(nil, ErrNotFound)` implementation would silently break env creation).
- **M9** `controller/internal/workspace/phase_active.go`: added a comment
  clarifying that the password-secret self-heal recycle deliberately does NOT
  increment `ControllerRestartCount` (US-24.7: that counter is health-check
  only). Validated as by-design, not a bug.

### Adversarial findings surfaced during review (Rule 11)

- **LastStableAt stale-on-restartGeneration-bump (pre-existing bug, NEW
  finding):** while validating C1/M7 I found that the restartGeneration bump
  cleared `ConsecutiveFailures`/`ControllerRestartCount` but left
  `LastStableAt` stale. A subsequent failure would then see an elapsed
  stability window and be prematurely forgiven. Fixed: the bump now clears
  `LastStableAt`. Regression test
  `TestRestartGeneration_InCreating_ClearsLastStableAt`.
- **G115 on `calculateBackoff` (pre-existing lint):** `recovery_policy.go`
  tripped gosec G115 (int→uint). Refactored to `BackoffBase << shift` —
  bit-identical for the bounded shift [0,30] and removes the conversion
  entirely (proper fix, not a nolint suppression).

### H8 — resolved document-only

The `stability_reset` SafeMode-exit label referenced in older worklogs is
intentionally absent: US-24.13 (authoritative) states SafeMode exits only via
restartGeneration bump or resume. The conflicting US-24.8 AC 3 is superseded.
No code reference to `stability_reset` exists. Recorded here (worklogs are
append-only, so worklog 0369 cannot be edited).

---

## Key Decisions

1. **M6 implemented despite partial SafeMode subsystem.** The SafeMode flag
   has real consumers (startup count in main.go, restartGeneration clear,
   termination, metrics) but `buildSafeModePod`/`safeModeImage`/the
   handleActive SafeMode early-return (US-24.13 AC 1-8) are NOT implemented —
   so the EXISTING ConsecutiveFailures→SafeMode trigger also sets the flag
   without building a special pod. M6 is consistent with that existing
   behavior and completes the US-24.7 detection side; the safe-mode pod side
   remains a separate pre-existing US-24.13 gap (out of scope here, noted for
   follow-up).
2. **M10 removed rather than wired.** Correct max-tracking needs a global
   rescan; an unused metric that always reads 0 is misleading. Removal is
   sanctioned by the worklog and Rule 5.
3. **H7 help-text-only.** Did not rename `llmsafespace_workspaces_failed_total`
   (would break dashboards); the worklog scoped this to help text. The
   name/help slight mismatch is a known residual.
4. **Design-decision items NOT guessed (Rule 6):** H1/H2/H3/M3 (AgentClient
   interface shape — coupled US-29.2/29.3 refactor), H6 (EstimatedMemoryMB
   formula/name — needs domain calibration or cross-component rename), M5/A4
   (envtest infrastructure — large investment), C3 (contract test
   restructure), M1/M2/M8/M11/M12/M13 (smaller refactors/perf). Surfaced to
   the user for a decision rather than implemented unilaterally.
5. **A2 confirmed FALSE ALARM:** `passwordGetter` was already removed from
   `SecretsHandler` in PR #251; the worklog's stated justification was stale
   but the conclusion (remove it) was already satisfied. No action needed.

---

## Blockers

None for the completed fixes. The design-decision items (Key Decision 4) are
blocked on user direction, not on technical unknowns.

---

## Tests Run

```bash
# Per-change TDD (red → green) for C2, H4, C1/M7/M6, H5, LastStableAt
go test -timeout 30s -race -run "<each new test>" ./...

# Touched packages, full, with -race — ALL GREEN
go test -timeout 180s -race ./controller/...                      # all controller
go test -timeout 180s -race ./api/internal/app/ ./api/internal/handlers/ ./pkg/agent/...

# Lint (golangci-lint v2.0.2) on touched packages — 0 issues
golangci-lint run ./controller/internal/workspace/... ./controller/internal/metrics/ \
                  ./api/internal/app/... ./api/internal/handlers/... ./pkg/agent/opencode/...

# go vet + gofmt — clean
go vet ./controller/internal/workspace/... ./api/internal/app/... ...
go build ./...   # whole repo — clean
```

Note: `golangci-lint` is not preinstalled in this environment; installed
v2.0.2 to a writable GOBIN (`/tmp/gobin`) to run the repo's `make lint`
equivalent. CI should re-run with the repo's pinned version.

---

## Next Steps

1. **User decision required** on the flagged design-decision items (Key
   Decision 4). Recommended order if pursued:
   - **M11/M12** (share one `http.Client` across `WorkspaceClient.resolve`)
     — contained perf fix, high value.
   - **H1/H2/M3** (return typed `[]Model` from `AgentClient`; split caller-
     shaped interface; make `resolveModelFromCatalog` a method) — coherent
     US-29.2/29.3 follow-on.
   - **H6** (decide: recalibrate `estimateSessionMemoryMB` against real KV
     cache cost, OR rename `EstimatedMemoryMB` → relative-weight and update
     CRD/agentd/API/frontend) — cross-component.
   - **M5/A4** (introduce envtest for CRD-default-sensitive code) — infra.
   - **C3** (restructure `contract_auth_test.go` to per-route assertions) —
     test quality.
2. **SafeMode pod infrastructure** (US-24.13 AC 1-8: `buildSafeModePod`,
   `safeModeImage`, handleActive SafeMode early-return) remains a pre-existing
   gap; now that both entry triggers (ConsecutiveFailures + ControllerRestart)
   are wired, completing the pod side would make SafeMode user-visible.
3. Open a PR for this branch (`fix/worklog-372-validation-findings`) per the
   mandatory branch-and-PR workflow once the user confirms scope.

---

## Files Modified

Production code:
- `pkg/agent/opencode/client.go` (C2: 32 MiB limit + constant)
- `api/internal/app/app.go` (H4: port constant + LimitReader + seam)
- `api/internal/handlers/workspace_env.go` (A3: nil-contract doc)
- `controller/internal/metrics/metrics.go` (H7 help text; M10 removal)
- `controller/internal/workspace/recovery_policy.go` (C1 guard; G115 fix)
- `controller/internal/workspace/phase_creating.go` (M7 + LastStableAt clear)
- `controller/internal/workspace/phase_suspend.go` (H5 Dec + F22)
- `controller/internal/workspace/phase_active.go` (M9 clarifying comment)
- `controller/internal/workspace/health.go` (M6 trigger + restartAgentPod helper)

Tests:
- `pkg/agent/opencode/client_listmodels_test.go` (NEW — C2)
- `api/internal/app/relay_checker_test.go` (NEW — H4)
- `controller/internal/workspace/controller_restart_test.go` (NEW — C1/C4/M6/M7/C5/LastStableAt)
- `controller/internal/workspace/recovery_wiring_test.go` (H5: seed gauge + CRC assertion)
- `controller/internal/metrics/metrics_test.go` (M10: drop removed metric)
