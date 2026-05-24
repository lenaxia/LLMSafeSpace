# US-7.8: Delete Legacy Runtime Artifacts

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** None (can be done first or last)

## Objective

Remove all V1 multi-image runtime artifacts that are no longer built, referenced, or used.

## Deletions

| Path | Reason |
|------|--------|
| `runtimes/python/` | V1 Python image — never built by CI since worklog 0032 |
| `runtimes/nodejs/` | V1 Node.js image — never built by CI |
| `runtimes/go/` | V1 Go image — never built by CI |
| `runtimes/tests/` | Tests for the above dead images |
| `design/RUNTIMEENV.md` | V1 design doc — superseded by this epic |
| `controller/examples/runtimeenvironment.yaml` | References `llmsafespace/runtime-python:3.10` which doesn't exist |

## Files to Migrate Before Deletion

These files contain useful policy content that should be moved to `runtimes/base/policies/` (US-7.7) before deletion:

| Source | Destination | Content |
|--------|-------------|---------|
| `runtimes/python/security/python/restricted_modules.json` | `runtimes/base/policies/python/restricted_modules.json` | Module blocklist |
| `runtimes/python/security/python/sitecustomize.py` | `runtimes/base/policies/python/sitecustomize.py` | Import hook |
| `runtimes/python/tools/python-security-wrapper.py` | `runtimes/base/policies/python/wrapper-reference.py` | Reference impl (for porting to Go wrapper logic) |
| `runtimes/nodejs/security/nodejs/restricted_modules.json` | `runtimes/base/policies/nodejs/restricted_modules.json` | Module blocklist |
| `runtimes/nodejs/tools/nodejs-security-wrapper.js` | `runtimes/base/policies/nodejs/restrict.js` | Require hook |
| `runtimes/go/security/go/` | `runtimes/base/policies/go/` | Restricted packages |

## Acceptance Criteria

1. `runtimes/python/`, `runtimes/nodejs/`, `runtimes/go/`, `runtimes/tests/` deleted
2. `design/RUNTIMEENV.md` deleted
3. `controller/examples/runtimeenvironment.yaml` deleted
4. Policy files migrated to `runtimes/base/policies/`
5. `make test` passes (no references to deleted paths)
6. `grep -r "runtime-python\|runtime-nodejs\|runtime-go" .` returns zero results (excluding git history)
