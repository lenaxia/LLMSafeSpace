# Worklog: Secrets global-default binding and inline update UX

**Date:** 2026-06-29
**Session:** Implement three secrets-page UX improvements (global-default checkbox, inline update form, softened post-creation warning) end-to-end across migration, backend, and frontend; address automated-reviewer findings.
**Status:** Complete

---

## Objective

Ship three secrets-page UX improvements requested by the user:

1. "Include in all workspaces" checkbox on the secrets settings page that auto-binds the secret to all newly-created workspaces (with a note that this is non-retroactive).
2. Drop the misleading "this will never be shown again" post-creation warning, since the reveal-secret feature already exists.
3. Add an inline update/roll-secret flow alongside the existing reveal flow, rather than forcing delete+recreate.

---

## Work Completed

### Migration (000004)
- Added `global_default BOOLEAN NOT NULL DEFAULT false` column to `user_secrets` (`api/migrations/000004_user_secrets_global_default.up.sql` + `.down.sql`).
- Added a partial index `idx_user_secrets_global_default ON user_secrets(user_id) WHERE global_default = true` so the per-user lookup stays O(log n) as the table grows.
- Mirrored both files to `charts/llmsafespaces/migrations/` (byte-parity requirement).

### Backend types & store
- `pkg/secrets/types.go`: added `GlobalDefault bool` to `UserSecret`, `SecretResponse`, `CreateSecretRequest`; `UpdateSecretRequest.GlobalDefault` is `*bool` (nil = leave unchanged).
- `pkg/secrets/store.go`: added `ListGlobalDefaultSecrets(ctx, userID)` to the `SecretStore` interface.
- `pkg/secrets/pg_secret_store.go`: added `global_default` to every SELECT/INSERT/UPDATE query; implemented `PgSecretStore.ListGlobalDefaultSecrets` (uses the partial index); added `AsyncAuditLogger.ListGlobalDefaultSecrets` delegation.

### Backend service
- `pkg/secrets/secret_service.go`: `GlobalDefault` propagated through `CreateSecret`, `GetSecret`, `GetSecretByName`, `ListSecrets`, and `SecretResponse`. `UpdateSecret` applies `*req.GlobalDefault` when non-nil.
- Added `SecretService.SeedGlobalDefaultSecrets(ctx, workspaceID, userID)`. **Calls the service-level `s.AddBindings` (not the store-level `s.store.AddBindings`)** so each auto-bind emits a `"bind"` audit entry — matching the audit-trail contract every user-initiated bind path already satisfies. Initial implementation used the store method; the automated reviewer flagged the audit gap and this was corrected.
- `UpdateSecret` audit metadata now records `"globalDefault": "true|false"` when the value actually changes (compared against `prevGlobalDefault`). Same-value re-sets do not emit the key (no change = no record).

### Backend workspace wiring
- `api/internal/services/workspace/workspace_service.go`: new `SecretAutoProvisioner` interface (`SeedGlobalDefaultSecrets(ctx, workspaceID, userID) error`); new `secretProvisioner` field + `SetSecretAutoProvisioner` setter on `Service`.
- `CreateWorkspace` calls `secretProvisioner.SeedGlobalDefaultSecrets` after `credProvisioner.SeedWorkspaceCredentials`. Best-effort: failure is logged at Error but does NOT roll back workspace creation (consistent with the existing credProvisioner pattern).
- `api/internal/app/app.go`: wires `secretService` as the `SecretAutoProvisioner` on `wsSvc`.

### Frontend
- `frontend/src/api/secrets.ts`: added `globalDefault` to `SecretResponse`, `CreateSecretRequest`, `UpdateSecretRequest`.
- `frontend/src/components/settings/SecretsTab.tsx`:
  - Create form: "Include in all workspaces" checkbox (default off) with tooltip ("Does not apply retroactively to existing workspaces").
  - Secret row: new "Update" button opens inline `UpdateSecretForm` (new value textarea + globalDefault checkbox pre-filled from current state); "all workspaces" pill badge on rows where `globalDefault=true`.
  - Post-creation warning softened from "⚠️ Save this value now. You won't be able to view it without re-entering your password." → "You can reveal this value later using your password."

### Tests
- `pkg/secrets/secret_service_test.go`: 9 new unit tests covering:
  - `CreateSecret_GlobalDefault` (true / default false propagation)
  - `ListGlobalDefaultSecrets_FilterCorrectness` (only global-default rows returned)
  - `SeedGlobalDefaultSecrets_HappyPath` (2 secrets → 2 bindings + 2 bind audit entries)
  - `SeedGlobalDefaultSecrets_EmptyPath` (no global-defaults → no-op, no error)
  - `SeedGlobalDefaultSecrets_Idempotent` (double-seed → 1 binding, no duplicates)
  - `SeedGlobalDefaultSecrets_StoreError` (DB outage → error propagates)
  - `UpdateSecret_GlobalDefault_NilUnchanged` (nil pointer preserves stored value)
  - `UpdateSecret_GlobalDefault_ToggleOff` / `ToggleOn` (flip + audit metadata present)
  - `UpdateSecret_GlobalDefault_SameValueNoAuditKey` (no-change re-set does not emit audit key)
- Added `listGlobalDefaultErr` field to `mockSecretStore` so the store-error path is testable.
- `api/internal/services/workspace/workspace_defaults_test.go`: 3 new integration tests with a `fakeSecretProvisioner` test double:
  - `TestCreateWorkspace_InvokesSecretProvisioner` (verifies SeedGlobalDefaultSecrets called with correct workspaceID + userID)
  - `TestCreateWorkspace_SecretProvisionerFailure_BestEffort` (provisioner error → workspace still created, no rollback)
  - `TestCreateWorkspace_NoSecretProvisioner_NoPanic` (nil provisioner → call skipped, no panic)
- Added `ListGlobalDefaultSecrets` to all 7 SecretStore mocks across the codebase (mockSecretStore, dbSecretStoreAdapter, memSecretStore, testSecretStore, e2eSecretStore, reloadE2EStore, pushPathSessionStore).

### Mock updates required by the new interface method
The new `ListGlobalDefaultSecrets` on the `SecretStore` interface broke 7 mock implementations across 4 packages. Each was fixed by adding a method that mirrors the production implementation's filter (`WHERE user_id = $1 AND global_default = true`).

### CI iterations
The PR went through three review rounds from the automated AI reviewer:
1. First round: build failures (gofmt + missing mock methods). Fixed in commit `332a1eb3`.
2. Second round: 4 more mocks missing the method. Fixed in commit `1ee919d5`.
3. Third round: 2 final mocks missing the method. Fixed in commit `af36bfbc`.
4. Fourth round: REQUEST CHANGES for missing test coverage at all levels + audit-logging gap in `SeedGlobalDefaultSecrets` + missing worklog. All three addressed in this commit.

---

## Key Decisions

1. **`global_default` is a column on `user_secrets`, not a separate table.** A secret has at most one global-default state; a join table would add a constraint without adding meaning. Partial index keeps the lookup efficient.
2. **Auto-binding is additive (`AddBindings`), not replace (`SetBindings`).** A new workspace has no existing bindings, but `AddBindings` is idempotent and advisory-locked — safe under the (rare) case of a re-trigger.
3. **`UpdateSecretRequest.GlobalDefault` is `*bool`.** Nil = leave unchanged, so the update endpoint can be used just to rotate the value without touching the global-default flag. The frontend always sends a concrete boolean.
4. **`SeedGlobalDefaultSecrets` calls the service-level `AddBindings`, not the store-level one.** This preserves the audit-trail contract: every bind path (user-initiated or auto) records a `"bind"` audit entry. The initial implementation called the store directly; the automated reviewer correctly flagged this as an audit gap and it was corrected.
5. **Best-effort failure mode for secret seeding.** A provisioning failure does not roll back workspace creation, matching the existing `credProvisioner` pattern. The workspace is created; the user can manually bind secrets via the workspace bindings UI.
6. **Keep the reveal feature.** The user asked whether to drop reveal in favor of roll-only. Reveal exists, works correctly, and is password-gated with auto-hide; dropping it would be a regression. Both reveal and update are now first-class.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 30s -race ./pkg/secrets/...` — new unit tests pass locally (gofmt clean, vet clean).
- `go test -timeout 30s ./api/internal/services/workspace/...` — new integration tests pass locally.
- CI: Lint, pkg/secrets integration (Postgres + Redis), Test (full suite, race detector), Frontend (unit + typecheck + e2e), review — all green on the latest push (commit `af36bfbc`). The new tests in this commit will run on the next push.

---

## Next Steps

1. Push this commit and wait for the automated reviewer's next pass.
2. If APPROVE: merge via squash merge.
3. Post-merge: the repolint bot will assign the real worklog number (replacing the `NNNN_` sentinel).

---

## Files Modified

- `api/migrations/000004_user_secrets_global_default.up.sql` (new)
- `api/migrations/000004_user_secrets_global_default.down.sql` (new)
- `charts/llmsafespaces/migrations/000004_user_secrets_global_default.up.sql` (new)
- `charts/llmsafespaces/migrations/000004_user_secrets_global_default.down.sql` (new)
- `pkg/secrets/types.go`
- `pkg/secrets/store.go`
- `pkg/secrets/pg_secret_store.go`
- `pkg/secrets/secret_service.go`
- `pkg/secrets/secret_service_test.go`
- `api/internal/services/workspace/workspace_service.go`
- `api/internal/services/workspace/workspace_defaults_test.go`
- `api/internal/app/app.go`
- `api/internal/app/secrets_adapters.go`
- `api/internal/services/auth/auth_e2e_secrets_test.go`
- `api/internal/handlers/pod_bootstrap_e2e_test.go`
- `api/internal/handlers/secrets_test_helpers_test.go`
- `api/internal/handlers/secrets_push_session_test.go`
- `cmd/workspace-agentd/reload_credentials_e2e_test.go`
- `frontend/src/api/secrets.ts`
- `frontend/src/components/settings/SecretsTab.tsx`
- `README-LLM.md`
- `worklogs/0572_2026-06-29_secrets-global-default-update-ux.md` (new)
