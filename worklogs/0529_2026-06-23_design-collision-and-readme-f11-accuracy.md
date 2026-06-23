# Worklog: Design-doc number collision + README F11 accuracy fix

**Date:** 2026-06-23
**Session:** Doc cleanup surfaced while reviewing/merging PR #327 and closing PR #256
**Status:** Complete

---

## Objective

Close two low-risk correctness gaps found incidentally during the PR #327/#256 review cycle:

1. A `design/` number collision (`0037` used by two distinct documents).
2. An overstated README-LLM.md changelog claim implying the F11 header-trust gap is closed by default.

---

## Work Completed

### Design-doc number collision (0037)

- `design/0037_2026-06-18_epic-46-codebase-debt-audit.md` was renumbered to
  `design/0044_2026-06-18_epic-46-codebase-debt-audit.md`. `0037` was held by
  two unrelated documents: `frontend-busy-indicator-consistency-analysis`
  (2026-06-16) and `epic-46-codebase-debt-audit` (2026-06-18). The newer
  document (epic-46) was moved to the next free number, `0044`.
- Updated the 3 **live** navigation links in
  `design/stories/epic-46-codebase-debt-audit/README.md` (parent-doc link +
  two anchor links) that would otherwise have become dead links.
- Verified `ls design/ | grep -oE "^[0-9]{4}" | sort | uniq -d` returns empty
  after the rename — no remaining design number collisions.

### README-LLM.md F11 accuracy (line 1680)

- The v1.17 changelog entry said the OIDC plumbing config was added "closing
  the F11 header-trust gap in chart-managed deploys." This is overstated:
  `values.yaml` ships `oidc.redirectBaseUrl: ""` (default empty), so the
  header-trust path (`org_sso.go`) still runs by default. The gap is
  *closeable* by operators who set the value, not closed by default.
- Reworded to: "exposing the F11 header-trust mitigation (operators set
  `oidc.redirectBaseUrl` to close it; ships default-empty so the gap remains
  open in unconfigured deploys)."
- The other two README mentions (config table line 1565 "**Set this in
  production** to remove header trust" and security-controls table line 1551)
  were already accurate and left unchanged.

---

## Key Decisions

- **Renumber the newer doc, not the older.** `frontend-busy-indicator` (06-16)
  is the original holder of `0037`; the epic-46 doc (06-18) is the late
  arrival that caused the collision, so it moves. This minimises disruption
  to the earliest reference.
- **Do NOT retroactively edit historical worklog path references.** Several
  already-merged worklogs (0337, 0341, 0345, 0347, 0348, 0349, 0354) reference
  `design/0037_...epic-46...` in their "Files Modified" tables. Per README
  worklog discipline rule 7 ("Never retroactively rewrite a worklog —
  append-only history"), these were left as-is — the paths were correct at
  the time of writing, and worklogs are historical narrative, not navigation
  surfaces. Only **live** design-doc links (the story-folder README) were
  updated. (Contrast with PR #327, where worklog 0465's path refs were
  updated because 0465 was that PR's own unmerged worklog.)

---

## Blockers

None.

---

## Tests Run

- `go build ./...` — pass (no code changes; sanity only).
- `make repolint` — pass (migrations, worklogs, chart, CRD-drift all OK;
  design-doc numbering is not repolint-enforced, only worklog/migration
  numbering is).
- `git status` / `ls design/ | uniq -d` — confirms single remaining
  collision-free state.

No functional code changed; no unit/integration tests applicable.

---

## Next Steps

- F11 itself (default-on `X-Forwarded-*` header trust when
  `oidc.redirectBaseUrl` is unset) remains a LOW-severity, IdP-mitigated
  residual. A dedicated follow-up should pick a fix shape: **fail-loud** at
  runtime when unset in non-dev mode, vs. derive a chart default from the
  ingress host. Awaiting owner decision before implementing.

---

## Files Modified

- `design/0037_2026-06-18_epic-46-codebase-debt-audit.md` → renamed to
  `design/0044_2026-06-18_epic-46-codebase-debt-audit.md`
- `design/stories/epic-46-codebase-debt-audit/README.md` — 3 parent-doc links
  updated 0037 → 0044
- `README-LLM.md` — line 1680 (v1.17 changelog F11 wording corrected)
- `worklogs/0529_2026-06-23_design-collision-and-readme-f11-accuracy.md` —
  this file
