# Worklog: Doc Audit — Sync Stale READMEs + Remove V1 References

**Date:** 2026-06-22
**Session:** Pull latest, refresh README.md + README-LLM.md for the relay fleet / master KEK / tenant-isolation work, then audit and fix every other stale doc in the repo.
**Status:** Complete (PR #340, one review iteration)

---

## Objective

After pulling 14 commits (Epic 42 relay fleet, Epic 50 master KEK, Epic 51 tenant isolation, Epic 52 test coverage), the top-level READMEs and the rest of the repo's docs were out of date. Two-part task: (1) update README.md and README-LLM.md as holistic project views, then (2) audit every other doc for factual drift and fix or delete as appropriate.

---

## Work Completed

### Phase 1 — README.md + README-LLM.md refresh (prior session, same branch)

- README.md: CRDs 2 → 3 (added `InferenceRelay`); new Inference Relay subsection; Relay Fleet admin REST API table (9 routes); repository layout updated (relay-router, relay-proxy, controller/internal/relay); build section (relay-router image + `make relay-bin`); security section (gVisor, tenant quotas, KEK file-mount).
- README-LLM.md: version 1.18 → 1.19; TOC + deliverables + repo structure; new Master KEK + Tenant isolation subsections; new Inference Relay Fleet H2 section (components, WG→HTTPS+token transition, feature gate, ops notes); Rule 8 design-doc table; API route inventory; version history.

### Phase 2 — Full repo doc audit

Audited every non-archived, non-node_modules doc. Findings:

**Deleted:**
- `APIIMPLEMENTATION.md` — V1 plan referencing removed WarmPool/Sandbox/Execution services.

**Fixed (factual errors):**
- `cmd/relay-proxy/README.md`, `cmd/relay-router/README.md` — WG mesh → HTTP + `X-Relay-Token`; upstream `ai.thekao.cloud` → `opencode.ai/zen/v1`; peer format `{wgIP,publicKey}` → `{endpoint,token}`.
- `pkg/README.md` — 2 → 3 CRDs; incomplete phase list → all 9.
- `charts/llmsafespaces/README.md` — 2 → 3 CRDs/Deployments; uninstall adds `inferencerelays`.
- `design/stories/README.md` — Epic 51 Not Started → Complete; audit date refreshed.

**Cleaned (freshness):**
- `COORDINATE.md` — pruned 2.5-week-stale tables.
- `pkg/secrets/README.md` — StaticKeyProvider now reflects file-mount default (Epic 50 US-50.1).
- `frontend/README.md` — added missing `org-admin/`, `shared/` dirs.
- `api/migrations/README.md` — removed dead `epic-19/` reference.
- `.github/prompts/context.md` — AI system prompt: fixed CRD list (removed Sandbox/SandboxProfile, added InferenceRelay), expanded cmd/ binaries list (2 → 8), fixed "sandbox" → "workspace" language.
- `charts/llmsafespaces/templates/NOTES.txt` — fixed `kubectl get crd` command (removed sandboxes/sandboxprofiles, added inferencerelays), fixed webhook advisory CRD names.

**Deleted (second pass):**
- `design/story2.1` — V1-era "Implementation Plan for API Service" referencing removed WarmPoolService/SandboxHandler/ExecutionService (same class as `APIIMPLEMENTATION.md`; unreferenced).

### Phase 3 — AI review remediation (1 iteration)

The automated reviewer (`review` CI job) found 3 real issues, all validated against source:

1. **Relay weighting error (README-LLM.md + relay-router README):** Both said "OCI gets 100% of traffic; GCP during OCI failure." Source (`fleet.go:146-151, 265-284`) is **AWS primary (weight 1000), OCI secondary (100), GCP tertiary (1)**. README.md was correct; the two other docs carried a stale pre-existing error that I propagated. Fixed all three to AWS-primary. Also dropped the GCP "(paid)" label (source is internally inconsistent: CRD line 63 says e2-micro is Always Free eligible, line 137 says "optional paid provider").
2. **Missing `UPSTREAM_AUTH_KEY`:** `main.go:54` reads it (the credential value); I only documented `UPSTREAM_AUTH_HEADER` (the header name). Added both; corrected the header description (default empty → `Authorization` sent as `Bearer <key>`, verified at `proxy.go:18,40,59`).
3. **Missing worklog** — this entry.

### Phase 4 — Second review iteration (scope completeness)

The reviewer's second pass accepted all prior fixes but found the audit missed 3 items in its stated scope:

4. **`charts/llmsafespaces/templates/NOTES.txt`** — user-facing `kubectl get crd` command listed removed `sandboxes` + `sandboxprofiles` CRDs (prints a failing command to every operator at install); webhook advisory referenced removed CRDs. Fixed.
5. **`.github/prompts/context.md`** — the AI system prompt listed 4 CRDs including 2 removed ones and only 2 cmd/ binaries (missing 6). This file is the reviewer's own entry point — it directly contradicted the refreshed README-LLM.md. Fixed: CRD list, cmd binaries, "sandbox" → "workspace" language.
6. **`design/story2.1`** — unreferenced 1113-line V1 implementation plan (same class as the already-deleted `APIIMPLEMENTATION.md`). Deleted.

---

## Key Decisions

1. **Historical design docs left unchanged.** Numbered `design/00NN_*.md` are point-in-time architectural records; repo convention treats them as frozen (superseded by evolution-v2 where they conflict). Remaining WG/WarmPool mentions there are correct historical-removal context, not staleness.
2. **COORDINATE.md cleared, not deleted.** It's the intended multi-agent coordination mechanism; only its stale content was the problem.
3. **GCP labeled "optional/tertiary" without paid/free.** Source is ambiguous (`inferencerelay_types.go:63` vs `:137`); the neutral phrasing matches README.md and avoids asserting a fact the codebase itself hasn't settled.

---

## Blockers

None.

---

## Tests Run

Docs-only PR — no code changes. CI verifies: Lint ✓, all migration checks ✓, gitleaks ✓, trivy ✓, govulncheck ✓, frontend ✓, build ✓. Test suites run as standard regression (not doc-affected).

---

## Next Steps

1. Re-run CI after remediation push; confirm `review` job passes.
2. Merge on approval.
3. Future doc audits: run as part of any major feature ship, not as standalone sweeps — drift accumulates fast in this repo.

---

## Files Modified

- `APIIMPLEMENTATION.md` (deleted)
- `design/story2.1` (deleted, second pass)
- `README.md`, `README-LLM.md`
- `cmd/relay-proxy/README.md`, `cmd/relay-router/README.md`
- `pkg/README.md`, `pkg/secrets/README.md`
- `charts/llmsafespaces/README.md`, `charts/llmsafespaces/templates/NOTES.txt`
- `design/stories/README.md`
- `.github/prompts/context.md`
- `frontend/README.md`
- `api/migrations/README.md`
- `COORDINATE.md`
- `worklogs/0474_2026-06-22_doc-audit-stale-readmes-v1-cleanup.md` (this entry)
