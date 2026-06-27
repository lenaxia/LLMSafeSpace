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

## Refs

- design/stories/epic-55-credential-slug-vs-kind/README.md — full design.
- worklogs/0552_2026-06-26_provider-not-found-end-to-end.md — diagnosis worklog.
