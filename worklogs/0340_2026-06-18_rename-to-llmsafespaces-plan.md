# Worklog: Rename `llmsafespace` → `llmsafespaces`

**Date:** 2026-06-18
**Session:** Scope + risk assessment, dry-run, PR for reviewer approval
**Status:** Blocked on review (PR pending)
**Branch:** `docs/rename-to-llmsafespaces`

---

## Objective

User wants to rename the project from `llmsafespace` (singular) to
`llmsafespaces` (plural) across code, infra, and external artifacts.
Asked for: (a) what it would take, (b) risk profile, (c) a PR with the
proposed plan and dry-run results, (d) hold execution pending review.

---

## Context & Key User Decisions

| Question | Decision | Impact |
|---|---|---|
| Is the project live? | **No — fresh deploys, no production data** | Eliminates most risk: CRD migration, image re-publish, SDK breakage, metric-gap, AWS tag churn all drop from 🔴 to 🟢 |
| Rename K8s API group `llmsafespace.dev` → `llmsafespaces.dev`? | **Yes** | Now cheap; no conversion webhook needed |
| Rename GitHub repo `lenaxia/LLMSafeSpace` → `LLMSafeSpaces`? | **Yes** | Keeps Go module path canonical |
| Rename historical docs (`worklogs/`, `design/`)? | **No — leave history alone** | ~2,783 matches excluded from rewrite; preserves audit trail |

---

## Risk Profile (post-decisions)

With no live deployments, residual risk is limited to:

- **🟡 Build/test/lint must stay green** — mitigated by running
  `make manifests test lint` after rewrite.
- **🟡 GitHub repo name → Go module path mismatch** — mitigated by
  renaming the repo so `github.com/lenaxia/llmsafespaces` resolves.
  GitHub auto-redirects old URL; existing clones keep working.
- **🟢 Published SDK npm/PyPI packages keep old name** — republish
  under new name on next release; no installed users yet.
- **🟢 Container images orphaned in registry** — old tags left behind;
  new tags pushed under new path going forward.
- **🟢 Dev-cluster Postgres role/db** — drop+recreate is trivial.

No data-loss, no external-breakage, no conversion-webhook concerns.

---

## Scope (full tree, pre-exclusion)

| Variant | Total matches | In excluded `worklogs/`+`design/` | In active tree |
|---|---:|---:|---:|
| `llmsafespace` (lowercase) | 4,197 | 2,247 | **1,950** |
| `LLMSAFESPACE` (ALL_CAPS env vars) | 536 | 186 | **350** |
| `LLMSafeSpace` (MixedCase, repo URL) | 850 | 352 | **498** |
| **Active-tree total** | | | **2,791** |

Pre-rename sanity check: **zero** existing occurrences of `llmsafespaces`
/ `LLMSafeSpaces` in tree — three case-variant rules are disjoint, so
order-independent and no word-boundary guarding needed.

---

## Categories Touched (active tree)

| Category | Examples | Files |
|---|---|---:|
| **Go module identity** | `go.mod` + 319 `.go` import paths; `sdk/go/go.mod` | ~320 |
| **K8s API surface** | `pkg/apis/llmsafespace/v1/{register,doc}.go` (group `llmsafespace.dev`); 3 CRDs in `charts/.../crds/`; annotations `relay.llmsafespace.dev/{rotate,paused}`; label `llmsafespace.com/workspace` | ~25 |
| **Helm chart** | `charts/llmsafespace/{Chart.yaml,values.yaml,_helpers.tpl,templates/*}`; release name in Makefile | ~15 |
| **Container images** | `runtimes/*/Dockerfile` (`ghcr.io/lenaxia/llmsafespace/base:latest` chain); in-image paths `/opt/llmsafespace/bin`, `/etc/llmsafespace/...`; CI image vars in `.github/workflows/ci.yml` | ~15 |
| **Prometheus metrics** | ~40 metric names `llmsafespace_*`; 42 metric refs in `charts/.../dashboards/{billing,operational}.json`; PromQL in `relay_admin.go` alert expressions | ~10 |
| **Env vars** | 261 `LLMSAFESPACE_*` occurrences across Go/YAML/TS/shell | ~30 |
| **SDK packages** | npm `@llmsafespace/sdk`, `vscode-llmsafespace`; PyPI `llmsafespace`; CF Worker `llmsafespace-inference-relay`; module path in `sdk/go/go.mod` | ~25 |
| **Infra/data identity** | Postgres role/db `llmsafespace` (Makefile, CI, `local/*`); AWS tag value `llmsafespace-relay`; agent cache `$HOME/.local/state/llmsafespace`; browser localStorage key `llmsafespace_user_settings` | ~15 |
| **Build glue** | Makefile `BINARY_NAME`, `CHART_DIR`, `RELEASE_NAME`, `RELEASE_NS`; gitleaks/security-scan skip paths in 5 workflows | ~10 |
| **Other docs/prose** | `README.md`, `README-LLM.md`, `COORDINATE.md`, `APIIMPLEMENTATION.md`, `pkg/README.md`, story docs (outside `design/` exclusion) | ~190 |

---

## Approach

A single scripted rewrite, executed in 3 phases:

### Phase 1 — Directory renames (`git mv`)
Three dirs whose name is part of an import path / chart name / package id:
- `pkg/apis/llmsafespace` → `pkg/apis/llmsafespaces`  (10 tracked files)
- `charts/llmsafespace` → `charts/llmsafespaces`  (115 tracked files)
- `sdks/vscode-llmsafespace` → `sdks/vscode-llmsafespaces`  (16 tracked files)

### Phase 2 — Content rewrites
Apply 3 case-sensitive rules to every tracked text file outside the
exclusion set:

```
llmsafespace  →  llmsafespaces     # module path, CRD group, metrics, env snake,
                                   #   PG role, image repo, binary name, dirs
LLMSAFESPACE  →  LLMSAFESPACES     # env vars (LLMSAFESPACE_*)
LLMSafeSpace  →  LLMSafeSpaces     # repo URL, prose headers
```

**Excluded** (per user decision): `worklogs/`, `design/`, `.git/`,
`bin/`, `node_modules/`, plus binary files (`workspace-agentd`,
`redact`, `tools`) and lockfiles (`go.sum`, `package-lock.json` —
regenerated).

Implementation: `perl -i -pe 's/\Q$pat\E/$rep/g'` — `\Q…\E` quotes the
literal pattern, so no regex-escape concerns.

### Phase 3 — Post-rewrite regeneration
Manual steps (cannot be safely scripted into the rename commit):
```
go mod edit -module github.com/lenaxia/llmsafespaces
(cd sdks/go && go mod edit -module github.com/lenaxia/llmsafespaces/sdk/go)
go mod tidy
(cd sdks/go && go mod tidy)
make manifests          # regenerate CRD YAML + zz_generated.deepcopy.go
make test lint          # verify
```

### Phase 4 — External manual steps
1. **GitHub repo rename:** `lenaxia/LLMSafeSpace` → `lenaxia/LLMSafeSpaces`
   (auto-redirects old URL; existing clones keep working).
2. **ghcr.io registry:** future pushes use new repo path; old tags orphaned.
3. **npm:** publish `@llmsafespaces/sdk` and `vscode-llmsafespaces` on
   next release; old packages deprecated.
4. **PyPI:** publish `llmsafespaces` on next release; old yanked.
5. **Cloudflare Worker:** rename `llmsafespace-inference-relay` →
   `llmsafespaces-inference-relay` in `wrangler.toml` + redeploy.
6. **Dev-cluster Postgres:** drop+recreate DB/role as `llmsafespaces`
   (or update `values.yaml` + `local/bootstrap.sh`).

---

## Dry-Run Results

The script `hack/rename-to-llmsafespaces.sh` was run with `DRY_RUN=1`
against `main` @ `16f336b9`. Full output captured in
`hack/rename-to-llmsafespaces.dryrun.txt`. Summary:

| | Count |
|---|---:|
| Directories to `git mv` | 3 (141 files total) |
| Files needing content edits | **612** |
| Total line-level matches | **2,791** |
| Stray-named files needing manual review | **0** |

Top 10 files by match count:
1. `charts/llmsafespace/templates/prometheus-rules.yaml` — 65
2. `sdks/vscode-llmsafespace/package.json` — 60
3. `.github/workflows/ci.yml` — 57
4. `charts/llmsafespace/templates/rbac.yaml` — 50
5. `api/internal/app/app_master_key_test.go` — 49
6. `charts/llmsafespace/dashboards/operational.json` — 44
7. `api/internal/config/config.go` — 43
8. `local/test.sh` — 42
9. `sdks/canary/TESTPLAN.md` — 38
10. `controller/internal/metrics/metrics.go` — 33

Zero stray files outside the 3 known renamed dirs needed manual
`git mv` — the rewrite is fully mechanical.

---

## Items Flagged for Reviewer Attention

1. **Metric-name flip.** All `llmsafespace_*` Prometheus metrics become
   `llmsafespaces_*`. Pre-existing Grafana snapshots/dashboards exported
   out-of-tree would become unreadable. Acceptable since project is not
   live, but reviewers should confirm no external dashboard consumers
   exist.

2. **Annotation/label keys change.** `relay.llmsafespace.dev/{rotate,paused}`
   and label `llmsafespace.com/workspace` flip. No CRs in etcd to worry
   about (fresh deploy), but reviewers should confirm no offline
   runbooks reference the old keys.

3. **Postgres role rename is destructive.** Script updates the source
   (Makefile, CI, `local/*`) but the DB/role itself must be
   drop+recreated in any existing dev cluster — not auto-migrated.
   Reviewer to confirm dev-cluster reset is acceptable.

4. **History docs untouched.** `worklogs/` and `design/` retain old
   name as historical record. Result: `git log` shows mixed naming.
   This was an explicit user decision, not an oversight.

5. **VS Code extension marketplace ID.** The extension will need to be
   re-published under a new marketplace ID (`vscode-llmsafespaces`);
   the old ID cannot be renamed in-place. Out of scope for this PR
   (deferred to release cut).

---

## Next Steps

1. **Reviewers:** Inspect `hack/rename-to-llmsafespaces.sh` (script) and
   `hack/rename-to-llmsafespaces.dryrun.txt` (full dry-run output).
2. **On approval:** Open a follow-up PR that runs `DRY_RUN=0` and
   includes Phase 3 regeneration (`go mod tidy`, `make manifests`).
   The `hack/rename-*` artifacts in this PR can be deleted in that
   follow-up (or retained as one-shot tooling — reviewer preference).
3. **Phase 4 external steps** are owned by the repo admin (GitHub
   rename, registry push, package publishes) and run out-of-band.

---

## Artifacts in this PR

- `hack/rename-to-llmsafespaces.sh` — executable rename script with
  `DRY_RUN=1` (default, report-only) and `DRY_RUN=0` (execute) modes.
- `hack/rename-to-llmsafespaces.dryrun.txt` — captured dry-run output
  against `main` @ `16f336b9` for reviewer inspection without needing
  to run the script.
- `worklogs/0340_2026-06-18_rename-to-llmsafespaces-plan.md` — this
  document.
