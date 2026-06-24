# Worklog: #87 — runtime-base smoke-test diagnostics + pip fallback

**Date:** 2026-06-24
**Session:** Diagnose/fix the amd64-only Build Runtime Base smoke-test failure
**Status:** Complete

---

## Objective

#87: `Build Runtime Base (linux/amd64)` fails in CI with a bare exit 1 — the
smoke test used `set -euo pipefail` and exited on the first failed `which`/
`mise which` with no per-check output, so the failing binary was invisible.
arm64 passes; the failure is amd64-specific.

---

## Work Completed

Rewrote `runtimes/base/tools/smoke-test.sh`:

- **Per-check reporting + run-all**: each check prints `OK`/`FAIL` and ALL
  checks run (no early exit). A summary lists every failure, so the next CI
  run pinpoints the exact missing component instead of a bare exit 1.
- **`pip` fallback**: `mise which pip || mise which pip3` — Python on amd64
  frequently ships `pip3` without a `pip` shim. This was the #1 suspect in the
  prior investigation's comment; the fallback accepts either.
- **`mise ls` diagnostic**: prints all installed mise tools/versions before
  the checks, so a missing tool is visible at a glance.
- **Soft JVM tools**: `java`/`maven`/`gradle` now print `WARN` (never fail the
  build), matching the Dockerfile which installs them best-effort
  (`|| echo WARN`, Dockerfile:275-279). Previously a missing JVM tool would
  have hard-failed the smoke test even though the Dockerfile tolerates it.

All other checks (internal binaries, apt tools, DB clients, cloud CLIs, the
HARD mise runtimes) are unchanged in intent — just individually reported.

---

## Key Decisions

- **Run-all, not fail-fast.** The whole point is diagnosis; exiting on the
  first failure hid the (possibly multiple) problems.
- **Soft JVM matching the Dockerfile.** The Dockerfile explicitly tolerates
  JVM install failure on some architectures; the smoke test must not be
  stricter than the build that produced it.
- **Can't run Docker locally.** Verified bash syntax (`bash -n`) and exercised
  the `verify`/`verify_any`/summary logic with mock commands (pass, fail,
  fallback-pass, fallback-fail → correct counts). CI's Build Runtime Base job
  is the real test: it either now passes (pip was the issue) or reports the
  exact failing check.

---

## Blockers

None (CI will confirm).

---

## Tests Run

- `bash -n smoke-test.sh` — pass.
- Mock logic test (verify/verify_any/summary with true/false/fallback) — pass
  (correct pass/fail accounting and fallback semantics).

---

## Next Steps

- If CI passes → #87 closed (pip was the culprit).
- If CI still fails → the summary now names the failing check; open a targeted
  follow-up for that specific tool.

---

## Files Modified

- `runtimes/base/tools/smoke-test.sh` — rewritten (per-check reporting, pip
  fallback, mise ls diagnostic, soft JVM).
- `worklogs/0540_2026-06-24_issue-87-smoke-test-diagnostics.md` — this file.
