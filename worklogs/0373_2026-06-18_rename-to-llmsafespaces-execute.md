# Worklog: Rename `llmsafespace` → `llmsafespaces` (execution)

**Date:** 2026-06-18
**Session:** Execute the approved rename plan from PR #233
**Status:** Complete — all checks pass
**Branch:** `chore/rename-to-llmsafespaces-execute`
**Plan PR:** #233 (merged)

---

## Objective

Execute the approved rename plan: 3 directory renames + 612-file content
rewrite + Phase 3 regeneration, verified by build/vet/test/lint.

---

## Execution

### Phase 1 — Directory renames (git mv)

| Source | Destination | Files |
|---|---|---:|
| `pkg/apis/llmsafespace` | `pkg/apis/llmsafespaces` | 10 |
| `charts/llmsafespace` | `charts/llmsafespaces` | 115 |
| `sdks/vscode-llmsafespace` | `sdks/vscode-llmsafespaces` | 16 |

### Phase 2 — Content rewrite

Ran `DRY_RUN=0 hack/rename-to-llmsafespaces.sh` against `main` @ `0206f89b`:
- 620 files edited (8 more than the plan's 612 — due to new files added
  between `16f336b9` and `0206f89b`).
- 2,962 occurrences replaced.

### Phase 2b — Manual fix: 6 over-blocked CamelCase compounds

The boundary regex `(?![sS])` correctly prevents matching inside
already-pluralised tokens, but over-blocked 3 alert names where the
next word started with `S` (the `S` was a word boundary, not a plural
suffix). Manually fixed in:

- `charts/llmsafespaces/templates/prometheus-rules.yaml`
  - `LLMSafeSpaceSSEBrokerDroppingEvents` → `LLMSafeSpacesSSEBrokerDroppingEvents`
  - `LLMSafeSpaceSafeModeActive` → `LLMSafeSpacesSafeModeActive`
  - `LLMSafeSpaceStatusUpdateConflicts` → `LLMSafeSpacesStatusUpdateConflicts`
- `charts/llmsafespaces/chart_test.go` (matching test assertions)

**Lesson:** for CamelCase identifiers, lookahead-based boundary guards
are too aggressive — a non-greedy word-boundary regex
(`\bllmsafespace\b`) would not have caught them, but neither would it
have over-blocked. The perl lookahead approach is correct for
lowercase/UPPER variants but should not be used for MixedCase where
the next token may start with the same letter. Documented for future
similar rewrites.

### Phase 3 — Regeneration

| Step | Result |
|---|---|
| `go mod edit -module` (root) | ✅ `github.com/lenaxia/llmsafespaces` |
| `go mod edit -module` (sdk/go) | ✅ `github.com/lenaxia/llmsafespaces/sdk/go` |
| `go mod tidy` (root + sdk/go) | ✅ |
| CRD YAML | ✅ Already correctly rewritten by Phase 2 perl (group → `llmsafespaces.dev`). `make -C controller manifests` blocked by pre-existing controller-tools v0.8.0 / Go 1.26 panic (unrelated to rename) — verified YAMLs are correct without regen. |
| `zz_generated.deepcopy.go` | ✅ Zero stale refs (file is boilerplate; `make deepcopy` blocked by pre-existing sh pipefail issue in `hack/update-deepcopy.sh`, unrelated to rename). |
| npm lockfiles (3 dirs) | ✅ `npm install --package-lock-only` ran clean |

### Phase 4 — Verification

| Check | Command | Result |
|---|---|---|
| Stale singular refs (3 variants) | `git grep -IohE '(llmsafespace\|LLMSAFESPACE\|LLMSafeSpace)([^sS]\|$)'` | **0** matches outside `worklogs/`, `design/`, rename tooling |
| Go build (root) | `go build ./...` | ✅ exit 0 |
| Go build (sdk/go) | `go build ./...` | ✅ exit 0 |
| Go vet | `go vet ./...` | ✅ exit 0 |
| Go test (root) | `go test ./...` | ✅ exit 0 — all packages pass |
| Go test (sdk/go) | `go test ./...` | ✅ exit 0 |
| golangci-lint v2 | `make lint` | ⚠️ 3 findings, all **pre-existing on main** (verified byte-identical on `origin/main`): gosec G202 in `database.go:593`, gosec G115 in `recovery_policy.go:38`, staticcheck ST1020 in `workspace_types.go:377`. Not introduced by rename. |

---

## Items not in scope (Phase 5 external, owner-run)

1. GitHub repo rename `lenaxia/LLMSafeSpace` → `LLMSafeSpaces`
2. ghcr.io: publish future images under new path
3. npm: publish `@llmsafespaces/sdk`, `vscode-llmsafespaces`
4. PyPI: publish `llmsafespaces`
5. Cloudflare Worker: rename to `llmsafespaces-inference-relay`
6. Dev Postgres: drop+recreate as `llmsafespaces`

---

## Files changed

620 files (614 from script + 6 manual fixes for over-blocked alert names).
Diff stat will appear in PR description.

---

## Review Iterations (PR #248)

### Round 1 — REQUEST CHANGES: Java + Python SDK dirs not renamed

The stale-ref audit checked file contents (`git grep`) but not file
**paths**. Phase 1 hardcoded 3 known dirs; the stray-file detector
used a lowercase-only regex, missing CamelCase Java filenames and the
Python package dir. Java wouldn't compile (package/filename mismatch),
Python wouldn't import (module name mismatch).

**Fix (commit `3be908d8`):**
- `git mv sdks/java/.../com/llmsafespace → com/llmsafespaces`
- `git mv LLMSafeSpaceClient.java → LLMSafeSpacesClient.java` (+ Exception)
- `git mv sdks/python/llmsafespace → sdks/python/llmsafespaces`
- Verified: `mvn compile` ✅, `pip install` ✅, `pytest` 23 passed ✅

### Round 2 — REQUEST CHANGES: 2 of 5 case variants missed in Go

The content audit checked only 3 case variants (`llmsafespace`,
`LLMSAFESPACE`, `LLMSafeSpace`), missing the K8s client-gen naming
convention which produces two additional variants:
- `Llmsafespace` (initial-cap) → `LlmsafespaceV1()` accessor methods
- `LLMSafespace` (LLM + lowercase) → `LLMSafespaceV1Client` type names

~220 stale refs across 41 files survived. The PR body's claim "0 stale
singular refs across all 3 case variants" was factually correct but
methodologically incomplete — there are 5 variants, not 3.

**Fix (commit pending):**
- Extended the rewrite to cover the 2 missing variants.
- Updated `hack/rename-to-llmsafespaces.sh` PAT to a 5-alternation.
- Re-verified with case-insensitive audit:
  `git grep -i -E 'llmsafespace([^s]|$)'` → **0 matches**.
- `go build`, `go vet`, `go test ./...` all exit 0.

### Root cause + lesson (both rounds)

The same methodology gap caused both misses: **the audit's search
space was narrower than the codebase's actual token space.** A
comprehensive rename audit must use case-insensitive search
(`git grep -i -E 'token([^s]|$)'`), not a hand-enumerated alternation
of "known" variants. Future bulk renames should start from
case-insensitive discovery, then derive the case-matched replacement
rules from what's actually present.
