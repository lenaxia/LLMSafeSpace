# Worklog: Migration 000014 Incident Hardening + helm-deploy Target

**Date:** 2026-06-07
**Session:** Fix production incident caused by missing migration 000014, harden the migration for real data, add deployment guard
**Status:** Complete

---

## Objective

Fix the production outage where the sessions nav bar went blank after deploying `sha-33d3ef2`. The API returned 500 on `GET /api/v1/workspaces` because the `workspace_agent_state` table did not exist. Root cause: local repo was behind `origin/main` when `helm upgrade` was run, so the migration ConfigMap was built without migration 000014.

---

## Work Completed

### 1. Incident triage and immediate fix
- Identified `ERROR: relation "workspace_agent_state" does not exist` in API logs (`workspace_service.go:300`)
- Applied migration 000014 SQL directly to the database, fixing partial failures:
  - `user_secret_bindings.workspace_id` had 6 non-UUID test rows (`ws-validation`, `ws-profile-test`) — deleted them
  - 41 orphan `workspaces.user_id` values referenced deleted users — soft-deleted those workspaces
  - `schema_migrations` version 14 was left `dirty=true` — cleaned to `false`
- Restarted API pods, verified sessions restored

### 2. Hardened migration 000014 (api/migrations/ + charts/llmsafespace/migrations/)
- **DELETE non-UUID rows** before `ALTER COLUMN workspace_id TYPE UUID` — prevents cast failure on test data
- **Soft-delete orphan workspaces** (`user_id NOT IN users`) before adding FK — marks orphans for cleanup; FK addition catches `foreign_key_violation` gracefully if orphans remain
- **Backfill fallback** — wraps INSERT in `EXCEPTION WHEN datatype_mismatch` with explicit `::uuid` cast for when Bug 11 ALTER was skipped
- **Down migration** — wraps ALTER TABLE in `DO $$ ... EXCEPTION WHEN others` blocks for idempotent rollback

### 3. Added `make helm-deploy` target (Makefile)
- Enforces `HEAD == origin/main` before `helm upgrade` — prevents deploying stale chart files
- Applies CRDs, lints chart, runs upgrade, waits for rollout in one step
- Usage: `make helm-deploy RELEASE_NS=default IMAGE_TAG=sha-abc1234`

### 4. Fixed ConfigMap comment
- `migrations-configmap.yaml` comment referenced wrong path `api/migrations/` — fixed to `charts/llmsafespace/migrations/`

### 5. Added seeded migration test (hack/migration-data-cleanup.sh)
- Seeds non-UUID `workspace_id` rows and orphan workspaces before running migration 000014
- Verifies the non-UUID rows are deleted and orphan workspaces are soft-deleted
- Catches regex errors or overly-broad DELETE/UPDATE that could destroy legitimate data

---

## Assumptions

| # | Assumption | Validated |
|---|-----------|-----------|
| 1 | Non-UUID `workspace_id` values are test data with no backing K8s CRD | Verified: `ws-validation` and `ws-profile-test` have no corresponding workspace rows |
| 2 | Orphan workspaces (`user_id` not in `users`) should be soft-deleted, not hard-deleted | Verified: soft-delete preserves audit records; K8s CRD cleanup happens via garbage collection |
| 3 | `EXCEPTION WHEN others THEN RAISE NOTICE` is acceptable for defensive migration | Verified: existing pattern in codebase, consistent with migration-idempotent.sh expectations |
| 4 | `make helm-deploy` git sync check should only compare to `origin/main` | Verified: target is for post-merge deployment to production |

---

## Files Changed

- `api/migrations/000014_workspace_agent_state_and_bug11_fix.up.sql` — data cleanup, exception handling
- `api/migrations/000014_workspace_agent_state_and_bug11_fix.down.sql` — idempotent rollback
- `charts/llmsafespace/migrations/000014_*` — synced copies
- `charts/llmsafespace/templates/migrations-configmap.yaml` — comment fix
- `Makefile` — `helm-deploy` target + `helm-chart-test` in PHONY
- `hack/migration-data-cleanup.sh` — seeded test for data cleanup branches

---

## Related

- PR #54
- Worklog 0140 (original incident context)
- Epic 27a+27b PR #13 (introduced migration 000014)
