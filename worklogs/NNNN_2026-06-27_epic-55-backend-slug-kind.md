# NNNN_2026-06-27: Epic 55 backend — slug-vs-kind credential identity

## What landed

PR collapses 46 historical migrations into a single `000001_initial_schema.up.sql` (pg_dump'd from the cumulative state) and reshapes `provider_credentials` in the same migration to align with Epic 55:

- New column `kind` (TEXT NOT NULL, CHECK enum of 15 SDK classes).
- New column `slug` (TEXT NOT NULL, CHECK regex `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`).
- New UNIQUE constraint on `(owner_type, owner_id, slug)`; legacy `(owner_type, owner_id, provider)` dropped.
- Legacy `provider` column dropped entirely (pre-launch — no back-compat needed).

The Go codebase replaces every `Provider` field on `CredentialBinding`, `CredentialRow`, and `LLMProviderData` with `Kind` + `Slug`. The agent-config.json provider-map key (formerly `cfg.Provider[p.Provider]`) is now `cfg.Provider[p.Slug]` — opencode persists the slug as `providerID` on session records, which solves the original incident (workspace `0283c028-…` got `providerID:"custom"` because the SDK kind was overloaded as identity).

## Why this shape

The user picked the three-column abstraction (slug + kind + name) from the four-option question. Pre-launch posture meant:

- No back-compat for the wire format.
- No two-release deprecation window.
- No live session migration (option C "user re-picks" — accepted).
- Migration collapse into one file (the 46-step history added no value with no production data).

The org credential `thekaocloud` (with `provider="custom"`) now backfills to `kind="openai_compatible", slug="thekaocloud"`. opencode sees `thekaocloud/glm-5.2`. The original incident is closed end-to-end.

## What's in PR

- `api/migrations/000001_initial_schema.{up,down}.sql` — collapsed schema.
- `api/migrations/test/000001_initial_schema_test.sql` — new structural assertions (kind+slug columns, CHECK constraints, slug regex coverage, UNIQUE blocks duplicates).
- 188 deleted migration files (canonical `api/migrations/` and chart copies `charts/llmsafespaces/migrations/`).
- 60 Go file modifications: types, DAL queries, handlers, materialize path, all test fixtures.
- `.github/workflows/migration-safety.yml` — replaced point-in-time regression jobs (000014/000044/000045) with a generic initial-schema invariant job.
- `hack/migration-roundtrip.sh` — unchanged shape; verified round-trip clean against the new schema.
- `hack/migration-idempotent.sh` — added exception comment for the 000001 baseline (pg_dump snapshot is not idempotent on its own; later incremental migrations MUST use `IF NOT EXISTS`).
- Removed `hack/migration-data-cleanup.sh`, `hack/migration-jwt-sessions.sh`, `hack/migration-orphan-org-id-backfill.sh` — pinned to migration numbers that no longer exist.

## Verification

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `gofmt -l .` | empty |
| `go test ./...` (unit, skip `TestProxy_SessionLeak` pre-existing flake) | all pass |
| Integration tests against `pkg/secrets/` (build tag `integration`, fresh DB) | all pass |
| Migration round-trip (up → snapshot1 → down → empty → up → snapshot2; diff snapshots) | byte-identical |
| Migration applied to fresh DB | 31 tables, 1 row in schema_migrations |
| Migration-test SQL assertions | all pass |
| Chart sync (`api/migrations/` ↔ `charts/llmsafespaces/migrations/`) | byte-identical |

## Cluster impact

Existing dev clusters with data WILL need to be wiped and re-bootstrapped. The migration is a one-way door — no path forward for clusters with non-trivial data. Per the pre-launch decision, this is acceptable.

## What's NOT in this PR

- Frontend (slug input + kind dropdown in credential forms). Tracked as PR-E.
- Go and TS SDK updates. Tracked as PR-F.
- Existing-session migration. Per design option C: users re-pick model in any session that was pinned to a defunct provider key.

## Review pass

PR #430 review surfaced three correctness/robustness findings; all addressed in this PR:

1. **Org handler stale-ciphertext on kind/slug rename.** Pre-fix the org `Update` only re-encrypted on apiKey/baseURL changes — kind/slug renames updated the DB column but left the old slug inside the encrypted `LLMProviderData` blob, so the materialize path emitted the OLD slug as the agent-config.json key. Fixed: the re-encrypt condition now includes `req.Kind != nil || req.Slug != nil`, mirroring the admin handler. Two regression tests pin the new behavior (`TestOrgCredentials_Update_SlugRename_PropagatesToCiphertext`, `TestOrgCredentials_Update_KindChange_PropagatesToCiphertext`).

2. **No Go-level validation; DB CHECK violations surfaced as 500.** Added `pkg/secrets/credential_identity.go` with `ValidateKind` (enum of 15 SDK classes) and `ValidateSlug` (regex `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`). All three handler Create paths and the admin/org Update paths now call these at the boundary and return 400 with a `field` tag on rejection. Two property tests pin the Go-side regex/enum to the DB CHECK definitions in the migration (cross-layer drift guard). `ClassifyPostgresError` also classifies PG SQLSTATE 23514 → `ErrCredentialCheckViolation` as defense in depth so a Go/SQL regex drift surfaces as 400 rather than 500.

3. **Missing test for canonical Epic 55 scenario.** Added `TestCredentialPrecedence_SameKind_DifferentSlugs_BothMaterialize`: two `openai_compatible` credentials with different slugs (`litellm-prod`, `litellm-staging`) both materialize as separate entries in the injected payload. Trip-wire that fires if dedup ever reverts from `seen[b.Slug]` to `seen[b.Kind]`.

Also fixed two stale comments that claimed the `HasUserProviderCredential` parameter was named "provider" — the parameter is actually `slug` in both the interface and the implementation. Removed legacy `kind:"custom"` from test fixtures (the value is not in the post-Epic-55 enum; tests passed only because in-memory fakes don't enforce CHECK).

### Two follow-up fixes after the first re-review

PR-#430 first re-review (commit `b023a405`) caught two CI failures from the migration collapse interacting badly with shared test infrastructure:

1. **Round-trip schema-diff drift across Postgres versions** (commit `40621be8`). The CI runner uses postgres:16 (`'standard public schema'` default comment); my local validation used postgres:17 (empty default). My initial fix tried to restore the comment in the down; that worked on PG17 but broke on PG16. The correct fix is to filter `COMMENT ON SCHEMA public` from the round-trip snapshot — it's PG-version-volatile metadata, not application schema.

2. **Initial schema not idempotent → testharness 'Dirty database version 1'** (commit `7c5239c5`). The integration testharness shares a single Postgres database across tests; every test calls `MigrateUp`. With a pg_dump'd initial schema, the second test hit `function "update_updated_at_column" already exists` and golang-migrate marked schema_migrations as dirty. Made the entire initial schema idempotent: `CREATE TABLE/SEQUENCE/INDEX IF NOT EXISTS`, `CREATE OR REPLACE FUNCTION`, trigger drop-and-recreate, and `ALTER TABLE ADD CONSTRAINT` wrapped in `DO $idempotent$ … EXCEPTION WHEN duplicate_object | invalid_table_definition | …` blocks. The four migration-safety CI jobs (round-trip, idempotency, FK cascade, initial-schema invariants) all act as independent witnesses that the mechanical transformation didn't introduce semantic drift.

### Second re-review feedback

The third review (after the two follow-up fixes) returned **APPROVE** with two non-blocking documentation nits:

- `admin_provider_credentials.go:318` comment said "Re-encrypt only when the caller is changing an encrypted field (apiKey or baseURL)" but the condition also includes Kind/Slug. The matching org handler comment already documents the rationale correctly. Mirrored the org comment to the admin handler.
- `pg_secret_store.go:707` had `AsyncAuditLogger.HasUserProviderCredential(..., provider string)` while the interface and implementation use `slug`. Renamed for consistency.

## Refs

- design/stories/epic-55-credential-slug-vs-kind/README.md — full design.
- worklogs/0552_2026-06-26_provider-not-found-end-to-end.md — diagnosis worklog.
- PR #430 — this PR.
