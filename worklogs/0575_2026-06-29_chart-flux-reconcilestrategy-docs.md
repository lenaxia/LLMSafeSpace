# Worklog: Document FluxCD reconcileStrategy trap (#456)

**Date:** 2026-06-29
**Session:** Fix issue #456 (option 1) — document the GitOps chart-freshness requirement that, when missed, makes FluxCD package the chart once and never re-package.
**Status:** Complete

---

## Objective

`Chart.yaml` has `version: 0.1.0` pinned intentionally (the chart is consumed from Git, not published to a registry). FluxCD's `source-controller` caches the packaged chart artifact keyed on the chart version. With the **default** `reconcileStrategy: ChartVersion`, the artifact is built once and re-used forever — so every `helm upgrade` after the first renders against a stale snapshot. New templates, new ConfigMap keys (e.g. new SQL migrations bundled via `(.Files.Glob "migrations/*.sql")`), new RBAC — none reach the cluster.

This caused the 2026-06-29 incident: the migrations ConfigMap still held only 000001 after PR #451 added 000002–000004, and it masked #455 (the new migrate-Job args never ran). Issue #456's recommended fix is documentation (option 1), with OCI-publish as a long-term follow-up (option 2).

---

## Assumptions (stated + validated)

1. **FluxCD `source-controller` caches the packaged chart keyed on Chart.yaml `version`.** → Validated: issue #456 reproduces with `kubectl get configmap <release>-migrations` showing only 000001 after #451 merged; the talos-ops-prod fix (`c914dc2e`) set `reconcileStrategy: Revision` and the ConfigMap refreshed.
2. **`reconcileStrategy: Revision` forces a re-package on every git revision.** → Validated: documented FluxCD behaviour; the talos-ops-prod regression (`1d6975f9` removed the line) + re-fix (`57a53dce`) confirm both directions.
3. **`helm show chart` surfaces Chart.yaml `description:` before the README.** → Validated: standard Helm behaviour; the description is the first thing a consumer sees without cloning.

---

## Work Completed

- **`README.md`**: added a prominent `## ⚠ GitOps deployment (FluxCD / Argo CD)` section immediately after the intro (before Prerequisites). Explains the trap (pinned version → `ChartVersion` packages once), the symptom (stale migrations ConfigMap), a copy-paste `HelmRelease` spec with `reconcileStrategy: Revision`, the Argo CD equivalent, and the long-term OCI-registry alternative. Links #455 and #456.
- **`Chart.yaml` `description:`**: extended so `helm show chart` warns consumers before they clone — flags the pinned version + the `reconcileStrategy` requirement, pointing at the README.
- **`chart_gitops_doc_test.go`** (new): two doc-presence regression tests.
  - `TestChart_ReadmeDocumentsFluxReconcileStrategy` — README must name `reconcileStrategy`, `Revision`, the `0.1.0` pin, Flux/`GitRepository`, and include a copy-pasteable `chart:` spec.
  - `TestChart_ChartYamlDescriptionReferencesGitOps` — Chart.yaml `description:` must flag the pin and reference GitOps/`reconcileStrategy`. Includes a small block-scalar parser for the `description:` field.
  - Rationale for the test: documentation is the deliverable, but an unguarded doc section can be silently deleted — which is exactly how `reconcileStrategy: Revision` was lost in talos-ops-prod `1d6975f9`. A presence-test forces deliberate edits (mirrors how the codebase pins dashboard UIDs).

### Adversarial self-review (Rule 11)

Reviewed: does documentation alone fix it? No — it relies on the operator reading it, which the issue itself flags as "cheap; relies on the operator reading docs." That is the explicit accepted trade-off of option 1; option 2 (CI version bump / OCI publish) is the robust fix and is tracked as a follow-up. The test pins the doc so it can't silently vanish. Zero real findings.

---

## Key Decisions

- **Option 1 (documentation) per the issue recommendation and user direction.** Option 2 (a `chart-version-bump.yaml` GitHub Action or OCI publish) is deferred — the issue flags downstream pre-release-string validation as a caveat requiring a human decision.
- **Doc-presence test, not just prose.** Silent deletion of the Flux `reconcileStrategy: Revision` line already caused a 2.5-day regression in talos-ops-prod. A test that fails if the section is removed makes the documentation a first-class contract.
- **Section placement: immediately after the intro, before Prerequisites.** The trap hits on first deploy, so it must be seen before the operator runs `helm install`. Buried at the bottom of a long README would re-create the "didn't read it" failure mode.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 180s ./charts/llmsafespaces/                            # PASS (full suite)
go test -timeout 120s -run 'TestChart_' -v ./charts/llmsafespaces/        # 2/2 PASS
helm lint ./charts/llmsafespaces                                         # 0 failed
gofmt -l <changed files>     # clean
go vet ./charts/llmsafespaces/   # clean
```

---

## Next Steps

- Address automated-reviewer findings; iterate to APPROVE; squash-merge.
- After merge: confirm the consuming Flux HelmRelease carries `reconcileStrategy: Revision` (already restored in talos-ops-prod `57a53dce`); the docs now make the requirement discoverable upstream for all consumers.
- Long-term (#456 option 2): OCI publish to ghcr.io or a CI version-bump Action to make the trap impossible regardless of consumer config.
- Companion PR: #457 (migrate Job CLI args, issue #455).

---

## Files Modified

- `charts/llmsafespaces/Chart.yaml`
- `charts/llmsafespaces/README.md`
- `charts/llmsafespaces/chart_gitops_doc_test.go` (new)
- `worklogs/0575_2026-06-29_chart-flux-reconcilestrategy-docs.md` (this worklog)
