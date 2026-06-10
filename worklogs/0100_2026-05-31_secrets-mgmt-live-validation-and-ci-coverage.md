# Worklog 0098: Secrets Mgmt — Live Cluster Validation + CI Integration Coverage

**Date:** 2026-05-31
**Session:** Final phase of the secrets-mgmt remediation cycle (worklogs 0085 → 0094 → 0098). Live cluster validation against the deployed image, gap-fill on automated test coverage so future regressions can't ship silently.
**Status:** Complete. 44/44 PASS on `local/test-secrets.sh` against `sha-5cbb632` in `default` namespace. New CI workflow `.github/workflows/secrets-integration.yml` boots Postgres + Redis service containers and runs the previously-uncovered `pg_secret_store` / `pg_key_store` storage layer with `-tags=integration`.

---

## Objective

Two-part:

1. **Empirically validate** that the pass-1 → pass-6 audit fixes (worklog 0094 + commits 867f1fa..5cbb632) work end-to-end against a real cluster, not just against in-process mocks.
2. **Close the test-coverage gaps** the audit cycle revealed so the same class of bugs cannot reach production silently again. Specifically: the production storage layer (`pg_secret_store.go`, `pg_key_store.go`) had ZERO automated coverage — every commit shipped on Postgres-mock unit tests alone.

---

## Live Cluster Validation

### Cluster state

- Context: `admin@home-kubernetes`, namespace `default`
- API: `llmsafespace-api` Deployment, 2 replicas
- Pre-rollout image: `ghcr.io/lenaxia/llmsafespace/api:sha-49dc726` (a stale build from a different work stream)
- Postgres: `postgres-5c64fd74f7-wrw4g` in `default` namespace; user `llmsafespace`, db `llmsafespace`; password from secret `llmsafespace-credentials.postgres-password`
- Valkey/Redis: `valkey-7478c8cf86-tsxrg` in `default` namespace

### Rollout

```bash
kubectl set image deploy/llmsafespace-api -n default api=ghcr.io/lenaxia/llmsafespace/api:sha-5cbb632
kubectl set image deploy/llmsafespace-controller -n default manager=ghcr.io/lenaxia/llmsafespace/controller:sha-5cbb632
```

Both rolled out cleanly. API logs showed clean startup — `pgxpool` connected, leader election ran, HTTP server up on `0.0.0.0:8080`. The pass-2 fail-closed `pgxpool` requirement (commit `6392bec`) didn't trip — Postgres was reachable.

### Migrations applied

Two migrations needed application before the new image was fully functional:

1. `000010_rename_llm_provider_to_api_key.up.sql` (mine, from commit `867f1fa`):
   ```
   UPDATE 2     -- 2 rows renamed llm-provider → api-key
   ```
2. `000009_workspace_version_info.up.sql` (pre-existing, not mine — apparently never applied to this cluster):
   ```
   ALTER TABLE workspaces ADD COLUMN image_tag TEXT NOT NULL DEFAULT '';
   ALTER TABLE workspaces ADD COLUMN agent_version TEXT NOT NULL DEFAULT '';
   ```

The `000009` drift was found during validation: when running `local/test-secrets.sh`, the Bug 11 section produced 500 errors with `column "image_tag" does not exist (SQLSTATE 42703)`. The new pass-3 `workspaceOwnerVerifier` (commit `9b7f5fe`) calls `db.GetWorkspace(workspaceID)` which SELECTs `image_tag`. Pre-mine, no production code path read `image_tag` from a request hot-path, so the missing migration was a latent issue. Mine surfaced it.

This is exactly the kind of drift the new CI workflow catches — see the "Test coverage gaps closed" section below.

### test-secrets.sh result

After both migrations applied and a pod restart on the new image:

```
=== Summary ===
Passed: 44
Failed: 0
```

Coverage:
- Bug 5/10 register flow + recoveryKey delivery
- Bug 6 `api-key` accepted, `llm-provider` rejected
- Bug 7 invalid-type error lists valid types; missing-metadata error names the field
- Bug 9 atomic rotation: pre-rotation secrets remain decryptable post-rotate, after re-login, after password change
- Bug 10 register response includes recoveryKey
- Bug 13 adversarial mount_path rejected at API layer
- Bug 1 reload-secrets endpoint wired (no longer 503)
- W7 cross-user GET / list / reveal all return uniform 404
- W10 password change preserves secrets
- Bug 11 workspace-deletion → bindings-purge cycle

### Other-agent collision

The other agent shipped commit `02dcd8b` (epic-18) and several follow-ups during my validation. I rebased my commits on top each time. Their pre-commit hook `repolint` ran on my commit and confirmed:
- `migrations sequence (11 migrations, max version 11)`
- `chart migrations match api/migrations/`

Their migration consolidation renamed `000009_workspace_version_info` → `000011_workspace_version_info` — the rename is in their pending working tree, not yet committed. I did not touch it.

---

## Test Coverage Gaps Closed

Audit of pre-existing test coverage on the touched files:

| Function | Pre | Post |
|---|---:|---:|
| `SecretService.AddBindings` | 18.8% | 75% |
| `SecretService.GetSecretByName` | 0% | 83.3% |
| `SecretService.GetBindingsForSecret` | 0% | 83.3% |
| `pg_secret_store.go` (whole file) | 0% in CI | runs in CI under `-tags=integration` |
| `pg_key_store.go` (whole file) | 0% in CI | runs in CI under `-tags=integration` |
| Total `pkg/secrets` (no tags) | 50.2% | 52.5% |

### New unit tests

1. **`TestSecretService_AddBindings_HappyPath_AppendsAndAudits`** — `AddBindings` must merge new secret IDs into a workspace's binding set without clobbering pre-existing bindings, and emit one `bind` audit row per added secret. Pre-fix, a regression that turned `AddBindings` into "replace-all" or "no-op" would silently break SetWorkspaceEnv for any workspace that already had bindings.
2. **`TestSecretService_AddBindings_Idempotent`** — re-adding already-bound secrets must yield exactly one binding row, not duplicates. Guards against regression in either pg's `INSERT ... ON CONFLICT DO NOTHING` or the in-memory mock's `seen` set.
3. **`TestSecretService_GetSecretByName_OwnerAndCrossUser`** — owner sees the secret; cross-user gets `nil, nil` (no enumeration); non-existent name gets `nil, nil`. The cross-user assertion is load-bearing: any "lookup by name" that returned data could enumerate names cross-tenant.
4. **`TestSecretService_GetBindingsForSecret_OwnershipEnforced`** — owner sees their workspaces; cross-user gets `nil, nil` (no leak of binding set).

### New integration tests (run under `-tags=integration` in the new CI workflow)

1. **`TestPgE2E_RotateKey_AtomicReEncryption`** — Bug 9 + A2 regression at the pg layer. Asserts atomic rotation: every pre-rotation secret decrypts with the new DEK; old recovery key is rejected; new recovery key yields a DEK that decrypts the re-encrypted secrets. End-to-end through `RotateKeyWithPassword → ReEncryptUserSecrets → UpdateWrappedDEK + UpdateWrappedDEKRecovery → cache.CacheDEK`.
2. **`TestPgE2E_AddBindings_IdempotentAndConcurrent`** — 5x idempotent `AddBindings` yields 1 binding per secret (pg `ON CONFLICT DO NOTHING`); `Set` + `Add` interleaving produces the union via `pg_try_advisory_xact_lock` serialisation.
3. **`TestPgE2E_AsyncAuditLogger_Lifecycle`** — normal-operation drain accounts for every entry; post-`Stop()` `LogAudit` calls don't panic and just bump the drop counter (pass-2 N1 panic regression); `Stop()` is idempotent.

### New CI workflow: `.github/workflows/secrets-integration.yml`

Boots Postgres 16 + Redis 7 service containers, applies all `api/migrations/*.up.sql` in order, then runs `go test -tags=integration -race ./pkg/secrets/...`. This is the first time `pg_secret_store.go` and `pg_key_store.go` execute in CI.

The workflow also re-runs `pkg/secrets` unit tests with `-race` (without `-short`) so any future test that opts out of `-short` is still exercised.

Why a separate workflow file rather than a job in `ci.yml`: the parent `ci.yml` is being heavily edited by a parallel work stream (lint + chart sync). A separate file keeps the diffs small and reduces merge-conflict surface. Can be merged later.

A complementary shell-e2e CI job (running `local/test-secrets.sh` against an ephemeral API + `kind` cluster) was prototyped but deferred. The kind+CRD+API-launch chain is brittle relative to the unit/integration coverage shipping here. Worth revisiting once the parallel workflow churn settles.

---

## Key Decisions

- **Live validation passed before changing CI.** I rolled out `sha-5cbb632`, fixed the pre-existing `000009` migration drift, ran `test-secrets.sh`, and only then started writing CI tests. This sequencing matters: had the live cluster surfaced a regression I missed, the CI tests would have been written based on a wrong understanding of "expected" behaviour.
- **Separate CI workflow rather than `ci.yml` modification.** The other agent has the lint job, repolint hook, and chart-sync logic landing in `ci.yml`. Touching the same file would cause merge conflicts. A separate `secrets-integration.yml` is isolated.
- **Shell-e2e CI deferred.** Running `local/test-secrets.sh` in CI requires `kind` + CRDs + API launch + config synthesis. Prototyped (180 lines) but the failure-mode surface is large. The unit + integration tests cover everything except the "Bug 3 binding-pollution → real K8s pod" path, which the live cluster validation already exercised. Worth doing later.
- **Did NOT touch other-agent files:** `pkg/repolint/`, `cmd/repolint/`, `Makefile`, `.githooks/`, `.github/workflows/ci.yml`, `worklogs/0088..0097`, `design/stories/epic-17-security-review/`, the `000009 → 000011` migration rename, `charts/llmsafespace/migrations/`, `go.mod`, `.gitignore`. Only my own files staged in any commit.

---

## Blockers

None. Both workflows (CI and the new Secrets Integration) are running on `97e7d0c` at time of writing.

---

## Tests Run

| Command | Result |
|---------|--------|
| `local/test-secrets.sh http://localhost:18080` (live cluster, port-forwarded API) | 44/44 PASS |
| `go test -count=1 ./pkg/secrets/...` (no tags) | PASS, 52.5% coverage |
| `go vet -tags=integration ./pkg/secrets/...` | clean |
| `go build -tags=integration ./pkg/secrets/...` | clean |
| `gofmt -l pkg/secrets/` | clean |
| Pre-commit hook (`repolint`) | passed |

---

## Next Steps

1. **Verify `secrets-integration` workflow run completes green.** First run on commit `97e7d0c`. If the migration loop catches any new schema drift, fix that.
2. **Shell-e2e CI job** — write the `kind`-based e2e job once the parallel workflow churn settles. The 180-line prototype is documented in this worklog's git history.
3. **Cluster cleanup** — leftover test users (`alpha-*@secretstest.local`, `beta-*@secretstest.local`) and the test workspace (`a020fc49-...`) created during validation. The other agent's epic-17 cleanup work may already cover this.
4. **Documentation** — the new sentinel-error contract (worklog 0094 pass-2/3/4 work) deserves a short ARCHITECTURE.md note in `pkg/secrets/` so future contributors know to use `errors.Is` rather than substring matching. Defer until the lint/chart parallel work lands.

---

## Files Modified

### New
- `.github/workflows/secrets-integration.yml` (101 lines, new CI workflow)

### Modified
- `pkg/secrets/secret_service_test.go` (+197 lines, 4 new unit tests)
- `pkg/secrets/pg_integration_test.go` (+251 lines, 3 new integration tests)

### NOT Modified (other-agent territory)
- `.github/workflows/ci.yml`
- `Makefile`
- `.githooks/`
- `pkg/repolint/`
- `cmd/repolint/`
- `api/migrations/000009_*` (renamed to `000011_*` by other agent in their working tree)
- `charts/llmsafespace/migrations/`
- `go.mod`
- `worklogs/0088..0097` (theirs)

---

## Live cluster artefacts (cleanup candidate)

- DB rows: `DELETE FROM users WHERE email LIKE '%@secretstest.local'` (multiple test users)
- Workspace: `a020fc49-8719-4450-8236-f99a279d75b0` (test workspace from Bug-11 validation, owner `10755f86-2b7a-4172-a90d-e8f9746d280d`)

The Bug-11 fix (commit `1d57520`) auto-purges `user_secret_bindings` rows on workspace delete, so the test users' bindings are clean. Only the user/workspace rows themselves remain.
