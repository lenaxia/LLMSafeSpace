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

## Refs

- design/stories/epic-55-credential-slug-vs-kind/README.md — full design.
- worklogs/0552_2026-06-26_provider-not-found-end-to-end.md — diagnosis worklog.
- PR #430 — this PR.
