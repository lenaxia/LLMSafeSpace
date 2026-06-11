# Worklog 0209 — Epic 36: PR Review Remediation

**Date:** 2026-06-10
**Session:** Address automated PR review findings (hand-rolled tests, CRD drift); fix pre-existing CI failures (Runtime Base, worklog conflicts, migration conflicts)
**Status:** Complete

---

## Objective

Open a PR for Epic 36, monitor the automated PR Review workflow, and fix any findings it returns. Also fix all pre-existing CI failures per Rule 5 (zero technical debt — pre-existing is not an excuse).

---

## Work Completed

### PR Creation

Created PR #90 (`feat/epic-36-context-usage` → `main`) containing the cherry-picked Epic 36 commits:
- `b899cb7c` — feat(epic-36): main implementation
- `bd0a259a` — fix(repolint): worklog + CRD drift
- `c6c94634` — fix(fmt): gofmt
- `c42f197f` — fix(runtime): pin python@3.13 (superseded)
- `c921519d` — fix(runtime): pin python@3.12 + retry logic

### Automated PR Review — Round 1

**Findings (REQUEST CHANGES):**

1. **Four hand-rolled statusz tests** (`main_test.go:226-547`) duplicated buildStatuszHandler logic instead of calling it. The F1 fix from the initial session added `TestBuildStatuszHandler_*` integration tests but left the 4 old tests as inline copies.

2. **CRD drift in pkg/crds/workspace_crd.yaml** — `sessions.items` was missing `status` (pre-existing drift) and `contextUsed` (new field).

**Fixes applied:**

1. Rewrote all 4 tests to call `buildStatuszHandler()` via helpers:
   - `newOpenCodeTestServer()` — shared mock opencode server
   - `newStatuszTestFixture(t, opencodeSrv)` — sets up client/cache/tracker with t.Cleanup
   - Disk assertions changed from hardcoded `Equal(100)` to `Greater(t, 0)` since `buildStatuszHandler` calls real `getDiskUsage()`
   - Removed ~215 lines of dead duplicated handler code

2. Added `status` (enum: idle/busy) and `contextUsed` (int64) to `pkg/crds/workspace_crd.yaml` sessions.items.properties.

### Pre-existing CI Failures Fixed (Rule 5)

#### Runtime Base Docker Build — Python 3.14/3.13 pip bootstrap failure
- **Root cause:** `python@latest` (3.14.6) and `python@3.13` both fail pip bootstrap during `mise install --system` — `pip-26.1.2` wheel install via freshly-built Python returns non-zero exit.
- **Fix:** Pinned to `python@3.12` which builds cleanly. Moved `python@3.13` to non-fatal list alongside java/maven/gradle.
- **Bash retry logic bug:** The `|| { echo "FATAL..."; exit 1; }` after the inner retry `for` loop never fired because `sleep 5` (last command on failed attempt) returns exit code 0. Replaced with explicit `[ "$attempt" = "3" ] && { exit 1; }` check.

#### Worklog Numbering Conflicts (parallel agents)
- `0203_epic37-frontend-phase2.md` collided with `0203_pr74-session-delete-regression-tests.md` → renumbered to 0208
- `0202_epic25-production-bugs.md` collided with `0202_session-delete-kebab-hover.md` → renumbered to 0209
- `0203_us-10-13-api-key-at-rest-encryption.md` collided with `0203_pr74-session-delete-regression-tests.md` → renumbered to 0210
- Cross-checked with local repolint: sequence now contiguous 0201→0210

#### CRD Drift — Helm chart vs Go types
- `pkg/apis/llmsafespace/v1/workspace_types.go::AgentSessionStatus.ContextUsed` added to Go struct but missing from `charts/llmsafespace/crds/workspace.yaml` sessions.items → added

#### Migration Conflict
- `000020_session_last_seen` collided with `000020_api_key_at_rest_encryption` (parallel agent) → renumbered to 000021 in both `api/migrations/` and `charts/llmsafespace/migrations/`

### Automated PR Review — Round 2

After fixes pushed, re-review returned **APPROVE** — both findings resolved.

### Final CI Status

| Job | Status |
|-----|--------|
| Lint (repolint + gofmt + vet + golangci-lint) | ✅ |
| Go tests (-short, coverage) | ✅ |
| Go tests (full suite, race detector) | ✅ |
| Frontend (unit + typecheck + e2e) | ✅ |
| SDK Contract Tests | ✅ |
| Build Frontend (amd64 + arm64) | ✅ |
| Build API (amd64 + arm64) | ✅ |
| Build Controller (amd64 + arm64) | ✅ |
| Build Runtime Base (amd64 + arm64) | ✅ |
| Merge manifests | ✅ |

All jobs pass. Zero pre-existing failures.

---

## Key Decisions

| Decision | Rationale |
|---|---|
| Migrate old tests to buildStatuszHandler rather than deleting | Preserves test coverage history and test names; ~215 lines of dead code removed |
| assert.Greater for disk values | buildStatuszHandler calls real getDiskUsage() — hardcoded values would be fragile |
| Force-push PR branch | Branch is private (no other collaborators) and rebased on main; acceptable per Rule 10 |

---

## Tests Run

| Package | Result |
|---------|--------|
| `cmd/workspace-agentd` — all statusz tests (7) | ✅ |
| `repolint` — all checks | ✅ |
| Frontend full suite (90 files, 840 tests) | ✅ |
| CI full pipeline (all jobs) | ✅ |

---

## Blockers

None.

---

## Files Modified

- `cmd/workspace-agentd/main_test.go` — 4 old tests migrated to buildStatuszHandler; 2 new helpers added
- `pkg/crds/workspace_crd.yaml` — added `status` and `contextUsed` to sessions.items
- `runtimes/base/Dockerfile` — pin python@3.12, fix retry logic, add python@3.13 as non-fatal
- `charts/llmsafespace/crds/workspace.yaml` — added `contextUsed` to sessions.items (repolint CRD drift fix)
- `worklogs/0203_epic36-context-usage-fix.md` → `0206` (renumber)
- `worklogs/0204_epic36-s36.3-s36.4-s36.5-completion.md` → `0207` (renumber)
- `worklogs/0203_epic37-frontend-phase2.md` → `0208` (renumber — existed on remote, re-done locally)
- `worklogs/0202_epic25-production-bugs.md` → `0209` (renumber — prefix collision)
- `worklogs/0203_us-10-13-api-key-at-rest-encryption.md` → `0210` (renumber — prefix collision)
- `api/migrations/000020_session_last_seen.*` → `000021` (renumber)
- `charts/llmsafespace/migrations/000020_session_last_seen.*` → `000021` (renumber)
- `worklogs/0209_2026-06-10_epic36-pr-review-remediation.md` — this file
