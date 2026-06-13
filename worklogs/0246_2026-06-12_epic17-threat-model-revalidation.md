# Worklog: Epic 17 Threat Model Re-Validation

**Date:** 2026-06-12
**Session:** Re-validate threat model and security review against actual code state; identify new gaps
**Status:** Complete

---

## Objective

The threat model (THREAT-MODEL.md v1.4) and security review (epic-17 README) were internally inconsistent — the revision history claimed 19 gaps closed but individual entries still showed "Open". Re-validate every gap against actual code, rewrite both documents to reflect verified state, and find any new gaps not in G1-G32.

---

## Work Completed

### Rewrote THREAT-MODEL.md (v1.4 → v2.0)

- Updated 12 gaps from stale "Open" to "Fixed" with verified code evidence: G5, G8, G11, G12, G15, G18, G19, G22, G24, G26, G27, G31
- Updated all 5 attack trees to reflect current mitigations (tmpfs-backed emptyDir, dual-key revocation, namespace-scoped RBAC, RuntimeDefault seccomp)
- Rewrote STRIDE table to remove stale references
- Re-validated all 10 assumptions against code
- Removed stale file:line references to deleted controller.go (now pod_builder.go)
- Added implementation status summary table (18 fixed, 7 open, 7 accepted)

### Rewrote epic-17 README.md

- Restructured pre-pentest remediation into three sections: Fixed (16), Open (9), Accepted (7)
- Updated all RT-* test case tables with strikethrough for fixed items
- Removed dependency on design docs and inline comments — all claims trace to code

### Found 15 new gaps (G33-G47)

Full details in `design/stories/epic-17-security-review/security-report-g33-g47.md`.

---

## Key Decisions

- Only updated gap statuses where code evidence was directly verified (read the file, saw the fix, confirmed the regression test exists). Did not trust the v1.4 revision history claims without re-reading code.
- Did not add findings for areas where source code was absent (MCP server — no `cmd/mcp/` directory found).
- Classified G33 as Critical based on the combination of: (a) the router comment explicitly claiming ownership check exists, (b) the proxy code having a `c.Get("workspace")` fallback path implying a middleware was planned, (c) other handlers (terminal, models) having the check, confirming this is an oversight not a design choice.

---

## Blockers

None.

---

## Tests Run

Could not run tests (build environment out of disk space). All verification was done by reading source files directly.

---

## Next Steps

- Fix G33 (proxy ownership check) — one-liner in `proxyToWorkspace` and `StreamEvents`
- Fix G34 (header forwarding) — strip sensitive headers before forwarding to sandbox
- Fix G35 (RecoverAccount rate limit) — move endpoint behind auth rate limiter
- Fix G36 (secret cleanup on deletion) — call `deleteEphemeralSecretsSecret` from `handleTerminating`
- Fix G37 (env var blocklist) — reject dangerous names (LD_PRELOAD, PATH, etc.)
- Fix G38 (session invalidation on password change) — call RevokeToken for all active sessions
- Address G39-G47 as prioritized
- Decide fate of `design/0027` (composable security policy) — implement or formally cancel

---

## Files Modified

- `design/stories/epic-17-security-review/THREAT-MODEL.md` — full rewrite (v2.0)
- `design/stories/epic-17-security-review/README.md` — full rewrite
- `design/stories/epic-17-security-review/security-report-g33-g47.md` — new file
- `worklogs/0235_2026-06-12_epic17-threat-model-revalidation.md` — this file
