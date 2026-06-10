# Worklog: Migration version collision recovery + repolint pre-commit/CI guard

**Date:** 2026-05-30
**Session:** Diagnose user `mike@kao.family` reporting "workspaces gone from UI"; recover live DB; build repository-layout lint to prevent the class of failure that caused the incident.
**Status:** Complete

---

## Objective

User reported workspaces and sessions vanished from the UI, immediately following the deploy I did in worklog 0096. Diagnose root cause, restore service, and build automated guards (unit tests, integration tests, pre-commit hook, CI job) so the same failure mode cannot recur.

---

## Stated assumptions and validation

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | The deploy from worklog 0096 broke something — user lost UI access right after | Verified: user reported issue ~7h after my 16:43 helm-upgrade; pre-deploy revisions 68-71 worked, my revision 72 introduced new failure modes |
| A2 | The `column "image_tag" does not exist` error means a code-vs-schema mismatch — API expects the column but Postgres doesn't have it | Verified: queried `\d workspaces` on live Postgres; `image_tag` and `agent_version` columns absent; `schema_migrations.version=9` reported as applied |
| A3 | The bug is a migration-numbering collision: two parallel agents both used version 9 | Verified: `api/migrations/` had **both** `000009_drop_workspace_phase_cache.{up,down}.sql` (commit `cdd6305`) and `000009_workspace_version_info.{up,down}.sql` (commit `7aa0da8`). golang-migrate runs files in lex order; `drop_workspace_phase_cache` (d<w) ran and was recorded as version 9; the other migration was thereafter silently skipped |
| A4 | A second drift problem exists: chart-bundled `charts/llmsafespace/migrations/` is stale relative to `api/migrations/` | Verified: chart copy was missing `000004_drop_sandbox_tables`, `000009_workspace_version_info`, `000010_rename_llm_provider_to_api_key`, `000011_*` (canonical name after rename). Last commit touching the chart copy was `cdd6305`; subsequent migrations (`7aa0da8`, `c8b7590`) only updated `api/migrations/` |
| A5 | The `vworkspace.llmsafespace.dev` webhook was unrelated to the migration issue but blocking workspace **creation** with HTTP 500 | Verified: live `validatingwebhookconfiguration` had a `vworkspace` rule pointing to `/validate-llmsafespace-dev-v1-workspace`. That handler is registered only at `controller/main.go:116` in commit `f03526e` (after my deployed image `sha-49dc726`). My helm-upgrade picked up unstaged template changes from another agent's work-in-progress on disk and pushed an inconsistent state to the cluster |
| A6 | Worklogs/ has its own pre-existing duplicate-numbering problem | Verified: `pkg/repolint` first run reported 7 collisions (74, 75, 76, 77, 78, 84, 96) and 1 gap (67) in `worklogs/`. None of these caused this incident but they are symptoms of the same class of failure |

---

## Work completed

### 1. Live database recovery

```sql
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS image_tag TEXT NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS agent_version TEXT NOT NULL DEFAULT '';
UPDATE user_secrets SET type = 'api-key' WHERE type = 'llm-provider';
UPDATE schema_migrations SET version = 11 WHERE version = 9;
```

Bumped `schema_migrations.version` 9 → 11 to match the new file numbering (renumbered the colliding migration to `000011_workspace_version_info` so future `migrate up` runs see the schema as "already at 11" and don't try to re-apply).

After recovery: `mike@kao.family` `GET /api/v1/workspaces` returns 200 with all 3 workspaces visible.

### 2. Live cluster webhook recovery

Removed the dangling `vworkspace.llmsafespace.dev` rule from the `llmsafespace-validate` validatingwebhookconfiguration via `kubectl patch --type=json`. The deployed controller image (`sha-49dc726`) does not implement the `/validate-llmsafespace-dev-v1-workspace` admission path; that path is only registered in commit `f03526e` (currently on `origin/main` but not yet built into a published image). Until a controller image at-or-after `f03526e` is deployed, leaving the webhook rule in place would block all workspace CREATE/UPDATE with HTTP 500.

When the cluster is next upgraded to an image that includes `f03526e`, the next `helm upgrade` will re-create the rule from the chart template.

### 3. Repo cleanup: rename collision, sync chart mirror

```bash
git mv api/migrations/000009_workspace_version_info.up.sql   api/migrations/000011_workspace_version_info.up.sql
git mv api/migrations/000009_workspace_version_info.down.sql api/migrations/000011_workspace_version_info.down.sql
rm -rf charts/llmsafespace/migrations
cp -r  api/migrations charts/llmsafespace/migrations
```

`api/migrations/` and `charts/llmsafespace/migrations/` are now byte-identical (validated by `pkg/repolint` `TestLive_ChartMigrations_NoDriftFromCanonical`).

### 4. README the rules

Added comprehensive READMEs to both directories explaining:

- File naming requirements (6-digit zero-padded versions, paired up/down, snake_case slugs)
- The four invariants enforced by `pkg/repolint`
- The dual-directory drift problem and the (manual, until refactored) sync workflow
- The future refactor: bundle migrations into the API container image, run them via the API binary's `--migrate-only` mode, eliminate the chart's parallel copy entirely. This makes "image expects schema X but cluster has Y" structurally impossible.

### 5. `pkg/repolint` — TDD validator package

New package with **17 tests**, all passing. Exposes two checks:

**`SequenceCheck`** — scans a directory for files matching a pattern (capture group 1 = numeric version), asserts:
- No duplicate version numbers
- Versions form a contiguous sequence `1..N`
- Optionally: every up has a matching down (and vice-versa), via `RequirePaired: true`

Supports `GrandfatherBelow: N` to exempt pre-existing historical messes (used for worklogs because rewriting 7 collisions across ~26 cross-references is too risky).

**`DriftCheck`** — verifies a mirror directory is byte-identical to a canonical directory for files matching a glob. Reports `MissingInMirror`, `ExtraInMirror`, `ContentDiffers`. Side files (READMEs, etc.) are excluded by the glob.

Three live-repo regression tests run the checks against the actual repository:
- `TestLive_Migrations_NoCollisionsOrGaps` — `api/migrations/`
- `TestLive_Worklogs_NoCollisionsOrGaps` — `worklogs/` (grandfathers <0097)
- `TestLive_ChartMigrations_NoDriftFromCanonical` — chart vs canonical

Each fails loudly if a future commit reintroduces the failure mode.

### 6. `cmd/repolint` — CLI

Used by:
- `make repolint` (Make target)
- `.githooks/pre-commit` (pre-commit hook)
- `Lint` job in `.github/workflows/ci.yml`

Supports `-fix-drift` to auto-sync `charts/llmsafespace/migrations/` from `api/migrations/`.

### 7. Pre-commit hook

`.githooks/pre-commit` invokes `make repolint`. Wired up via `make install-hooks` (sets `git config core.hooksPath .githooks`). Run once per fresh clone.

If repolint finds issues, the commit is blocked with a labelled error and remediation hints (rename to next available version; `make chart-sync-migrations`; `git commit --no-verify` for emergencies).

### 8. CI lint job

`.github/workflows/ci.yml` gains a new `lint` job that runs `make repolint` before `test`. Subsequent jobs (`test`, `sdk-contract`, `build-*`) gain a `needs: [lint]` so the entire pipeline fails fast on layout issues.

### 9. Makefile targets

```make
repolint                 # build + run cmd/repolint
chart-sync-migrations    # repolint -fix-drift  (use after adding a migration)
install-hooks            # wire .githooks/ into git
```

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Renumber my colliding migration to `000011` rather than `000010` (which is the "next" lex-free slot) | `000010_rename_llm_provider_to_api_key` already exists from another agent. I bumped to 11 to avoid a third collision |
| Set `worklogs.GrandfatherBelow = 97` rather than renumbering history | Renumbering would touch 26+ cross-references in other markdown/code files; the risk of missing one and creating dead links is high relative to the marginal benefit. Pre-existing collisions are real artifacts of work that happened — they don't need to be erased to prevent NEW ones |
| Keep `charts/llmsafespace/migrations/` as a mirror with a strict drift check rather than refactoring NOW | The proper fix (bundle migrations into the API image) is a multi-component change and unrelated to user recovery. Documented as the future refactor in both READMEs and tracked for a later epic |
| Remove the `vworkspace` webhook from the live cluster rather than rolling forward to an image that includes `f03526e` | Rolling forward requires CI to build an `origin/main` image. That's available but slow (~10min) and orthogonal to the migration recovery the user actually asked about. Removing the dangling rule is one `kubectl patch`. The next chart-managed `helm upgrade` will recreate it from the template once the image catches up |
| Build the validator in Go rather than a shell script | Three reasons: (1) it's testable from Go's existing test framework, so the live-repo regression tests run on every `go test ./...`; (2) cross-platform; (3) the same logic is consumed both by tests and by the CLI, with no duplication |

---

## Tests Run

```bash
# Unit + integration tests for the new validator
go test -timeout 30s -race ./pkg/repolint/...
  → 17/17 PASS (includes 3 live-repo regression tests)

# Full repo, all tests, race detector, short mode
go test -timeout 240s -race -short ./...
  → 39/39 packages OK (api, controller, cmd, pkg, charts/llmsafespace, mocks)

# Static analysis
go vet ./...
  → clean

# Validator manual smoke tests
make repolint
  → ok    migrations sequence (11 migrations, max version 11)
  → ok    worklogs sequence (96 worklogs, max 0097, grandfathered <0097)
  → ok    chart migrations match api/migrations/

# Adversarial smoke: introduce a new collision >= grandfather threshold
touch worklogs/0097_2026-05-30_collision.md && /tmp/repolint
  → FAIL  worklogs sequence … (caught the new duplicate)

# Pre-commit hook end-to-end
bash .githooks/pre-commit
  → exit 0, repolint output as above
```

Live cluster validation:

```bash
# Pre-fix verification of bug
psql -c "\d workspaces" | grep image_tag
  → (no rows)
GET /api/v1/workspaces
  → 500 column "image_tag" does not exist (SQLSTATE 42703)

# Post-fix
psql -c "\d workspaces" | grep -E "image_tag|agent_version"
  → image_tag      | text  | not null | ''::text
  → agent_version  | text  | not null | ''::text
GET /api/v1/workspaces (mike@kao.family)
  → 200, 3 workspaces returned

# Webhook removal
kubectl get validatingwebhookconfiguration llmsafespace-validate -o jsonpath='{.webhooks[*].name}'
  → vruntimeenvironment.llmsafespace.dev   (only — vworkspace removed)
```

---

## Files Modified

- `api/migrations/000011_workspace_version_info.up.sql` — renamed from `000009_*`
- `api/migrations/000011_workspace_version_info.down.sql` — renamed from `000009_*`
- `api/migrations/README.md` — new (rules, future refactor)
- `charts/llmsafespace/migrations/` — re-synced to match canonical (now byte-identical)
- `charts/llmsafespace/migrations/README.md` — new (mirror responsibility, future refactor)
- `pkg/repolint/sequence.go` — new (`SequenceCheck`, `DriftCheck`, patterns)
- `pkg/repolint/sequence_test.go` — new (17 tests including 3 live-repo regression tests)
- `cmd/repolint/main.go` — new (CLI)
- `Makefile` — new targets `repolint`, `chart-sync-migrations`, `install-hooks`
- `.githooks/pre-commit` — new (calls `make repolint`)
- `.github/workflows/ci.yml` — new `lint` job; `test`/`sdk-contract`/`build-*` now `needs: [lint]`
- `.gitignore` — exclude `/bin/` (where Makefile drops the repolint binary)

---

## Next Steps

1. **Commit + push to main**: CI will build `sha-<commit>`. Pre-commit hook will run on the commit (and pass — verified).
2. **Helm upgrade after CI completes**: bump api/controller/frontend images to the new sha. The migrate Job will run with the (now-correct) configmap; nothing should change because schema is already at version 11.
3. **Future epic — eliminate the chart migration mirror**: bundle migrations into the API image; replace the external `migrate/migrate` Helm Job with a pre-install Job that runs the API binary in `--migrate-only` mode. Files: design/stories/epic-NN-migrations-in-image/ (TBD).
4. **Future cleanup — legacy three-digit migration files**: `001_initial_schema.sql` and `001_initial_schema_rollback.sql` are pre-canonical and ignored by repolint's pattern. Should be removed in a separate cleanup commit.
5. **Re-deploy controller from `origin/main`**: once an image at-or-after commit `f03526e` is built and rolled out, the next `helm upgrade` will recreate the `vworkspace` webhook rule and admission validation will start working as designed.
