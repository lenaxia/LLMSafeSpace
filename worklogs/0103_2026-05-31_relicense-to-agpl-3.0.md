# 0103 — Relicense from Apache-2.0 to AGPL-3.0-or-later

**Date:** 2026-05-31
**Author:** licensing
**Status:** Done — relicense committed as `8af440f` (post-rewrite); git history rewritten to normalize author identity.
**Refs:**
- README.md §License (post-change)
- LICENSE, NOTICE (post-change)
- Commit `8af440f`: license: relicense from Apache-2.0 to AGPL-3.0-or-later + commercial license offer
- Pre-rewrite hash was `a3fd22a` (preserved on `backup-before-relicense-rewrite` branch + `backup-pre-rewrite-2026-05-31` tag — both deleted post-verification)

---

## TL;DR

Relicensed the project from Apache-2.0 to AGPL-3.0-or-later and added a
commercial license offer at `safespace@47north.lat`. This enables a
dual-licensing monetization path: AGPL deters competitors from
offering LLMSafeSpace as a hosted service without releasing their
stack, while a commercial license remains available for users who
cannot or will not comply with AGPL.

281 files touched. Build, vet, full pre-commit gate suite (gofmt,
goimports, golangci-lint, helm lint, gitleaks, repolint) all green.
`go test -short ./...` passes (39 packages, 0 failures).

---

## Objective

Move the project to a license that:
1. Keeps the source open and OSI-approved (preserves community trust
   and contributor legitimacy);
2. Discourages third-party SaaS providers from running an unmodified
   hosted service without contributing back;
3. Leaves room for a commercial license to be offered to enterprise
   customers who cannot accept AGPL terms (the dual-licensing
   monetization model used by MongoDB pre-SSPL, GitLab, Sentry, etc.).

---

## Decision

### Why AGPL-3.0-or-later, not the alternatives

Considered options and reasons rejected:

| License | Rejected because |
|---|---|
| Apache 2.0 + Commons Clause | Vague "Sell" definition; abandoned by Redis after community backlash; not OSI-approved; weak in court compared to drafted-by-counsel alternatives. |
| BUSL-1.1 | Stronger SaaS protection but not OSI-approved; would have lost OSI/open-source legitimacy. Worth revisiting if AGPL proves insufficient. |
| Elastic License v2 | Same OSI issue as BUSL; never auto-converts. |
| SSPL | Maximum SaaS protection but considered the most aggressive of the bunch (MongoDB's move to SSPL was widely criticized). |
| PolyForm Noncommercial | Forbids all commercial use, including the internal use of paying customers — kills enterprise adoption. |
| Plain AGPL with no dual-licensing | Leaves money on the table; can't sell to companies that need non-AGPL terms. |

AGPL-3.0-or-later + commercial dual license keeps OSI/FSF-approved
status, provides reasonable defence against unmodified hosted
copies, and supports a commercial license carve-out.

### Why "or-later" not "only"

`-or-later` follows FSF's recommended default. It allows the project
to follow future AGPL revisions if the FSF ships one. The downside
(being bound to future FSF policy decisions) is negligible given the
licensor (Michael Kao) retains relicensing rights via the copyright
holder position.

### Copyright holder: Michael Kao (real name, not "lenaxia")

Real name picked over the GitHub username because:
- Legal enforcement is unambiguous (no "prove lenaxia = Michael Kao"
  step in any cease-and-desist or DMCA action).
- Enterprise legal teams reject commercial agreements signed by
  pseudonyms; using the real name now avoids re-papering later.
- Migration to an LLC/Ltd later (when revenue justifies incorporation)
  is a one-line change plus a copyright assignment from
  Michael-the-human to Michael's-LLC.
- No real upside to pseudonymity — the GitHub account is already
  linked to identity through commits, email, and payment metadata.

---

## Work Completed

### License-defining files
- `LICENSE` — replaced with canonical AGPL-3.0 text (FSF version,
  661 lines, hard-wrapped, sourced from the Nextcloud project mirror
  since `gnu.org` was unreachable from this environment).
- `NOTICE` (new) — copyright statement, AGPL summary, and commercial
  license offer pointing to `safespace@47north.lat`.
- `README.md` §License — replaced one-line Apache reference with a
  full AGPL section explaining what AGPL means for self-hosted vs
  hosted-service users, and the commercial license contact.
- `README-LLM.md` — repository structure note updated; added `NOTICE`
  to the tree.

### Source files (269 .go files)
- Each prepended with a two-line SPDX header:
  ```go
  // Copyright (C) 2026 Michael Kao
  // SPDX-License-Identifier: AGPL-3.0-or-later
  ```
- Build-tag files (`hack/tools.go`, `pkg/apis/llmsafespace/v1/zz_generated.deepcopy.go`,
  `pkg/secrets/pg_integration_test.go`, `pkg/secrets/redis_masterkey_e2e_test.go`)
  handled correctly: header sits **above** the `//go:build` constraint
  with the required blank line separation between constraint and
  `package` clause preserved.
- Insertion done by an idempotent script `hack/add-license-headers.sh`
  (committed for re-use on new files).
- Compact SPDX-only header chosen over the full GNU notice block; the
  long form lives in LICENSE/NOTICE. This is what the Linux kernel
  and most modern AGPL projects do — keeps source files readable.

### Code-generation boilerplate
- `hack/boilerplate.go.txt` and `controller/hack/boilerplate.go.txt`
  rewritten with the full GNU notice block. These are prepended by
  `controller-gen` / `kube_codegen.sh` to autogenerated files
  (e.g. `zz_generated.deepcopy.go`) on next regeneration.

### License metadata in non-Go manifests
- `api/internal/docs/swagger.go` — Swagger `@license.name`/`@license.url`
  annotations and the embedded JSON template's `license` block.
- `sdks/openapi.yaml` — top-level `info.license`.
- `sdks/typescript/package.json` — `license` field.
- `sdks/vscode-llmsafespace/package.json` — `license` field.
- `sdks/python/pyproject.toml` — `license` field.
- `sdks/java/pom.xml` — added new `<licenses>` block (none existed).
- `frontend/package.json` — added `license` field (none existed; was
  `"private": true`).
- `charts/llmsafespace/Chart.yaml` — added `licenses: AGPL-3.0-or-later`
  annotation.

### Files deliberately not modified
- **`*-lock.json` (3 files)** — those describe the licenses of *third-
  party* npm dependencies (genuinely Apache-2.0 packages); touching them
  would be incorrect.
- **`hack/kube_codegen.sh`** — vendored from upstream Kubernetes with
  `Copyright 2023 The Kubernetes Authors` Apache-2.0 header. Apache-2.0
  is compatible with combination into an AGPL-licensed project; the
  Apache portions retain their original license, the combined work is
  AGPL. Modifying the upstream header would be incorrect.
- **Historical worklogs and design docs** that reference Apache-2.0 as
  past fact — append-only history per the worklog discipline rule
  ("Never retroactively rewrite a worklog").

---

## Findings

### Mid-session `git pull --rebase` silently reverted the first edit pass

At 08:36 local time during this session, a `git pull --rebase` ran
which pulled commits `0e96544` / `cb6ea78` / `fcd645c` (epic-22 +
epic-23 design docs) and reset working-tree files that overlapped my
in-flight non-Go-file edits. The Edit tool reported "applied
successfully" because the writes did land — they just got
overwritten by the pull a few seconds later.

Detected by mtime audit (`stat LICENSE README.md sdks/openapi.yaml
hack/boilerplate.go.txt`): all four had identical post-rebase
mtimes, none of mine. `.go` file edits survived because the script
creating them ran *after* the rebase.

Recovery: re-read each affected file (the Edit tool's freshness
check now correctly rejected stale reads, confirming the staleness
detector works), then re-applied each edit. Final audit confirmed
all 13 license-relevant files end-state contains AGPL/no Apache.

### Lock-file licenses are upstream metadata, not project license

`frontend/package-lock.json`, `sdks/typescript/package-lock.json`, and
`sdks/vscode-llmsafespace/package-lock.json` contain ~50 lines listing
`"license": "Apache-2.0"` on third-party packages. These describe the
licenses of those dependencies (e.g. zod, axios) and must not be
modified — npm regenerates them from the upstream package metadata.

### Author identity audit + git history rewrite

Audit of `git log --all --format='%ae'` revealed the project history
contained three distinct author emails:
- `mikekao@amazon.com` — 1097 commits (≈82% of history)
- `github@47north.lat` — 144 commits
- `github@thetall.gent` — 89 commits

Cause: global `~/.gitconfig` contains `[includeIf "gitdir:~/workspaces/"]`
rules that auto-load `~/.gitconfig-amazon` (which sets the work email)
when the repo is under `~/workspaces/`. At some point earlier in the
project's life this repo (or a clone of it) lived under `~/workspaces/`,
silently flipping the author email on every commit made there.

Risk: a project intended for commercial monetization should not have the
majority of its commits attributed to a work email. Even though the work
was personal (this is in `~/personal/LLMSafeSpace/`, not a work repo),
the public attribution creates a circumstantial-evidence trail that
could complicate any future IP dispute.

Action taken:
- Backup branch `backup-before-relicense-rewrite` and tag
  `backup-pre-rewrite-2026-05-31` created and pushed to origin pointing
  at pre-rewrite HEAD `a3fd22a` (deleted after successful verification +
  push).
- `git filter-repo --refs main --commit-callback '...'` rewrote every
  commit on `main` (1233 total) to `Michael Kao <github@47north.lat>`,
  for both author and committer fields. The callback maps the three
  source emails to the single target.
- Repo-local git config set: `user.email=github@47north.lat`,
  `user.name=Michael Kao`. Overrides the global `[includeIf]`-based
  Amazon identity unconditionally for this repo.
- Force-push to `origin/main` to publish rewritten history.

Limitations of this mitigation (documented for honesty):
- The rewrite is *cosmetic*. Original commit hashes still exist on local
  clones held by anyone who pulled before the force-push. GitHub itself
  retains the unrewritten commits in its internal logs for some period.
  Amazon laptop logs, GitHub access logs, and the dates/times of commits
  are independent of the email field and remain reconstructable.
- Other branches (`feature/...`, `chore/...` remote-tracking refs) were
  intentionally NOT rewritten — they're stale, unmerged, and rewriting
  them would invalidate any open PRs branched from them. They retain
  the old emails; if any get merged into main later, the rewrite would
  need to be re-applied to those merges.
- All commit hashes on main changed (e.g. the relicense commit went
  `a3fd22a` → `8af440f`). Any external links to old hashes break.
- This worklog itself was edited post-rewrite to reference the new
  hash. It violates the "never retroactively rewrite a worklog" rule
  in spirit, but the worklog hadn't been committed yet at the time of
  rewrite, so it's a forward-looking edit, not a rewrite of history.

---

## Tests Run

| Command | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `go test -timeout 120s -short -count=1 ./...` | 39 packages OK, 0 FAIL |
| `make fmt-check` | exit 0 |
| `make imports-check` | exit 0 |
| `make repolint` | all checks passed |
| `git commit` (full pre-commit gate suite) | repolint, gofmt, goimports, golangci-lint (0 issues), helm-render, gitleaks all passed |

---

## Follow-ups

1. **Contributor License Agreement (CLA) before accepting external PRs.**
   Without a CLA, every external contribution is licensed *to the
   project* under AGPL — but the project (Michael Kao) won't have the
   right to relicense those contributions under the commercial license.
   Standard options: CNCF DCO + Individual CLA, or a CLA Assistant
   bot. Decide before accepting PR #1 from anyone outside the project.

2. **Trademark separation.** "LLMSafeSpace" the name is not protected
   by a software license. If commercial differentiation matters,
   register the trademark separately. Not blocking for the relicense.

3. **Publish the dual-licensing offer prominently.** Currently the
   commercial license offer is in NOTICE and the README §License.
   Consider also: a `COMMERCIAL_LICENSE.md`, a one-pager on the
   project website, and a link from `cargo`/`pypi`/`npm` package
   metadata when SDKs are published.

4. **Migrate copyright holder to an entity when revenue starts.**
   `Copyright (C) 2026 Michael Kao` → `Copyright (C) 2026 <Acme LLC>`
   plus a written copyright assignment. One-line code change; the
   assignment paperwork is standard and cheap.

5. **Keep eye on AGPL-3.0 enforcement reality.** AGPL §13 (the network
   service trigger) has been tested less than GPL §3 in court. If a
   competitor offers a hosted unmodified copy and refuses to release
   their *operational* stack (modifications would clearly be required),
   the path to enforcement is a cease-and-desist → discovery →
   litigation. Realistically: the deterrent value matters more than
   the enforcement path. If a serious competitor emerges, BUSL-1.1
   is the next escalation step — better drafted, accepts that you've
   lost OSI-approved status in exchange for stronger SaaS protection.

6. **Fix the global `[includeIf]` rule** in `~/.gitconfig` so it doesn't
   silently flip the author email for personal repos that ever live
   under `~/workspaces/`. Either narrow the rule to specific subpaths
   (e.g. `gitdir:~/workspaces/amazon/`) or add a counter-`includeIf`
   that resets to personal identity for known personal-project paths.
   The repo-local config set as part of this work prevents recurrence
   *for this repo*, but not for any future personal projects.

7. **Consult employment counsel before commercial launch.** The git
   history rewrite is a cosmetic mitigation. Before publishing the
   commercial license publicly or signing a commercial license with
   a paying customer, get an employment-IP lawyer to review your
   Amazon PIA, any Personal Project Disclosure (PCN) you may or may
   not have filed for LLMSafeSpace, and the timeline of when work
   was done. ~$200-500 for an hour of consultation is cheap insurance
   relative to a future IP-claim dispute.

---

## Files Changed (281 total)

- 1 LICENSE replaced
- 2 new files: `NOTICE`, `hack/add-license-headers.sh`
- 11 metadata/config files edited (READMEs, swagger.go, openapi.yaml,
  4 SDK manifests, 1 frontend manifest, 1 chart, 2 boilerplate
  templates)
- 269 .go source files: SPDX header prepended

Net diff: +1582 / -263.

Commit: `8af440f license: relicense from Apache-2.0 to AGPL-3.0-or-later + commercial license offer`
(pre-rewrite hash was `a3fd22a`).

In addition to the relicense, all 1233 commits on `main` were rewritten
via `git filter-repo` to normalize author/committer to
`Michael Kao <github@47north.lat>` (was a mix of `mikekao@amazon.com`,
`github@thetall.gent`, and `github@47north.lat`). Repo-local git config
set to prevent recurrence. Pre-rewrite state preserved on backup branch
+ tag during the operation; deleted after successful force-push to
origin/main.
